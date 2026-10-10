package backup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Restic runs the restic command-line tool. Repository passwords and cloud
// keys are passed in the environment, never on the command line.
type Restic struct {
	Bin      string
	CacheDir string
}

var (
	ErrNoRepo        = errors.New("there is no backup at this destination yet")
	ErrWrongPassword = errors.New("the recovery key doesn't match the backups at this destination")
	ErrLocked        = errors.New("the destination is locked by another backup")
)

// FindRestic looks for restic ($ALFA_RESTIC, then PATH).
func FindRestic(cacheDir string) (*Restic, error) {
	bin := os.Getenv("ALFA_RESTIC")
	if bin == "" {
		p, err := exec.LookPath("restic")
		if err != nil {
			return nil, errors.New("restic is not installed on this server")
		}
		bin = p
	}
	return &Restic{Bin: bin, CacheDir: cacheDir}, nil
}

// Target is everything restic needs to reach one repository.
type Target struct {
	Repository string
	Password   string
	Env        []string // e.g. cloud keys
	Args       []string // global options, e.g. -o sftp.command=…
}

// env is a small, explicit environment: no stray RESTIC_* or cloud keys from
// the server's own environment leak into a run.
func (r *Restic) env(t Target) []string {
	env := []string{"RESTIC_REPOSITORY=" + t.Repository, "RESTIC_PASSWORD=" + t.Password, "RESTIC_PROGRESS_FPS=2"}
	for _, k := range []string{"PATH", "HOME", "TMPDIR", "SystemRoot", "USERPROFILE", "LOCALAPPDATA", "TEMP", "TMP"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	if r.CacheDir != "" {
		env = append(env, "RESTIC_CACHE_DIR="+r.CacheDir)
	}
	return append(env, t.Env...)
}

// tail keeps the last few KB of stderr for error messages.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 8192 {
		t.buf = t.buf[len(t.buf)-8192:]
	}
	return len(p), nil
}

// run executes restic; onLine (if set) gets each stdout line, else stdout goes to out.
func (r *Restic) run(ctx context.Context, t Target, args []string, out io.Writer, onLine func([]byte)) error {
	cmd := exec.CommandContext(ctx, r.Bin, append(append([]string{}, t.Args...), args...)...)
	cmd.Env = r.env(t)
	cmd.WaitDelay = 10 * time.Second
	errs := &tail{}
	cmd.Stderr = errs
	var pipe io.ReadCloser
	if onLine != nil {
		p, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		pipe = p
	} else {
		cmd.Stdout = out
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start restic: %w", err)
	}
	if pipe != nil {
		sc := bufio.NewScanner(pipe)
		sc.Buffer(make([]byte, 64*1024), 8<<20)
		for sc.Scan() {
			onLine(sc.Bytes())
		}
	}
	err := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return resticError(string(errs.buf), err)
	}
	return nil
}

// resticError turns restic's stderr into a short, readable error.
func resticError(stderr string, err error) error {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "wrong password"):
		return ErrWrongPassword
	case strings.Contains(low, "is there a repository at the following location"),
		strings.Contains(low, "unable to open config file"),
		strings.Contains(low, "repository does not exist"):
		return ErrNoRepo
	case strings.Contains(low, "repository is already locked"):
		return ErrLocked
	}
	// The last "Fatal:" line, or the last non-empty line.
	var fatal, last string
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Fatal:") {
			fatal = strings.TrimSpace(strings.TrimPrefix(line, "Fatal:"))
		}
		last = line
	}
	if fatal != "" {
		last = fatal
	}
	if last == "" {
		return fmt.Errorf("restic: %w", err)
	}
	if len(last) > 300 {
		last = last[:300] + "…"
	}
	return errors.New(last)
}

// Version reports restic's version ("0.16.2").
func (r *Restic) Version(ctx context.Context) (string, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, r.Bin, "version")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	f := strings.Fields(out.String())
	if len(f) >= 2 {
		return f[1], nil
	}
	return strings.TrimSpace(out.String()), nil
}

// Exists checks the repository is there and the password opens it.
func (r *Restic) Exists(ctx context.Context, t Target) error {
	return r.run(ctx, t, []string{"cat", "config"}, io.Discard, nil)
}

func (r *Restic) Init(ctx context.Context, t Target) error {
	return r.run(ctx, t, []string{"init"}, io.Discard, nil)
}

func (r *Restic) Unlock(ctx context.Context, t Target) error {
	return r.run(ctx, t, []string{"unlock"}, io.Discard, nil)
}

// Snapshot is one backup in a repository.
type Snapshot struct {
	ID      string    `json:"id"`
	ShortID string    `json:"short_id"`
	Time    time.Time `json:"time"`
	Paths   []string  `json:"paths"`
	Tags    []string  `json:"tags"`
	Host    string    `json:"hostname"`
}

// Snapshots lists snapshots carrying the tag ("" for all), oldest first.
func (r *Restic) Snapshots(ctx context.Context, t Target, tag string) ([]Snapshot, error) {
	args := []string{"snapshots", "--json"}
	if tag != "" {
		args = append(args, "--tag", tag)
	}
	var out bytes.Buffer
	if err := r.run(ctx, t, args, &out, nil); err != nil {
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &snaps); err != nil {
		return nil, fmt.Errorf("read snapshot list: %w", err)
	}
	return snaps, nil
}

