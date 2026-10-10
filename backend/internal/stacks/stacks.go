// Package stacks manages Compose stacks: a compose.yaml (and optional .env)
// per stack under DataDir/stacks/<name>, deployed with the engine's own
// Compose tool (docker compose, docker-compose or podman compose).
package stacks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/syspkg"
)

const (
	composeFile = "compose.yaml"
	envFile     = ".env"
	maxOutput   = 64 << 10
)

// ErrNoCompose means no Compose tool is installed.
var ErrNoCompose = errors.New("Docker Compose isn't installed on this server")

var (
	ErrNotFound = errors.New("no stack with that name")
	ErrBusy     = errors.New("this stack is busy with another task; wait for it to finish")
	ErrExists   = errors.New("a stack with that name already exists")
	nameRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
)

// ValidName reports whether name can be a stack (Compose project) name.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Actions a stack can run, with the Compose arguments for each.
var actions = map[string][][]string{
	"up":       {{"up", "-d", "--remove-orphans"}},
	"redeploy": {{"pull", "--ignore-pull-failures"}, {"up", "-d", "--remove-orphans"}},
	"pull":     {{"pull"}},
	"start":    {{"start"}},
	"stop":     {{"stop"}},
	"restart":  {{"restart"}},
	"down":     {{"down", "--remove-orphans"}},
}

// ValidAction reports whether action is one Run accepts.
func ValidAction(action string) bool { _, ok := actions[action]; return ok }

