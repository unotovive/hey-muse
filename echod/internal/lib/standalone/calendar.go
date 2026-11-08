package standalone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/ical"
)

type snapshot struct {
	Events  []Event   `json:"events"`
	Updated time.Time `json:"updated"`
	Until   time.Time `json:"until"`
}

var feedPath = regexp.MustCompile("^/calendar/ical/[^/]+/(?:private-[A-Za-z0-9_-]+|public)/basic[.]ics$")

func (h *Home) calendar(ctx context.Context, addr, name string, now, end time.Time) ([]Event, error) {
	u, err := url.Parse(addr)
	if err != nil || u.Scheme != "https" || u.Host != "calendar.google.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !feedPath.MatchString(u.Path) {
		return nil, errors.New("invalid Google Calendar feed address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, errors.New("invalid calendar request")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, errors.New("calendar connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("calendar HTTP %d", resp.StatusCode)
	}
	const limit = 20 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	data = bytes.TrimSpace(data)
	if len(data) > limit || !bytes.HasPrefix(data, []byte("BEGIN:VCALENDAR")) || !bytes.HasSuffix(data, []byte("END:VCALENDAR")) {
		return nil, errors.New("incomplete calendar feed")
	}
	evs, err := ical.Events(bytes.NewReader(data), now, end)
	if err != nil {
		return nil, err
	}
	rows := make([]Event, 0, len(evs))
	for _, ev := range evs {
		title := []rune(ev.Summary)
		if len(title) == 0 {
			title = []rune("予定")
		}
		rows = append(rows, Event{Title: string(title[:min(len(title), 160)]), Calendar: name, Start: ev.Start, End: ev.End, Day: ev.AllDay})
	}
	return rows, nil
}

func (h *Home) refresh(ctx context.Context, cfg settings, now time.Time) (snapshot, error) {
	var tok googleToken
	if h.read("google-token.json", &tok) == nil {
		return h.refreshGoogle(ctx, cfg, tok, now)
	}
	raw, err := os.ReadFile(filepath.Join(h.dir, "calendar-feeds.txt"))
	if err != nil {
		return snapshot{}, err
	}
	urls := strings.Fields(strings.TrimPrefix(string(raw), "\ufeff"))
	if len(urls) == 0 || len(urls) != len(cfg.Calendars) {
		return snapshot{}, errors.New("calendar feed count mismatch")
	}
	data := snapshot{Events: []Event{}, Updated: now, Until: now.AddDate(0, 0, 8)}
	seen := map[string]bool{}
	for i, addr := range urls {
		if seen[addr] {
			return snapshot{}, errors.New("duplicate calendar feed")
		}
		seen[addr] = true
		rows, err := h.calendar(ctx, addr, cfg.Calendars[i].Name, now, data.Until)
		if err != nil {
			return snapshot{}, err
		}
		data.Events = append(data.Events, rows...)
	}
	slices.SortStableFunc(data.Events, func(a, b Event) int { return a.Start.Compare(b.Start) })
	return data, nil
}

func (h *Home) refreshGoogle(ctx context.Context, cfg settings, tok googleToken, now time.Time) (snapshot, error) {
	if len(cfg.Calendars) == 0 {
		return snapshot{}, errors.New("no calendars configured")
	}
	data := snapshot{Events: []Event{}, Updated: now, Until: now.AddDate(0, 0, 8)}
	for _, c := range cfg.Calendars {
		if c.ID == "" {
			return snapshot{}, errors.New("calendar id required")
		}
		rows, err := h.googleEvents(ctx, tok, c.ID, c.Name, now, data.Until)
		if err != nil {
			return snapshot{}, err
		}
		data.Events = append(data.Events, rows...)
	}
	slices.SortStableFunc(data.Events, func(a, b Event) int { return a.Start.Compare(b.Start) })
	return data, nil
}

func (h *Home) agenda(ctx context.Context, cfg settings) (Result, error) {
	now := time.Now()
	data, err := h.refresh(ctx, cfg, now)
	stale := err != nil
	if stale {
		if readErr := h.read("agenda-cache.json", &data); readErr != nil || !data.Until.After(now) {
			return Result{}, err
		}
	} else if err = h.save("agenda-cache.json", data); err != nil {
		return Result{}, err
	}
	result := Result{OK: true, Stale: stale, Fetched: data.Updated}
	end := now.AddDate(0, 0, 7)
	for _, e := range data.Events {
		if e.Start.Before(end) && (e.End.After(now) || e.Start.Equal(e.End) && !e.Start.Before(now)) {
			result.Events = append(result.Events, e)
			if len(result.Events) == 24 {
				break
			}
		}
	}
	return result, nil
}
