//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/p2pquake"
)

// An earthquake, a tsunami forecast or an early warning from P2P 地震情報 (feature/quake): the whole
// screen, under a call and a ring and over everything else, until its time runs out or it is tapped.
// The words are Japanese whatever the screen's language: the messages, their places and the agency's
// terms are, and a translation of 震度 would be a different scale.

var (
	quakeRed = color.RGBA{0xd9, 0x3b, 0x2f, 0xff}
	quakeJST = time.FixedZone("JST", 9*60*60)
)

// quakeLines are the page's words: the kind of message, the early warning's disclaimer, where and
// when, and the rest.
type quakeLines struct {
	head   string
	note   string
	where  string
	detail []string
}

func quakeText(a p2pquake.Alert) quakeLines {
	var q quakeLines
	when := ""
	if !a.Issued.IsZero() {
		when = a.Issued.In(quakeJST).Format("1月2日 15:04")
	}
	switch a.Code {
	case 551:
		q.head = "地震情報 震度" + p2pquake.ScaleLabel(a.MaxScale)
		q.where = joinNonEmpty("  ", a.Place, suffixed(when, " 発生"))
		q.detail = []string{magnitude(a.Magnitude) + "  深さ " + depth(a.Depth)}
		if len(a.Prefs) > 0 {
			q.detail = append(q.detail, strings.Join(a.Prefs, "、"))
		}
	case 556:
		q.head = "緊急地震速報（警報）※参考情報"
		q.note = "気象庁の警報そのものではありません"
		q.where = joinNonEmpty("  ", a.Place, suffixed(when, " 発生"))
		if len(a.Prefs) > 0 {
			q.detail = []string{strings.Join(a.Prefs, "、")}
		}
	case 552:
		q.head = "津波予報"
		q.where = suffixed(when, " 発表")
		q.detail = tsunamiLines(a)
	}
	return q
}

func suffixed(s, suffix string) string {
	if s == "" {
		return ""
	}
	return s + suffix
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func magnitude(m float64) string {
	if m < 0 {
		return "M不明"
	}
	return fmt.Sprintf("M%.1f", m)
}

// depth is the agency's way of saying it: 0 is "ごく浅い", a depth a rounded "約".
func depth(km int) string {
	switch {
	case km < 0:
		return "不明"
	case km == 0:
		return "ごく浅い"
	}
	return fmt.Sprintf("約%dkm", km)
}

// tsunamiGrades are a tsunami forecast's grades, the gravest first.
var tsunamiGrades = []struct{ grade, label string }{
	{"MajorWarning", "大津波警報"},
	{"Warning", "津波警報"},
	{"Watch", "津波注意報"},
	{"Unknown", "津波予報（不明）"},
}

// tsunamiLines are a forecast's areas, a line for each grade, the gravest first.
func tsunamiLines(a p2pquake.Alert) []string {
	var lines []string
	for _, g := range tsunamiGrades {
		var areas []string
		for i, name := range a.Prefs {
			grade := "Unknown"
			if i < len(a.Grades) {
				grade = a.Grades[i]
			}
			known := grade == "MajorWarning" || grade == "Warning" || grade == "Watch"
			if grade == g.grade || (!known && g.grade == "Unknown") {
				areas = append(areas, name)
			}
		}
		if len(areas) > 0 {
			lines = append(lines, g.label+": "+strings.Join(areas, "、"))
		}
	}
	return lines
}

// quakeDetailLines is as many lines as the rest is given; a longer one ends in an ellipsis.
const quakeDetailLines = 4

func (r *renderer) quakePage(s scene) {
	q := quakeText(s.quake)
	mark := amber
	if s.quake.Code != 551 {
		mark = quakeRed
	}
	r.fillRect(r.dst.Rect, walnut)
	r.fillRect(image.Rect(0, 0, r.s(12), r.h), mark)

	x := r.s(40)
	width := r.w - x - r.margin
	r.text(r.title, r.clipTo(r.title, q.head, width), x, r.s(80), mark)
	y := r.s(80)
	if q.note != "" {
		y += r.s(40)
		r.text(r.small, r.clipTo(r.small, q.note, width), x, y, amber)
	}
	if q.where != "" {
		y += r.s(58)
		r.text(r.body, r.clipTo(r.body, q.where, width), x, y, cream)
	}

	var lines []string
	for _, d := range q.detail {
		lines = append(lines, wrapText(r.small, d, width)...)
	}
	if len(lines) > quakeDetailLines {
		lines = append(lines[:quakeDetailLines-1], r.clipTo(r.small, strings.Join(lines[quakeDetailLines-1:], ""), width))
	}
	y += r.s(16)
	for _, l := range lines {
		y += r.s(44)
		r.text(r.small, r.clipTo(r.small, l, width), x, y, cream)
	}

	foot := r.h - r.s(36)
	r.text(r.tiny, "タップで閉じる", x, foot, dim)
	r.rightText(r.tiny, "出典: P2P地震情報", r.w-r.margin, foot, dim)
}

// withQuake adds the earthquake page's switch to the Display card.
func withQuake(rows []settingRow) []settingRow {
	return append(rows, quakeRows(config.Get().Quake)...)
}

func quakeRows(q config.Quake) []settingRow {
	return []settingRow{
		{label: tr("quake_heading"), kind: ctlHeading},
		{id: "quake", label: tr("quake_label"), sub: tr("quake_description"), kind: ctlToggle, on: q.On},
	}
}
