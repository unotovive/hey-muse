package p2pquake

import (
	"os"
	"slices"
	"testing"
	"time"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParse551(t *testing.T) {
	a, ok, err := Parse(load(t, "551.json"))
	if err != nil || !ok {
		t.Fatalf("Parse = ok %v, err %v", ok, err)
	}
	if a.Code != 551 || a.ID != "6730a8f2c4e1d2a3b4c5d6e7" {
		t.Errorf("code %d id %q", a.Code, a.ID)
	}
	if a.MaxScale != 40 || a.Place != "千葉県北西部" || a.Magnitude != 5.0 || a.Depth != 60 {
		t.Errorf("earthquake %+v", a)
	}
	if want := []string{"千葉県", "東京都", "神奈川県"}; !slices.Equal(a.Prefs, want) {
		t.Errorf("prefs %v, want %v", a.Prefs, want)
	}
	// The earthquake's own time, in Japan's time, whatever the device's zone.
	if a.Issued.Location().String() != "Asia/Tokyo" && a.Issued.Location().String() != "JST" {
		t.Errorf("zone %v", a.Issued.Location())
	}
	if want := time.Date(2026, 11, 9, 1, 12, 0, 0, time.UTC); !a.Issued.Equal(want) {
		t.Errorf("issued %v, want %v", a.Issued, want)
	}
	if a.Cancelled {
		t.Error("cancelled")
	}
}

func TestParse552And556(t *testing.T) {
	a, ok, err := Parse(load(t, "552.json"))
	if err != nil || !ok {
		t.Fatalf("552: ok %v, err %v", ok, err)
	}
	if a.Code != 552 || a.Cancelled {
		t.Errorf("552 %+v", a)
	}
	if want := []string{"千葉県九十九里・外房", "伊豆諸島"}; !slices.Equal(a.Prefs, want) {
		t.Errorf("552 areas %v", a.Prefs)
	}
	if want := []string{"Watch", "Watch"}; !slices.Equal(a.Grades, want) {
		t.Errorf("552 grades %v", a.Grades)
	}
	if want := time.Date(2026, 11, 9, 1, 20, 5, 123e6, time.UTC); !a.Issued.Equal(want) {
		t.Errorf("552 issued %v", a.Issued)
	}

	a, ok, err = Parse(load(t, "556.json"))
	if err != nil || !ok {
		t.Fatalf("556: ok %v, err %v", ok, err)
	}
	if a.Code != 556 || a.Cancelled || a.Place != "千葉県北西部" {
		t.Errorf("556 %+v", a)
	}
	if want := []string{"千葉", "東京"}; !slices.Equal(a.Prefs, want) {
		t.Errorf("556 prefs %v", a.Prefs)
	}
	if want := time.Date(2026, 11, 9, 1, 12, 0, 0, time.UTC); !a.Issued.Equal(want) {
		t.Errorf("556 issued %v", a.Issued)
	}

	a, ok, err = Parse([]byte(`{"id":"x","code":556,"time":"2026/11/09 10:12:30.000","cancelled":true}`))
	if err != nil || !ok || !a.Cancelled {
		t.Errorf("cancelled 556: %+v ok %v err %v", a, ok, err)
	}
}

func TestParseIgnoresOtherCodes(t *testing.T) {
	if _, ok, err := Parse(load(t, "561.json")); ok || err != nil {
		t.Errorf("561: ok %v, err %v", ok, err)
	}
	if _, _, err := Parse([]byte(`{"code":`)); err == nil {
		t.Error("broken JSON parsed")
	}
}

func TestScaleLabel(t *testing.T) {
	for scale, want := range map[int]string{
		10: "1", 20: "2", 30: "3", 40: "4", 45: "5弱", 46: "5弱以上", 50: "5強",
		55: "6弱", 60: "6強", 70: "7", -1: "不明", 0: "不明", 99: "不明",
	} {
		if got := ScaleLabel(scale); got != want {
			t.Errorf("ScaleLabel(%d) = %q, want %q", scale, got, want)
		}
	}
}
