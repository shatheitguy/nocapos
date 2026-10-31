package webapps

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"
)

// Streams runs native GUI apps on their own virtual displays and streams each
// one to the browser exactly the way Brave is streamed: Xvfb + x11vnc +
// noVNC/websockify on loopback ports, reached only through alfad's
// same-origin proxy. One session per app per person; idle sessions end.
//
// Brave keeps its own display (:99) and ports (5999/6080); app streams use
// a separate range so the two never collide.

const (
	streamSlots     = 32
	streamDisplay0  = 120  // :120 … :151
	streamVNCPort0  = 6200 // 6200 … 6231, loopback only
	streamWebPort0  = 6300 // 6300 … 6331, loopback only
	streamStartWait = 60 * time.Second
	// DefaultStreamIdle ends a stream nobody has looked at for this long.
	DefaultStreamIdle = 20 * time.Minute
)

// ErrNoSlots: every stream slot is taken.
var ErrNoSlots = errors.New("too many apps are streaming right now; close one and try again")

// RunAs is the Linux account a streamed app runs as (nil = alfad's own).
type RunAs struct {
	Username string
	UID, GID uint32
	Groups   []uint32
	Home     string
}

// StreamSpec says what to stream.
type StreamSpec struct {
	// Key identifies the app (one session per Key per person).
	Key   string
	Title string
	// Command is the app's argv, run inside the virtual display.
	Command []string
	RunAs   *RunAs
	// Home is HOME when RunAs is nil (alfad's own account).
	Home string
}

// StreamStatus is a session as the UI sees it.
type StreamStatus struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Title   string `json:"title"`
	Phase   Phase  `json:"phase"`
	Message string `json:"message"`
	Ready   bool   `json:"ready"`
}

// streamSlot is where a session lives: its display and loopback ports.
type streamSlot struct {
	Index   int
	Display string
	VNCPort int
	WebPort int
}

func slotAt(i int) streamSlot {
	return streamSlot{
		Index:   i,
		Display: ":" + strconv.Itoa(streamDisplay0+i),
		VNCPort: streamVNCPort0 + i,
		WebPort: streamWebPort0 + i,
	}
}

type streamSession struct {
	id      string
	userID  string
	spec    StreamSpec
	slot    streamSlot
	phase   Phase
	message string
	proc    *os.Process
	active  int // open proxy requests / WebSockets
	seen    time.Time
	ended   bool
}

func (s *streamSession) status() StreamStatus {
	return StreamStatus{ID: s.id, Key: s.spec.Key, Title: s.spec.Title, Phase: s.phase, Message: s.message, Ready: s.phase == PhaseReady}
}

// starter launches a session's processes and waits until its web port
// answers. It returns the process (its own group) and a channel closed when
// it exits. Swappable in tests.
type starter func(ctx context.Context, s *streamSession, logDir string) (*os.Process, <-chan struct{}, error)

// Streams supervises native app streams.
type Streams struct {
	log    *slog.Logger
	logDir string
	idle   time.Duration
	start  starter
	stop   func(*os.Process)
	now    func() time.Time

	mu       sync.Mutex
	sessions map[string]*streamSession
	closed   bool
}

// NewStreams makes the supervisor; logs go to dataDir/streams.
func NewStreams(log *slog.Logger, dataDir string) *Streams {
	return &Streams{
		log: log, logDir: dataDir, idle: DefaultStreamIdle,
		start: startStream, stop: stopProcess, now: time.Now,
		sessions: map[string]*streamSession{},
	}
}

// Supported reports whether app streams can run here (Linux only).
func (m *Streams) Supported() bool { return Supported }

// Open returns the person's session for spec.Key, starting one if needed.
// It returns at once; poll Status until Ready.
func (m *Streams) Open(userID string, spec StreamSpec) (StreamStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return StreamStatus{}, errors.New("NoCapOS is shutting down")
	}
	for _, s := range m.sessions {
		if s.userID == userID && s.spec.Key == spec.Key && !s.ended {
			s.seen = m.now()
			return s.status(), nil
		}
	}
	used := map[int]bool{}
	for _, s := range m.sessions {
		used[s.slot.Index] = true
	}
	slot := -1
	for i := 0; i < streamSlots; i++ {
		if !used[i] {
			slot = i
			break
		}
	}
	if slot < 0 {
		return StreamStatus{}, ErrNoSlots
	}
	s := &streamSession{
		id: newStreamID(), userID: userID, spec: spec, slot: slotAt(slot),
		phase: PhaseStarting, message: "Starting " + spec.Title + "…", seen: m.now(),
	}
	m.sessions[s.id] = s
	go m.run(s)
	return s.status(), nil
}

