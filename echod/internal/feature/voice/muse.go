package voice

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
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

const (
	sttFailuresToSkip = 2
	sttSkipFor        = time.Minute
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
	sttFailures  int
	sttSkipUntil time.Time
	now          func() time.Time
	nextProbe    int
	hall         hallTurns
	sessionID    string // owned by the send queue and captured by End
	utterance
}

func newViaMuse(link museLink, voice func() speech.Voice, speak speakFunc, post func(event)) *viaMuse {
	return &viaMuse{link: link, voice: voice, speak: speak, post: post,
		brain: func() config.Brain { return config.Get().Brain }, transcribe: transcribe, classify: classifyReadOnly, now: time.Now}
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
			m.sttFailures = 0
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
		if m.sttFailures++; m.sttFailures >= sttFailuresToSkip {
			m.sttSkipUntil = m.clock().Add(sttSkipFor)
		}
		slog.Warn("muse: local recognition unavailable, using Muse voice recognition", "err", err)
	}
	return m.link.Ask(ctx, wavFile(media.Normalize(samples)), sessionID, on)
}

// skipLocalSTT is true while the owner's recogniser has failed often enough to be left alone.
func (m *viaMuse) skipLocalSTT() bool { return m.clock().Before(m.sttSkipUntil) }

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

// Each completed chat message can be spoken before the overall task is finished.
// The playback stream closes only after Ask returns, so a pause between messages
// does not end the turn. The Muse read loop keeps collecting updates during TTS.
func (m *viaMuse) answer(ctx context.Context, pcm []byte, sessionID string) {
	ctx = muse.WithLatency(ctx)
	muse.LogLatency(ctx, slog.Default(), "recognition_start")
	start := time.Now()
	var heardAt, repliedAt, firstAudioAt time.Duration
	var preview, spoken string
	var speechErr error
	askCtx, stopAsk := context.WithCancel(ctx)
	defer stopAsk()

	speakPart := func(part, display string) {
		if part == "" || speechErr != nil || ctx.Err() != nil {
			return
		}
		if repliedAt == 0 {
			repliedAt = time.Since(start)
		}
		muse.LogLatency(ctx, slog.Default(), "speech_start", "characters", len([]rune(part)))
		m.post(event{kind: evReplyText, text: display, ctx: ctx})
		first, err := m.say(ctx, part, start)
		if firstAudioAt == 0 {
			firstAudioAt = first
		}
		if err != nil {
			speechErr = err
			stopAsk()
			return
		}
		if first > 0 && ctx.Err() == nil {
			m.post(event{kind: evMusePartEnd, ctx: ctx})
		}
	}
	// Settled and the final return value contain the entire answer. Speak only
	// the remainder, including a partial last message returned at the turn cap.
	speakRemainder := func(full string) {
		full = speech.Spoken(full)
		tail, ok := strings.CutPrefix(full, spoken)
		if !ok {
			return
		} // already spoken text was edited on the server
		part := strings.TrimSpace(tail)
		if part != "" {
			speakPart(part, full)
			spoken = full
		}
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
			}
		case muse.MessageReady:
			part := speech.Spoken(v.Text)
			if part == "" {
				return
			}
			if spoken != "" {
				spoken += "\n\n"
			}
			spoken += part
			display := spoken
			if strings.HasPrefix(preview, spoken) {
				display = preview
			}
			speakPart(part, display)
		case muse.Settled:
			speakRemainder(v.Text)
		}
	})
	if ctx.Err() != nil {
		return
	}
	if err == nil {
		speakRemainder(reply.Text)
	}
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
	if spoken != "" {
		m.post(event{kind: evStreamEnd, ctx: ctx})
	}
	m.post(event{kind: evRunEnd, ctx: ctx})
	muse.LogLatency(ctx, slog.Default(), "voice_done")
	slog.Info("muse turn answered", "heard_ms", heardAt.Milliseconds(), "replied_ms", repliedAt.Milliseconds(), "first_audio_ms", firstAudioAt.Milliseconds(), "spoken_ms", time.Since(start).Milliseconds())
}

// say sends PCM for one message. Its caller owns the lifetime of the turn.
func (m *viaMuse) say(ctx context.Context, text string, start time.Time) (firstAudio time.Duration, err error) {
	held := make([]int16, 0, 2*museChunk)
	flush := func() {
		if len(held) == 0 || ctx.Err() != nil {
			return
		}
		if firstAudio == 0 {
			muse.LogLatency(ctx, slog.Default(), "first_audio_chunk")
			firstAudio = time.Since(start)
		}
		m.post(event{kind: evStreamAudio, audio: bytesOf(held), ctx: ctx})
		held = held[:0]
	}
	speak := func(v speech.Voice) error {
		return m.speak(ctx, v, text, func(samples []int16) error {
			held = append(held, samples...)
			if len(held) >= museChunk {
				flush()
			}
			return ctx.Err()
		})
	}
	v := m.voice()
	err = speak(v)
	if err != nil && ctx.Err() == nil && !v.Local() && firstAudio == 0 {
		slog.Warn("muse: the speech endpoint failed, so the answer is in the built-in voice", "voice", v, "err", err)
		held = held[:0]
		if lerr := speak(speech.BuiltIn); lerr != nil {
			err = fmt.Errorf("%w; %w", err, lerr)
		} else {
			err = nil
		}
	}
	if err != nil || ctx.Err() != nil {
		return firstAudio, err
	}
	flush()
	return firstAudio, nil
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
