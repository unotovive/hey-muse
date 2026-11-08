// Package quake puts P2P 地震情報's messages on the screen: an earthquake strong enough (or in a
// prefecture somebody chose), a tsunami forecast, and the early warning, each for a while and then
// gone, or gone at a tap.
//
// The device itself holds the connection (lib/p2pquake), only while the setting is on, and no server
// of ours stands in between. What is shown is P2P 地震情報's relay of the Japan Meteorological
// Agency's messages, not the agency's warning itself, and the screen says so for the early warning.
//
// The page never stays up of itself: each one has its time (holdFor), and a timer says Changed when
// it runs out, so the screen goes back whether or not anything else happens.
package quake

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/p2pquake"
)

const (
	// seenFor is how long an id is remembered, so a message sent again is not shown again.
	seenFor = 24 * time.Hour

	// watchEvery is how often Run looks at the setting and for a test request.
	watchEvery = 2 * time.Second
)

// TestRequestPath is where `techo5 tools quake-test` leaves the code of a sample for the running
// device to show (the tool is a process of its own, with no other way in).
var TestRequestPath = layout.StateDir + "/quake-test"

// tone is a quake's chime, when there is one: two low notes, twice, apart from the alarm's and a
// reminder's.
var tone = []speaker.Note{{Freq: 440, Ms: 160}, {Freq: 330, Ms: 160}, {Freq: 440, Ms: 160}, {Freq: 330, Ms: 260}}

type Feature struct {
	mu      sync.Mutex
	seen    map[string]time.Time
	showing *p2pquake.Alert
	until   time.Time
	stop    func() bool // the expiry timer of what is showing
	gen     int         // which page the timer belongs to, so a stale one says nothing

	// Changed fires when a page comes up or goes away, by a tap or by running out.
	Changed hook.Hook[struct{}]

	// now, chime, config and after are the clock, the sound, the setting and the timer; replaced in
	// tests.
	now    func() time.Time
	chime  func()
	config func() config.Quake
	after  func(d time.Duration, f func()) (stop func() bool)
}

func build() *Feature {
	return &Feature{
		seen:   map[string]time.Time{},
		now:    time.Now,
		chime:  func() { ring.Sample(tone) },
		config: func() config.Quake { return config.Get().Quake },
		after: func(d time.Duration, f func()) func() bool {
			return time.AfterFunc(d, f).Stop
		},
	}
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() { shared = build() })
	return shared
}

func init() {
	// On the network: it reaches outside, to P2P 地震情報.
	component.Register(component.Network, Get(), component.Order(86))
}

func (f *Feature) Name() string { return "earthquake alerts" }

// shows is whether an alert is put on the screen: an earthquake at the least intensity set or more, or
// felt in a prefecture chosen; a tsunami forecast and an early warning always; a cancellation never.
func shows(cfg config.Quake, a p2pquake.Alert) bool {
	if a.Cancelled {
		return false
	}
	switch a.Code {
	case 551:
		if a.MaxScale >= cfg.QuakeMinScale() {
			return true
		}
		return slices.ContainsFunc(a.Prefs, func(p string) bool { return slices.Contains(cfg.Prefs, p) })
	case 552, 556:
		return true
	}
	return false
}

// holdFor is how long an alert stays up.
func holdFor(a p2pquake.Alert) time.Duration {
	if a.Code == 556 {
		return 60 * time.Second
	}
	return 90 * time.Second
}

// Offer is a message from P2P 地震情報: shown if it is one to show and has not been already.
func (f *Feature) Offer(a p2pquake.Alert) {
	cfg := f.config()
	if a.Cancelled {
		f.cancelled(a)
		return
	}
	if !shows(cfg, a) {
		slog.Info("quake: not shown", "code", a.Code, "id", a.ID, "max scale", a.MaxScale)
		return
	}
	if !f.firstTime(a.ID) {
		return
	}
	f.show(a, cfg.Chime)
}

// firstTime records id and reports whether it is new within seenFor. An alert without an id is
// always new.
func (f *Feature) firstTime(id string) bool {
	if id == "" {
		return true
	}
	now := f.now()
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, at := range f.seen {
		if now.Sub(at) >= seenFor {
			delete(f.seen, k)
		}
	}
	if _, ok := f.seen[id]; ok {
		return false
	}
	f.seen[id] = now
	return true
}

