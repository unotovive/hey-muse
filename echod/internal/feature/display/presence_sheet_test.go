//go:build !dot

package display

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestPresenceRowsHaveGesturesWithoutDetection(t *testing.T) {
	for _, on := range []bool{false, true} {
		rows := presenceRows(config.Presence{On: false, Gestures: on})
		var found bool
		for _, r := range rows {
			if r.id == "gestures" {
				found = true
				if r.kind != ctlToggle || r.on != on || r.label != tr("gestures_label") {
					t.Fatalf("gestures row = %+v, want toggle on=%v", r, on)
				}
			}
			if r.id == "awayoff" {
				t.Fatal("away row shown with detection off")
			}
		}
		if !found {
			t.Fatal("no gestures row with presence detection off")
		}
	}
}
