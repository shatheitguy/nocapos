package hostaccess

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- is NoCap Desktop confirmed working here? ----
//
// The earlier desktop's packages only go once NoCap Desktop has proven
// itself on this machine: a person's NoCap session with NoCap Shell up and
// signed in (it fetched its session from alfad) and the screen check
// healthy, for a while, more than once. Recorded in the data folder
// (desktop-confirmed.json), so it survives restarts and updates.

// ConfirmFileName is in the data folder.
const ConfirmFileName = "desktop-confirmed.json"

// Confirmation thresholds: two separate shell runs and two hours in all,
// or eight hours in one go.
const (
	confirmRunMinutes   = 5 // a shell run counts once it stayed up this long
	confirmMinRuns      = 2
	confirmMinMinutes   = 120
	confirmSingleMinute = 8 * 60
)

// DesktopConfirm is the record.
type DesktopConfirm struct {
	// Runs: NoCap Shell runs (in a person's session) that stayed up at
	// least confirmRunMinutes; Minutes: healthy minutes in all.
	Runs      int       `json:"runs"`
	Minutes   int       `json:"minutes"`
	First     time.Time `json:"first,omitempty"`
	Confirmed time.Time `json:"confirmed,omitempty"`
}

// IsConfirmed: NoCap Desktop has proven itself here.
func (c DesktopConfirm) IsConfirmed() bool { return !c.Confirmed.IsZero() }

// confirmed reports whether the counts are enough.
func (c DesktopConfirm) enough() bool {
	return c.Minutes >= confirmMinMinutes && (c.Runs >= confirmMinRuns || c.Minutes >= confirmSingleMinute)
}

// confirmState is what the tracker keeps between ticks.
type confirmState struct {
	mu        sync.Mutex
	loaded    bool
	rec       DesktopConfirm
	pid       int       // the NoCap Shell being watched
	pidSince  time.Time // when it was first seen
	counted   bool      // its run was counted
	lastTick  time.Time
	lastSave  time.Time
	shellSeen time.Time // NoCap Shell last fetched a session from alfad
}

func (d *Desktop) confirmPath() string {
	dir := d.DataDir
	if dir == "" {
		dir = "/var/lib/nocapos"
	}
	return path.Join(filepath.ToSlash(dir), ConfirmFileName)
}

// NoteShellSession: NoCap Shell just got its session from alfad (the
// socket handler calls this), so its pages are up and signed in.
func (d *Desktop) NoteShellSession() {
	d.cs.mu.Lock()
	d.cs.shellSeen = d.now()
	d.cs.mu.Unlock()
}

// Confirmation is the record as it stands.
func (d *Desktop) Confirmation() DesktopConfirm {
	d.cs.mu.Lock()
	defer d.cs.mu.Unlock()
	d.loadConfirmLocked()
	return d.cs.rec
}

func (d *Desktop) loadConfirmLocked() {
	if d.cs.loaded {
		return
	}
	d.cs.loaded = true
	if b, err := d.readFile(d.confirmPath()); err == nil {
		var c DesktopConfirm
		if json.Unmarshal(b, &c) == nil && c.Runs >= 0 && c.Minutes >= 0 {
			d.cs.rec = c
		}
	}
}

func (d *Desktop) saveConfirmLocked() {
	b, _ := json.Marshal(d.cs.rec)
	if d.writeFile(d.confirmPath(), append(b, '\n'), 0o644) == nil {
		d.cs.lastSave = d.now()
	}
}

// shellPID finds the NoCap Shell (not an app window) of uid: the oldest
// nocap-shell process whose command line has no options.
func shellPID(procRoot string, uid int) int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0
	}
	best := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		comm, err := os.ReadFile(filepath.Join(dir, "comm"))
		if err != nil || strings.TrimSpace(string(comm)) != "nocap-shell" || procUID(filepath.Join(dir, "status")) != uid {
			continue
		}
		cmd, _ := os.ReadFile(filepath.Join(dir, "cmdline"))
		args := strings.Split(strings.TrimRight(string(cmd), "\x00"), "\x00")
		if len(args) > 1 {
			continue // --app, --greeter, --toggle ...
		}
		if best == 0 || pid < best {
			best = pid
		}
	}
	return best
}

// TrackConfirm adds up how long NoCap Desktop has worked (WatchSession
// calls it about once a minute). It returns the record.
func (d *Desktop) TrackConfirm(ctx context.Context) DesktopConfirm {
	if !d.Native || !d.conf().Full {
		return DesktopConfirm{}
	}
	now := d.now()
	var pid int
	healthy := false
	if s, ok := d.ActiveSession(ctx); ok {
		pid = shellPID(d.procRoot(), s.UID)
		if pid != 0 {
			st := EvaluateHealth(d.gather(ctx)).Status
			healthy = st == "ok" || st == "warning"
		}
	}
	d.cs.mu.Lock()
	defer d.cs.mu.Unlock()
	d.loadConfirmLocked()
	elapsed := now.Sub(d.cs.lastTick)
	d.cs.lastTick = now
	if d.cs.rec.IsConfirmed() {
		return d.cs.rec
	}
	signedIn := !d.cs.shellSeen.IsZero()
	if pid == 0 || !healthy || !signedIn {
		if pid != d.cs.pid {
			d.cs.pid, d.cs.pidSince, d.cs.counted = 0, time.Time{}, false
		}
		return d.cs.rec
	}
	if pid != d.cs.pid {
		// A new shell run (a new session, or it restarted).
		d.cs.pid, d.cs.pidSince, d.cs.counted = pid, now, false
		return d.cs.rec
	}
	changed := false
	if now.Sub(d.cs.pidSince) >= confirmRunMinutes*time.Minute {
		if !d.cs.counted {
			d.cs.counted = true
			d.cs.rec.Runs++
			if d.cs.rec.First.IsZero() {
				d.cs.rec.First = now
			}
			changed = true
		}
		// Whole minutes since the last tick (ticks are about a minute apart;
		// a long gap, e.g. a suspend, counts as one).
		if elapsed > 0 && elapsed < 5*time.Minute {
			d.cs.rec.Minutes += max(1, int(elapsed.Round(time.Minute)/time.Minute))
		} else {
			d.cs.rec.Minutes++
		}
	}
	if d.cs.rec.enough() {
		d.cs.rec.Confirmed = now
		changed = true
	}
	if changed || now.Sub(d.cs.lastSave) >= 10*time.Minute {
		d.saveConfirmLocked()
	}
	return d.cs.rec
}
