package voice

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/feedback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	gadget "github.com/HuskerMinion/techo5/echod/internal/feature/muse"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/lib/endpoint"
	"github.com/HuskerMinion/techo5/echod/internal/lib/muse"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/speech"
)

// Muse answers a locally transcribed message or a voice note. Local weather and
// configured appliance commands can be handled before a Muse request.

// museLink is the connection a turn asks through: feature/muse's Feature, or a test's stand-in.
type museLink interface {
	Ready() bool
	// Paired is whether the Muse app has paired the device, connected just now or not.
	Paired() bool
	Ask(ctx context.Context, wav []byte, sessionID string, on func(muse.ReplyEvent)) (muse.Reply, error)
}

// speakFunc says text in voice v, calling out with the samples at 16 kHz as they arrive.
type speakFunc func(ctx context.Context, v speech.Voice, text string, out func(samples []int16) error) error

// museChunk is the least audio handed to the conversation at a time, in 16 kHz samples: 100 ms. A
// voice arrives in reads of a few kilobytes, and the reply's queue holds chunkQueue of whatever
// it is given, so each read must not take a slot of its own.
const museChunk = mic.Rate / 10

// museThinkingAfter is how long a question goes unanswered before a chime says Muse is on it.
const museThinkingAfter = 5 * time.Second

const (
	// sttFailuresToSkip は、ローカル認識がこの回数だけ続けて失敗したら遮断する回数。
	sttFailuresToSkip = 2
	// sttSkipFor は、遮断してローカル認識を飛ばす時間。
	// 窓が終わった後は失敗 1 回で再遮断する。毎分 1 回、最大 2 秒のプローブになるが意図的。
	sttSkipFor = time.Minute
)

type viaMuse struct {
	link       museLink
	voice      func() speech.Voice // the one the settings choose, asked for every reply
	speak      speakFunc
	post       func(event)
	brain      func() config.Brain
	transcribe func(context.Context, config.Brain, []byte) (string, error)
	classify   func(context.Context, string) (string, error)
	early      *earlySTT
	// After sttFailuresToSkip failures in a row the owner's recogniser is left alone for sttSkipFor,
	// so a mothership that is down does not cost its connect timeout on every turn.
	sttMu        sync.Mutex // End は answer を別 goroutine で走らせるため、下の 2 つを守る
	sttFailures  int
	sttSkipUntil time.Time
	now          func() time.Time
	nextProbe    int
	hall         hallTurns
	sessionID    string // owned by the send queue and captured by End
	// thinking is played once when no voice has started thinkingAfter after the question ended.
	thinking      func()
	thinkingAfter time.Duration
	utterance
}

func newViaMuse(link museLink, voice func() speech.Voice, speak speakFunc, post func(event)) *viaMuse {
	return &viaMuse{link: link, voice: voice, speak: speak, post: post,
		brain: func() config.Brain { return config.Get().Brain }, transcribe: transcribe, classify: classifyReadOnly, now: time.Now,
		thinking: feedback.Thinking, thinkingAfter: museThinkingAfter}
}

type museTextLink interface {
	AskText(context.Context, string, string, func(muse.ReplyEvent)) (muse.Reply, error)
}

