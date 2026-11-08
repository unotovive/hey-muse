package voice

import (
	"slices"
	"testing"
)

func TestSplitSentences(t *testing.T) {
	for in, want := range map[string]struct {
		complete []string
		rest     string
	}{
		"今日は晴れです。気温は":         {[]string{"今日は晴れです。"}, "気温は"},
		"いいですね！「本当？」と言った。終わり": {[]string{"いいですね！", "「本当？」と言った。"}, "終わり"},
		"一行目\n二行目。":           {[]string{"一行目", "二行目。"}, ""},
		"3.5倍です。":             {[]string{"3.5倍です。"}, ""},
		"途中":                  {nil, "途中"},
		"本当！？すごい":             {[]string{"本当！？"}, "すごい"},
		"行こう。」と彼。":            {[]string{"行こう。」", "と彼。"}, ""},
		"段落一\n\n段落二":          {[]string{"段落一"}, "段落二"},
	} {
		complete, rest := splitSentences(in)
		if !slices.Equal(complete, want.complete) || rest != want.rest {
			t.Errorf("splitSentences(%q) = %q, %q; want %q, %q", in, complete, rest, want.complete, want.rest)
		}
	}
}

func TestSentenceCursorReturnsEachSentenceOnce(t *testing.T) {
	var c sentenceCursor
	for _, step := range []struct {
		full  string
		final bool
		want  []string
	}{
		{"今日は晴れです。", false, []string{"今日は晴れです。"}},
		{"今日は晴れです。気温は20度", false, nil},
		{"今日は晴れです。気温は20度。明日は", false, []string{"気温は20度。"}},
		{"今日は晴れです。気温は20度。明日は雨", true, []string{"明日は雨"}},
		{"今日は晴れです。気温は20度。明日は雨", true, nil},
	} {
		if got := c.Next(step.full, step.final); !slices.Equal(got, step.want) {
			t.Fatalf("Next(%q, %v) = %q, want %q", step.full, step.final, got, step.want)
		}
	}
}

func TestSentenceCursorIgnoresRewrittenPrefix(t *testing.T) {
	var c sentenceCursor
	c.Next("今日は晴れです。", false)
	if got := c.Next("今日は雨です。気温は20度。", false); got != nil {
		t.Errorf("rewritten prefix spoke %q", got)
	}
	// The final text speaks what follows the sentences already said, never the rewritten one again.
	if got := c.Next("今日は雨です。気温は20度。明日も雨", true); !slices.Equal(got, []string{"気温は20度。", "明日も雨"}) {
		t.Errorf("final after a rewrite spoke %q", got)
	}
	// A shorter final text, a message delivered after the snapshot went past it, says nothing again.
	var d sentenceCursor
	d.Next("一つ目。\n\n二つ目。", false)
	if got := d.Next("一つ目。", true); got != nil {
		t.Errorf("an earlier message spoke %q again", got)
	}
	if got := d.Next("一つ目。\n\n二つ目。三つ目。", false); !slices.Equal(got, []string{"三つ目。"}) {
		t.Errorf("after an earlier message: %q", got)
	}
}