// Op is the latest task run on a stack.
type Op struct {
	Action   string    `json:"action"`
	Running  bool      `json:"running"`
	Output   string    `json:"output"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
}

type op struct {
	Op
	buf bytes.Buffer
}

// Write keeps the tail of the output.
func (o *op) Write(p []byte) (int, error) {
	o.buf.Write(p)
	if o.buf.Len() > maxOutput {
		b := o.buf.Bytes()
		keep := append([]byte(nil), b[len(b)-maxOutput:]...)
		o.buf.Reset()
		o.buf.Write(keep)
	}
	return len(p), nil
}

// Stack is a stack stored on disk.
type Stack struct {
	Name    string    `json:"name"`
	Updated time.Time `json:"updated"`
	Op      *Op       `json:"op,omitempty"`
}

type Manager struct {
	dir        string
	dockerHost string
	log        *slog.Logger

	mu  sync.Mutex
	ops map[string]*op
	cmd []string // detected Compose command; nil = not checked yet
}

func NewManager(dataDir, dockerHost string, log *slog.Logger) *Manager {
	return &Manager{dir: filepath.Join(dataDir, "stacks"), dockerHost: dockerHost, log: log, ops: map[string]*op{}}
}

// Dir is where a stack's files live (shown to users so relative paths make sense).
func (m *Manager) Dir(name string) string { return filepath.Join(m.dir, name) }

func (m *Manager) env() []string {
	env := os.Environ()
	if m.dockerHost != "" {
		env = append(env, "DOCKER_HOST="+m.dockerHost)
	}
	return env
}

// compose finds the Compose tool, rechecking until one is found.
func (m *Manager) compose(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()
	if cmd != nil {
		return cmd, nil
	}
	for _, c := range [][]string{{"docker", "compose"}, {"docker-compose"}, {"podman", "compose"}} {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		x := exec.CommandContext(ctx, c[0], append(append([]string{}, c[1:]...), "version")...)
		x.Env = m.env()
		err := x.Run()
		cancel()
		if err == nil {
			m.mu.Lock()
			m.cmd = c
			m.mu.Unlock()
			return c, nil
		}
	}
	return nil, ErrNoCompose
}

// ComposeVersion returns the Compose tool's version, or ErrNoCompose.
func (m *Manager) ComposeVersion(ctx context.Context) (string, error) {
	c, err := m.compose(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	x := exec.CommandContext(ctx, c[0], append(append([]string{}, c[1:]...), "version", "--short")...)
	x.Env = m.env()
	out, err := x.Output()
	if err != nil {
		return strings.Join(c, " "), nil
	}
	return strings.TrimSpace(string(out)), nil
}

// CanInstall reports whether InstallCompose can work here.
func CanInstall() bool { return syspkg.CanInstall() }

// InstallCompose installs the Compose tool with the system's package manager,
// trying the package names different distributions use.
func (m *Manager) InstallCompose(ctx context.Context) error {
	var last error
	for _, names := range []syspkg.Names{
		{"apt": {"docker-compose-plugin"}, "dnf": {"docker-compose-plugin"}, "pacman": {"docker-compose"}, "zypper": {"docker-compose"}, "apk": {"docker-cli-compose"}},
		{"apt": {"docker-compose-v2"}, "dnf": {"docker-compose"}},
		{"apt": {"docker-compose"}},
	} {
		if len(names[syspkg.Manager()]) == 0 {
			continue
		}
		if last = syspkg.Install(ctx, names); last == nil {
			break
		}
	}
	if last != nil {
		return last
	}
	if _, err := m.compose(ctx); err != nil {
		return errors.New("Compose was installed but doesn't work with this engine yet; try restarting NoCapOS")
	}
	return nil
}

// List returns the stacks stored on disk.
func (m *Manager) List() ([]Stack, error) {
	entries, err := os.ReadDir(m.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Stack{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Stack{}
	for _, e := range entries {
		if !e.IsDir() || !ValidName(e.Name()) {
			continue
		}
		fi, err := os.Stat(filepath.Join(m.dir, e.Name(), composeFile))
		if err != nil {
			continue
		}
		out = append(out, Stack{Name: e.Name(), Updated: fi.ModTime(), Op: m.Op(e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Exists reports whether a stack is stored on disk.
func (m *Manager) Exists(name string) bool {
	if !ValidName(name) {
		return false
	}
	_, err := os.Stat(filepath.Join(m.Dir(name), composeFile))
	return err == nil
}

// Files returns a stack's compose.yaml and .env.
func (m *Manager) Files(name string) (compose, env string, err error) {
	if !m.Exists(name) {
		return "", "", ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(m.Dir(name), composeFile))
	if err != nil {
		return "", "", err
	}
	e, err := os.ReadFile(filepath.Join(m.Dir(name), envFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", "", err
	}
	return string(b), string(e), nil
}

// Save writes a stack's files after Compose has checked them (when it is
// installed). create refuses to overwrite an existing stack.
func (m *Manager) Save(ctx context.Context, name, compose, env string, create bool) error {
	if !ValidName(name) {
		return errors.New("stack names use lowercase letters, numbers, - and _")
	}
	if strings.TrimSpace(compose) == "" {
		return errors.New("the compose file is empty")
	}
	if create && m.Exists(name) {
		return ErrExists
	}
	if !create && !m.Exists(name) {
		return ErrNotFound
	}
	if m.busy(name) {
		return ErrBusy
	}
	dir := m.Dir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmpC, tmpE := filepath.Join(dir, ".compose.yaml.new"), filepath.Join(dir, ".env.new")
	defer os.Remove(tmpC)
	defer os.Remove(tmpE)
	if err := os.WriteFile(tmpC, []byte(compose), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(tmpE, []byte(env), 0o600); err != nil {
		return err
	}
	if c, err := m.compose(ctx); err == nil {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		args := append(append([]string{}, c[1:]...), "-p", name, "--project-directory", dir, "-f", tmpC, "--env-file", tmpE, "config", "-q")
		x := exec.CommandContext(ctx, c[0], args...)
		x.Dir, x.Env = dir, m.env()
		var out bytes.Buffer
		x.Stdout, x.Stderr = &out, &out
		if err := x.Run(); err != nil {
			if create {
				_ = os.Remove(dir) // only removes it while still empty
			}
			return fmt.Errorf("The compose file has a problem: %s", cleanMessage(out.String(), tmpC))
		}
	}
	if err := os.Rename(tmpC, filepath.Join(dir, composeFile)); err != nil {
		return err
	}
	if strings.TrimSpace(env) == "" {
		_ = os.Remove(filepath.Join(dir, envFile))
		return nil
	}
	return os.Rename(tmpE, filepath.Join(dir, envFile))
}

// cleanMessage turns Compose's validation output into one readable line.
func cleanMessage(out, tmp string) string {
	out = strings.TrimSpace(out)
	if tmp != "" {
		out = strings.ReplaceAll(out, tmp, composeFile)
	}
	lines := []string{}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "time=") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return "Compose rejected it"
	}
	return strings.Join(lines, "; ")
}

func (m *Manager) busy(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.ops[name]
	return o != nil && o.Running
}

// Op returns the latest task on a stack, or nil.
func (m *Manager) Op(name string) *Op {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.ops[name]
	if o == nil {
		return nil
	}
	c := o.Op
	c.Output = o.buf.String()
	return &c
}

// Run starts an action in the background; follow it with Op.
func (m *Manager) Run(ctx context.Context, name, action string) error {
	steps, ok := actions[action]
	if !ok {
		return errors.New("unknown action")
	}
	if !m.Exists(name) {
		return ErrNotFound
	}
	c, err := m.compose(ctx)
	if err != nil {
		return err
	}
	o := &op{Op: Op{Action: action, Running: true, Started: time.Now()}}
	m.mu.Lock()
	if cur := m.ops[name]; cur != nil && cur.Running {
		m.mu.Unlock()
		return ErrBusy
	}
	m.ops[name] = o
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
		defer cancel()
		var runErr error
		for _, step := range steps {
			if runErr = m.exec(ctx, c, name, step, o); runErr != nil {
				break
			}
		}
		m.mu.Lock()
		o.Running, o.Finished = false, time.Now()
		if runErr != nil {
			o.Error = runErr.Error()
		}
		m.mu.Unlock()
		if runErr != nil {
			m.log.Warn("stack task failed", "stack", name, "action", action, "err", runErr)
		}
	}()
	return nil
}

// lockedWriter serialises output writes with readers of the op.
type lockedWriter struct {
	mu *sync.Mutex
	o  *op
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.o.Write(p)
}

func (m *Manager) exec(ctx context.Context, c []string, name string, step []string, o *op) error {
	dir := m.Dir(name)
	args := append(append([]string{}, c[1:]...), "-p", name, "--project-directory", dir, "-f", filepath.Join(dir, composeFile))
	if _, err := os.Stat(filepath.Join(dir, envFile)); err == nil {
		args = append(args, "--env-file", filepath.Join(dir, envFile))
	}
	args = append(args, step...)
	x := exec.CommandContext(ctx, c[0], args...)
	x.Dir, x.Env = dir, append(m.env(), "COMPOSE_ANSI=never", "NO_COLOR=1")
	w := lockedWriter{&m.mu, o}
	_, _ = fmt.Fprintf(w, "$ compose %s\n", strings.Join(step, " "))
	x.Stdout, x.Stderr = w, w
	if err := x.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("it took too long and was stopped")
		}
		return fmt.Errorf("compose %s failed", step[0])
	}
	return nil
}

// Logs returns the latest log lines of a stack's services.
func (m *Manager) Logs(ctx context.Context, name string, tail int) (string, error) {
	if !m.Exists(name) {
		return "", ErrNotFound
	}
	c, err := m.compose(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	o := &op{}
	var mu sync.Mutex
	dir := m.Dir(name)
	args := append(append([]string{}, c[1:]...), "-p", name, "--project-directory", dir, "-f", filepath.Join(dir, composeFile), "logs", "--no-color", "--tail", fmt.Sprint(tail))
	x := exec.CommandContext(ctx, c[0], args...)
	x.Dir, x.Env = dir, m.env()
	x.Stdout, x.Stderr = lockedWriter{&mu, o}, lockedWriter{&mu, o}
	err = x.Run()
	if err != nil && o.buf.Len() == 0 {
		return "", fmt.Errorf("could not read the logs: %w", err)
	}
	return o.buf.String(), nil
}

// Delete removes a stack's files; the caller takes the stack down first.
func (m *Manager) Delete(name string) error {
	if !m.Exists(name) {
		return ErrNotFound
	}
	if m.busy(name) {
		return ErrBusy
	}
	m.mu.Lock()
	delete(m.ops, name)
	m.mu.Unlock()
	return os.RemoveAll(m.Dir(name))
}

// Down takes a stack down synchronously (for deleting), optionally with its volumes.
func (m *Manager) Down(ctx context.Context, name string, volumes bool) error {
	c, err := m.compose(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	step := []string{"down", "--remove-orphans"}
	if volumes {
		step = append(step, "-v")
	}
	o := &op{}
	if err := m.exec(ctx, c, name, step, o); err != nil {
		return fmt.Errorf("%w: %s", err, cleanMessage(o.buf.String(), ""))
	}
	return nil
}