func (m *Streams) run(s *streamSession) {
	ctx, cancel := context.WithTimeout(context.Background(), streamStartWait)
	defer cancel()
	proc, done, err := m.start(ctx, s, m.logDir)
	m.mu.Lock()
	if err != nil {
		s.phase, s.message, s.ended = PhaseError, err.Error(), true
		m.mu.Unlock()
		m.log.Warn("app stream", "app", s.spec.Key, "err", err)
		return
	}
	if m.closed || s.ended { // stopped while starting
		m.mu.Unlock()
		m.stop(proc)
		return
	}
	s.proc, s.phase, s.message, s.seen = proc, PhaseReady, "Ready", m.now()
	m.mu.Unlock()
	go func() {
		<-done
		m.mu.Lock()
		defer m.mu.Unlock()
		s.proc = nil
		if !s.ended {
			s.ended, s.phase, s.message = true, PhaseIdle, s.spec.Title+" was closed"
		}
	}()
}

// Status reports a session that belongs to userID.
func (m *Streams) Status(userID, id string) (StreamStatus, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || s.userID != userID {
		return StreamStatus{}, false
	}
	return s.status(), true
}

// Target is the loopback web port of a ready session that belongs to userID.
func (m *Streams) Target(userID, id string) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || s.userID != userID || s.phase != PhaseReady || s.ended {
		return 0, false
	}
	return s.slot.WebPort, true
}

// Hold marks a session in use for the life of a proxied request (the VNC
// WebSocket holds it while the window is open); call the result when done.
func (m *Streams) Hold(id string) func() {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		s.active++
		s.seen = m.now()
	}
	m.mu.Unlock()
	if !ok {
		return func() {}
	}
	return func() {
		m.mu.Lock()
		s.active--
		s.seen = m.now()
		m.mu.Unlock()
	}
}

// Stop ends a session that belongs to userID.
func (m *Streams) Stop(userID, id string) bool {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok || s.userID != userID {
		m.mu.Unlock()
		return false
	}
	p := m.endLocked(s)
	m.mu.Unlock()
	if p != nil {
		m.stop(p)
	}
	return true
}

// StopUser ends every session of a person (signed out, removed).
func (m *Streams) StopUser(userID string) {
	m.mu.Lock()
	var procs []*os.Process
	for _, s := range m.sessions {
		if s.userID == userID {
			if p := m.endLocked(s); p != nil {
				procs = append(procs, p)
			}
		}
	}
	m.mu.Unlock()
	for _, p := range procs {
		m.stop(p)
	}
}

// StopKey ends every session of an app (it was uninstalled).
func (m *Streams) StopKey(key string) {
	m.mu.Lock()
	var procs []*os.Process
	for _, s := range m.sessions {
		if s.spec.Key == key {
			if p := m.endLocked(s); p != nil {
				procs = append(procs, p)
			}
		}
	}
	m.mu.Unlock()
	for _, p := range procs {
		m.stop(p)
	}
}

func (m *Streams) endLocked(s *streamSession) *os.Process {
	p := s.proc
	s.proc = nil
	s.ended = true
	if s.phase != PhaseError {
		s.phase, s.message = PhaseIdle, s.spec.Title+" was closed"
	}
	delete(m.sessions, s.id)
	return p
}

// Reap ends sessions nobody has used for the idle timeout, and forgets ended
// ones after a while. Run it periodically (Run does).
func (m *Streams) Reap() {
	now := m.now()
	m.mu.Lock()
	var procs []*os.Process
	for _, s := range m.sessions {
		switch {
		case s.ended && now.Sub(s.seen) > time.Minute:
			delete(m.sessions, s.id)
		case !s.ended && s.active == 0 && now.Sub(s.seen) > m.idle:
			if p := m.endLocked(s); p != nil {
				procs = append(procs, p)
			}
		}
	}
	m.mu.Unlock()
	for _, p := range procs {
		m.stop(p)
	}
}

// Run reaps idle sessions until ctx ends, then stops them all.
func (m *Streams) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.Close()
			return
		case <-t.C:
			m.Reap()
		}
	}
}

// Close stops every stream (on alfad shutdown).
func (m *Streams) Close() {
	m.mu.Lock()
	m.closed = true
	var procs []*os.Process
	for _, s := range m.sessions {
		if p := m.endLocked(s); p != nil {
			procs = append(procs, p)
		}
	}
	m.mu.Unlock()
	for _, p := range procs {
		m.stop(p)
	}
}

func newStreamID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ValidStreamID: what newStreamID makes (the proxy checks path segments).
func ValidStreamID(id string) bool {
	if len(id) != 24 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
