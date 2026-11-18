package osimage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Phases of a system update.
const (
	PhaseInstalling = "installing" // writing the new system into the spare slot
	PhaseReady      = "ready"      // written: "Restart to finish"
	PhaseRestarting = "restarting" // the trial boot of the new system was asked for
	PhaseFailed     = "failed"     // the install itself failed: nothing changed
)

// Outcomes of a finished update, worked out after the restart.
const (
	OutcomeUpdated    = "updated"     // the new system passed its check and is kept
	OutcomeRolledBack = "rolled_back" // it failed its check: back on the previous system
	OutcomeChecking   = "checking"    // running the new system on trial right now
)

// State is os-update.json in the data folder: the update in flight or the
// last one, so a restart (of NoCapOS or the machine) loses nothing.
type State struct {
	Phase    string    `json:"phase"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Security bool      `json:"security,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	Progress float64   `json:"progress"` // 0-100 while installing
	Error    string    `json:"error,omitempty"`
	// Restarted: when the trial boot was asked for.
	Restarted time.Time `json:"restarted,omitzero"`
	// Seen: the outcome was shown once (a rollback stays until dismissed).
	Seen bool `json:"seen,omitempty"`
}

// BootCheck is os-boot-check.json, written by /usr/lib/nocapos/health-commit
// after a trial boot.
type BootCheck struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version"`
	Group     string `json:"group"`
	Reason    string `json:"reason"`
	CheckedAt string `json:"checked_at"`
}

// Slots is what Rugix Ctrl says about the boot groups.
type Slots struct {
	Active  string `json:"active"`
	Default string `json:"default"`
}

// Trial reports whether the running system is on trial (not yet the default).
func (s Slots) Trial() bool { return s.Active != "" && s.Default != "" && s.Active != s.Default }

// Ctrl runs Rugix Ctrl. Replaced in tests.
type Ctrl interface {
	// Install writes the bundle at url into the spare slot without changing
	// what boots next, reporting progress (0-100).
	Install(ctx context.Context, url, bundleHash string, progress func(float64)) error
	// RestartIntoSpare boots the spare slot once.
	RestartIntoSpare(ctx context.Context) error
	Slots(ctx context.Context) (Slots, error)
}

// Updater runs system updates on an image install.
type Updater struct {
	DataDir string
	Info    Info
	Ctrl    Ctrl
	// SignedOnly: the image requires signed bundles (a signing root is
	// configured), so the bundle hash isn't passed and the signature decides.
	SignedOnly bool

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

// NewUpdater returns the updater for the running image.
func NewUpdater(dataDir string, info Info) *Updater {
	return &Updater{DataDir: dataDir, Info: info, Ctrl: RugixCtrl{}, SignedOnly: signingRootConfigured()}
}

func (u *Updater) statePath() string     { return filepath.Join(u.DataDir, "os-update.json") }
func (u *Updater) bootCheckPath() string { return filepath.Join(u.DataDir, "os-boot-check.json") }

// State returns the saved state (nil when there never was an update).
func (u *Updater) State() *State {
	b, err := os.ReadFile(u.statePath())
	if err != nil {
		return nil
	}
	var st State
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	return &st
}

func (u *Updater) save(st *State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := u.statePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, u.statePath())
}

// BootCheck returns the last post-update health check's result, if any.
func (u *Updater) BootCheck() *BootCheck {
	b, err := os.ReadFile(u.bootCheckPath())
	if err != nil {
		return nil
	}
	var c BootCheck
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return &c
}

// Running reports whether an install is in progress in this process.
func (u *Updater) Running() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.running
}

// ErrBusy: an update is already being installed.
var ErrBusy = errors.New("a system update is already being installed")

// ErrRestartPending: an update is written and waits for the restart.
var ErrRestartPending = errors.New("an update is ready: restart to finish it first")

// Start installs m's system for this machine in the background. The
// machine keeps running; the new system is used after "Restart to finish".
func (u *Updater) Start(m *Manifest) error {
	t, err := m.Check(u.Info.Target, u.Info.Version)
	if err != nil {
		return err
	}
	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		return ErrBusy
	}
	u.running = true
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.mu.Unlock()

	st := &State{Phase: PhaseInstalling, From: u.Info.Version, To: m.Version, Security: m.Security, Started: time.Now().UTC()}
	if err := u.save(st); err != nil {
		u.finish()
		cancel()
		return err
	}
	hash := t.BundleHash
	if u.SignedOnly && t.Signed {
		hash = "" // the image's signing root decides
	}
	go func() {
		defer cancel()
		defer u.finish()
		var mu sync.Mutex
		last := time.Time{}
		err := u.Ctrl.Install(ctx, t.URL, hash, func(p float64) {
			mu.Lock()
			defer mu.Unlock()
			st.Progress = p
			if time.Since(last) > time.Second {
				last = time.Now()
				_ = u.save(st)
			}
		})
		mu.Lock()
		defer mu.Unlock()
		st.Finished = time.Now().UTC()
		if err != nil {
			st.Phase, st.Error = PhaseFailed, err.Error()
		} else {
			st.Phase, st.Progress = PhaseReady, 100
		}
		_ = u.save(st)
	}()
	return nil
}

