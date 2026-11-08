package voice

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/lib/endpoint"
)

// fakeVAD is the local Silero detector's socket (tools/echo-asr) with a detector as hasty as can be:
// once it has heard speech, the first quiet chunk ends the utterance, and from then on it answers that
// the utterance has ended, as Neural.Feed does. It counts the samples it was sent.
func fakeVAD(t *testing.T) (dial func(context.Context) (*endpoint.Neural, error), heard *atomic.Int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vad.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	heard = &atomic.Int64{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var sent, end uint32
				speech := false
				for {
					var size [4]byte
					if _, err := io.ReadFull(conn, size[:]); err != nil {
						return
					}
					buf := make([]byte, binary.LittleEndian.Uint32(size[:]))
					if _, err := io.ReadFull(conn, buf); err != nil {
						return
					}
					var sum float64
					for i := 0; i+1 < len(buf); i += 2 {
						v := float64(int16(binary.LittleEndian.Uint16(buf[i:])))
						sum += v * v
					}
					n := uint32(len(buf) / 2)
					heard.Add(int64(n))
					loud := n > 0 && math.Sqrt(sum/float64(n)) > 500
					if end == 0 {
						if loud {
							speech = true
						} else if speech {
							end = sent + 1
						}
					}
					sent += n
					var answer [8]byte
					if end != 0 {
						binary.LittleEndian.PutUint32(answer[:], 2)
						binary.LittleEndian.PutUint32(answer[4:], end)
					}
					if _, err := conn.Write(answer[:]); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return func(ctx context.Context) (*endpoint.Neural, error) { return endpoint.DialNeural(ctx, path) }, heard
}

// pumpWhole pumps frames through a turn on slot 0 answered by an answerer sent the utterance whole
// (the direct pipeline), with no history, and reports whether the device decided the speaker had
// finished. The audio sent is not delivered: only the decision is looked at.
func pumpWhole(t *testing.T, frames ...[]int16) (ended bool) {
	t.Helper()
	c := &conversation{source: mic.New(), out: make(chan func() error, 1024), events: make(chan event, 64)}
	ch := make(chan []int16, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	c.pump(context.Background(), &direct{}, 0, false, false, holdsForTone(0, false), "turn", nil, ch)
	for len(c.events) > 0 {
		if (<-c.events).kind == evSpokeEnd {
			ended = true
		}
	}
	return ended
}

// Silero decides once and for good, so it hears nothing of the wake sound: the wake word's end and
// the quiet of someone waiting for the sound would otherwise end the turn as the sound stops, before
// the request. It starts on what follows the sound, and the request said then ends the turn on the
// quiet after it.
func TestSileroDoesNotEndTheTurnOnTheQuietInsideTheWakeSound(t *testing.T) {
	n := stepping(t)
	dial, heard := fakeVAD(t)
	defer func(d func(context.Context) (*endpoint.Neural, error)) { dialNeural = d }(dialNeural)
	dialNeural = dial

	// The end of the wake word, then quiet through the sound and a second past it.
	var frames [][]int16
	frames = append(frames, voiceAt(1, 1100), voiceAt(1, 1100), voiceAt(1, 1100))
	for len(frames) < n+10 {
		frames = append(frames, voiceAt(1, 20))
	}
	if pumpWhole(t, frames...) {
		t.Fatal("the quiet inside the wake sound ended the turn before the request")
	}
	if heard.Load() == 0 {
		t.Fatal("Silero was never asked: the test does not reach it")
	}
	// None of the wake word at the front, which only the sound's own detector hears.
	if got, most := heard.Load(), int64((len(frames)-3)*mic.Rate/10); got > most {
		t.Fatalf("Silero was sent %d samples, the wake word's included (at most %d without it)", got, most)
	}

	// The request after the sound goes through whole...
	for range 10 {
		frames = append(frames, voiceAt(1, 1500))
	}
	if pumpWhole(t, frames...) {
		t.Fatal("the turn ended while the request was being said")
	}
	// ...and the quiet after it ends the turn.
	frames = append(frames, voiceAt(1, 20))
	if !pumpWhole(t, frames...) {
		t.Fatal("the quiet after the request did not end the turn")
	}
}
