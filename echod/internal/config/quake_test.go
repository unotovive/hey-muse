package config

import (
	"slices"
	"testing"
)

func TestQuakeDefaults(t *testing.T) {
	st := testStore(t)
	q := st.Get().Quake
	if q.On || q.Chime || len(q.Prefs) != 0 || q.QuakeMinScale() != 30 {
		t.Fatalf("new device: %+v, min scale %d", q, q.QuakeMinScale())
	}

	w := st.Set().Quake()
	if err := w.On(true); err != nil {
		t.Fatal(err)
	}
	_ = w.MinScale(45)
	_ = w.Prefs([]string{"千葉県", "東京都"})
	_ = w.Chime(true)

	again, err := Load(st.path)
	if err != nil {
		t.Fatal(err)
	}
	q = again.Get().Quake
	if !q.On || !q.Chime || q.MinScale != 45 || q.QuakeMinScale() != 45 || !slices.Equal(q.Prefs, []string{"千葉県", "東京都"}) {
		t.Fatalf("after reload: %+v", q)
	}

	// The list handed back is a copy: changing it changes nothing in the store.
	q.Prefs[0] = "大阪府"
	if again.Get().Quake.Prefs[0] != "千葉県" {
		t.Error("Get shares the prefecture list")
	}
}