// ask uses the configured Wyoming recognizer when present. Muse still answers
// and runs its device commands; only transcription moves to the owner's server.
// If that server is unavailable, preserve the original voice-note path.
func (m *viaMuse) ask(ctx context.Context, pcm []byte, sessionID string, on func(muse.ReplyEvent)) (muse.Reply, error) {
	samples := endpoint.Trim(samplesOf(pcm))
	b := m.brain()
	if textLink, ok := m.link.(museTextLink); ok && strings.TrimSpace(b.STT) != "" && !m.skipLocalSTT() {
		sttCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		text, err := m.finalTranscript(sttCtx, b, bytesOf(samples))
		cancel()
		if ctx.Err() != nil {
			return muse.Reply{}, ctx.Err()
		}
		if err == nil {
			m.noteSTTSuccess()
			text = strings.TrimSpace(text)
			if text == "" {
				return muse.Reply{}, nil
			}
			slog.Info("muse: using local speech recognition", "language", b.Language)
			on(muse.Heard{Text: text})
			if answer, ok := localWeatherAnswer(text); ok {
				return muse.Reply{Heard: text, Text: answer}, nil
			}
			if answer, ok := readOnlyAnswer(exactCalendarIntent(text)); ok {
				return muse.Reply{Heard: text, Text: answer}, nil
			}
			if command, ok := parseAppliance(text); ok {
				answer := m.hall.executeCommand(ctx, sessionID, command)
				return muse.Reply{Heard: text, Text: answer}, nil
			}
			if m.hall.hold(sessionID, text) {
				return muse.Reply{Heard: text, Text: hallHoldReply(text)}, nil
			}
			if m.classify != nil && ReadOnlyViews.Listeners() > 0 {
				intent, classifyErr := m.classify(ctx, text)
				if ctx.Err() != nil {
					return muse.Reply{}, ctx.Err()
				}
				if classifyErr == nil {
					if answer, ok := readOnlyAnswer(intent); ok {
						return muse.Reply{Heard: text, Text: answer}, nil
					}
				}
			}
			prompt := m.hall.withReceipt(sessionID, text)
			reply, err := textLink.AskText(ctx, prompt, sessionID, func(e muse.ReplyEvent) {
				if _, heard := e.(muse.Heard); !heard {
					on(e)
				}
			})
			reply.Heard = text
			return reply, err
		}
		m.noteSTTFailure()
		slog.Warn("muse: local recognition unavailable, using Muse voice recognition", "err", err)
	}
	return m.link.Ask(ctx, wavFile(media.Normalize(samples)), sessionID, on)
}

// skipLocalSTT is true while the owner's recogniser has failed often enough to be left alone.
func (m *viaMuse) skipLocalSTT() bool {
	m.sttMu.Lock()
	defer m.sttMu.Unlock()
	return m.clock().Before(m.sttSkipUntil)
}

func (m *viaMuse) noteSTTSuccess() {
	m.sttMu.Lock()
	m.sttFailures = 0
	m.sttMu.Unlock()
}

func (m *viaMuse) noteSTTFailure() {
	m.sttMu.Lock()
	defer m.sttMu.Unlock()
	if m.sttFailures++; m.sttFailures >= sttFailuresToSkip {
		m.sttSkipUntil = m.clock().Add(sttSkipFor)
	}
}

func (m *viaMuse) clock() time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}

func (m *viaMuse) startTurn(phrase string, followUp bool) error {
	if !followUp {
		m.sessionID = gadget.TargetChat(m.brain().Muse.Chat)
	}
	m.early = nil
	m.nextProbe = 32000
	return m.utterance.Start(phrase)
}

func (m *viaMuse) Name() string { return "muse" }

func (m *viaMuse) Ready() bool {
	b := config.Get().Brain
	return b.Mode == config.BrainMuse && (m.link.Ready() || standaloneHomeReady(b.STT))
}

// Excuse is why Muse cannot take a turn, for the device to say: a tone alone does not tell somebody
// who was heard whether to say it again, wait, or go and pair the device.
func (m *viaMuse) Excuse() string {
	if !m.link.Paired() {
		return "This device is not paired with Muse yet."
	}
	return "I can't reach Muse right now."
}

// End hands the utterance over to be answered, off the send queue as direct does: Muse takes
// seconds to answer, and the next turn's start must not wait behind it.
func (m *viaMuse) End() error {
	if ctx, pcm := m.take(); ctx != nil {
		sessionID := m.sessionID
		if m.early != nil {
			ctx = context.WithValue(ctx, earlySTTKey{}, m.early)
		}
		safe.Go("muse turn", func() { m.answer(ctx, pcm, sessionID) })
	}
	return nil
}

// museVoice is the voice the settings choose, read afresh for every reply so a voice chosen on the
// screen is the next answer's.
func museVoice() speech.Voice {
	s := config.Get().Brain.Muse.Speech
	return speech.Chosen(s.Voice, s.Key)
}

