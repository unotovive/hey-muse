package standalone

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(s string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(s)), Header: make(http.Header)}
}

func TestSwitchBotDispatch(t *testing.T) {
	posts := 0
	failPost := false
	var lastPath string
	var lastCommand map[string]string
	h := Home{dir: t.TempDir(), client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		mac := hmac.New(sha256.New, []byte("test-secret"))
		io.WriteString(mac, "test-token"+r.Header.Get("t")+r.Header.Get("nonce"))
		if r.Header.Get("Authorization") != "test-token" || r.Header.Get("sign") != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
			t.Fatal("invalid request signature")
		}
		if r.Method == "POST" {
			posts++
			lastPath = r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&lastCommand); err != nil {
				t.Fatal(err)
			}
			if failPost {
				return nil, errors.New("unknown delivery")
			}
			return response(`{"statusCode":100}`), nil
		}
		switch r.URL.Path {
		case "/v1.1/scenes":
			return response(`{"statusCode":100,"body":[{"sceneName":"ac on","sceneId":"s1"},{"sceneName":"ac off","sceneId":"s2"}]}`), nil
		case "/v1.1/devices":
			return response(`{"statusCode":100,"body":{"deviceList":[{"deviceName":"bulb","deviceType":"Color Bulb","deviceId":"bulb1"}],"infraredRemoteList":[{"deviceName":"tv","remoteType":"TV","deviceId":"unrelated"}]}}`), nil
		}
		t.Fatalf("unexpected request %s", r.URL.Path)
		return nil, nil
	})}}
	if err := os.WriteFile(filepath.Join(h.dir, "switchbot.env"), []byte("\ufeffexport SWITCHBOT_TOKEN=test-token\r\nSWITCHBOT_SECRET=test-secret\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := settings{Controls: map[string]control{"ac": {Label: "AC", On: "ac on", Off: "ac off"}}, Hall: target{"bulb", "Color Bulb"}}
	if err := h.save("home-local.json", cfg); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r, err := h.Dashboard(ctx, "scene", "power/ac/on")
	if err != nil || !r.OK || posts != 1 || lastPath != "/v1.1/scenes/s1/execute" || r.Controls[0].ID != "power/ac/off" {
		t.Fatalf("dispatch %+v %v posts=%d", r, err, posts)
	}
	r, err = h.Dashboard(ctx, "scene", "power/ac/on")
	if err != nil || r.OK || posts != 1 {
		t.Fatal("stale direction sent")
	}
	if _, err := h.Dashboard(ctx, "scene", "s2"); err == nil || posts != 1 {
		t.Fatal("sent unauthorized scene")
	}
	if err := h.Appliance(ctx, "hall", "off", 0); err != nil || lastPath != "/v1.1/devices/bulb1/commands" || lastCommand["command"] != "turnOff" || posts != 2 {
		t.Fatal("wrong device command", err)
	}
	failPost = true
	if _, err := h.Dashboard(ctx, "scene", "power/ac/off"); err == nil || posts != 3 {
		t.Fatal("failed POST replayed or accepted")
	}
}

func TestCalendarAtomicRefreshAndRecurrence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(time.Hour)
	stamp := func(t time.Time) string { return t.Format("20060102T150405Z") }
	feed := fmt.Sprintf("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:t\r\nSUMMARY:planned\r\nDTSTART:%s\r\nDURATION:PT30M\r\nRRULE:FREQ=DAILY;COUNT=3\r\nEXDATE:%s\r\nRDATE:%s\r\nEND:VEVENT\r\nBEGIN:VEVENT\r\nUID:t\r\nRECURRENCE-ID:%s\r\nDTSTART:%s\r\nDURATION:PT30M\r\nSUMMARY:moved\r\nEND:VEVENT\r\nEND:VCALENDAR", stamp(start), stamp(start.AddDate(0, 0, 1)), stamp(start.AddDate(0, 0, 4)), stamp(start.AddDate(0, 0, 2)), stamp(start.AddDate(0, 0, 2).Add(time.Hour)))
	fail := false
	h := Home{dir: t.TempDir(), client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/second/") {
			if fail {
				return response("BEGIN:VCALENDAR\r\n"), nil
			}
			return response("BEGIN:VCALENDAR\r\nEND:VCALENDAR"), nil
		}
		return response(feed), nil
	})}}
	var cfg settings
	if err := json.Unmarshal([]byte(`{"calendars":[{"name":"one"},{"name":"two"}]}`), &cfg); err != nil {
		t.Fatal(err)
	}
	urls := "https://calendar.google.com/calendar/ical/first/public/basic.ics\nhttps://calendar.google.com/calendar/ical/second/public/basic.ics"
	if err := os.WriteFile(filepath.Join(h.dir, "calendar-feeds.txt"), []byte(urls), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := h.agenda(context.Background(), cfg)
	if err != nil || !r.OK || r.Stale || len(r.Events) != 3 || r.Events[1].Title != "moved" || !r.Events[2].Start.Equal(start.AddDate(0, 0, 4)) {
		t.Fatalf("unexpected expansion %+v %v", r, err)
	}
	fail = true
	cached, err := h.agenda(context.Background(), cfg)
	if err != nil || !cached.Stale || !cached.Fetched.Equal(r.Fetched) || len(cached.Events) != 3 {
		t.Fatalf("lost previous snapshot: %+v %v", cached, err)
	}
}

func TestCachedEvents(t *testing.T) {
	h := Home{dir: t.TempDir()}
	if _, _, err := h.CachedEvents(); err == nil {
		t.Fatal("no cache yet should be an error")
	}
	until := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	if err := h.save("agenda-cache.json", snapshot{Events: []Event{{Title: "会議"}}, Until: until}); err != nil {
		t.Fatal(err)
	}
	evs, got, err := h.CachedEvents()
	if err != nil || len(evs) != 1 || evs[0].Title != "会議" || !got.Equal(until) {
		t.Fatalf("got %v %v %v", evs, got, err)
	}
}
