package display

import "testing"

func TestCoverStopsRingBeforeTurn(t *testing.T) {
	canceled := 0
	if !coverStops(func() bool { return true }, func() bool { return true }, func() { canceled++ }) || canceled != 0 {
		t.Fatal("ring should take the cover")
	}
	if !coverStops(func() bool { return false }, func() bool { return true }, func() { canceled++ }) || canceled != 1 {
		t.Fatal("turn should be canceled")
	}
	if coverStops(func() bool { return false }, func() bool { return false }, func() { canceled++ }) || canceled != 1 {
		t.Fatal("nothing to stop")
	}
}
