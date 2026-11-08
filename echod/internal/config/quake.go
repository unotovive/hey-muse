package config

import "slices"

// Quake is the earthquake page (feature/quake): P2P 地震情報's earthquake, tsunami and early-warning
// messages shown over the screen. Off on a new device.
type Quake struct {
	On bool `json:"on,omitempty"`

	// MinScale is the least maximum intensity (震度, P2P 地震情報's scale: 30 is 3, 45 is 5弱) an
	// earthquake must reach to be shown; 0 is the default, 30.
	MinScale int `json:"min_scale,omitempty"`

	// Prefs are prefectures (千葉県) where an earthquake is shown whatever its intensity.
	Prefs []string `json:"prefs,omitempty"`

	// Chime sounds when the page comes up; silent otherwise.
	Chime bool `json:"chime,omitempty"`
}

// DefaultQuakeMinScale is 震度 3.
const DefaultQuakeMinScale = 30

// QuakeMinScale is the least intensity in force.
func (q Quake) QuakeMinScale() int {
	if q.MinScale <= 0 {
		return DefaultQuakeMinScale
	}
	return q.MinScale
}

type QuakeWriter struct{ st *Store }

func (w QuakeWriter) On(v bool) error {
	return w.st.Update(func(c *Config) { c.Quake.On = v })
}

func (w QuakeWriter) MinScale(v int) error {
	return w.st.Update(func(c *Config) { c.Quake.MinScale = min(max(v, 0), 70) })
}

func (w QuakeWriter) Prefs(v []string) error {
	return w.st.Update(func(c *Config) { c.Quake.Prefs = slices.Clone(v) })
}

func (w QuakeWriter) Chime(v bool) error {
	return w.st.Update(func(c *Config) { c.Quake.Chime = v })
}
