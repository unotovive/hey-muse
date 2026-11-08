//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// weatherShow is how long the weather page stays after the question that brought it up.
const weatherShow = 60 * time.Second

// weatherPage is today large on the left and the next days as columns on the right.
// weatherPageKept is weatherPage, drawn only when what it shows has changed and copied otherwise: the
// page is most of a frame's cost (its faded icon is a whole panel blended), and a moving sky asks for a
// frame every 83 ms. What it shows is the reading, the forecast, the corner clock and the colors.
func (r *renderer) weatherPageKept(s scene) {
	key := fmt.Sprint(s.now.Format("2006-01-02 15:04"), s.weather, s.forecast, s.forecastAt, s.weatherPlace, r.w, r.h, walnut, cream, amber, dim, ember)
	if key == r.weatherKey && len(r.weatherKept) == len(r.dst.Pix) {
		copy(r.dst.Pix, r.weatherKept)
		return
	}
	r.weatherPage(s)
	r.weatherKept = append(r.weatherKept[:0], r.dst.Pix...)
	r.weatherKey = key
}

func (r *renderer) weatherPage(s scene) {
	draw.Draw(r.dst, r.dst.Bounds(), image.NewUniform(homeInk.bg), image.Point{}, draw.Src)
	text := func(size int, bold bool, line string, x, y int, c color.RGBA) {
		r.text(r.styleFace(bold, size), line, r.s(x), r.s(y), c)
	}
	text(27, true, "今日の天気", 28, 42, homeInk.text)
	text(22, false, s.weatherPlace, 208, 42, homeInk.mint)
	text(24, false, s.now.Format("15:04"), 856, 42, homeInk.muted)
	today := s.now.Format("2006-01-02")
	var day *hass.Day
	for i := range s.forecast {
		if s.forecast[i].When.Local().Format("2006-01-02") == today {
			day = &s.forecast[i]
			break
		}
	}
	cond := s.weather.Condition
	if day != nil {
		cond = day.Condition
	}
	if cond != "" {
		r.weatherIcon(cond, r.s(89), r.s(136), r.s(102))
	}
	current := s.weather.Temp
	if current == "" {
		current = "—"
	}
	text(18, false, "現在の外気温", 166, 92, homeInk.muted)
	text(56, true, current, 164, 154, homeInk.text)
	if day != nil {
		text(26, true, locale.Sky(day.Condition, "ja"), 338, 100, homeInk.text)
		text(23, false, fmt.Sprintf("最高 %.0f° / 最低 %.0f°", day.High, day.Low), 338, 141, homeInk.muted)
		text(24, true, "降水確率 "+rainChance(day.Rain), 338, 183, homeInk.mint)
	} else {
		text(23, false, "今日の予報を取得できません", 338, 128, homeInk.muted)
	}
	text(23, true, "7日間の予報", 28, 232, homeInk.text)
	for i := 0; i < 7; i++ {
		at := s.now.AddDate(0, 0, i)
		var d *hass.Day
		for j := range s.forecast {
			if s.forecast[j].When.Local().Format("2006-01-02") == at.Format("2006-01-02") {
				d = &s.forecast[j]
				break
			}
		}
		x := 26 + i*131
		b := image.Rect(r.s(x), r.s(248), r.s(x+122), r.s(408))
		r.roundFill(b, 12, homeInk.panel, homeInk.panel)
		label := fmt.Sprintf("%d/%d(%s)", at.Month(), at.Day(), locale.ShortWeekday(at, "ja"))
		if i == 0 {
			label = "今日"
		}
		text(19, true, label, x+9, 276, homeInk.text)
		if d == nil {
			text(20, false, "未取得", x+29, 338, homeInk.muted)
			continue
		}
		r.weatherIcon(d.Condition, r.s(x+61), r.s(308), r.s(44))
		text(20, true, fmt.Sprintf("%.0f° / %.0f°", d.High, d.Low), x+12, 355, homeInk.text)
		text(19, false, rainChance(d.Rain), x+37, 389, homeInk.mint)
	}
	text(16, false, "各日の最高 / 最低気温・降水確率（日内最大）", 28, 429, homeInk.muted)
	stamp := "未取得"
	if !s.forecastAt.IsZero() {
		stamp = "取得 " + s.forecastAt.Local().Format("1/2 15:04")
	}
	text(15, false, "Open-Meteo  ·  "+stamp, 28, 464, homeInk.muted)
	text(19, false, "画面をタップして戻る", 510, 464, homeInk.muted)
	r.weatherToggle("雨雲")
}

func rainChance(p int) string {
	if p < 0 {
		return "—"
	}
	return fmt.Sprintf("%d%%", p)
}

// forecastDays is a type alias for the scene.
type forecastDays = []hass.Day

// radarStep is how long each frame of the rain map's loop shows; the newest holds for three.
const radarStep = 600 * time.Millisecond

// weatherButton is the button at the foot of the weather page that turns between the forecast and
// the rain map.
func (r *renderer) weatherButton() image.Rectangle {
	return image.Rect(r.w-r.margin-r.s(150), r.h-r.s(46), r.w-r.margin, r.h-r.s(6))
}

