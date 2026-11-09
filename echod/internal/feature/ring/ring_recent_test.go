package ring

import (
	"testing"
	"time"
)

func TestRangWithin(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	defer func() { state.now = time.Now }()
	state.mu.Lock()
	state.quiet = time.Time{}
	state.mu.Unlock()
	if RangWithin(time.Minute) {
		t.Fatal("nothing has rung")
	}
	done := Sounding()
	if !RangWithin(time.Minute) {
		t.Fatal("a ring is sounding")
	}
	done()
	now = now.Add(30 * time.Second)
	if !RangWithin(time.Minute) {
		t.Fatal("the ring ended 30 seconds ago")
	}
	now = now.Add(time.Minute)
	if RangWithin(time.Minute) {
		t.Fatal("the ring ended 90 seconds ago")
	}
}
