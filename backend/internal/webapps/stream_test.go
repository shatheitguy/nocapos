package webapps

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeStarts struct {
	mu      sync.Mutex
	started []*streamSession
	stopped int
	done    map[string]chan struct{}
	fail    error
}

func newTestStreams(t *testing.T) (*Streams, *fakeStarts) {
	t.Helper()
	f := &fakeStarts{done: map[string]chan struct{}{}}
	m := NewStreams(slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
	m.start = func(_ context.Context, s *streamSession, _ string) (*os.Process, <-chan struct{}, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail != nil {
			return nil, nil, f.fail
		}
		f.started = append(f.started, s)
		d := make(chan struct{})
		f.done[s.id] = d
		return &os.Process{Pid: 1}, d, nil
	}
	m.stop = func(*os.Process) {
		f.mu.Lock()
		f.stopped++
		f.mu.Unlock()
	}
	t.Cleanup(m.Close)
	return m, f
}

func waitReady(t *testing.T, m *Streams, user, id string) StreamStatus {
	t.Helper()
	for i := 0; i < 200; i++ {
		st, ok := m.Status(user, id)
		if !ok {
			t.Fatalf("session %s vanished", id)
		}
		if st.Phase != PhaseStarting {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("session never left starting")
	return StreamStatus{}
}

func TestStreamsOneSessionPerAppPerPerson(t *testing.T) {
	m, f := newTestStreams(t)
	spec := StreamSpec{Key: "flatpak:org.gimp.GIMP", Title: "GIMP", Command: []string{"flatpak", "run", "org.gimp.GIMP"}}
	a, err := m.Open("alex", spec)
	if err != nil {
		t.Fatal(err)
	}
	if st := waitReady(t, m, "alex", a.ID); !st.Ready {
		t.Fatalf("not ready: %+v", st)
	}
	again, _ := m.Open("alex", spec)
	if again.ID != a.ID {
		t.Fatal("a second open should reuse the session")
	}
	other, _ := m.Open("sam", spec)
	if other.ID == a.ID {
		t.Fatal("another person gets their own session")
	}
	waitReady(t, m, "sam", other.ID)

	// Nobody reaches someone else's stream.
	if _, ok := m.Target("sam", a.ID); ok {
		t.Fatal("sam reached alex's stream")
	}
	if _, ok := m.Status("sam", a.ID); ok {
		t.Fatal("sam saw alex's stream")
	}
	if m.Stop("sam", a.ID) {
		t.Fatal("sam stopped alex's stream")
	}
	port, ok := m.Target("alex", a.ID)
	if !ok || port < streamWebPort0 || port >= streamWebPort0+streamSlots {
		t.Fatalf("target = %d %v", port, ok)
	}
	// Never Brave's display or ports.
	f.mu.Lock()
	for _, s := range f.started {
		if s.slot.WebPort == braveWebPort || s.slot.VNCPort == 5999 || s.slot.Display == ":99" {
			t.Errorf("stream collides with Brave: %+v", s.slot)
		}
	}
	f.mu.Unlock()
}

func TestStreamsIdleReapAndExit(t *testing.T) {
	m, f := newTestStreams(t)
	now := time.Now()
	m.now = func() time.Time { return now }
	st, _ := m.Open("alex", StreamSpec{Key: "k", Title: "App", Command: []string{"app"}})
	waitReady(t, m, "alex", st.ID)

	release := m.Hold(st.ID) // the window is open
	now = now.Add(2 * DefaultStreamIdle)
	m.Reap()
	if _, ok := m.Target("alex", st.ID); !ok {
		t.Fatal("a watched stream must not be reaped")
	}
	release()
	now = now.Add(2 * DefaultStreamIdle)
	m.Reap()
	if _, ok := m.Target("alex", st.ID); ok {
		t.Fatal("idle stream should end")
	}
	f.mu.Lock()
	stopped := f.stopped
	f.mu.Unlock()
	if stopped != 1 {
		t.Fatalf("stopped %d processes, want 1", stopped)
	}

	// The app closing ends the session.
	st2, _ := m.Open("alex", StreamSpec{Key: "k2", Title: "Other", Command: []string{"other"}})
	waitReady(t, m, "alex", st2.ID)
	f.mu.Lock()
	close(f.done[st2.ID])
	f.mu.Unlock()
	for i := 0; i < 200; i++ {
		if s, _ := m.Status("alex", st2.ID); !s.Ready {
			if !strings.Contains(s.Message, "closed") {
				t.Fatalf("message = %q", s.Message)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("exited app still ready")
}

func TestStreamsStartErrorAndSlots(t *testing.T) {
	m, f := newTestStreams(t)
	f.fail = errors.New("Xvfb is missing")
	st, _ := m.Open("alex", StreamSpec{Key: "k", Title: "App", Command: []string{"app"}})
	got := waitReady(t, m, "alex", st.ID)
	if got.Phase != PhaseError || got.Message != "Xvfb is missing" {
		t.Fatalf("got %+v", got)
	}
	f.fail = nil
	m.sessions = map[string]*streamSession{}
	for i := 0; i < streamSlots; i++ {
		if _, err := m.Open("u", StreamSpec{Key: "k" + strings.Repeat("x", i), Command: []string{"a"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Open("u", StreamSpec{Key: "one-too-many", Command: []string{"a"}}); !errors.Is(err, ErrNoSlots) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidStreamID(t *testing.T) {
	if !ValidStreamID(newStreamID()) {
		t.Fatal("own id rejected")
	}
	for _, bad := range []string{"", "../etc", "ABCDEFABCDEFABCDEFABCDEF", strings.Repeat("a", 25)} {
		if ValidStreamID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
