package voice

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// 話し終わりが聞き分けられないまま聞き取りの上限に来ても、母艦で答えるときは聞こえた分に答える
// （Muse と同じ。捨てると、15 秒待たせたうえに何も答えない）。
func TestDirectListeningLimitAnswersWhatWasHeard(t *testing.T) {
	c := thinkingTurn(t)
	c.be = newDirect(func(event) {})
	c.enter(phaseListening)
	c.stopAudio = func() {}
	c.handle(event{kind: evTimeout})
	if c.phase != phaseThinking {
		t.Fatalf("phase=%s, want thinking", c.phase)
	}
}

// 会話が始まったら、話している間に母艦の音声認識（と答え手・読み上げ）を起こしておく。
func TestDirectStartWarmsRecognizer(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Brain().Set(config.Brain{Mode: config.BrainDirect, STT: "pc:50024", TTS: "pc:50024",
		LLM: "http://pc:50024/v1", Language: "ja"}); err != nil {
		t.Fatal(err)
	}
	type call struct{ addr, lang string }
	got := make(chan call, 2)
	was := warmRecognizer
	warmRecognizer = func(_ context.Context, addr, lang string) error {
		got <- call{addr, lang}
		return nil
	}
	t.Cleanup(func() { warmRecognizer = was })
	d := newDirect(func(event) {})
	if err := d.Start("Hey Muse"); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-got:
		if c != (call{"pc:50024", "ja"}) {
			t.Fatalf("warmed %+v", c)
		}
	case <-time.After(time.Second):
		t.Fatal("not warmed")
	}
	_ = d.Stop()
}

// Muse で答えるときも、聞き取りが母艦なら会話の始まりに起こす。端末内の認識（127.0.0.1）は起こさない。
func TestMuseStartWarmsPCRecognizerOnly(t *testing.T) {
	got := make(chan string, 2)
	was := warmRecognizer
	warmRecognizer = func(_ context.Context, addr, _ string) error {
		got <- addr
		return nil
	}
	t.Cleanup(func() { warmRecognizer = was })
	for _, tc := range []struct {
		stt  string
		want bool
	}{{"pc:50024", true}, {"127.0.0.1:50024", false}, {"", false}} {
		m := &viaMuse{brain: func() config.Brain { return config.Brain{Mode: config.BrainMuse, STT: tc.stt, Language: "ja"} }}
		_ = m.startTurn("Hey Muse", true)
		select {
		case addr := <-got:
			if !tc.want || addr != tc.stt {
				t.Fatalf("stt %q: warmed %q", tc.stt, addr)
			}
		case <-time.After(300 * time.Millisecond):
			if tc.want {
				t.Fatalf("stt %q: not warmed", tc.stt)
			}
		}
		_ = m.Stop()
	}
}