// speakMuse says text in voice v: through the endpoint the settings name, or in the device's own.
func speakMuse(ctx context.Context, v speech.Voice, text string, out func(samples []int16) error) error {
	if played, err := speakHomeReply(ctx, text, out); played {
		return err
	}
	s := config.Get().Brain.Muse.Speech
	return speech.Speaker{
		Cloud: speech.Client{Base: s.Base, Key: s.Key, Model: s.Model, Style: s.Style},
		Local: speech.Installed(),
	}.Speak(ctx, v, text, out)
}

// The answer is said a sentence at a time, each as soon as the reply shown contains it, while Muse
// is still writing the rest. A completed chat message, a settled answer and the final one are
// flushed whole before the call returns, so the playback of one message ends (evMusePartEnd) before
// the next begins. The playback stream closes only after Ask returns, so a pause between messages
// does not end the turn.
func (m *viaMuse) answer(ctx context.Context, pcm []byte, sessionID string) {
	ctx = muse.WithLatency(ctx)
	muse.LogLatency(ctx, slog.Default(), "recognition_start")
	start := time.Now()
	var heardAt, repliedAt time.Duration
	var firstAudioAt atomic.Int64
	var preview, messages string
	var cursor sentenceCursor
	var part *ttsQueue // the message being said, nil between messages
	var spoke bool
	var speechErr error
	askCtx, stopAsk := context.WithCancel(ctx)
	defer stopAsk()
	defer func() {
		if part != nil {
			_ = part.Close() // the turn was stopped: nothing is reported
		}
	}()

	// Nothing heard for a while after the question: one short chime says the answer is on its way.
	var audible atomic.Bool
	if m.thinking != nil && m.thinkingAfter > 0 {
		chime := time.AfterFunc(m.thinkingAfter, func() {
			if !audible.Load() && ctx.Err() == nil {
				m.thinking()
			}
		})
		defer chime.Stop()
	}
	emit := func(samples []int16) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !audible.Swap(true) {
			muse.LogLatency(ctx, slog.Default(), "first_audio_chunk")
			firstAudioAt.Store(int64(time.Since(start)))
		}
		m.post(event{kind: evStreamAudio, audio: bytesOf(samples), ctx: ctx})
		return nil
	}
	v := m.voice()
	fetch := func(qctx context.Context, text string, out func([]int16) error) error {
		err := m.say(qctx, v, text, out)
		if err != nil && qctx.Err() == nil {
			stopAsk() // the voice has failed: there is no use waiting for more to say
		}
		return err
	}
	say := func(sentences []string, display string) {
		if len(sentences) == 0 || speechErr != nil || ctx.Err() != nil {
			return
		}
		if part == nil {
			if repliedAt == 0 {
				repliedAt = time.Since(start)
			}
			muse.LogLatency(ctx, slog.Default(), "speech_start", "characters", len([]rune(sentences[0])))
			m.post(event{kind: evReplyText, text: display, ctx: ctx})
			part, spoke = newTTSQueue(ctx, fetch, emit), true
		}
		for _, s := range sentences {
			part.Push(s)
		}
	}
	// endPart waits for the message being said to have played.
	endPart := func() {
		if part == nil {
			return
		}
		q := part
		part = nil
		err := q.Close()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if speechErr == nil {
				speechErr = err
			}
			stopAsk()
			return
		}
		if !q.FirstAudio().IsZero() {
			m.post(event{kind: evMusePartEnd, ctx: ctx})
		}
	}
	// Settled and the final return value contain the entire answer: what has not been said of it,
	// a partial last message returned at the turn cap included, is said now.
	sayRest := func(full string) {
		full = speech.Spoken(full)
		say(cursor.Next(full, true), full)
		endPart()
	}
	reply, err := m.ask(askCtx, pcm, sessionID, func(e muse.ReplyEvent) {
		if askCtx.Err() != nil {
			return
		}
		switch v := e.(type) {
		case muse.Heard:
			muse.LogLatency(ctx, slog.Default(), "recognition_done")
			heardAt = time.Since(start)
			m.post(event{kind: evHeard, text: strings.TrimSpace(v.Text), ctx: ctx})
		case muse.ReplyText:
			text := speech.Spoken(v.Text)
			if text != "" && text != preview {
				if preview == "" {
					slog.Info("muse: first reply text received", "elapsed_ms", time.Since(start).Milliseconds())
				}
				preview = text
				m.post(event{kind: evReplyPreview, text: text, ctx: ctx})
				say(cursor.Next(text, false), text)
			}
		case muse.MessageReady:
			msg := speech.Spoken(v.Text)
			if msg == "" {
				return
			}
			if messages != "" {
				messages += "\n\n"
			}
			messages += msg
			display := messages
			if strings.HasPrefix(preview, messages) {
				display = preview
			}
			say(cursor.Next(messages, true), display)
			endPart()
		case muse.Settled:
			sayRest(v.Text)
		}
	})
	if ctx.Err() != nil {
		return
	}
	if err == nil && speechErr == nil {
		sayRest(reply.Text)
	}
	endPart()
	if ctx.Err() != nil {
		return
	}
	if speechErr != nil {
		m.post(event{kind: evError, code: "tts-failed", msg: speechErr.Error(), ctx: ctx})
		return
	}
	if err != nil {
		m.post(event{kind: evError, code: "intent-failed", msg: err.Error(), ctx: ctx})
		return
	}
	final := speech.Spoken(reply.Text)
	if strings.HasSuffix(final, "?") || strings.HasSuffix(final, "？") {
		m.post(event{kind: evContinue, ctx: ctx})
	}
	if spoke {
		m.post(event{kind: evStreamEnd, ctx: ctx})
	}
	m.post(event{kind: evRunEnd, ctx: ctx})
	muse.LogLatency(ctx, slog.Default(), "voice_done")
	slog.Info("muse turn answered", "heard_ms", heardAt.Milliseconds(), "replied_ms", repliedAt.Milliseconds(), "first_audio_ms", time.Duration(firstAudioAt.Load()).Milliseconds(), "spoken_ms", time.Since(start).Milliseconds())
}