// show puts a up in place of whatever was, for its time.
func (f *Feature) show(a p2pquake.Alert, chime bool) {
	slog.Info("quake: shown", "code", a.Code, "id", a.ID, "place", a.Place, "max scale", a.MaxScale)
	d := holdFor(a)
	f.mu.Lock()
	if f.stop != nil {
		f.stop()
	}
	f.gen++
	gen := f.gen
	f.showing, f.until = &a, f.now().Add(d)
	f.stop = f.after(d, func() { f.expire(gen) })
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
	if chime {
		f.chime()
	}
}

// expire takes the page down when its time is up, if it is still the one the timer was set for.
func (f *Feature) expire(gen int) {
	f.mu.Lock()
	if f.gen != gen || f.showing == nil {
		f.mu.Unlock()
		return
	}
	f.showing, f.stop = nil, nil
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// cancelled is a cancellation: an early warning or a tsunami forecast taken back takes its page down.
func (f *Feature) cancelled(a p2pquake.Alert) {
	f.mu.Lock()
	match := f.showing != nil && f.showing.Code == a.Code
	f.mu.Unlock()
	if match {
		slog.Info("quake: cancelled", "code", a.Code, "id", a.ID)
		f.Dismiss()
	}
}

// Showing is the alert on the screen and when it goes, if there is one.
func (f *Feature) Showing() (p2pquake.Alert, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.showing == nil || !f.now().Before(f.until) {
		return p2pquake.Alert{}, time.Time{}, false
	}
	return *f.showing, f.until, true
}

// Dismiss takes the page down: a tap on it.
func (f *Feature) Dismiss() {
	f.mu.Lock()
	up := f.showing != nil
	if f.stop != nil {
		f.stop()
	}
	f.showing, f.stop = nil, nil
	f.gen++
	f.mu.Unlock()
	if up {
		f.Changed.Emit(struct{}{})
	}
}

// Run holds the connection while the setting is on, and shows what `techo5 tools quake-test` asks.
func (f *Feature) Run(ctx context.Context) error {
	var halt func() // stops the connection and waits for it; nil while there is none
	defer func() {
		if halt != nil {
			halt()
		}
	}()
	tick := time.NewTicker(watchEvery)
	defer tick.Stop()
	for {
		on := f.config().On
		switch {
		case on && halt == nil:
			halt = connect(ctx, f.Offer)
		case !on && halt != nil:
			slog.Info("quake: turned off")
			halt()
			halt = nil
		}
		f.testRequest()
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// connect holds the connection to P2P 地震情報 until the function it returns is called, which waits
// for it to close.
func connect(ctx context.Context, on func(p2pquake.Alert)) (halt func()) {
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p2pquake.Run(sctx, p2pquake.URL, on)
	}()
	return func() {
		cancel()
		<-done
	}
}

// testRequest shows the sample a test asked for, whatever the settings: it is for seeing the page.
func (f *Feature) testRequest() {
	b, err := os.ReadFile(TestRequestPath)
	if err != nil {
		return
	}
	_ = os.Remove(TestRequestPath)
	code, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || !SampleCode(code) {
		slog.Warn("quake: unknown test request", "request", string(b))
		return
	}
	slog.Info("quake: test", "code", code)
	f.show(Sample(code), f.config().Chime)
}

//go:embed sample/*.json
var samples embed.FS

var sampleN atomic.Int64

// SampleCode is whether Sample has one of code.
func SampleCode(code int) bool { return code == 551 || code == 552 || code == 556 }

// Sample is a fixed message of code (551, 552 or 556) for trying the page, with an id of its own each
// time so it is never taken for one already shown. Any other code is the zero Alert.
func Sample(code int) p2pquake.Alert {
	if !SampleCode(code) {
		return p2pquake.Alert{}
	}
	b, err := samples.ReadFile(fmt.Sprintf("sample/%d.json", code))
	if err != nil {
		panic(err) // embedded above
	}
	a, _, err := p2pquake.Parse(b)
	if err != nil {
		panic(err)
	}
	a.ID = fmt.Sprintf("quake-test-%d-%d", time.Now().UnixNano(), sampleN.Add(1))
	return a
}
