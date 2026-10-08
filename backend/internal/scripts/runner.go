// Package scripts runs saved Quick Script Launcher scripts on the host and
// keeps their recent output in memory.
package scripts

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"sync"
	"time"
)

const (
	maxOutput = 256 << 10 // output kept per run
	maxRuns   = 50        // finished runs kept in memory
	Timeout   = 15 * time.Minute
)

var ErrDisabled = errors.New("running scripts on the host is disabled")

// Run is one execution of a script.
type Run struct {
	ID        string     `json:"id"`
	ScriptID  string     `json:"script_id"`
	Name      string     `json:"name"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Running   bool       `json:"running"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	Output    string     `json:"output"`
	Truncated bool       `json:"truncated"`
	Error     string     `json:"error,omitempty"`
}

type run struct {
	mu     sync.Mutex
	r      Run
	buf    bytes.Buffer
	cancel context.CancelFunc
}

// Write collects output, keeping at most maxOutput bytes.
func (x *run) Write(p []byte) (int, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if room := maxOutput - x.buf.Len(); room > 0 {
		if len(p) > room {
			x.buf.Write(p[:room])
			x.r.Truncated = true
		} else {
			x.buf.Write(p)
		}
	} else if len(p) > 0 {
		x.r.Truncated = true
	}
	return len(p), nil
}

func (x *run) snapshot() Run {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := x.r
	out.Output = x.buf.String()
	return out
}

// Runner executes scripts with the platform shell.
type Runner struct {
	enabled bool
	shell   []string // command prefix; the script body is appended

	mu    sync.Mutex
	runs  map[string]*run
	order []string
}

func NewRunner(enabled bool) *Runner {
	return &Runner{enabled: enabled, shell: detectShell(), runs: map[string]*run{}}
}

func detectShell() []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"}
	}
	for _, sh := range []string{"/bin/bash", "/usr/bin/bash"} {
		if _, err := os.Stat(sh); err == nil {
			return []string{sh, "-c"}
		}
	}
	return []string{"/bin/sh", "-c"}
}

// Info describes where scripts run, for the UI.
type Info struct {
	Enabled bool   `json:"enabled"`
	Shell   string `json:"shell"`
	User    string `json:"user"`
	Root    bool   `json:"root"`
	OS      string `json:"os"`
}

func (rn *Runner) Info() Info {
	in := Info{Enabled: rn.enabled, Shell: rn.shell[0], OS: runtime.GOOS, Root: runtime.GOOS != "windows" && os.Geteuid() == 0}
	if u, err := user.Current(); err == nil {
		in.User = u.Username
	}
	return in
}

// Start launches body in the background and returns the run id. onDone is
// called with the exit code once it finishes (-1 when it could not start).
func (rn *Runner) Start(scriptID, name, body string, onDone func(exit int, at time.Time)) (string, error) {
	if !rn.enabled {
		return "", ErrDisabled
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	x := &run{cancel: cancel, r: Run{ID: newID(), ScriptID: scriptID, Name: name, StartedAt: time.Now().UTC(), Running: true}}

	args := append(append([]string{}, rn.shell[1:]...), body)
	cmd := exec.CommandContext(ctx, rn.shell[0], args...)
	cmd.Stdout, cmd.Stderr = x, x
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	cmd.WaitDelay = 3 * time.Second
	prepare(cmd)

	rn.add(x)
	if err := cmd.Start(); err != nil {
		cancel()
		rn.finish(x, -1, err.Error())
		if onDone != nil {
			onDone(-1, time.Now())
		}
		return x.r.ID, nil
	}
	go func() {
		defer cancel()
		err := cmd.Wait()
		code, msg := 0, ""
		if err != nil {
			code = -1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
			switch {
			case errors.Is(ctx.Err(), context.DeadlineExceeded):
				msg = "Stopped: the script ran longer than 15 minutes."
			case errors.Is(ctx.Err(), context.Canceled):
				msg = "Stopped."
			case code == -1:
				msg = err.Error()
			}
		}
		rn.finish(x, code, msg)
		if onDone != nil {
			onDone(code, time.Now())
		}
	}()
	return x.r.ID, nil
}

func (rn *Runner) add(x *run) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.runs[x.r.ID] = x
	rn.order = append(rn.order, x.r.ID)
	// Forget the oldest finished runs beyond the cap.
	for len(rn.order) > maxRuns {
		old := rn.runs[rn.order[0]]
		if old != nil && old.snapshot().Running {
			break
		}
		delete(rn.runs, rn.order[0])
		rn.order = rn.order[1:]
	}
}

func (rn *Runner) finish(x *run, code int, msg string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	now := time.Now().UTC()
	x.r.Running, x.r.EndedAt, x.r.ExitCode, x.r.Error = false, &now, &code, msg
}

// Get returns a snapshot of a run.
func (rn *Runner) Get(id string) (Run, bool) {
	rn.mu.Lock()
	x := rn.runs[id]
	rn.mu.Unlock()
	if x == nil {
		return Run{}, false
	}
	return x.snapshot(), true
}

// Cancel stops a running script.
func (rn *Runner) Cancel(id string) bool {
	rn.mu.Lock()
	x := rn.runs[id]
	rn.mu.Unlock()
	if x == nil {
		return false
	}
	x.cancel()
	return true
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
