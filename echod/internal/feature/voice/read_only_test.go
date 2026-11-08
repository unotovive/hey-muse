package voice

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/muse"
)

func TestReadOnlyParaphraseKeepsTranscriptAndFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name, intent string
		err          error
		handled      bool
	}{
		{"weather", "weather_tomorrow", nil, true},
		{"calendar", "calendar_today", nil, true},
		{"uncertain", "", nil, false},
		{"unknown operation", "hall_off", nil, false},
		{"offline", "weather_today", errors.New("offline"), false},
		{"no local view", "calendar_week", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := &textTestLink{}
			m := newViaMuse(link, builtIn, nil, func(event) {})
			m.brain = func() config.Brain { return config.Brain{STT: "local:50024"} }
			m.transcribe = func(context.Context, config.Brain, []byte) (string, error) {
				return "今日あと何かあったっけ？", nil
			}
			m.classify = func(ctx context.Context, text string) (string, error) {
				if text != "今日あと何かあったっけ？" {
					t.Fatal("transcript replaced")
				}
				return tc.intent, tc.err
			}
			cancel := ReadOnlyViews.Listen(func(q *ReadOnlyView) { q.Handled = tc.handled; q.Answer = "表示します。" })
			defer cancel()
			var heard []string
			reply, err := m.ask(context.Background(), someAudio, "same-chat", func(e muse.ReplyEvent) {
				if h, ok := e.(muse.Heard); ok {
					heard = append(heard, h.Text)
				}
			})
			if err != nil || len(heard) != 1 || reply.Heard != heard[0] {
				t.Fatalf("reply=%+v heard=%v err=%v", reply, heard, err)
			}
			if tc.handled {
				if link.text != "" || reply.Text != "表示します。" {
					t.Fatal("local route asked Muse")
				}
			} else if link.text != reply.Heard || link.sessionID != "same-chat" {
				t.Fatal("fallback lost input or session")
			}
		})
	}
}

func TestExactReadOnlyAndApplianceHoldBypassJev(t *testing.T) {
	cancel := ReadOnlyViews.Listen(func(q *ReadOnlyView) { q.Handled = true; q.Answer = "表示します。" })
	defer cancel()
	for _, text := range []string{"今日の天気", "明日の予定を見せて", "廊下の電気は消さないで"} {
		if text == "今日の天気" && !weatherBypassesJev {
			continue
		}
		m := newViaMuse(&textTestLink{}, builtIn, nil, func(event) {})
		m.brain = func() config.Brain { return config.Brain{STT: "local:50024"} }
		m.transcribe = func(context.Context, config.Brain, []byte) (string, error) { return text, nil }
		m.classify = func(context.Context, string) (string, error) { t.Fatal("unnecessary Jev request"); return "", nil }
		if _, err := m.ask(context.Background(), someAudio, "test", func(muse.ReplyEvent) {}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCanceledClassificationDoesNotShowOrAskMuse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	link := &textTestLink{}
	m := newViaMuse(link, builtIn, nil, func(event) {})
	m.brain = func() config.Brain { return config.Brain{STT: "local:50024"} }
	m.transcribe = func(context.Context, config.Brain, []byte) (string, error) { return "傘いる？", nil }
	m.classify = func(context.Context, string) (string, error) { cancel(); return "weather_today", nil }
	stop := ReadOnlyViews.Listen(func(*ReadOnlyView) { t.Error("canceled request showed a view") })
	defer stop()
	_, err := m.ask(ctx, someAudio, "test", func(muse.ReplyEvent) {})
	if !errors.Is(err, context.Canceled) || link.text != "" {
		t.Fatal("cancellation was ignored")
	}
}

func TestReadOnlyTransportAuthenticatesAndRestrictsResponse(t *testing.T) {
	key := bytes.Repeat([]byte{43}, 32)
	block, _ := aes.NewCipher(key)
	box, _ := cipher.NewGCM(block)
	intent := "calendar_tomorrow"
	tamper := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, key)
		mac.Write(body)
		if r.URL.Path != "/echo-intent/v1" || r.Header.Get("X-Echo-Signature") != hex.EncodeToString(mac.Sum(nil)) {
			t.Error("wrong authenticated endpoint")
		}
		packet, _ := base64.StdEncoding.DecodeString(string(body))
		plain, err := box.Open(nil, packet[:12], packet[12:], []byte("echo-home-request-1"))
		var request map[string]any
		if err != nil || json.Unmarshal(plain, &request) != nil || request["op"] != "classify_read_only" || len(request) != 3 {
			t.Error("request contains unexpected data")
		}
		response, _ := json.Marshal(map[string]any{"ok": true, "intent": intent})
		nonce := bytes.Repeat([]byte{10}, 12)
		out := box.Seal(nonce, nonce, response, append([]byte("echo-home-response-1"), packet[:12]...))
		if tamper {
			out[len(out)-1] ^= 1
		}
		io.WriteString(w, base64.StdEncoding.EncodeToString(out))
	}))
	defer server.Close()
	cfg := readOnlyConfig{URL: server.URL + "/echo-home/v1", Key: hex.EncodeToString(key), Jev: true}
	got, err := requestReadOnly(context.Background(), cfg, "明日のスケジュール確認して")
	if err != nil || got != intent {
		t.Fatalf("%q %v", got, err)
	}
	intent = "hall_off"
	if got, err = requestReadOnly(context.Background(), cfg, "text"); err != nil || got != "" {
		t.Fatal("unknown operation accepted")
	}
	tamper = true
	if _, err = requestReadOnly(context.Background(), cfg, "text"); err == nil {
		t.Fatal("tampered response accepted")
	}
	cfg.Jev = false
	_, _ = requestReadOnly(context.Background(), cfg, "text")
	if calls != 3 {
		t.Fatal("disabled request sent")
	}
}

func TestReadOnlyTransportHonorsDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err := requestReadOnly(ctx, readOnlyConfig{URL: server.URL + "/echo-home/v1", Key: hex.EncodeToString(make([]byte, 32)), Jev: true}, "雨？")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline lost: %v", err)
	}
}