// say speaks one sentence in voice v, handing out its samples in chunks of at least museChunk. If
// the endpoint fails before any chunk was handed out, a piece held back included, the sentence is
// said in the built-in voice instead. Chunks are cut per sentence, so the end of one sentence never
// waits for the next one's voice.
func (m *viaMuse) say(ctx context.Context, v speech.Voice, text string, out func([]int16) error) error {
	held := make([]int16, 0, 2*museChunk)
	started := false
	flush := func() error {
		if len(held) == 0 {
			return nil
		}
		started = true
		chunk := slices.Clone(held)
		held = held[:0]
		return out(chunk)
	}
	speak := func(v speech.Voice) error {
		return m.speak(ctx, v, text, func(samples []int16) error {
			held = append(held, samples...)
			if len(held) >= museChunk {
				if err := flush(); err != nil {
					return err
				}
			}
			return ctx.Err()
		})
	}
	err := speak(v)
	if err != nil && ctx.Err() == nil && !v.Local() && !started {
		slog.Warn("muse: the speech endpoint failed, so the answer is in the built-in voice", "voice", v, "err", err)
		held = held[:0]
		if lerr := speak(speech.BuiltIn); lerr != nil {
			err = fmt.Errorf("%w; %w", err, lerr)
		} else {
			err = nil
		}
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return flush()
}

// wavFile wraps 16 kHz 16-bit mono samples as the voice note Muse takes.
func wavFile(samples []int16) []byte {
	data := 2 * len(samples)
	b := make([]byte, 0, 44+data)
	b = append(b, "RIFF"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(36+data))
	b = append(b, "WAVEfmt "...)
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, 1) // PCM
	b = binary.LittleEndian.AppendUint16(b, 1) // mono
	b = binary.LittleEndian.AppendUint32(b, mic.Rate)
	b = binary.LittleEndian.AppendUint32(b, mic.Rate*2)
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint16(b, 16)
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(data))
	return append(b, bytesOf(samples)...)
}