// Progress is a backup or restore status update.
type Progress struct {
	Percent    float64 `json:"percent"`
	FilesDone  int64   `json:"files_done"`
	FilesTotal int64   `json:"files_total"`
	BytesDone  int64   `json:"bytes_done"`
	BytesTotal int64   `json:"bytes_total"`
}

// Summary is what a finished backup reports.
type Summary struct {
	SnapshotID string `json:"snapshot_id"`
	FilesNew   int64  `json:"files_new"`
	FilesTotal int64  `json:"total_files_processed"`
	BytesTotal int64  `json:"total_bytes_processed"`
	DataAdded  int64  `json:"data_added"`
}

type BackupOptions struct {
	Host     string
	Tags     []string
	Excludes []string
}

// Backup backs up paths, reporting progress; it returns restic's summary.
func (r *Restic) Backup(ctx context.Context, t Target, paths []string, o BackupOptions, progress func(Progress)) (Summary, error) {
	args := []string{"backup", "--json", "--exclude-caches"}
	if o.Host != "" {
		args = append(args, "--host", o.Host)
	}
	for _, tag := range o.Tags {
		args = append(args, "--tag", tag)
	}
	for _, x := range o.Excludes {
		args = append(args, "--exclude", x)
	}
	args = append(args, "--")
	args = append(args, paths...)
	var sum Summary
	var warnings []string
	err := r.run(ctx, t, args, nil, func(line []byte) {
		var m struct {
			Type       string  `json:"message_type"`
			Percent    float64 `json:"percent_done"`
			TotalFiles int64   `json:"total_files"`
			FilesDone  int64   `json:"files_done"`
			TotalBytes int64   `json:"total_bytes"`
			BytesDone  int64   `json:"bytes_done"`
			Item       string  `json:"item"`
			During     string  `json:"during"`
		}
		if json.Unmarshal(line, &m) != nil {
			return
		}
		switch m.Type {
		case "status":
			if progress != nil {
				progress(Progress{Percent: m.Percent * 100, FilesDone: m.FilesDone, FilesTotal: m.TotalFiles, BytesDone: m.BytesDone, BytesTotal: m.TotalBytes})
			}
		case "summary":
			_ = json.Unmarshal(line, &sum)
		case "error":
			if len(warnings) < 5 {
				warnings = append(warnings, m.Item)
			}
		}
	})
	// restic exits 3 when some files couldn't be read but a snapshot was made.
	var exit *exec.ExitError
	if err != nil && sum.SnapshotID != "" {
		return sum, nil
	}
	if err != nil && errors.As(err, &exit) && exit.ExitCode() == 3 && sum.SnapshotID != "" {
		return sum, nil
	}
	return sum, err
}

// Keep is a retention policy; zero values are left out.
type Keep struct {
	Hourly  int `json:"hourly,omitempty"`
	Daily   int `json:"daily,omitempty"`
	Weekly  int `json:"weekly,omitempty"`
	Monthly int `json:"monthly,omitempty"`
	Yearly  int `json:"yearly,omitempty"`
}

func (k Keep) empty() bool { return k == Keep{} }

// Forget drops snapshots with the tag that the policy doesn't keep.
func (r *Restic) Forget(ctx context.Context, t Target, tag string, k Keep) error {
	if k.empty() {
		return nil
	}
	args := []string{"forget", "--tag", tag, "--group-by", "tags"}
	for _, kv := range []struct {
		flag string
		n    int
	}{{"--keep-hourly", k.Hourly}, {"--keep-daily", k.Daily}, {"--keep-weekly", k.Weekly}, {"--keep-monthly", k.Monthly}, {"--keep-yearly", k.Yearly}} {
		if kv.n > 0 {
			args = append(args, kv.flag, strconv.Itoa(kv.n))
		}
	}
	return r.run(ctx, t, args, io.Discard, nil)
}

// Prune frees the space of forgotten snapshots.
func (r *Restic) Prune(ctx context.Context, t Target) error {
	return r.run(ctx, t, []string{"prune"}, io.Discard, nil)
}

// Node is a file or folder inside a snapshot.
type Node struct {
	Name    string    `json:"name"`
	Type    string    `json:"type"` // file, dir, symlink
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// Ls lists the immediate contents of dir in a snapshot.
func (r *Restic) Ls(ctx context.Context, t Target, snapshot, dir string) ([]Node, error) {
	dir = path.Clean("/" + dir)
	var nodes []Node
	err := r.run(ctx, t, []string{"ls", "--json", snapshot, dir}, nil, func(line []byte) {
		var n struct {
			Node
			StructType string `json:"struct_type"`
		}
		if json.Unmarshal(line, &n) != nil || n.StructType != "node" {
			return
		}
		if n.Path != dir && path.Dir(n.Path) == dir {
			nodes = append(nodes, n.Node)
		}
	})
	return nodes, err
}

// Dump writes a file (or a folder as a tar archive) from a snapshot to w.
func (r *Restic) Dump(ctx context.Context, t Target, snapshot, p string, w io.Writer) error {
	return r.run(ctx, t, []string{"dump", snapshot, path.Clean("/" + p)}, w, nil)
}

// Restore restores the given paths of a snapshot under target (keeping their full paths).
func (r *Restic) Restore(ctx context.Context, t Target, snapshot, target string, includes []string) error {
	args := []string{"restore", snapshot, "--target", target}
	for _, p := range includes {
		args = append(args, "--include", path.Clean("/"+p))
	}
	return r.run(ctx, t, args, io.Discard, nil)
}
