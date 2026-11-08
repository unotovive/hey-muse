package p2pquake

import (
	"context"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
)

// URL is P2P 地震情報's WebSocket. It allows two connections from one address; a device holds one.
const URL = "wss://api.p2pquake.net/v2/ws"

// backoffBase is the first wait before connecting again; each failure doubles it, up to maxBackoff.
var backoffBase = time.Second

const (
	maxBackoff = time.Minute

	// stable is how long a connection has to have lasted for the next failure to start the waits
	// over: a server that takes the connection and drops it at once is not hammered every second.
	stable = time.Minute

	// pingEvery and readFor keep a connection that died without a word from being waited on forever:
	// a ping goes out every pingEvery, and nothing back (a message or the pong) for readFor ends it.
	pingEvery = 30 * time.Second
	readFor   = 90 * time.Second

	// readLimit is far more than any message is.
	readLimit = 1 << 20
)

// backoff is the wait after the n-th failure in a row (from 0).
func backoff(n int) time.Duration {
	d := backoffBase
	for range n {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return min(d, maxBackoff)
}

// Run keeps a connection to url until ctx is done, handing on each message that is shown (Parse)
// to on, in the order they come. A message that cannot be read is logged and skipped. A connection
// that fails or ends is made again after 1, 2, 4… seconds, at most a minute apart.
func Run(ctx context.Context, url string, on func(Alert)) {
	fails := 0
	for ctx.Err() == nil {
		began := time.Now()
		err := session(ctx, url, on)
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) >= stable {
			fails = 0
		}
		wait := backoff(fails)
		fails++
		slog.Warn("p2pquake: disconnected", "err", err, "retry in", wait)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// session is one connection, read until it ends.
func session(ctx context.Context, url string, on func(Alert)) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return err
	}
	slog.Info("p2pquake: connected", "url", url)
	conn.SetReadLimit(readLimit)
	alive := func() { _ = conn.SetReadDeadline(time.Now().Add(readFor)) }
	alive()
	conn.SetPongHandler(func(string) error { alive(); return nil })

	// Closing the connection is what ends a read waiting on it, for a cancel and for the pings alike.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(pingEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = conn.Close()
				return
			case <-stop:
				_ = conn.Close()
				return
			case <-tick.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()

	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		alive()
		if kind != websocket.TextMessage {
			continue
		}
		a, ok, err := Parse(data)
		if err != nil {
			slog.Warn("p2pquake: unreadable message", "err", err, "bytes", len(data))
			continue
		}
		if ok {
			on(a)
		}
	}
}
