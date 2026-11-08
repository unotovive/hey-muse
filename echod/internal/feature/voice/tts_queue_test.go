package voice

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeTTS is a voice the test drives: each sentence's fetch says it has started, hands over one
// sample tagged with the sentence, and finishes (with the error set for it) only when told.
type fakeTTS struct {
	mu      sync.Mutex
	started map[string]chan struct{}
	finish  map[string]chan struct{}
	errs    map[string]error
	tags    map[string]int16
}

func newFakeTTS(texts ...string) *fakeTTS {
	f := &fakeTTS{started: map[string]chan struct{}{}, finish: map[string]chan struct{}{}, errs: map[string]error{}, tags: map[string]int16{}}
	for i, s := range texts {
		f.started[s] = make(chan struct{})
		f.finish[s] = make(chan struct{})
		f.tags[s] = int16(i + 1)
	}
	return f
}

func (f *fakeTTS) fetch(ctx context.Context, text string, out func([]int16) error) error {
	close(f.started[text])
	if err := out([]int16{f.tags[text]}); err != nil {
		return err
	}
	select {
	case <-f.finish[text]:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := f.errs[text]; err != nil {
		return err
	}
	return out([]int16{f.tags[text]})
}

func (f *fakeTTS) hasStarted(text string) bool {
	select {
	case <-f.started[text]:
		return true
	default:
		return false
	}
}

func waitClosed(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not happen", what)
	}
}

// stillNot gives the queue a moment to do what it must not.
func stillNot(t *testing.T, f *fakeTTS, text, why string) {
	t.Helper()
	time.Sleep(20 * time.Millisecond)
	if f.hasStarted(text) {
		t.Fatalf("%s was fetched %s", text, why)
	}
}

// emitted is what reached the speaker, in order.
type emitted struct {
	mu   sync.Mutex
	tags []int16
	gate map[int16]chan struct{} // a tag's playing holds until its gate is closed
}

func (e *emitted) emit(s []int16) error {
	if g := e.gate[s[0]]; g != nil {
		<-g
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tags = append(e.tags, s...)
	return nil
}

func (e *emitted) all() []int16 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.tags)
}

func TestTTSQueuePlaysInOrderAndPrefetchesOne(t *testing.T) {
	f := newFakeTTS("A", "B", "C")
	aPlaying := make(chan struct{})
	e := &emitted{gate: map[int16]chan struct{}{1: aPlaying}}
	q := newTTSQueue(context.Background(), f.fetch, e.emit)
	q.Push("A")
	q.Push("B")
	q.Push("C")

	waitClosed(t, f.started["A"], "A's fetch")
	waitClosed(t, f.started["B"], "B's fetch while A's is still running")
	stillNot(t, f, "C", "while A was being fetched")
	close(f.finish["B"]) // B is ready long before A
	close(f.finish["A"])
	stillNot(t, f, "C", "before A had played")
	close(aPlaying)
	waitClosed(t, f.started["C"], "C's fetch after A played")
	close(f.finish["C"])
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if got := e.all(); !slices.Equal(got, []int16{1, 1, 2, 2, 3, 3}) {
		t.Errorf("played %v, want A, B and C in order", got)
	}
}

func TestTTSQueueStopsOnError(t *testing.T) {
	f := newFakeTTS("A", "B", "C")
	refused := errors.New("speech: 401 Unauthorized")
	f.errs["B"] = refused
	e := &emitted{}
	q := newTTSQueue(context.Background(), f.fetch, e.emit)
	q.Push("A")
	q.Push("B")
	q.Push("C")
	waitClosed(t, f.started["B"], "B's fetch")
	close(f.finish["B"])
	for deadline := time.Now().Add(2 * time.Second); !q.failed(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("B's failure was not noticed")
		}
	}
	close(f.finish["A"])
	if err := q.Close(); !errors.Is(err, refused) {
		t.Errorf("Close() = %v, want %v", err, refused)
	}
	if f.hasStarted("C") {
		t.Error("C was fetched after B failed")
	}
	if got := e.all(); !slices.Equal(got, []int16{1, 1, 2}) {
		t.Errorf("played %v, want A and what B said before failing", got)
	}
}

func TestTTSQueueStopsOnCancel(t *testing.T) {
	f := newFakeTTS("A", "B")
	ctx, cancel := context.WithCancel(context.Background())
	q := newTTSQueue(ctx, f.fetch, (&emitted{}).emit)
	q.Push("A")
	q.Push("B")
	waitClosed(t, f.started["A"], "A's fetch")
	cancel()
	done := make(chan error, 1)
	go func() { done <- q.Close() }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Close() = %v, want %v", err, context.Canceled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the cancel")
	}
}

func TestTTSQueueFirstAudio(t *testing.T) {
	f := newFakeTTS("A")
	gate := make(chan struct{})
	q := newTTSQueue(context.Background(), f.fetch, (&emitted{gate: map[int16]chan struct{}{1: gate}}).emit)
	if !q.FirstAudio().IsZero() {
		t.Fatal("first audio before anything was pushed")
	}
	q.Push("A")
	waitClosed(t, f.started["A"], "A's fetch")
	if !q.FirstAudio().IsZero() {
		t.Fatal("first audio before anything was played")
	}
	close(gate)
	close(f.finish["A"])
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if q.FirstAudio().IsZero() {
		t.Error("no first audio after A played")
	}
}
