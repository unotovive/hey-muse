//go:build live

package assistant

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wyoming"
)

// 母艦を通しで試す: 質問の音声（16 kHz モノラルの WAV）を音声認識へ送り、答え手に聞き、答えを読み上げに
// 回し、その音声をもう一度認識して、言った内容が返ってくるかを見る。マイクとスピーカー以外は端末と同じ経路。
//
//	TECHO5_ASR=母艦:50024 TECHO5_LLM=http://母艦:50024/v1 TECHO5_MODEL=qwen3:8b TECHO5_WAVS=質問のWAVのフォルダ \
//	  go test -tags live -run TurnJa -v ./internal/feature/assistant/
func TestLiveTurnJa(t *testing.T) {
	asr, base, dir := os.Getenv("TECHO5_ASR"), os.Getenv("TECHO5_LLM"), os.Getenv("TECHO5_WAVS")
	if asr == "" || base == "" || dir == "" {
		t.Skip("TECHO5_ASR, TECHO5_LLM or TECHO5_WAVS is not set")
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Brain().Set(config.Brain{Mode: config.BrainDirect, LLM: base, STT: asr, TTS: asr,
		Model: os.Getenv("TECHO5_MODEL"), Language: "ja", Prompt: os.Getenv("TECHO5_PROMPT")}); err != nil {
		t.Fatal(err)
	}
	config.Set().Home().Place(config.Place{Name: "東京", Lat: 35.68, Lon: 139.68, Country: "JP"})

	files, _ := filepath.Glob(filepath.Join(dir, "*.wav"))
	sort.Strings(files)
	for _, f := range files {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		start := time.Now()
		heard := hear(ctx, t, asr, readWAV(t, f))
		heardAt := time.Since(start)
		reply, err := Get().Think(ctx, heard)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		thoughtAt := time.Since(start)
		voice, format, err := wyoming.Synthesize(ctx, asr, reply, "")
		if err != nil {
			t.Fatalf("%s: speech: %v", f, err)
		}
		spokenAt := time.Since(start)
		// 読み上げを 16 kHz に間引いて（24 kHz → 16 kHz は 3 つに 2 つ）認識し直す
		back := hear(ctx, t, asr, toMic(voice, format.Rate))
		cancel()
		t.Logf("%s: 認識 %q (%v) → 答え %q (%v) → 読み上げ %.1f 秒 (%v) → 聞き直し %q",
			filepath.Base(f), heard, heardAt.Round(10*time.Millisecond), reply, (thoughtAt - heardAt).Round(10*time.Millisecond),
			float64(len(voice))/float64(format.Rate), (spokenAt - thoughtAt).Round(10*time.Millisecond), back)
		if strings.TrimSpace(reply) == "" || len(voice) == 0 {
			t.Errorf("%s: 答えか読み上げが空", f)
		}
	}
}

func readWAV(t *testing.T, path string) []int16 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pcm := data[44:]
	out := make([]int16, len(pcm)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(pcm[2*i:]))
	}
	return out
}

func toMic(samples []int16, rate int) []int16 {
	if rate == 16000 {
		return samples
	}
	n := len(samples) * 16000 / rate
	out := make([]int16, n)
	for i := range out {
		out[i] = samples[i*rate/16000]
	}
	return out
}

func hear(ctx context.Context, t *testing.T, addr string, samples []int16) string {
	t.Helper()
	tr, err := wyoming.Transcribe(ctx, addr, "ja")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 0, 3200)
	for i, s := range samples {
		buf = append(buf, byte(s), byte(s>>8))
		if len(buf) == 3200 || i == len(samples)-1 {
			if err := tr.Audio(buf); err != nil {
				t.Fatal(err)
			}
			buf = buf[:0]
		}
	}
	text, err := tr.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(text)
}