func (u *Updater) finish() {
	u.mu.Lock()
	u.running = false
	u.cancel = nil
	u.mu.Unlock()
}

// Cancel stops an install in progress.
func (u *Updater) Cancel() {
	u.mu.Lock()
	c := u.cancel
	u.mu.Unlock()
	if c != nil {
		c()
	}
}

// Restart boots the written update once ("Restart to finish").
func (u *Updater) Restart(ctx context.Context) error {
	st := u.State()
	if st == nil || st.Phase != PhaseReady {
		return errors.New("no update is waiting for a restart")
	}
	st.Phase, st.Restarted = PhaseRestarting, time.Now().UTC()
	if err := u.save(st); err != nil {
		return err
	}
	_ = os.Remove(u.bootCheckPath())
	return u.Ctrl.RestartIntoSpare(ctx)
}

// Dismiss hides a finished update's outcome (the "OK" on a rollback notice).
func (u *Updater) Dismiss() error {
	st := u.State()
	if st == nil {
		return nil
	}
	st.Seen = true
	return u.save(st)
}

// Outcome works out how the last restarted update went: kept, rolled back
// or still on trial. "" when no update was restarted into.
func Outcome(st *State, running string, slots Slots, check *BootCheck) (outcome, reason string) {
	if st == nil || st.Phase != PhaseRestarting {
		return "", ""
	}
	onNew := running == st.To || (st.To != "" && !Newer(st.To, running) && !Newer(running, st.To))
	switch {
	case onNew && slots.Trial():
		return OutcomeChecking, ""
	case onNew:
		return OutcomeUpdated, ""
	default:
		// Back on the old system: the new one failed its check, or didn't
		// boot far enough to run it (then there's no record of this try).
		if check != nil && !check.OK && check.Version == st.To {
			return OutcomeRolledBack, check.Reason
		}
		return OutcomeRolledBack, "did_not_start"
	}
}

// ---- Rugix Ctrl ----------------------------------------------------------------

// RugixCtrl is the real rugix-ctrl.
type RugixCtrl struct{}

// Install runs `rugix-ctrl update install --reboot no`. With a block index
// on the slots, only the blocks that changed are downloaded.
func (RugixCtrl) Install(ctx context.Context, url, bundleHash string, progress func(float64)) error {
	args := []string{"update", "install", "--reboot", "no"}
	if bundleHash != "" {
		args = append(args, "--bundle-hash", bundleHash)
	}
	args = append(args, url)
	cmd := exec.CommandContext(ctx, "rugix-ctrl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitWriter{w: &stderr, n: 8 << 10}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the system updater: %w", err)
	}
	ParseProgress(stdout, progress)
	if err := cmd.Wait(); err != nil {
		msg := lastLine(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("the system update didn't install: %s", msg)
	}
	return nil
}

// RestartIntoSpare: `rugix-ctrl system reboot --spare`.
func (RugixCtrl) RestartIntoSpare(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "rugix-ctrl", "system", "reboot", "--spare").CombinedOutput()
	if err != nil {
		return fmt.Errorf("restarting into the new system: %s", lastLine(string(out)))
	}
	return nil
}

// Slots: `rugix-ctrl system info --json`.
func (RugixCtrl) Slots(ctx context.Context) (Slots, error) {
	out, err := exec.CommandContext(ctx, "rugix-ctrl", "system", "info", "--json").Output()
	if err != nil {
		return Slots{}, err
	}
	return ParseSlots(out)
}

// ParseSlots reads `rugix-ctrl system info --json`.
func ParseSlots(b []byte) (Slots, error) {
	var info struct {
		Boot struct {
			ActiveGroup  string `json:"activeGroup"`
			DefaultGroup string `json:"defaultGroup"`
		} `json:"boot"`
	}
	if err := json.Unmarshal(b, &info); err != nil {
		return Slots{}, err
	}
	return Slots{Active: info.Boot.ActiveGroup, Default: info.Boot.DefaultGroup}, nil
}

// ParseProgress reads rugix-ctrl's JSON event lines
// ({"event":"UpdateProgress","progress":42.5}) and reports 0-100.
func ParseProgress(r io.Reader, progress func(float64)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev struct {
			Event    string   `json:"event"`
			Progress *float64 `json:"progress"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Event != "UpdateProgress" || ev.Progress == nil {
			continue
		}
		p := *ev.Progress // a percentage
		if p < 0 {
			p = 0
		}
		if p > 100 {
			p = 100
		}
		progress(p)
	}
}

// signingRootConfigured: /etc/rugix/ctrl.toml lists signing roots.
func signingRootConfigured() bool {
	b, err := os.ReadFile("/etc/rugix/ctrl.toml")
	return err == nil && strings.Contains(string(b), "roots")
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		q = q[:l.n]
	}
	l.n -= len(q)
	_, _ = l.w.Write(q)
	return len(p), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 300 {
				l = l[:300]
			}
			return l
		}
	}
	return ""
}

// FormatPercent is a progress number for logs.
func FormatPercent(p float64) string { return strconv.FormatFloat(p, 'f', 1, 64) + "%" }
