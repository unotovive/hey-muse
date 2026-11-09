//go:build live

package assistant

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// 日本語で話しかけたときに、母艦の答え手（Ollama など）が道具を正しく使い、日本語で答えるかを見る。
// 端末と同じ指示文・道具一式を使う。プロンプトは TECHO5_PROMPT（未指定なら手順書の推奨文）。
//
//	TECHO5_LLM=http://母艦:50024/v1 TECHO5_MODEL=qwen2.5:7b go test -tags live -run Ja -v ./internal/feature/assistant/
func TestLiveAssistantJa(t *testing.T) {
	base := os.Getenv("TECHO5_LLM")
	if base == "" {
		t.Skip("TECHO5_LLM is not set")
	}
	prompt := os.Getenv("TECHO5_PROMPT")
	if prompt == "" {
		prompt = "必ず自然な日本語の話し言葉で答えてください。結論を先に、一〜二文で短く答えます。"
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Brain().Set(config.Brain{Mode: config.BrainDirect, LLM: base, STT: "x:1", TTS: "x:1",
		Model: os.Getenv("TECHO5_MODEL"), Language: "ja", Prompt: prompt}); err != nil {
		t.Fatal(err)
	}
	config.Set().Home().Place(config.Place{Name: "東京都中央区", Lat: 35.68, Lon: 139.78, Country: "JP"})

	ask := func(s string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		start := time.Now()
		reply, err := Get().Think(ctx, s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		t.Logf("%q -> %q (%v)", s, reply, time.Since(start).Round(10*time.Millisecond))
		return reply
	}

	ask("パスタ用に10分のタイマーをセットして")
	found := false
	for _, c := range timer.Get().List(time.Now()) {
		if c.Total == 10*time.Minute {
			found = true
		}
	}
	if !found {
		t.Errorf("10 分のタイマーが無い: %+v", timer.Get().List(time.Now()))
	}
	ask("あと何分？")
	ask("今何時？")
	ask("平日の朝6時半にアラームをセットして")
	if n := len(config.Get().Alarms.List); n != 1 {
		t.Errorf("アラームが %d 件（1 件のはず）", n)
	}
	ask("アラームを全部教えて")
	ask("6時半のアラームを消して")
	if n := len(config.Get().Alarms.List); n != 0 {
		t.Errorf("消したあともアラームが %d 件", n)
	}
	ask("音量を5にして")
	ask("今日の天気を画面に出して")
	ask("富士山の高さは？")
	ask("タイマーを止めて")
	if n := len(timer.Get().List(time.Now())); n != 0 {
		t.Errorf("止めたあともタイマーが %d 件", n)
	}
}
