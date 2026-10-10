package notify

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// CrashWatch decides which container exits are worth a notification: an App
// Store app's container that died on its own with an error. Exits NoCapOS
// caused (stop, restart, recreate, update, uninstall) are ignored, and a crash
// loop notifies at most once per app every CrashEvery.
type CrashWatch struct {
	// Busy reports whether the App Store is installing, updating or removing
	// the app right now (its containers come and go on purpose then).
	Busy func(app string) bool
	now  func() time.Time

	mu       sync.Mutex
	expected map[string]time.Time // container id/name or "app:<id>" → when NoCapOS touched it
	killed   map[string]time.Time // container id → last "kill" event (someone stopped it)
	lastApp  map[string]time.Time // app → last crash notification
}

const (
	// ExpectFor is how long a container NoCapOS stopped or recreated stays quiet.
	ExpectFor = 2 * time.Minute
	// CrashEvery limits crash notifications to one per app in this window.
	CrashEvery = 10 * time.Minute
	killGrace  = 30 * time.Second
)

func NewCrashWatch() *CrashWatch {
	return &CrashWatch{now: time.Now, expected: map[string]time.Time{}, killed: map[string]time.Time{}, lastApp: map[string]time.Time{}}
}

// Expect marks refs (container ids or names, or "app:<id>") as being stopped,
// restarted or recreated by NoCapOS for the next ExpectFor.
func (w *CrashWatch) Expect(refs ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	for _, r := range refs {
		if r = strings.TrimPrefix(r, "/"); r != "" {
			w.expected[r] = now
		}
	}
}

// ContainerExit is a Docker "die" (or "kill") event of a container.
type ContainerExit struct {
	Action   string // die | kill
	ID       string
	Name     string
	App      string // the nocapos.app label ("" = not an App Store app)
	ExitCode string
	Time     time.Time
}

// Crash is a container exit that should be notified.
type Crash struct {
	Key      string
	App      string
	ExitCode string
}

// Observe feeds one event and returns the crash to notify, if any.
func (w *CrashWatch) Observe(e ContainerExit) (Crash, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	w.forget(now)
	switch e.Action {
	case "kill": // docker stop/kill (from anywhere) sends kill before die
		w.killed[e.ID] = now
		return Crash{}, false
	case "die":
	default:
		return Crash{}, false
	}
	if e.App == "" {
		return Crash{}, false // only App Store apps notify
	}
	switch strings.TrimSpace(e.ExitCode) {
	case "", "0", "143": // clean exit, or SIGTERM from a stop
		return Crash{}, false
	}
	if t, ok := w.killed[e.ID]; ok && now.Sub(t) < killGrace {
		return Crash{}, false
	}
	if w.Busy != nil && w.Busy(e.App) {
		return Crash{}, false
	}
	name := strings.TrimPrefix(e.Name, "/")
	for ref, t := range w.expected {
		if now.Sub(t) >= ExpectFor {
			continue
		}
		if ref == "app:"+e.App || ref == name || (len(ref) >= 12 && strings.HasPrefix(e.ID, ref)) {
			return Crash{}, false
		}
	}
	if t, ok := w.lastApp[e.App]; ok && now.Sub(t) < CrashEvery {
		return Crash{}, false
	}
	w.lastApp[e.App] = now
	at := e.Time
	if at.IsZero() {
		at = now
	}
	id := e.ID
	if len(id) > 12 {
		id = id[:12]
	}
	return Crash{Key: fmt.Sprintf("appcrash:%s:%d", id, at.Unix()/int64(CrashEvery/time.Second)), App: e.App, ExitCode: e.ExitCode}, true
}

// forget drops old marks so the maps stay small.
func (w *CrashWatch) forget(now time.Time) {
	for k, t := range w.expected {
		if now.Sub(t) >= ExpectFor {
			delete(w.expected, k)
		}
	}
	for k, t := range w.killed {
		if now.Sub(t) >= killGrace {
			delete(w.killed, k)
		}
	}
}