// weatherToggle is that button drawn, with its label centered in it. The box is scaled along with the
// type: it used to be a fixed 150 wide, so on a Show 8 the label grew and the box did not, and
// "Forecast" ran out of both ends of it.
func (r *renderer) weatherToggle(label string) {
	b := r.weatherButton()
	r.roundFill(b, float64(r.s(10)), homeInk.button, homeInk.button)
	f := r.styleFace(false, 22)
	r.text(f, label, b.Min.X+(b.Dx()-r.width(f, label))/2, b.Max.Y-r.s(12), homeInk.text)
}

// radarPage is the rain map filling the panel, the loop's time and the credits over it, and home
// marked in the middle.
func (r *renderer) radarPage(s scene) {
	v := s.radar
	fillCanvas(r.dst, walnut)
	if len(v.Frames) == 0 {
		r.text(r.small, tr("ui_radar"), r.margin, r.margin+r.s(26), amber)
		msg := tr("loading_the_rain_map")
		if !v.Loading && v.Problem != "" {
			msg = tr("no_rain_map") + v.Problem
		}
		for i, line := range r.wrap(r.body, msg, r.w-2*r.margin) {
			r.text(r.body, line, r.margin, r.s(200)+i*r.s(44), dim)
		}
		r.weatherToggle("天気")
		return
	}

	// The loop: each past frame for a step, the newest for three.
	n := len(v.Frames)
	cycle := int64(n+2) * radarStep.Milliseconds()
	i := int(s.now.UnixMilli() % cycle / radarStep.Milliseconds())
	if i >= n {
		i = n - 1
	}
	f := v.Frames[i]
	if f.Image.Rect.Size() == r.dst.Rect.Size() {
		draw.Draw(r.dst, r.dst.Bounds(), f.Image, image.Point{}, draw.Src)
	} else {
		// Made before a panel's screen said its size, and about to be made again at it: scaled for now
		// rather than filling a corner.
		xdraw.ApproxBiLinear.Scale(r.dst, r.dst.Rect, f.Image, cover(f.Image.Rect, r.dst.Rect), draw.Src, nil)
	}

	// The weather alerts' shapes, under everything written over the map.
	alertShapes(r.dst, v, s.alerts.Near, image.Point{}, 1, nil, &r.shapes)
	pillY := r.margin + r.s(42) + r.s(10)
	pills, _, _ := r.alertPills(s.alerts.Here, r.margin, pillY)

	// Home: a ring in the accent with a dark edge, readable over rain and map alike.
	h := v.Home
	r.ring(h, r.s(9), walnut)
	r.ring(h, r.s(7), amber)
	r.ring(h, r.s(5), amber)

	// The towns, where there is room: clear of the bands, the Forecast button and the home marker.
	keep := []image.Rectangle{
		image.Rect(0, 0, r.w, r.margin+r.s(42)),
		image.Rect(0, r.h-r.s(34), r.w, r.h),
		image.Rect(r.w-r.s(200), r.h-r.s(110), r.w, r.h),
		image.Rect(h.X-r.s(12), h.Y-r.s(12), h.X+r.s(12), h.Y+r.s(12)),
	}
	keep = append(keep, pills...)
	if v.Note != "" {
		// The note's line over the credits, as wide as the note draws.
		room := r.weatherButton().Min.X - r.s(12) - r.margin
		keep = append(keep, image.Rect(0, r.h-r.s(62), r.margin+r.width(r.tiny, clipText(r, r.tiny, v.Note, room))+r.s(10), r.h-r.s(34)))
	}
	drawPlaces(r.dst, r.tiny, v.Places, keep, func(b image.Rectangle) bool { return b.In(r.dst.Bounds()) }, 14)

	// The time of the frame, and the clock, on dark bands so they read over the map.
	shade := func(rect image.Rectangle) {
		washOver(r.dst, rect, color.RGBA{0, 0, 0, 150})
	}
	label := "Radar  " + clockText(f.At.Local())
	if i == n-1 {
		label += "  (latest)"
	}
	shade(image.Rect(0, 0, r.w, r.margin+r.s(42)))
	r.text(r.small, label, r.margin, r.margin+r.s(26), amber)
	r.cornerClock(s)
	r.drawAlertPills(s.alerts.Here, r.margin, pillY)

	credit := clipText(r, r.tiny, v.Credit, r.w-2*r.margin)
	shade(image.Rect(0, r.h-r.s(34), r.w, r.h))
	r.text(r.tiny, credit, r.margin, r.h-r.s(10), dim)
	if v.Note != "" {
		// Why the source is not the one chosen: a line of its own over the credits, clear of the button.
		room := r.weatherButton().Min.X - r.s(12) - r.margin
		note := clipText(r, r.tiny, v.Note, room)
		shade(image.Rect(0, r.h-r.s(62), r.margin+r.width(r.tiny, note)+r.s(10), r.h-r.s(34)))
		r.text(r.tiny, note, r.margin, r.h-r.s(40), amber)
	}
	r.weatherToggle("天気")
}

// ring is a circle outline one pixel wide.
func (r *renderer) ring(c image.Point, radius int, col color.RGBA) {
	for a := 0; a < 360; a += 2 {
		rad := float64(a) * math.Pi / 180
		x := c.X + int(math.Round(float64(radius)*math.Cos(rad)))
		y := c.Y + int(math.Round(float64(radius)*math.Sin(rad)))
		r.dst.SetRGBA(x, y, col)
	}
}
