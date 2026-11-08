package standalone

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func remoHome(t *testing.T, onPost func(path, body string)) (*Home, *int) {
	gets := 0
	h := &Home{dir: t.TempDir(), client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer remo-token" {
			t.Fatal("missing bearer")
		}
		if r.Method == "POST" {
			b, _ := io.ReadAll(r.Body)
			onPost(r.URL.Path, string(b))
			return response(`{}`), nil
		}
		gets++
		switch r.URL.Path {
		case "/1/appliances":
			return response(`[{"id":"L1","type":"LIGHT","nickname":"リビング照明","light":{"state":{"power":"on"}}},{"id":"A1","type":"AC","nickname":"エアコン","settings":{"mode":"cool","temp":"26"}}]`), nil
		case "/1/devices":
			return response(`[{"id":"D1","newest_events":{"te":{"val":23.5},"hu":{"val":55}}}]`), nil
		}
		t.Fatalf("unexpected GET %s", r.URL.Path)
		return nil, nil
	})}}
	os.WriteFile(filepath.Join(h.dir, "natureremo.env"), []byte("NATURE_REMO_TOKEN=remo-token\n"), 0600)
	os.WriteFile(filepath.Join(h.dir, "home-local.json"), []byte(`{"backend":"nature_remo","calendars":[],"voice_devices":{"living":{"name":"リビング照明","type":"LIGHT"},"ac":{"name":"エアコン","type":"AC"}}}`), 0600)
	return h, &gets
}

func TestRemoLightAndAirconCommands(t *testing.T) {
	var paths, bodies []string
	h, gets := remoHome(t, func(p, b string) { paths = append(paths, p); bodies = append(bodies, b) })
	ctx := context.Background()
	for _, c := range []struct {
		key, action string
		value       int
		path, body  string
	}{
		{"living", "off", 0, "/1/appliances/L1/light", "button=off"},
		{"ac", "off", 0, "/1/appliances/A1/aircon_settings", "button=power-off"},
		{"ac", "cool", 26, "/1/appliances/A1/aircon_settings", "operation_mode=cool&temperature=26"},
		{"ac", "heat", 0, "/1/appliances/A1/aircon_settings", "operation_mode=warm"},
		{"ac", "temp", 24, "/1/appliances/A1/aircon_settings", "operation_mode=cool&temperature=24"},
	} {
		if err := h.Appliance(ctx, c.key, c.action, c.value); err != nil {
			t.Fatalf("%v: %v", c, err)
		}
		if paths[len(paths)-1] != c.path || bodies[len(bodies)-1] != c.body {
			t.Errorf("%v: got %s %s", c, paths[len(paths)-1], bodies[len(bodies)-1])
		}
	}
	if *gets != 1 {
		t.Errorf("appliances fetched %d times, want 1 (60 s cache)", *gets)
	}
}

func TestRemoRateLimitedSendsNothing(t *testing.T) {
	gets, posts := 0, 0
	h, _ := remoHome(t, func(string, string) {})
	h.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			posts++
			return response(`{}`), nil
		}
		gets++
		resp := response(`{}`)
		resp.StatusCode = 429
		resp.Header.Set("Retry-After", "60")
		return resp, nil
	})}
	ctx := context.Background()
	if err := h.Appliance(ctx, "ac", "off", 0); err == nil {
		t.Fatal("expected error on 429")
	}
	if gets != 1 || posts != 0 {
		t.Fatalf("gets=%d posts=%d", gets, posts)
	}
	if err := h.Appliance(ctx, "ac", "off", 0); err == nil {
		t.Fatal("expected error while rate limited")
	}
	if gets != 1 || posts != 0 {
		t.Fatalf("after retry: gets=%d posts=%d, want no further calls", gets, posts)
	}
}

func TestRemoMissingTokenFails(t *testing.T) {
	calls := 0
	h, _ := remoHome(t, func(string, string) {})
	h.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(`[]`), nil
	})}
	os.Remove(filepath.Join(h.dir, "natureremo.env"))
	if err := h.Appliance(context.Background(), "ac", "off", 0); err == nil {
		t.Fatal("expected error without token")
	}
	if calls != 0 {
		t.Fatalf("%d HTTP calls without a token", calls)
	}
}

func TestRemoEnvironmentAndCheck(t *testing.T) {
	h, _ := remoHome(t, func(string, string) { t.Fatal("unexpected POST") })
	ctx := context.Background()
	r, err := h.Dashboard(ctx, "environment", "")
	if err != nil || !r.OK || r.Environment == nil {
		t.Fatalf("environment %+v %v", r, err)
	}
	e := r.Environment
	if e.Temperature == nil || *e.Temperature != 23.5 || e.Humidity == nil || *e.Humidity != 55 || e.CO2 != nil || e.Battery != nil {
		t.Fatalf("unexpected readings %+v", e)
	}
	n, err := h.CheckTargets(ctx)
	if err != nil || n != 2 {
		t.Fatalf("CheckTargets = %d, %v; want 2", n, err)
	}
	// SwitchBot 固有のボタンは出ない。
	if p, err := h.Dashboard(ctx, "power_status", ""); err != nil || len(p.Controls) != 0 {
		t.Fatalf("power_status %+v %v", p, err)
	}
}

func TestUnknownBackendIsRefused(t *testing.T) {
	h := &Home{dir: t.TempDir(), client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP")
		return nil, nil
	})}}
	os.WriteFile(filepath.Join(h.dir, "home-local.json"), []byte(`{"backend":"xyz"}`), 0600)
	if err := h.Appliance(context.Background(), "ac", "off", 0); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}
