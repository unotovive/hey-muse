// Package p2pquake reads P2P 地震情報's API v2 (https://www.p2pquake.net/develop/json_api_v2/): the
// earthquake, tsunami and early-warning messages the Japan Meteorological Agency issues, relayed by
// P2P 地震情報 over a WebSocket.
//
// Only what the screen shows is kept: 551 (地震情報), 552 (津波予報) and 556 (緊急地震速報（警報）).
// Every other code (561, the peers' own reports, and the rest) is left alone.
package p2pquake

import (
	"encoding/json"
	"slices"
	"strconv"
	"time"
)

// jst is Japan's time, fixed rather than loaded: the device may have no zone database, and Japan has
// no daylight saving.
var jst = time.FixedZone("JST", 9*60*60)

// The layouts of the times in a message: the message's own with milliseconds, an earthquake's without.
const (
	timeLayout  = "2006/01/02 15:04:05.000"
	shortLayout = "2006/01/02 15:04:05"
)

// Alert is one message worth showing.
type Alert struct {
	ID        string
	Code      int       // 551, 552, 556
	Issued    time.Time // 551: when the earthquake struck, 556: its origin time, 552: the message's "time"; in JST
	Place     string    // earthquake.hypocenter.name (551/556)
	MaxScale  int       // 551: earthquake.maxScale (10..70; -1 unknown)
	Magnitude float64   // 551 (-1 unknown)
	Depth     int       // 551, in km (-1 unknown)
	Prefs     []string  // 551: points[].pref without repeats, 552: areas[].name, 556: areas[].pref without repeats
	Grades    []string  // 552: areas[].grade, one for each of Prefs
	Cancelled bool      // 552/556
}

// message is the part of every code's JSON that is read.
type message struct {
	ID        string `json:"id"`
	OldID     string `json:"_id"` // the REST API's name for it
	Code      int    `json:"code"`
	Time      string `json:"time"`
	Cancelled bool   `json:"cancelled"`

	Earthquake struct {
		Time       string `json:"time"`       // 551
		OriginTime string `json:"originTime"` // 556
		MaxScale   *int   `json:"maxScale"`
		Hypocenter struct {
			Name      string   `json:"name"`
			Magnitude *float64 `json:"magnitude"`
			Depth     *int     `json:"depth"`
		} `json:"hypocenter"`
	} `json:"earthquake"`

	Points []struct {
		Pref string `json:"pref"`
	} `json:"points"`

	Areas []struct {
		Pref  string `json:"pref"`
		Name  string `json:"name"`
		Grade string `json:"grade"`
	} `json:"areas"`
}

// Parse reads one message. ok is false for a code that is not shown (561 and the rest); a message that
// is not JSON, or not the shape of one, is an error.
func Parse(data []byte) (Alert, bool, error) {
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		return Alert{}, false, err
	}
	switch m.Code {
	case 551, 552, 556:
	default:
		return Alert{}, false, nil
	}
	a := Alert{ID: m.ID, Code: m.Code, Cancelled: m.Cancelled, MaxScale: -1, Magnitude: -1, Depth: -1}
	if a.ID == "" {
		a.ID = m.OldID
	}
	a.Issued = parseTime(m.Time)
	eq := m.Earthquake
	switch m.Code {
	case 551:
		if t := parseTime(eq.Time); !t.IsZero() {
			a.Issued = t
		}
		if eq.MaxScale != nil {
			a.MaxScale = *eq.MaxScale
		}
		for _, p := range m.Points {
			a.Prefs = addOnce(a.Prefs, p.Pref)
		}
	case 552:
		for _, ar := range m.Areas {
			a.Prefs = append(a.Prefs, ar.Name)
			a.Grades = append(a.Grades, ar.Grade)
		}
	case 556:
		if t := parseTime(eq.OriginTime); !t.IsZero() {
			a.Issued = t
		}
		for _, ar := range m.Areas {
			a.Prefs = addOnce(a.Prefs, ar.Pref)
		}
	}
	if m.Code != 552 {
		a.Place = eq.Hypocenter.Name
		if eq.Hypocenter.Magnitude != nil {
			a.Magnitude = *eq.Hypocenter.Magnitude
		}
		if eq.Hypocenter.Depth != nil {
			a.Depth = *eq.Hypocenter.Depth
		}
	}
	return a, true, nil
}

func addOnce(list []string, s string) []string {
	if s == "" || slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// parseTime reads either layout, in JST; the zero time when it is neither.
func parseTime(s string) time.Time {
	for _, layout := range []string{timeLayout, shortLayout} {
		if t, err := time.ParseInLocation(layout, s, jst); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ScaleLabel is a seismic intensity (震度) as it is written: 45 is "5弱", -1 (or anything not a
// scale) "不明". 46 is the agency's "5弱以上と推定" for a place it has no reading from yet.
func ScaleLabel(scale int) string {
	switch scale {
	case 10, 20, 30, 40:
		return strconv.Itoa(scale / 10)
	case 45:
		return "5弱"
	case 46:
		return "5弱以上"
	case 50:
		return "5強"
	case 55:
		return "6弱"
	case 60:
		return "6強"
	case 70:
		return "7"
	}
	return "不明"
}
