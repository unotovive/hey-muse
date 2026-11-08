package standalone

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func googleHome(t *testing.T, tokenStatus int, tokenBody string, pages map[string]string) (*Home, *int) {
	refreshes := 0
	h := &Home{dir: t.TempDir(), client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "oauth2.googleapis.com" {
			refreshes++
			r.ParseForm()
			if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "rt" || r.PostForm.Get("client_id") != "cid" {
				t.Fatal("bad refresh form")
			}
			return &http.Response{StatusCode: tokenStatus, Body: io.NopCloser(strings.NewReader(tokenBody)), Header: make(http.Header)}, nil
		}
		if r.Header.Get("Authorization") != "Bearer at" {
			t.Fatal("missing access token")
		}
		q := r.URL.Query()
		if q.Get("singleEvents") != "true" || q.Get("maxResults") != "100" {
			t.Fatal("missing query")
		}
		body, ok := pages[r.URL.EscapedPath()+"?pageToken="+q.Get("pageToken")]
		if !ok {
			t.Fatalf("unexpected %s", r.URL)
		}
		return response(body), nil
	})}}
	os.WriteFile(filepath.Join(h.dir, "google-token.json"), []byte(`{"client_id":"cid","client_secret":"cs","refresh_token":"rt"}`), 0600)
	os.WriteFile(filepath.Join(h.dir, "home-local.json"), []byte(`{"calendars":[{"id":"a@group.calendar.google.com","name":"家族"},{"id":"me@gmail.com","name":"自分"}]}`), 0600)
	return h, &refreshes
}

const goodToken = `{"access_token":"at","expires_in":3600,"token_type":"Bearer"}`

func googleAgendaHome(t *testing.T) (*Home, *int) {
	day := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	next := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	soon := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	soonEnd := time.Now().Add(4 * time.Hour).UTC().Format(time.RFC3339)
	later := time.Now().Add(50 * time.Hour).UTC().Format(time.RFC3339)
	laterEnd := time.Now().Add(51 * time.Hour).UTC().Format(time.RFC3339)
	return googleHome(t, 200, goodToken, map[string]string{
		"/calendar/v3/calendars/a@group.calendar.google.com/events?pageToken=": `{"nextPageToken":"p2","items":[
			{"status":"confirmed","summary":"遅い","start":{"dateTime":"` + later + `"},"end":{"dateTime":"` + laterEnd + `"}},
			{"status":"cancelled","summary":"中止","start":{"dateTime":"` + soon + `"},"end":{"dateTime":"` + soonEnd + `"}}]}`,
		"/calendar/v3/calendars/a@group.calendar.google.com/events?pageToken=p2": `{"items":[
			{"summary":"","start":{"date":"` + day + `"},"end":{"date":"` + next + `"}}]}`,
		"/calendar/v3/calendars/me@gmail.com/events?pageToken=": `{"items":[
			{"summary":"早い","start":{"dateTime":"` + soon + `"},"end":{"dateTime":"` + soonEnd + `"}}]}`,
	})
}

func TestGoogleAgendaMapsTimedAllDayAndPages(t *testing.T) {
	h, refreshes := googleAgendaHome(t)
	var cfg settings
	if err := h.read("home-local.json", &cfg); err != nil {
		t.Fatal(err)
	}
	res, err := h.agenda(context.Background(), cfg)
	if err != nil || !res.OK || res.Stale {
		t.Fatalf("agenda: %+v %v", res, err)
	}
	if len(res.Events) != 3 {
		t.Fatalf("events: %+v", res.Events)
	}
	if res.Events[0].Title != "早い" || res.Events[0].Calendar != "自分" || res.Events[0].Day {
		t.Fatalf("first: %+v", res.Events[0])
	}
	// 終日は今日+2 日の 0 時で、現在から 24〜48 時間後に収まる。「遅い」は +50 時間なので常にその後ろ。
	if res.Events[2].Title != "遅い" || res.Events[2].Calendar != "家族" {
		t.Fatalf("third: %+v", res.Events[2])
	}
	d := res.Events[1]
	want := time.Now().AddDate(0, 0, 2)
	wantStart := time.Date(want.Year(), want.Month(), want.Day(), 0, 0, 0, 0, time.Local)
	if d.Title != "予定" || !d.Day || !d.Start.Equal(wantStart) || !d.End.Equal(wantStart.AddDate(0, 0, 1)) {
		t.Fatalf("all-day: %+v", d)
	}
	if *refreshes != 1 {
		t.Fatalf("refreshes=%d", *refreshes)
	}
}

func TestGoogleRefreshFailureUsesCache(t *testing.T) {
	h, _ := googleHome(t, 400, `{"error":"invalid_grant"}`, nil)
	var cfg settings
	h.read("home-local.json", &cfg)
	if _, err := h.agenda(context.Background(), cfg); err == nil {
		t.Fatal("expected error without cache")
	}
	now := time.Now()
	h.save("agenda-cache.json", snapshot{Updated: now.Add(-time.Hour), Until: now.Add(time.Hour), Events: []Event{
		{Title: "a", Calendar: "x", Start: now.Add(time.Hour), End: now.Add(2 * time.Hour)},
		{Title: "b", Calendar: "x", Start: now.Add(3 * time.Hour), End: now.Add(4 * time.Hour)},
	}})
	res, err := h.agenda(context.Background(), cfg)
	if err != nil || !res.OK || !res.Stale || len(res.Events) != 2 {
		t.Fatalf("cache fallback: %+v %v", res, err)
	}
}

func TestGoogleTokenIsReusedUntilExpiry(t *testing.T) {
	h, refreshes := googleAgendaHome(t)
	var cfg settings
	h.read("home-local.json", &cfg)
	for i := 0; i < 2; i++ {
		if _, err := h.agenda(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	if *refreshes != 1 {
		t.Fatalf("refreshes=%d", *refreshes)
	}
}
