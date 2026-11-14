package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPickType(t *testing.T) {
	for _, c := range []struct {
		types []string
		want  string
		ok    bool
	}{
		{[]string{"TARGETS", "UTF8_STRING", "image/png"}, "UTF8_STRING", true},
		{[]string{"image/jpeg", "image/png"}, "image/png", true},
		{[]string{"text/plain", "text/plain;charset=utf-8"}, "text/plain;charset=utf-8", true},
		{[]string{"UTF8_STRING", "x-kde-passwordManagerHint"}, "", false}, // a password manager's secret
		{[]string{"image/gif", "application/x-foo"}, "", false},
		{nil, "", false},
	} {
		got, ok := pickType(c.types)
		if got != c.want || ok != c.ok {
			t.Errorf("%v: %q %v", c.types, got, ok)
		}
	}
}

// fakeClip is a desktop clipboard in memory.
type fakeClip struct {
	mu  sync.Mutex
	cur localClip
	set chan localClip
}

func (f *fakeClip) name() string { return "fake" }
func (f *fakeClip) watch(ctx context.Context, _ chan<- struct{}) error {
	<-ctx.Done()
	return ctx.Err()
}
func (f *fakeClip) read(context.Context) (localClip, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur, len(f.cur.data) > 0
}
func (f *fakeClip) write(_ context.Context, c localClip) error {
	f.mu.Lock()
	f.cur = c
	f.mu.Unlock()
	f.set <- c
	return nil
}

func TestClipAgentLoop(t *testing.T) {
	var mu sync.Mutex
	var posted []string
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/events":
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			ev := agentEvents{Cursor: int64(n), Enabled: true}
			if n == 2 {
				ev.Clip = &agentClip{ID: 2, Kind: "text", Text: "from phone"}
			}
			if n > 2 {
				time.Sleep(50 * time.Millisecond)
			}
			_ = json.NewEncoder(w).Encode(ev)
		case "/v1/clip":
			var req struct{ Text string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			posted = append(posted, req.Text)
			mu.Unlock()
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()
	as := agentServer{c: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}}}}

	fc := &fakeClip{set: make(chan localClip, 4)}
	g := &clipAgent{tool: fc, srv: as, log: log.New(io.Discard, "", 0)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed := make(chan struct{}, 1)
	go g.follow(ctx)
	go g.share(ctx, changed)

	// Phone → desktop.
	select {
	case c := <-fc.set:
		if string(c.data) != "from phone" || c.mime != "text" {
			t.Fatalf("set: %+v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the phone's clip never reached the desktop")
	}
	// The change it caused isn't sent back.
	changed <- struct{}{}
	time.Sleep(400 * time.Millisecond)
	// A real copy on the desktop is.
	fc.mu.Lock()
	fc.cur = localClip{mime: "text", data: []byte("copied on desktop")}
	fc.mu.Unlock()
	changed <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(posted)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posted) != 1 || posted[0] != "copied on desktop" {
		t.Fatalf("posted: %q", posted)
	}
}
