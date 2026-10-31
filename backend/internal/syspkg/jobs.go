package syspkg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	jobOutputMax = 512 << 10 // output kept per job (the tail)
	jobsKept     = 20
	jobTimeout   = time.Hour
)

// ErrBusy means another package job is still running (package managers
// hold a lock, so jobs run one at a time).
var ErrBusy = errors.New("another package task is still running; wait for it to finish")

// Step is one command of a job. Optional steps may fail without failing the
// job (a package a distribution doesn't have). Fn runs Go code instead.
type Step struct {
	Args     []string
	Optional bool
	Fn       func(ctx context.Context, out io.Writer) error
	Label    string // shown instead of Args (for Fn steps)
}

// Job is a package task as the UI sees it.
type Job struct {
	ID        string     `json:"id"`
	Action    string     `json:"action"`
	Target    string     `json:"target,omitempty"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Running   bool       `json:"running"`
	OK        bool       `json:"ok"`
	Output    string     `json:"output"`
	Error     string     `json:"error,omitempty"`
}

type job struct {
	mu  sync.Mutex
	j   Job
	buf []byte
}

func (x *job) Write(p []byte) (int, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.buf = append(x.buf, p...)
	if len(x.buf) > jobOutputMax {
		x.buf = append([]byte("…\n"), x.buf[len(x.buf)-jobOutputMax:]...)
	}
	return len(p), nil
}

func (x *job) snapshot() Job {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := x.j
	out.Output = string(x.buf)
	return out
}

// Jobs runs package tasks in the background, one at a time.
type Jobs struct {
	mu    sync.Mutex
	jobs  map[string]*job
	order []string
	busy  bool
	// exec builds a command (swappable in tests).
	exec func(ctx context.Context, args []string) *exec.Cmd
}

func NewJobs() *Jobs {
	return &Jobs{jobs: map[string]*job{}, exec: func(ctx context.Context, args []string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a", "LC_ALL=C")
		cmd.WaitDelay = 5 * time.Second
		return cmd
	}}
}

// Start runs steps in the background. onDone gets the result (for auditing).
func (js *Jobs) Start(action, target string, steps []Step, onDone func(Job)) (Job, error) {
	js.mu.Lock()
	if js.busy {
		js.mu.Unlock()
		return Job{}, ErrBusy
	}
	js.busy = true
	x := &job{j: Job{ID: newJobID(), Action: action, Target: target, StartedAt: time.Now().UTC(), Running: true}}
	js.jobs[x.j.ID] = x
	js.order = append(js.order, x.j.ID)
	for len(js.order) > jobsKept {
		delete(js.jobs, js.order[0])
		js.order = js.order[1:]
	}
	js.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
		defer cancel()
		err := js.run(ctx, x, steps)
		now := time.Now().UTC()
		x.mu.Lock()
		x.j.Running, x.j.EndedAt, x.j.OK = false, &now, err == nil
		if err != nil {
			x.j.Error = err.Error()
		}
		x.mu.Unlock()
		js.mu.Lock()
		js.busy = false
		js.mu.Unlock()
		if onDone != nil {
			onDone(x.snapshot())
		}
	}()
	return x.snapshot(), nil
}

func (js *Jobs) run(ctx context.Context, x *job, steps []Step) error {
	var skipped []string
	for _, st := range steps {
		label := st.Label
		if label == "" {
			label = strings.Join(st.Args, " ")
		}
		fmt.Fprintf(x, "$ %s\n", label)
		var err error
		if st.Fn != nil {
			err = st.Fn(ctx, x)
		} else {
			cmd := js.exec(ctx, st.Args)
			cmd.Stdout, cmd.Stderr = x, x
			err = cmd.Run()
		}
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return errors.New("stopped: the task ran longer than an hour")
		}
		if st.Optional {
			fmt.Fprintf(x, "(skipped: %v)\n", err)
			skipped = append(skipped, label)
			continue
		}
		return fmt.Errorf("%s failed: %v", label, err)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(x, "\nFinished; %d optional step(s) were skipped.\n", len(skipped))
	} else {
		fmt.Fprintln(x, "\nDone.")
	}
	return nil
}

// Get returns a job by id.
func (js *Jobs) Get(id string) (Job, bool) {
	js.mu.Lock()
	x, ok := js.jobs[id]
	js.mu.Unlock()
	if !ok {
		return Job{}, false
	}
	return x.snapshot(), true
}

// Recent lists the kept jobs, newest first, without their output.
func (js *Jobs) Recent() []Job {
	js.mu.Lock()
	defer js.mu.Unlock()
	out := make([]Job, 0, len(js.order))
	for i := len(js.order) - 1; i >= 0; i-- {
		j := js.jobs[js.order[i]].snapshot()
		j.Output = ""
		out = append(out, j)
	}
	return out
}

// StepsFor turns a plan into job steps.
func StepsFor(plan [][]string) []Step {
	steps := make([]Step, 0, len(plan))
	for _, p := range plan {
		steps = append(steps, Step{Args: p})
	}
	return steps
}

// FeatureSteps installs a feature's packages one by one, skipping any this
// distribution doesn't have, after refreshing the package lists.
func FeatureSteps(f Feature, mgr string) []Step {
	if f.ID == "zfs" && ZFSNeedsBuild(mgr) {
		return ZFSDebianSteps()
	}
	var steps []Step
	if refresh, err := Plan(mgr, ActRefresh, ""); err == nil {
		for _, r := range refresh {
			steps = append(steps, Step{Args: r, Optional: true})
		}
	}
	for _, p := range f.Packages[mgr] {
		plan, err := Plan(mgr, ActInstall, p)
		if err != nil {
			continue
		}
		last := plan[len(plan)-1] // skip the per-install refresh on apt
		steps = append(steps, Step{Args: last, Optional: true})
	}
	return steps
}

func newJobID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
