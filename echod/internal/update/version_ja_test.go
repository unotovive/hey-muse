package update

import "testing"

func TestNewerRanksJaSuffix(t *testing.T) {
	for _, c := range []struct {
		offered, running string
		want             bool
	}{
		{"v1.6.30-ja.2", "v1.6.30-ja.1", true},
		{"v1.6.31-ja.1", "v1.6.30-ja.2", true},
		{"v1.6.30-ja.1", "v1.6.30-ja.2", false},
		{"v1.6.30-ja.10", "v1.6.30-ja.9", true},
	} {
		if got := Newer(c.offered, c.running); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.offered, c.running, got, c.want)
		}
	}
	if err := ValidVersion("v1.6.30-ja.1"); err != nil {
		t.Errorf("v1.6.30-ja.1 should be a valid version: %v", err)
	}
}
