package p2pquake

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// server serves each connection the frames frames(n) gives for it (n from 1), then closes it.
func server(t *testing.T, frames func(n int) [][]byte) (url string, conns *atomic.Int32) {
	t.Helper()
	conns = new(atomic.Int32)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for _, f := range frames(int(conns.Add(1))) {
			if err := c.WriteMessage(websocket.TextMessage, f); err != nil {
				return
			}
		}
		_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		time.Sleep(20 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), conns
}

func shortBackoff(t *testing.T) {
	old := backoffBase
	backoffBase = 10 * time.Millisecond
	t.Cleanup(func() { backoffBase = old })
}

// collect runs Run until want alerts arrived (or a deadline), and returns them once Run has returned.
func collect(t *testing.T, url string, want int) []Alert {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu  sync.Mutex
		got []Alert
	)
	enough := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, url, func(a Alert) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, a)
			if len(got) == want {
				close(enough)
			}
		})
	}()
	select {
	case <-enough:
	case <-time.After(5 * time.Second):
		t.Error("alerts did not arrive")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]Alert(nil), got...)
}

func TestClientReconnectsAndDelivers(t *testing.T) {
	shortBackoff(t)
	f551, f556 := load(t, "551.json"), load(t, "556.json")
	url, conns := server(t, func(n int) [][]byte {
		switch n {
		case 1:
			return [][]byte{f551}
		case 2:
			return [][]byte{f556}
		}
		return nil
	})
	got := collect(t, url, 2)
	if len(got) != 2 || got[0].Code != 551 || got[1].Code != 556 {
		t.Fatalf("got %+v", got)
	}
	if n := conns.Load(); n < 2 {
		t.Errorf("connections %d, want 2 or more", n)
	}
}

func TestClientSkipsBadFrames(t *testing.T) {
	shortBackoff(t)
	f551, f561 := load(t, "551.json"), load(t, "561.json")
	url, _ := server(t, func(n int) [][]byte {
		if n == 1 {
			return [][]byte{[]byte(`{"code":`), f561, f551}
		}
		return nil
	})
	got := collect(t, url, 1)
	// The bad frames came before the 551, so anything made of them would have arrived first.
	if len(got) != 1 || got[0].Code != 551 {
		t.Fatalf("got %+v, want only the 551", got)
	}
}

func TestBackoff(t *testing.T) {
	shortBackoff(t)
	for n, want := range []time.Duration{10, 20, 40, 80, 160} {
		if got := backoff(n); got != want*time.Millisecond {
			t.Errorf("backoff(%d) = %v, want %v", n, got, want*time.Millisecond)
		}
	}
	backoffBase = time.Second
	if got := backoff(10); got != time.Minute {
		t.Errorf("backoff(10) = %v, want the 60 s cap", got)
	}
	if got := backoff(200); got != time.Minute {
		t.Errorf("backoff(200) = %v, want the 60 s cap", got)
	}
}
