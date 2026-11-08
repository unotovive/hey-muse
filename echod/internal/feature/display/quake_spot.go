//go:build spot

package display

// withQuake: the Spot has no earthquake page, so no switch for one.
func withQuake(rows []settingRow) []settingRow { return rows }
