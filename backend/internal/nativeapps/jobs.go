package nativeapps

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	jobTimeout   = time.Hour
	jobOutputMax = 64 << 10
	jobsKept     = 20
	// Finished jobs stay visible on app tiles this long.
	jobRecent = 10 * time.Minute
)

// ErrBusy: Flatpak's system installation takes one change at a time.
var ErrBusy = errors.New("Hang on, another app is installing or uninstalling. Try again when it's done")

// Job is an install or uninstall as the UI sees it.
type Job struct {
	ID        string     `json:"id"`
	App       string     `json:"app"`
	Name      string     `json:"name"`
	Action    string     `json:"action"` // install | uninstall
	Phase     string     `json:"phase"`
	Percent   int        `json:"percent"`
	Done      bool       `json:"done"`
	OK        bool       `json:"ok"`
	Error     string     `json:"error,omitempty"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Output    string     `json:"output,omitempty"`
}

type job struct {
	mu   sync.Mutex
	j    Job
	prog Progress
	buf  []byte
	line []byte
}

// Write keeps the output's tail and follows its progress lines, which
// flatpak redraws with carriage returns.
func (x *job) Write(p []byte) (int, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.buf = append(x.buf, p...)
	if len(x.buf) > jobOutputMax {
		x.buf = x.buf[len(x.buf)-jobOutputMax:]
	}
	for _, c := range p {
		if c == '\r' || c == '\n' {
			x.feed()
			continue
		}
		if len(x.line) < 4096 {
			x.line = append(x.line, c)
		}
	}
	return len(p), nil
}

func (x *job) feed() {
	if len(x.line) == 0 {
		return
	}
	x.prog.Line(string(x.line))
	x.line = x.line[:0]
	x.j.Percent = x.prog.Percent
	if x.prog.Phase != "" {
		x.j.Phase = x.prog.Phase
	}
}

func (x *job) snapshot(withOutput bool) Job {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := x.j
	if withOutput {
		out.Output = string(x.buf)
	}
	return out
}

// Jobs runs installs and uninstalls in the background, one at a time.
type Jobs struct {
	// Exec runs one command, streaming its output (swappable in tests).
	Exec func(ctx context.Context, args []string, out io.Writer) error

	mu    sync.Mutex
	jobs  map[string]*job
	order []string
	busy  bool
}

func NewJobs() *Jobs {
	return &Jobs{jobs: map[string]*job{}, Exec: execCommand}
}

func execCommand(ctx context.Context, args []string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 5 * time.Second
	return cmd.Run()
}

// Step is one command; Optional ones may fail.
type Step struct {
	Args     []string
	Optional bool
}

// Start runs steps in the background. onDone gets the result.
func (js *Jobs) Start(app, name, action string, steps []Step, onDone func(Job)) (Job, error) {
	js.mu.Lock()
	if js.busy {
		js.mu.Unlock()
		return Job{}, ErrBusy
	}
	js.busy = true
	phase := "Getting ready"
	if action == "uninstall" {
		phase = "Removing"
	}
	x := &job{j: Job{ID: newID(), App: app, Name: name, Action: action, Phase: phase, StartedAt: time.Now().UTC()}}
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
		x.feed()
		x.j.Done, x.j.EndedAt, x.j.OK = true, &now, err == nil
		if err == nil {
			x.j.Percent, x.j.Phase = 100, "Done"
		} else {
			x.j.Error = err.Error()
		}
		x.mu.Unlock()
		if onDone != nil {
			onDone(x.snapshot(true))
		}
		js.mu.Lock()
		js.busy = false
		js.mu.Unlock()
	}()
	return x.snapshot(false), nil
}

func (js *Jobs) run(ctx context.Context, x *job, steps []Step) error {
	for _, st := range steps {
		_, _ = io.WriteString(x, "$ "+strings.Join(st.Args, " ")+"\n")
		err := js.Exec(ctx, st.Args, x)
		if err == nil || st.Optional {
			continue
		}
		if ctx.Err() != nil {
			return errors.New("stopped: it ran longer than an hour")
		}
		return friendlyError(x.snapshot(true).Output, err)
	}
	return nil
}

// friendlyError picks the useful line out of flatpak's output.
func friendlyError(out string, err error) error {
	lines := strings.FieldsFunc(out, func(r rune) bool { return r == '\n' || r == '\r' })
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "Error:") {
			msg := strings.TrimSpace(l[len("error:"):])
			low := strings.ToLower(msg)
			switch {
			case strings.Contains(low, "unable to connect") || strings.Contains(low, "could not resolve") || strings.Contains(low, "while fetching") || strings.Contains(low, "timeout"):
				return errors.New("Couldn't grab that app, Flathub's being weird. Try again in a sec")
			case strings.Contains(low, "no space"):
				return errors.New("Not enough disk space for that app")
			}
			return errors.New(msg)
		}
	}
	return errors.New("Couldn't grab that app, Flathub's being weird. Try again in a sec (" + err.Error() + ")")
}

// Busy reports whether a job (or its completion callback) is still running.
func (js *Jobs) Busy() bool {
	js.mu.Lock()
	defer js.mu.Unlock()
	return js.busy
}

// Get returns a job with its output.
func (js *Jobs) Get(id string) (Job, bool) {
	js.mu.Lock()
	x, ok := js.jobs[id]
	js.mu.Unlock()
	if !ok {
		return Job{}, false
	}
	return x.snapshot(true), true
}

// Recent lists running jobs and ones that ended in the last few minutes,
// newest first, one per app.
func (js *Jobs) Recent() []Job {
	js.mu.Lock()
	defer js.mu.Unlock()
	out := []Job{}
	seen := map[string]bool{}
	for i := len(js.order) - 1; i >= 0; i-- {
		j := js.jobs[js.order[i]].snapshot(false)
		if seen[j.App] || (j.Done && j.EndedAt != nil && time.Since(*j.EndedAt) > jobRecent) {
			continue
		}
		seen[j.App] = true
		out = append(out, j)
	}
	return out
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// runOutput runs a quick command and returns its stdout.
func runOutput(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}
