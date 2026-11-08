package voice

import (
	"context"
	"sync"
	"time"
)

// ttsQueue says sentences one after another while the answer is still being written. Each sentence
// is fetched (turned into speech) and played (emitted) in the order pushed, and the next sentence's
// fetch runs while the current one plays, so there is no wait for the voice between sentences.
// Exactly one sentence is fetched ahead: sentence n+2 is not asked for until sentence n has played,
// so an answer cut short has not paid for a voice nobody hears.
type ttsQueue struct {
	ctx    context.Context
	cancel context.CancelFunc
	fetch  func(ctx context.Context, text string, out func([]int16) error) error
	emit   func([]int16) error

	slots   chan struct{}     // a sentence fetched or playing holds one; two of them
	handles chan *ttsSentence // fetched sentences, in order, for the player
	more    chan struct{}     // something was pushed, or the queue closed
	done    chan struct{}     // the player has finished
	wg      sync.WaitGroup    // the feeder and every fetch

	mu      sync.Mutex
	pending []string
	closed  bool
	err     error
	first   time.Time
}

// ttsSentence is one sentence's voice as its fetch hands it over.
type ttsSentence struct {
	mu     sync.Mutex
	chunks [][]int16
	done   bool
	err    error
	more   chan struct{}
}

func newTTSQueue(ctx context.Context, fetch func(ctx context.Context, text string, out func([]int16) error) error, emit func([]int16) error) *ttsQueue {
	ctx, cancel := context.WithCancel(ctx)
	q := &ttsQueue{ctx: ctx, cancel: cancel, fetch: fetch, emit: emit,
		slots: make(chan struct{}, 2), handles: make(chan *ttsSentence, 2),
		more: make(chan struct{}, 1), done: make(chan struct{})}
	q.wg.Add(1)
	go q.feed()
	go q.play()
	return q
}

// Push puts a sentence after the ones already pushed. It does not wait.
func (q *ttsQueue) Push(text string) {
	q.mu.Lock()
	if !q.closed {
		q.pending = append(q.pending, text)
	}
	q.mu.Unlock()
	q.wake()
}

// Close waits until every sentence pushed has played, or the queue has stopped, and returns the
// first error: the context's when it was canceled.
func (q *ttsQueue) Close() error {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.wake()
	<-q.done
	q.cancel()
	q.wg.Wait()
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.err
}

// FirstAudio is when the first of the voice was played, or zero while none has been.
func (q *ttsQueue) FirstAudio() time.Time {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.first
}

func (q *ttsQueue) wake() {
	select {
	case q.more <- struct{}{}:
	default:
	}
}

// fail records the first error. A sentence failing stops any further fetch, though what is already
// fetched before it still plays.
func (q *ttsQueue) fail(err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err == nil {
		if ctxErr := q.ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		q.err = err
	}
}

func (q *ttsQueue) failed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.err != nil
}

// next waits for the next sentence pushed; false once the queue is closed and empty, or stopped.
func (q *ttsQueue) next() (string, bool) {
	for {
		q.mu.Lock()
		if len(q.pending) > 0 {
			text := q.pending[0]
			q.pending = q.pending[1:]
			q.mu.Unlock()
			return text, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return "", false
		}
		select {
		case <-q.more:
		case <-q.ctx.Done():
			return "", false
		}
	}
}

// feed starts each sentence's fetch as soon as there is a slot for it.
func (q *ttsQueue) feed() {
	defer q.wg.Done()
	defer close(q.handles)
	for {
		text, ok := q.next()
		if !ok {
			return
		}
		select {
		case q.slots <- struct{}{}:
		case <-q.ctx.Done():
			return
		}
		if q.failed() || q.ctx.Err() != nil {
			return
		}
		s := &ttsSentence{more: make(chan struct{}, 1)}
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			err := q.fetch(q.ctx, text, func(samples []int16) error {
				if err := q.ctx.Err(); err != nil {
					return err
				}
				s.add(samples)
				return nil
			})
			if err != nil {
				q.fail(err)
			}
			s.finish(err)
		}()
		q.handles <- s // never waits: there are no more sentences than slots
	}
}

// play emits each sentence's voice as it arrives, in order, and frees its slot once it has played.
func (q *ttsQueue) play() {
	defer close(q.done)
	for s := range q.handles {
		err := q.playOne(s)
		<-q.slots
		if err != nil {
			q.fail(err)
			q.cancel()
			for range q.handles {
				<-q.slots
			}
			return
		}
	}
	if err := q.ctx.Err(); err != nil {
		q.fail(err)
	}
}

func (q *ttsQueue) playOne(s *ttsSentence) error {
	for {
		chunks, done, err := s.take()
		for _, c := range chunks {
			if err := q.ctx.Err(); err != nil {
				return err
			}
			if err := q.emit(c); err != nil {
				return err
			}
			q.mu.Lock()
			if q.first.IsZero() {
				q.first = time.Now()
			}
			q.mu.Unlock()
		}
		if done {
			return err
		}
		select {
		case <-s.more:
		case <-q.ctx.Done():
			return q.ctx.Err()
		}
	}
}

func (s *ttsSentence) add(samples []int16) {
	s.mu.Lock()
	s.chunks = append(s.chunks, samples)
	s.mu.Unlock()
	s.signal()
}

func (s *ttsSentence) finish(err error) {
	s.mu.Lock()
	s.done, s.err = true, err
	s.mu.Unlock()
	s.signal()
}

func (s *ttsSentence) signal() {
	select {
	case s.more <- struct{}{}:
	default:
	}
}

// take hands over what has arrived since the last take, and whether that is all.
func (s *ttsSentence) take() (chunks [][]int16, done bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chunks, s.chunks = s.chunks, nil
	return chunks, s.done, s.err
}
