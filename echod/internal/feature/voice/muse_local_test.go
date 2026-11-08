package voice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/muse"
)

type textTestLink struct {
	fakeLink
	text      string
	sessionID string
}

func (l *textTestLink) AskText(_ context.Context, text, sessionID string, on func(muse.ReplyEvent)) (muse.Reply, error) {
	l.text = text
	l.sessionID = sessionID
	on(muse.Heard{Text: "server echo"})
	on(muse.Settled{Text: "こんばんは"})
	return muse.Reply{Text: "こんばんは"}, nil
}

func TestMuseLocalRecognitionSendsTextAndKeepsTranscript(t *testing.T) {
	l := &textTestLink{fakeLink: fakeLink{ask: func(context.Context, []byte, func(muse.ReplyEvent)) (muse.Reply, error) {
		t.Fatal("audio was sent to Muse instead of the local transcript")
		return muse.Reply{}, nil
	}}}
	m := newViaMuse(l, builtIn, speaking(320, 320), func(event) {})
	m.brain = func() config.Brain { return config.Brain{STT: "local:50024", Language: "ja"} }
	m.transcribe = func(ctx context.Context, b config.Brain, pcm []byte) (string, error) {
		if b.Language != "ja" || len(pcm) == 0 {
			t.Fatal("missing language or audio")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded recognition request")
		}
		return " 日本語で短く挨拶して ", nil
	}
	var heard []string
	reply, err := m.ask(context.Background(), someAudio, "echo-test", func(e muse.ReplyEvent) {
		if e, ok := e.(muse.Heard); ok {
			heard = append(heard, e.Text)
		}
	})
	if err != nil || l.sessionID != "echo-test" || l.text != "日本語で短く挨拶して" || reply.Heard != l.text || len(heard) != 1 || heard[0] != l.text {
		t.Fatalf("wrong text path: text=%q heard=%q reply=%+v err=%v", l.text, heard, reply, err)
	}
}

func TestMuseLocalRecognitionFailureKeepsVoiceFallback(t *testing.T) {
	called := 0
	l := &textTestLink{fakeLink: fakeLink{ask: func(_ context.Context, wav []byte, _ func(muse.ReplyEvent)) (muse.Reply, error) {
		called++
		if string(wav[:4]) != "RIFF" {
			t.Fatal("fallback is not WAV")
		}
		return muse.Reply{Text: "fallback"}, nil
	}}}
	m := newViaMuse(l, builtIn, speaking(320, 320), func(event) {})
	m.brain = func() config.Brain { return config.Brain{STT: "local:50024", Language: "ja"} }
	m.transcribe = func(context.Context, config.Brain, []byte) (string, error) { return "", errors.New("offline") }
	reply, err := m.ask(context.Background(), someAudio, "echo-test", func(muse.ReplyEvent) {})
	if err != nil || called != 1 || reply.Text != "fallback" || l.text != "" {
		t.Fatalf("fallback: %+v %v calls=%d", reply, err, called)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.ask(ctx, someAudio, "echo-test", func(muse.ReplyEvent) {}); !errors.Is(err, context.Canceled) || called != 1 {
		t.Fatal("canceled turn was sent to Muse")
	}
}

func TestMuseLocalSilenceDoesNotAsk(t *testing.T) {
	l := &textTestLink{fakeLink: fakeLink{ask: func(context.Context, []byte, func(muse.ReplyEvent)) (muse.Reply, error) {
		t.Fatal("silence should not be sent to Muse")
		return muse.Reply{}, nil
	}}}
	m := newViaMuse(l, builtIn, speaking(320, 320), func(event) {})
	m.brain = func() config.Brain { return config.Brain{STT: "local:50024"} }
	m.transcribe = func(context.Context, config.Brain, []byte) (string, error) { return " ", nil }
	if reply, err := m.ask(context.Background(), someAudio, "echo-test", func(muse.ReplyEvent) {}); err != nil || reply.Text != "" || l.text != "" {
		t.Fatal("silence was not ignored")
	}
}

func TestMuseSkipsLocalSTTAfterFailures(t *testing.T) {
	askCalls := 0
	l := &textTestLink{fakeLink: fakeLink{ask: func(context.Context, []byte, func(muse.ReplyEvent)) (muse.Reply, error) {
		askCalls++
		return muse.Reply{Text: "fallback"}, nil
	}}}
	m := newViaMuse(l, builtIn, speaking(320, 320), func(event) {})
	clock := time.Unix(1000, 0)
	m.now = func() time.Time { return clock }
	m.brain = func() config.Brain { return config.Brain{STT: "local:50024", Language: "ja"} }
	transcribeCalls := 0
	fail := true
	m.transcribe = func(context.Context, config.Brain, []byte) (string, error) {
		transcribeCalls++
		if fail {
			return "", errors.New("offline")
		}
		return "こんにちは", nil
	}
	turn := func() {
		t.Helper()
		if _, err := m.ask(context.Background(), someAudio, "echo-test", func(muse.ReplyEvent) {}); err != nil {
			t.Fatal(err)
		}
	}
	turn()
	if transcribeCalls != 1 || askCalls != 1 {
		t.Fatalf("turn 1: transcribe=%d ask=%d", transcribeCalls, askCalls)
	}
	turn()
	if transcribeCalls != 2 || askCalls != 2 {
		t.Fatalf("turn 2: transcribe=%d ask=%d", transcribeCalls, askCalls)
	}
	clock = clock.Add(30 * time.Second)
	turn()
	if transcribeCalls != 2 || askCalls != 3 {
		t.Fatalf("turn 3 inside the window: transcribe=%d ask=%d", transcribeCalls, askCalls)
	}
	clock = clock.Add(31 * time.Second)
	fail = false
	turn()
	if transcribeCalls != 3 || askCalls != 3 || l.text != "こんにちは" || m.sttFailures != 0 {
		t.Fatalf("turn 4 after the window: transcribe=%d ask=%d text=%q failures=%d", transcribeCalls, askCalls, l.text, m.sttFailures)
	}
}

func TestMuseFallsBackToVoiceNoteWhenLocalSTTFails(t *testing.T) {
	askCalls := 0
	l := &textTestLink{fakeLink: fakeLink{ask: func(context.Context, []byte, func(muse.ReplyEvent)) (muse.Reply, error) {
		askCalls++
		return muse.Reply{Text: "Muse の返事"}, nil
	}}}
	m := newViaMuse(l, builtIn, speaking(320, 320), func(event) {})
	m.brain = func() config.Brain { return config.Brain{STT: "local:50024", Language: "ja"} }
	transcribeCalls := 0
	m.transcribe = func(context.Context, config.Brain, []byte) (string, error) {
		transcribeCalls++
		return "", errors.New("offline")
	}
	reply, err := m.ask(context.Background(), someAudio, "echo-test", func(muse.ReplyEvent) {})
	if reply.Text == "聞き取れませんでした。もう一度お願いします。" {
		t.Fatal("asked the speaker to repeat instead of using Muse")
	}
	if err != nil || reply.Text != "Muse の返事" || transcribeCalls != 1 || askCalls != 1 {
		t.Fatalf("reply=%+v err=%v transcribe=%d ask=%d", reply, err, transcribeCalls, askCalls)
	}
}
