package standalone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	googleTokenURL = "https://oauth2.googleapis.com/token"
	googleAPIBase  = "https://www.googleapis.com/calendar/v3/calendars/"
)

type googleToken struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

// googleAccess はリフレッシュトークンからアクセストークンを得る。期限の 60 秒前まではメモリに保持する。
func (h *Home) googleAccess(ctx context.Context, tok googleToken) (string, error) {
	h.googleMu.Lock()
	defer h.googleMu.Unlock()
	if h.googleAccessToken != "" && time.Now().Before(h.googleUntil) {
		return h.googleAccessToken, nil
	}
	if tok.ClientID == "" || tok.ClientSecret == "" || tok.RefreshToken == "" {
		return "", errors.New("incomplete Google token file")
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok.RefreshToken}, "client_id": {tok.ClientID}, "client_secret": {tok.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("invalid Google token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.client.Do(req)
	if err != nil {
		return "", errors.New("Google token connection failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Google token HTTP %d", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		return "", errors.New("invalid Google token response")
	}
	h.googleAccessToken = out.AccessToken
	h.googleUntil = time.Now().Add(time.Duration(out.ExpiresIn)*time.Second - 60*time.Second)
	return out.AccessToken, nil
}

type googleTime struct {
	Date     string `json:"date"`
	DateTime string `json:"dateTime"`
}

func (g googleTime) parse() (t time.Time, day bool, err error) {
	if g.Date != "" {
		t, err = time.ParseInLocation("2006-01-02", g.Date, time.Local)
		return t, true, err
	}
	t, err = time.Parse(time.RFC3339, g.DateTime)
	return t, false, err
}

func (h *Home) googleEvents(ctx context.Context, tok googleToken, calendarID, name string, from, to time.Time) ([]Event, error) {
	if calendarID == "" {
		return nil, errors.New("calendar id required")
	}
	access, err := h.googleAccess(ctx, tok)
	if err != nil {
		return nil, err
	}
	rows := []Event{}
	pageToken := ""
	for page := 0; page < 20; page++ {
		q := url.Values{"singleEvents": {"true"}, "orderBy": {"startTime"}, "maxResults": {"100"},
			"timeMin": {from.Format(time.RFC3339)}, "timeMax": {to.Format(time.RFC3339)}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleAPIBase+url.PathEscape(calendarID)+"/events?"+q.Encode(), nil)
		if err != nil {
			return nil, errors.New("invalid calendar request")
		}
		req.Header.Set("Authorization", "Bearer "+access)
		resp, err := h.client.Do(req)
		if err != nil {
			return nil, errors.New("calendar connection failed")
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("calendar HTTP %d", resp.StatusCode)
		}
		var out struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []struct {
				Status  string     `json:"status"`
				Summary string     `json:"summary"`
				Start   googleTime `json:"start"`
				End     googleTime `json:"end"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, errors.New("invalid calendar response")
		}
		for _, it := range out.Items {
			if it.Status == "cancelled" {
				continue
			}
			start, day, err := it.Start.parse()
			if err != nil {
				return nil, errors.New("invalid event start")
			}
			end, _, err := it.End.parse()
			if err != nil {
				end = start
			}
			title := []rune(it.Summary)
			if len(title) == 0 {
				title = []rune("予定")
			}
			rows = append(rows, Event{Title: string(title[:min(len(title), 160)]), Calendar: name, Start: start, End: end, Day: day})
		}
		if out.NextPageToken == "" {
			return rows, nil
		}
		pageToken = out.NextPageToken
	}
	return nil, errors.New("too many calendar pages")
}
