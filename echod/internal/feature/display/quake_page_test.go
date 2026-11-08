//go:build !dot && !spot

package display

import (
	"image"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/quake"
	"github.com/HuskerMinion/techo5/echod/internal/lib/p2pquake"
)

func TestQuakeText(t *testing.T) {
	q := quakeText(quake.Sample(551))
	if q.head != "地震情報 震度4" || q.note != "" {
		t.Errorf("551 head %q note %q", q.head, q.note)
	}
	if q.where != "千葉県北西部  11月9日 10:12 発生" {
		t.Errorf("551 where %q", q.where)
	}
	if !slices.Equal(q.detail, []string{"M5.0  深さ 約60km", "千葉県、東京都、神奈川県"}) {
		t.Errorf("551 detail %q", q.detail)
	}

	q = quakeText(quake.Sample(556))
	if q.head != "緊急地震速報（警報）※参考情報" || q.note != "気象庁の警報そのものではありません" {
		t.Errorf("556 head %q note %q", q.head, q.note)
	}
	if q.where != "千葉県北西部  11月9日 10:12 発生" || !slices.Equal(q.detail, []string{"千葉、東京"}) {
		t.Errorf("556 where %q detail %q", q.where, q.detail)
	}

	q = quakeText(quake.Sample(552))
	if q.head != "津波予報" || q.where != "11月9日 10:20 発表" {
		t.Errorf("552 head %q where %q", q.head, q.where)
	}
	if !slices.Equal(q.detail, []string{"津波注意報: 千葉県九十九里・外房、伊豆諸島"}) {
		t.Errorf("552 detail %q", q.detail)
	}

	// What the agency does not know is said so, not drawn as a number.
	q = quakeText(p2pquake.Alert{Code: 551, MaxScale: -1, Magnitude: -1, Depth: 0})
	if q.head != "地震情報 震度不明" || !slices.Equal(q.detail, []string{"M不明  深さ ごく浅い"}) {
		t.Errorf("unknowns: %q %q", q.head, q.detail)
	}
}

// drawnQuake is the quake page for a, drawn the way draw() does on a fresh canvas.
func drawnQuake(t *testing.T, a p2pquake.Alert) *renderer {
	t.Helper()
	r := testRenderer()
	r.useFont()
	fillCanvas(r.dst, walnut)
	r.quakePage(scene{now: time.Date(2026, 11, 9, 10, 15, 0, 0, time.UTC), quake: a, showQuake: true})
	return r
}

func notBackground(img *image.RGBA, rect image.Rectangle) int {
	n := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if img.RGBAAt(x, y) != walnut {
				n++
			}
		}
	}
	return n
}

func TestQuakePageDraws(t *testing.T) {
	for _, code := range []int{551, 556, 552} {
		r := drawnQuake(t, quake.Sample(code))
		b := r.dst.Bounds()
		if n := notBackground(r.dst, b); n < 2000 {
			t.Errorf("%d: only %d pixels drawn", code, n)
		}
		// The words reach the foot: the hint and the source are drawn there.
		foot := image.Rect(b.Min.X, b.Max.Y-r.s(70), b.Max.X, b.Max.Y)
		if n := notBackground(r.dst, foot); n < 100 {
			t.Errorf("%d: nothing at the foot (%d pixels)", code, n)
		}
	}
}

func TestRedClockGivesWayToTheQuakePage(t *testing.T) {
	s := scene{phase: "idle", redClock: true}
	if !s.redClockFace() {
		t.Fatal("the red clock is not up to begin with")
	}
	s.showQuake = true
	if s.redClockFace() {
		t.Error("the red clock is drawn over an earthquake")
	}
}

func TestQuakeRow(t *testing.T) {
	for _, on := range []bool{false, true} {
		rows := quakeRows(config.Quake{On: on})
		var found bool
		for _, r := range rows {
			if r.id == "quake" {
				found = true
				if r.kind != ctlToggle || r.on != on || r.label != tr("quake_label") {
					t.Errorf("quake row %+v", r)
				}
				if !strings.Contains(r.sub, "P2P") {
					t.Errorf("quake row says nothing of where it comes from: %q", r.sub)
				}
			}
		}
		if !found {
			t.Error("no quake row")
		}
	}
}
