package quake

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/p2pquake"
)

func TestShows(t *testing.T) {
	min30 := config.Quake{MinScale: 30}
	chiba := config.Quake{MinScale: 30, Prefs: []string{"千葉県"}}
	for _, c := range []struct {
		name string
		cfg  config.Quake
		a    p2pquake.Alert
		want bool
	}{
		{"551 at 3, least 3", min30, p2pquake.Alert{Code: 551, MaxScale: 30}, true},
		{"551 at 2, least 3", min30, p2pquake.Alert{Code: 551, MaxScale: 20}, false},
		{"551 at 2, the default least", config.Quake{}, p2pquake.Alert{Code: 551, MaxScale: 20}, false},
		{"551 at 2 in a chosen prefecture", chiba, p2pquake.Alert{Code: 551, MaxScale: 20, Prefs: []string{"東京都", "千葉県"}}, true},
		{"551 at 2 elsewhere", chiba, p2pquake.Alert{Code: 551, MaxScale: 20, Prefs: []string{"東京都"}}, false},
		{"551 of unknown intensity", min30, p2pquake.Alert{Code: 551, MaxScale: -1}, false},
		{"556", min30, p2pquake.Alert{Code: 556}, true},
		{"552", min30, p2pquake.Alert{Code: 552}, true},
		{"556 cancelled", min30, p2pquake.Alert{Code: 556, Cancelled: true}, false},
		{"552 cancelled", min30, p2pquake.Alert{Code: 552, Cancelled: true}, false},
		{"561", min30, p2pquake.Alert{Code: 561}, false},
	} {
		if got := shows(c.cfg, c.a); got != c.want {
			t.Errorf("%s: shows = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHoldFor(t *testing.T) {
	for code, want := range map[int]time.Duration{551: 90 * time.Second, 552: 90 * time.Second, 556: 60 * time.Second} {
		if got := holdFor(p2pquake.Alert{Code: code}); got != want {
			t.Errorf("holdFor(%d) = %v, want %v", code, got, want)
		}
	}
}

// fake is a Feature on a clock the test moves, with its chimes and its expiry timer counted.
type fake struct {
	*Feature
	at      time.Time
	cfg     config.Quake
	chimes  int
	changes int
	expire  func() // the last timer set
	stopped int
}

func newFake(cfg config.Quake) *fake {
	f := &fake{at: time.Date(2026, 11, 9, 10, 15, 0, 0, time.UTC), cfg: cfg}
	f.Feature = build()
	f.now = func() time.Time { return f.at }
	f.chime = func() { f.chimes++ }
	f.config = func() config.Quake { return f.cfg }
	f.after = func(_ time.Duration, fn func()) func() bool {
		f.expire = fn
		return func() bool { f.stopped++; return true }
	}
	f.Changed.Listen(func(struct{}) { f.changes++ })
	return f
}

func TestOfferDedupesAndExpires(t *testing.T) {
	f := newFake(config.Quake{MinScale: 30})
	a := Sample(551)
	f.Offer(a)
	f.Offer(a)
	if f.changes != 1 {
		t.Fatalf("Changed %d times for one alert offered twice", f.changes)
	}
	if f.chimes != 0 {
		t.Errorf("chimed %d times with the chime off", f.chimes)
	}
	got, until, up := f.Showing()
	if !up || got.ID != a.ID || !until.Equal(f.at.Add(90*time.Second)) {
		t.Fatalf("Showing = %+v, %v, %v", got, until, up)
	}

	f.at = f.at.Add(89 * time.Second)
	if _, _, up := f.Showing(); !up {
		t.Error("gone before 90 s")
	}
	// The timer is what takes the page down when nothing else happens: it has to say so.
	f.at = f.at.Add(time.Second)
	f.expire()
	if _, _, up := f.Showing(); up {
		t.Error("still up after 90 s")
	}
	if f.changes != 2 {
		t.Errorf("Changed %d times, want 2 (up, then down when it ran out)", f.changes)
	}

	// The same id a day later is news again; within the day it is not.
	f.at = f.at.Add(23 * time.Hour)
	f.Offer(a)
	if _, _, up := f.Showing(); up {
		t.Error("the same id shown again within 24 h")
	}
	f.at = f.at.Add(time.Hour)
	f.Offer(a)
	if _, _, up := f.Showing(); !up {
		t.Error("the same id not shown again after 24 h")
	}

	// With the chime on, once per alert shown.
	c := newFake(config.Quake{Chime: true})
	w := Sample(556)
	c.Offer(w)
	c.Offer(w)
	if c.chimes != 1 {
		t.Errorf("chimed %d times, want 1", c.chimes)
	}
	if _, until, _ := c.Showing(); !until.Equal(c.at.Add(60 * time.Second)) {
		t.Errorf("556 until %v", until)
	}

	// One that is not shown does nothing.
	n := newFake(config.Quake{MinScale: 50, Chime: true})
	n.Offer(Sample(551))
	if _, _, up := n.Showing(); up || n.changes != 0 || n.chimes != 0 {
		t.Errorf("a 震度 4 shown with 5強 the least: changes %d chimes %d", n.changes, n.chimes)
	}
}

func TestDismiss(t *testing.T) {
	f := newFake(config.Quake{})
	f.Dismiss() // nothing up: nothing to say
	if f.changes != 0 {
		t.Errorf("Changed on dismissing nothing")
	}
	f.Offer(Sample(552))
	f.Dismiss()
	if _, _, up := f.Showing(); up {
		t.Error("still up after Dismiss")
	}
	if f.changes != 2 || f.stopped == 0 {
		t.Errorf("changes %d, timer stopped %d", f.changes, f.stopped)
	}
	// The expiry of a page already gone says nothing more.
	f.expire()
	if f.changes != 2 {
		t.Errorf("Changed again by a stale timer")
	}
}

func TestCancelledEarlyWarningTakesItsPageDown(t *testing.T) {
	f := newFake(config.Quake{})
	f.Offer(Sample(556))
	f.Offer(p2pquake.Alert{ID: "cancel", Code: 556, Cancelled: true})
	if _, _, up := f.Showing(); up {
		t.Error("a cancelled early warning left up")
	}
}

func TestSample(t *testing.T) {
	for _, code := range []int{551, 552, 556} {
		if a := Sample(code); a.Code != code || a.ID == "" {
			t.Errorf("Sample(%d) = %+v", code, a)
		}
	}
	// Each run of the test command is shown, not taken for the one before.
	if Sample(551).ID == Sample(551).ID {
		t.Error("two samples share an id")
	}
}

func TestRunShowsATestRequest(t *testing.T) {
	old := TestRequestPath
	TestRequestPath = filepath.Join(t.TempDir(), "quake-test")
	t.Cleanup(func() { TestRequestPath = old })
	if err := os.WriteFile(TestRequestPath, []byte("556\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := build()
	f.config = func() config.Quake { return config.Quake{} } // off: no connection, the test still shows
	f.chime = func() {}
	up := make(chan struct{}, 1)
	f.Changed.Listen(func(struct{}) {
		select {
		case up <- struct{}{}:
		default:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- f.Run(ctx) }()
	select {
	case <-up:
	case <-time.After(5 * time.Second):
		t.Fatal("the test request was not shown")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if a, _, ok := f.Showing(); !ok || a.Code != 556 {
		t.Errorf("Showing = %+v, %v", a, ok)
	}
	if _, err := os.Stat(TestRequestPath); !os.IsNotExist(err) {
		t.Errorf("the request was left behind: %v", err)
	}
}
