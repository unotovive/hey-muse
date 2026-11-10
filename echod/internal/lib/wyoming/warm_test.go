package wyoming

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

// Warm は transcribe を 1 つ送って閉じるだけ: 母艦はそれを会話の始まりとして、冷えた部品を起こす。
func TestWarmSendsTranscribeAndCloses(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := make(chan []string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		var types []string
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				break
			}
			var h header
			if json.Unmarshal(line, &h) == nil {
				types = append(types, h.Type)
				if h.DataLength > 0 {
					_, _ = io.CopyN(io.Discard, r, int64(h.DataLength))
				}
			}
		}
		got <- types
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Warm(ctx, l.Addr().String(), "ja"); err != nil {
		t.Fatal(err)
	}
	select {
	case types := <-got:
		if len(types) != 1 || types[0] != "transcribe" {
			t.Fatalf("sent %v, want [transcribe]", types)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connection was not closed")
	}
}

func TestWarmNoServer(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	if Warm(context.Background(), addr, "ja") == nil {
		t.Fatal("want an error")
	}
}
