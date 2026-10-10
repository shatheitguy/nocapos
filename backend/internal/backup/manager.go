// Package backup is Backup & Rewind: encrypted, incremental backups made with
// restic to a drive, another server (SFTP) or S3-compatible cloud storage, on
// a schedule with a retention policy, plus browsing and restoring old versions.
package backup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

// Sealer encrypts secrets at rest (the server's secret box).
type Sealer interface {
	Seal(plain string) []byte
	Open(sealed []byte) (string, error)
}

const (
	snapshotHost = "nocapos"
	pruneEvery   = 7 * 24 * time.Hour
	// SpecialNoCapOS backs up NoCapOS's own accounts, settings and keys.
	SpecialNoCapOS = "nocapos"
)

// RepoConfig is a destination's non-secret settings.
type RepoConfig struct {
	Path     string `json:"path,omitempty"`     // local folder, or the folder on an SFTP server
	Host     string `json:"host,omitempty"`     // sftp
	Port     int    `json:"port,omitempty"`     // sftp
	User     string `json:"user,omitempty"`     // sftp
	Endpoint string `json:"endpoint,omitempty"` // s3, e.g. https://s3.eu-central-003.backblazeb2.com
	Bucket   string `json:"bucket,omitempty"`   // s3
	Prefix   string `json:"prefix,omitempty"`   // s3, folder inside the bucket
	KeyID    string `json:"key_id,omitempty"`   // s3 access key id
}

// Repo is a destination as the UI sees it (no secrets).
type Repo struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	Config    RepoConfig `json:"config"`
	Location  string     `json:"location"`
	CreatedAt time.Time  `json:"created_at"`
}

// Source is something to back up: a folder in a storage location, or NoCapOS itself.
type Source struct {
	Root    string `json:"root,omitempty"`
	Path    string `json:"path,omitempty"`
	Special string `json:"special,omitempty"`
}

// Schedule says when a plan runs: every "hour", "day" or "week" (at a time, on
// a weekday), or "manual".
type Schedule struct {
	Every   string `json:"every"`
	At      string `json:"at,omitempty"`      // "02:30", local time
	Weekday int    `json:"weekday,omitempty"` // 0 = Sunday
}

// Plan is a backup plan as the UI sees it.
type Plan struct {
	ID           int64     `json:"id"`
	RepoID       int64     `json:"repo_id"`
	Name         string    `json:"name"`
	Sources      []Source  `json:"sources"`
	Schedule     Schedule  `json:"schedule"`
	Keep         Keep      `json:"keep"`
	Enabled      bool      `json:"enabled"`
	LastRun      time.Time `json:"last_run"`
	LastStatus   string    `json:"last_status"`
	LastError    string    `json:"last_error,omitempty"`
	LastSnapshot string    `json:"last_snapshot,omitempty"`
	LastBytes    int64     `json:"last_bytes"`
	NextRun      time.Time `json:"next_run"`
	Job          *Job      `json:"job,omitempty"`
}

// Job is a running (or recently finished) backup or restore.
type Job struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // backup | restore
	PlanID   int64     `json:"plan_id"`
	Phase    string    `json:"phase"`
	Progress Progress  `json:"progress"`
	Done     bool      `json:"done"`
	Error    string    `json:"error,omitempty"`
	Result   string    `json:"result,omitempty"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitempty"`

	cancel context.CancelFunc
}

type Manager struct {
	st      *store.Store
	box     Sealer
	files   *files.Service
	dataDir string
	log     *slog.Logger

	mu        sync.Mutex
	restic    *Restic
	resticErr error
	jobs      map[string]*Job
	repoLocks map[int64]*sync.Mutex
	onResult  func(Result)
	base      context.Context // alfad's lifetime, set by RunScheduler
}

// Result is how a backup plan run ended (for notifications).
type Result struct {
	PlanID  int64
	Plan    string
	Started time.Time
	Status  string // ok | failed | canceled
	Error   string
}

// OnResult sets a function called after every backup plan run.
func (m *Manager) OnResult(fn func(Result)) {
	m.mu.Lock()
	m.onResult = fn
	m.mu.Unlock()
}

func NewManager(st *store.Store, box Sealer, fsvc *files.Service, dataDir string, log *slog.Logger) *Manager {
	m := &Manager{st: st, box: box, files: fsvc, dataDir: dataDir, log: log, jobs: map[string]*Job{}, repoLocks: map[int64]*sync.Mutex{}}
	m.Refind()
	return m
}

// Refind looks for restic again (after it was installed).
func (m *Manager) Refind() {
	r, err := FindRestic(filepath.Join(m.dataDir, "restic-cache"))
	m.mu.Lock()
	m.restic, m.resticErr = r, err
	m.mu.Unlock()
}

func (m *Manager) tool() (*Restic, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restic, m.resticErr
}

// Status describes the backup engine.
type Status struct {
	Installed  bool   `json:"installed"`
	Version    string `json:"version,omitempty"`
	Error      string `json:"error,omitempty"`
	CanInstall bool   `json:"can_install"`
	SSHKey     string `json:"ssh_public_key,omitempty"`
}

func (m *Manager) Status(ctx context.Context) Status {
	st := Status{CanInstall: canInstall()}
	r, err := m.tool()
	if err != nil {
		st.Error = err.Error()
	} else if v, err := r.Version(ctx); err != nil {
		st.Error = "restic is installed but won't run: " + err.Error()
	} else {
		st.Installed, st.Version = true, v
	}
	if k, err := m.SSHPublicKey(); err == nil {
		st.SSHKey = k
	}
	return st
}

func (m *Manager) lockRepo(id int64) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.repoLocks[id]
	if l == nil {
		l = &sync.Mutex{}
		m.repoLocks[id] = l
	}
	return l
}

// ---------- destinations ----------

func location(kind string, c RepoConfig) string {
	switch kind {
	case "sftp":
		port := ""
		if c.Port != 0 && c.Port != 22 {
			port = fmt.Sprintf(":%d", c.Port)
		}
		return fmt.Sprintf("%s@%s%s:%s", c.User, c.Host, port, c.Path)
	case "s3":
		return strings.TrimRight(c.Endpoint, "/") + "/" + c.Bucket + "/" + strings.Trim(c.Prefix, "/")
	default:
		return c.Path
	}
}

func repoView(r *store.BackupRepo) Repo {
	var c RepoConfig
	_ = json.Unmarshal([]byte(r.Config), &c)
	return Repo{ID: r.ID, Name: r.Name, Kind: r.Kind, Config: c, Location: location(r.Kind, c), CreatedAt: r.CreatedAt}
}

func (m *Manager) Repos(ctx context.Context) ([]Repo, error) {
	rows, err := m.st.BackupRepos(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Repo, 0, len(rows))
	for _, r := range rows {
		out = append(out, repoView(r))
	}
	return out, nil
}

// target builds the restic target for a stored destination.
func (m *Manager) target(r *store.BackupRepo) (Target, error) {
	var c RepoConfig
	if err := json.Unmarshal([]byte(r.Config), &c); err != nil {
		return Target{}, err
	}
	pw, err := m.box.Open(r.Password)
	if err != nil {
		return Target{}, fmt.Errorf("unlock destination key: %w", err)
	}
	secret := ""
	if len(r.Secret) > 0 {
		if secret, err = m.box.Open(r.Secret); err != nil {
			return Target{}, fmt.Errorf("unlock cloud key: %w", err)
		}
	}
	return m.targetFor(r.Kind, c, pw, secret)
}

func (m *Manager) targetFor(kind string, c RepoConfig, password, secret string) (Target, error) {
	t := Target{Password: password}
	switch kind {
	case "local":
		t.Repository = c.Path
	case "sftp":
		key, err := m.sshKeyPath()
		if err != nil {
			return t, err
		}
		port := c.Port
		if port == 0 {
			port = 22
		}
		known := filepath.Join(m.dataDir, "secrets", "backup_known_hosts")
		t.Repository = fmt.Sprintf("sftp:%s@%s:%s", c.User, c.Host, c.Path)
		t.Args = []string{"-o", fmt.Sprintf("sftp.command=ssh -i %s -p %d -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=%s -o ConnectTimeout=15 %s@%s -s sftp",
			key, port, known, c.User, c.Host)}
	case "s3":
		t.Repository = "s3:" + strings.TrimRight(c.Endpoint, "/") + "/" + c.Bucket
		if p := strings.Trim(c.Prefix, "/"); p != "" {
			t.Repository += "/" + p
		}
		t.Env = []string{"AWS_ACCESS_KEY_ID=" + c.KeyID, "AWS_SECRET_ACCESS_KEY=" + secret}
	default:
		return t, fmt.Errorf("unknown destination type %q", kind)
	}
	return t, nil
}

// NewRepo is a destination to add. Leave RecoveryKey empty to start a new
// backup there; give it to connect to backups made before (e.g. after a reinstall).
type NewRepo struct {
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	Config      RepoConfig `json:"config"`
	Secret      string     `json:"secret"`
	RecoveryKey string     `json:"recovery_key"`
}

func cleanConfig(kind string, c RepoConfig) (RepoConfig, error) {
	switch kind {
	case "local":
		c = RepoConfig{Path: filepath.Clean(strings.TrimSpace(c.Path))}
		if !filepath.IsAbs(c.Path) {
			return c, errors.New("choose a folder by its full path, e.g. /mnt/usb/backups")
		}
	case "sftp":
		c = RepoConfig{Host: strings.TrimSpace(c.Host), Port: c.Port, User: strings.TrimSpace(c.User), Path: strings.TrimSpace(c.Path)}
		if c.Host == "" || c.User == "" || c.Path == "" || strings.ContainsAny(c.Host+c.User, " @:/\"'") {
			return c, errors.New("enter the server, user name and folder")
		}
		if c.Port < 0 || c.Port > 65535 {
			return c, errors.New("the port must be between 1 and 65535")
		}
	case "s3":
		c = RepoConfig{Endpoint: strings.TrimSpace(c.Endpoint), Bucket: strings.TrimSpace(c.Bucket), Prefix: strings.Trim(strings.TrimSpace(c.Prefix), "/"), KeyID: strings.TrimSpace(c.KeyID)}
		if !strings.HasPrefix(c.Endpoint, "https://") && !strings.HasPrefix(c.Endpoint, "http://") {
			return c, errors.New("the endpoint must start with https://")
		}
		if c.Bucket == "" || c.KeyID == "" {
			return c, errors.New("enter the bucket and the access key")
		}
	default:
		return c, fmt.Errorf("unknown destination type %q", kind)
	}
	return c, nil
}

// AddRepo checks the destination, creates the backup repository there (or
// connects to an existing one with its recovery key) and saves it. It returns
// the recovery key.
func (m *Manager) AddRepo(ctx context.Context, in NewRepo) (Repo, string, error) {
	r, err := m.tool()
	if err != nil {
		return Repo{}, "", err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 80 {
		return Repo{}, "", errors.New("give the destination a name (up to 80 characters)")
	}
	cfg, err := cleanConfig(in.Kind, in.Config)
	if err != nil {
		return Repo{}, "", err
	}
	if in.Kind == "s3" && in.Secret == "" {
		return Repo{}, "", errors.New("enter the secret key")
	}
	if in.Kind == "local" {
		if err := os.MkdirAll(cfg.Path, 0o700); err != nil {
			return Repo{}, "", fmt.Errorf("can't use that folder: %w", err)
		}
	}
	pw := strings.TrimSpace(in.RecoveryKey)
	connecting := pw != ""
	if !connecting {
		pw = newPassword()
	}
	t, err := m.targetFor(in.Kind, cfg, pw, in.Secret)
	if err != nil {
		return Repo{}, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	switch err := r.Exists(ctx, t); {
	case err == nil && connecting:
		// Connected to existing backups.
	case err == nil:
		return Repo{}, "", errors.New("there are already backups at this destination; enter their recovery key to use them")
	case errors.Is(err, ErrWrongPassword):
		if connecting {
			return Repo{}, "", err
		}
		return Repo{}, "", errors.New("there are already backups at this destination; enter their recovery key to use them")
	case errors.Is(err, ErrNoRepo) && !connecting:
		if err := r.Init(ctx, t); err != nil {
			return Repo{}, "", fmt.Errorf("couldn't set up backups there: %w", err)
		}
	case errors.Is(err, ErrNoRepo):
		return Repo{}, "", errors.New("there are no backups at this destination to connect to")
	default:
		return Repo{}, "", fmt.Errorf("couldn't reach the destination: %w", err)
	}
	raw, _ := json.Marshal(cfg)
	rec := &store.BackupRepo{Name: name, Kind: in.Kind, Config: string(raw), Password: m.box.Seal(pw)}
	if in.Secret != "" {
		rec.Secret = m.box.Seal(in.Secret)
	}
	id, err := m.st.CreateBackupRepo(ctx, rec)
	if err != nil {
		return Repo{}, "", err
	}
	rec.ID, rec.CreatedAt = id, time.Now().UTC()
	return repoView(rec), pw, nil
}

// RecoveryKey returns a destination's repository password.
func (m *Manager) RecoveryKey(ctx context.Context, id int64) (string, error) {
	rec, err := m.st.BackupRepo(ctx, id)
	if err != nil {
		return "", err
	}
	return m.box.Open(rec.Password)
}

// DeleteRepo forgets a destination; the backups stay where they are.
func (m *Manager) DeleteRepo(ctx context.Context, id int64) error {
	return m.st.DeleteBackupRepo(ctx, id)
}

func newPassword() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	// Grouped for reading aloud / writing down: xxxx-xxxx-…
	s := strings.ToUpper(base64.RawURLEncoding.EncodeToString(b))
	s = strings.NewReplacer("-", "X", "_", "Y").Replace(s)
	var parts []string
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:min(i+4, len(s))])
	}
	return strings.Join(parts, "-")
}

// ---------- plans ----------

func planView(p *store.BackupPlan) Plan {
	v := Plan{ID: p.ID, RepoID: p.RepoID, Name: p.Name, Enabled: p.Enabled, LastRun: p.LastRun, LastStatus: p.LastStatus,
		LastError: p.LastError, LastSnapshot: p.LastSnapshot, LastBytes: p.LastBytes}
	_ = json.Unmarshal([]byte(p.Sources), &v.Sources)
	_ = json.Unmarshal([]byte(p.Schedule), &v.Schedule)
	_ = json.Unmarshal([]byte(p.Keep), &v.Keep)
	if v.Sources == nil {
		v.Sources = []Source{}
	}
	if p.Enabled {
		v.NextRun = nextRun(v.Schedule, p.LastRun, p.CreatedAt)
	}
	return v
}

func (m *Manager) Plans(ctx context.Context) ([]Plan, error) {
	rows, err := m.st.BackupPlans(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Plan, 0, len(rows))
	for _, p := range rows {
		v := planView(p)
		v.Job = m.planJob(p.ID)
		out = append(out, v)
	}
	return out, nil
}

// SavePlan validates and stores a plan (ID 0 creates it).
func (m *Manager) SavePlan(ctx context.Context, p Plan) (int64, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 80 {
		return 0, errors.New("give the backup a name (up to 80 characters)")
	}
	if _, err := m.st.BackupRepo(ctx, p.RepoID); err != nil {
		return 0, errors.New("choose where to back up to")
	}
	if len(p.Sources) == 0 {
		return 0, errors.New("choose at least one thing to back up")
	}
	for i, s := range p.Sources {
		if s.Special != "" {
			if s.Special != SpecialNoCapOS {
				return 0, fmt.Errorf("unknown source %q", s.Special)
			}
			p.Sources[i] = Source{Special: s.Special}
			continue
		}
		if _, err := m.files.Root(s.Root); err != nil {
			return 0, fmt.Errorf("unknown storage location %q", s.Root)
		}
		rel, err := files.Clean(s.Path)
		if err != nil {
			return 0, err
		}
		p.Sources[i] = Source{Root: s.Root, Path: "/" + strings.TrimPrefix(rel, ".")}
	}
	switch p.Schedule.Every {
	case "hour", "manual":
		p.Schedule = Schedule{Every: p.Schedule.Every}
	case "day", "week":
		if _, err := time.Parse("15:04", p.Schedule.At); err != nil {
			return 0, errors.New("choose a time like 02:30")
		}
		if p.Schedule.Every == "day" {
			p.Schedule.Weekday = 0
		} else if p.Schedule.Weekday < 0 || p.Schedule.Weekday > 6 {
			return 0, errors.New("choose a day of the week")
		}
	default:
		return 0, errors.New("choose how often to back up")
	}
	for _, n := range []int{p.Keep.Hourly, p.Keep.Daily, p.Keep.Weekly, p.Keep.Monthly, p.Keep.Yearly} {
		if n < 0 || n > 1000 {
			return 0, errors.New("keep between 0 and 1000 of each")
		}
	}
	src, _ := json.Marshal(p.Sources)
	sch, _ := json.Marshal(p.Schedule)
	keep, _ := json.Marshal(p.Keep)
	return m.st.SaveBackupPlan(ctx, &store.BackupPlan{ID: p.ID, RepoID: p.RepoID, Name: p.Name, Sources: string(src), Schedule: string(sch), Keep: string(keep), Enabled: p.Enabled})
}

func (m *Manager) DeletePlan(ctx context.Context, id int64) error {
	if m.running(id) {
		return errors.New("wait for the running backup to finish")
	}
	return m.st.DeleteBackupPlan(ctx, id)
}

// nextRun is when a plan is next due (zero for manual plans).
func nextRun(s Schedule, last, created time.Time) time.Time {
	from := last
	if from.IsZero() {
		from = created
	}
	from = from.In(time.Local)
	switch s.Every {
	case "hour":
		return from.Truncate(time.Hour).Add(time.Hour)
	case "day", "week":
		at, err := time.Parse("15:04", s.At)
		if err != nil {
			return time.Time{}
		}
		t := time.Date(from.Year(), from.Month(), from.Day(), at.Hour(), at.Minute(), 0, 0, time.Local)
		for !t.After(from) || (s.Every == "week" && int(t.Weekday()) != s.Weekday) {
			t = t.AddDate(0, 0, 1)
		}
		return t
	}
	return time.Time{}
}

// ---------- jobs ----------

func newJobID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func (m *Manager) planJob(planID int64) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.PlanID == planID && j.Kind == "backup" && (!j.Done || time.Since(j.Ended) < time.Minute) {
			c := *j
			return &c
		}
	}
	return nil
}

// running reports whether a backup of the plan is in progress.
func (m *Manager) running(planID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.PlanID == planID && j.Kind == "backup" && !j.Done {
			return true
		}
	}
	return false
}

// JobByID returns a copy of a job.
func (m *Manager) JobByID(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, false
	}
	c := *j
	return &c, true
}

// Cancel stops a running job.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.Done {
		return false
	}
	j.cancel()
	return true
}

func (m *Manager) update(j *Job, f func(*Job)) {
	m.mu.Lock()
	f(j)
	m.mu.Unlock()
}

// startJob registers a job and runs work in the background.
func (m *Manager) startJob(kind string, planID int64, work func(ctx context.Context, j *Job) (string, error)) *Job {
	ctx, cancel := context.WithCancel(m.lifetime())
	j := &Job{ID: newJobID(), Kind: kind, PlanID: planID, Phase: "Starting", Started: time.Now().UTC(), cancel: cancel}
	m.mu.Lock()
	for id, old := range m.jobs { // forget jobs finished long ago
		if old.Done && time.Since(old.Ended) > time.Hour {
			delete(m.jobs, id)
		}
	}
	m.jobs[j.ID] = j
	m.mu.Unlock()
	go func() {
		defer cancel()
		result, err := work(ctx, j)
		m.update(j, func(j *Job) {
			j.Done, j.Ended, j.Result = true, time.Now().UTC(), result
			switch {
			case ctx.Err() != nil:
				j.Error = "canceled"
			case err != nil:
				j.Error = err.Error()
			}
		})
	}()
	c := *j
	return &c
}

// RunPlan starts a backup now (unless one is already running for the plan).
func (m *Manager) RunPlan(ctx context.Context, id int64) (*Job, error) {
	if _, err := m.tool(); err != nil {
		return nil, err
	}
	p, err := m.st.BackupPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	for _, j := range m.jobs {
		if j.PlanID == id && j.Kind == "backup" && !j.Done {
			c := *j
			m.mu.Unlock()
			return &c, nil
		}
	}
	m.mu.Unlock()
	return m.startJob("backup", id, func(ctx context.Context, j *Job) (string, error) { return m.backup(ctx, j, p) }), nil
}

// backup runs one plan: stage NoCapOS's own data if asked, back up, then
// apply the retention policy (and prune the destination about weekly).
func (m *Manager) backup(ctx context.Context, j *Job, p *store.BackupPlan) (result string, err error) {
	view := planView(p)
	started := time.Now()
	var sum Summary
	defer func() {
		status := "ok"
		msg := ""
		if ctx.Err() != nil {
			status, msg = "canceled", "canceled"
		} else if err != nil {
			status, msg = "failed", err.Error()
		}
		if e := m.st.SetBackupPlanResult(context.Background(), p.ID, started, status, msg, sum.SnapshotID, sum.DataAdded); e != nil {
			m.log.Warn("backup: save result", "plan", p.ID, "err", e)
		}
		m.log.Info("backup finished", "plan", p.Name, "status", status, "snapshot", sum.SnapshotID, "added", sum.DataAdded, "err", msg)
		m.mu.Lock()
		hook := m.onResult
		m.mu.Unlock()
		if hook != nil {
			hook(Result{PlanID: p.ID, Plan: p.Name, Started: started, Status: status, Error: msg})
		}
	}()

	r, err := m.tool()
	if err != nil {
		return "", err
	}
	repo, err := m.st.BackupRepo(ctx, p.RepoID)
	if err != nil {
		return "", err
	}
	t, err := m.target(repo)
	if err != nil {
		return "", err
	}
	lock := m.lockRepo(repo.ID)
	m.update(j, func(j *Job) { j.Phase = "Waiting for the destination" })
	lock.Lock()
	defer lock.Unlock()

	paths, excludes, cleanup, err := m.resolveSources(ctx, view.Sources, repo)
	defer cleanup()
	if err != nil {
		return "", err
	}
	m.update(j, func(j *Job) { j.Phase = "Backing up" })
	opts := BackupOptions{Host: snapshotHost, Tags: []string{"nocapos", planTag(p.ID)}, Excludes: excludes}
	progress := func(pr Progress) { m.update(j, func(j *Job) { j.Progress = pr }) }
	sum, err = r.Backup(ctx, t, paths, opts, progress)
	if errors.Is(err, ErrLocked) && ctx.Err() == nil {
		// A crashed run can leave a stale lock behind; clear it and try once more.
		_ = r.Unlock(ctx, t)
		sum, err = r.Backup(ctx, t, paths, opts, progress)
	}
	if err != nil {
		return "", err
	}
	m.update(j, func(j *Job) { j.Phase = "Tidying up old backups"; j.Progress.Percent = 100 })
	if err := r.Forget(ctx, t, planTag(p.ID), view.Keep); err != nil {
		m.log.Warn("backup: forget old snapshots", "plan", p.ID, "err", err)
	} else if time.Since(repo.LastPrune) > pruneEvery {
		m.update(j, func(j *Job) { j.Phase = "Freeing space" })
		if err := r.Prune(ctx, t); err != nil {
			m.log.Warn("backup: prune", "repo", repo.ID, "err", err)
		} else {
			_ = m.st.SetBackupRepoPruned(context.Background(), repo.ID, time.Now())
		}
	}
	return sum.SnapshotID, nil
}

func planTag(id int64) string { return fmt.Sprintf("plan:%d", id) }

// resolveSources turns a plan's sources into absolute paths, plus things to
// leave out (recycle bins, caches, a destination inside a source).
func (m *Manager) resolveSources(ctx context.Context, sources []Source, repo *store.BackupRepo) (paths, excludes []string, cleanup func(), err error) {
	cleanup = func() {}
	seenRoot := map[string]bool{}
	for _, s := range sources {
		if s.Special == SpecialNoCapOS {
			dir, done, err := m.stageNoCapOS(ctx)
			if err != nil {
				return nil, nil, cleanup, fmt.Errorf("prepare NoCapOS settings: %w", err)
			}
			cleanup = done
			paths = append(paths, dir)
			continue
		}
		root, err := m.files.Root(s.Root)
		if err != nil {
			return nil, nil, cleanup, err
		}
		abs := filepath.Join(root.Path, filepath.FromSlash(strings.TrimPrefix(s.Path, "/")))
		if _, err := os.Stat(abs); err != nil {
			m.log.Warn("backup: source missing, skipped", "path", abs)
			continue
		}
		paths = append(paths, abs)
		if !seenRoot[root.ID] {
			seenRoot[root.ID] = true
			excludes = append(excludes, filepath.Join(root.Path, files.RecycleDir))
		}
	}
	if len(paths) == 0 {
		return nil, nil, cleanup, errors.New("none of the folders to back up exist")
	}
	excludes = append(excludes, ".upload-*.part",
		filepath.Join(m.dataDir, "photos-cache"), filepath.Join(m.dataDir, "restic-cache"), filepath.Join(m.dataDir, "backup-staging"))
	if repo.Kind == "local" {
		var c RepoConfig
		_ = json.Unmarshal([]byte(repo.Config), &c)
		excludes = append(excludes, c.Path)
	}
	return paths, excludes, cleanup, nil
}

// stageNoCapOS copies NoCapOS's database (consistently) and its keys into a
// staging folder that gets backed up.
func (m *Manager) stageNoCapOS(ctx context.Context) (string, func(), error) {
	dir := filepath.Join(m.dataDir, "backup-staging", "nocapos")
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", func() {}, err
	}
	done := func() { _ = os.RemoveAll(dir) }
	if err := m.st.BackupTo(ctx, filepath.Join(dir, "alfa.db")); err != nil {
		return "", done, err
	}
	if err := copyDir(filepath.Join(m.dataDir, "secrets"), filepath.Join(dir, "secrets")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", done, err
	}
	return dir, done, nil
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		in, err := os.Open(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(dst, e.Name()), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// RunScheduler starts due plans until ctx ends.
// lifetime is the context jobs run under: alfad's own once the scheduler is
// running, so restic is stopped on shutdown instead of left behind.
func (m *Manager) lifetime() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.base != nil {
		return m.base
	}
	return context.Background()
}

func (m *Manager) RunScheduler(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if _, err := m.tool(); err != nil {
			continue
		}
		plans, err := m.st.BackupPlans(ctx)
		if err != nil {
			continue
		}
		for _, p := range plans {
			v := planView(p)
			if !v.Enabled || v.NextRun.IsZero() || time.Now().Before(v.NextRun) || m.running(p.ID) {
				continue
			}
			if _, err := m.RunPlan(ctx, p.ID); err != nil {
				m.log.Warn("backup: scheduled run", "plan", p.Name, "err", err)
			}
		}
	}
}

// ---------- rewind ----------

// SnapshotView is a backup point in time, newest first in lists.
type SnapshotView struct {
	ID    string    `json:"id"`
	Short string    `json:"short_id"`
	Time  time.Time `json:"time"`
	Paths []string  `json:"paths"`
}

func (m *Manager) planTarget(ctx context.Context, planID int64) (*Restic, Target, *store.BackupPlan, error) {
	r, err := m.tool()
	if err != nil {
		return nil, Target{}, nil, err
	}
	p, err := m.st.BackupPlan(ctx, planID)
	if err != nil {
		return nil, Target{}, nil, err
	}
	repo, err := m.st.BackupRepo(ctx, p.RepoID)
	if err != nil {
		return nil, Target{}, nil, err
	}
	t, err := m.target(repo)
	return r, t, p, err
}

// Snapshots lists a plan's backups, newest first.
func (m *Manager) Snapshots(ctx context.Context, planID int64) ([]SnapshotView, error) {
	r, t, _, err := m.planTarget(ctx, planID)
	if err != nil {
		return nil, err
	}
	snaps, err := r.Snapshots(ctx, t, planTag(planID))
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotView, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, SnapshotView{ID: s.ID, Short: s.ShortID, Time: s.Time, Paths: s.Paths})
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Time.After(out[k].Time) })
	return out, nil
}

// snapshotOf finds one of the plan's snapshots (so a request can't reach other plans' data).
func (m *Manager) snapshotOf(ctx context.Context, r *Restic, t Target, planID int64, id string) (*Snapshot, error) {
	snaps, err := r.Snapshots(ctx, t, planTag(planID))
	if err != nil {
		return nil, err
	}
	for i := range snaps {
		if snaps[i].ID == id || snaps[i].ShortID == id {
			return &snaps[i], nil
		}
	}
	return nil, store.ErrNotFound
}

// inside reports whether p is one of the snapshot's backed-up paths or below one.
func inside(p string, roots []string) bool {
	for _, r := range roots {
		r = filepath.ToSlash(r)
		if p == r || strings.HasPrefix(p, strings.TrimSuffix(r, "/")+"/") {
			return true
		}
	}
	return false
}

// Browse lists a folder in a snapshot. An empty dir lists the backed-up paths.
func (m *Manager) Browse(ctx context.Context, planID int64, snapID, dir string) ([]Node, error) {
	r, t, _, err := m.planTarget(ctx, planID)
	if err != nil {
		return nil, err
	}
	snap, err := m.snapshotOf(ctx, r, t, planID, snapID)
	if err != nil {
		return nil, err
	}
	if dir == "" || dir == "/" {
		nodes := make([]Node, 0, len(snap.Paths))
		for _, p := range snap.Paths {
			p = filepath.ToSlash(p)
			nodes = append(nodes, Node{Name: m.label(p), Type: "dir", Path: p})
		}
		return nodes, nil
	}
	if !inside(dir, snap.Paths) {
		return nil, store.ErrNotFound
	}
	return r.Ls(ctx, t, snap.ID, dir)
}

// Dump streams a file (or a folder as tar) from a snapshot.
func (m *Manager) Dump(ctx context.Context, planID int64, snapID, p string, w io.Writer) error {
	r, t, _, err := m.planTarget(ctx, planID)
	if err != nil {
		return err
	}
	snap, err := m.snapshotOf(ctx, r, t, planID, snapID)
	if err != nil {
		return err
	}
	if !inside(p, snap.Paths) {
		return store.ErrNotFound
	}
	return r.Dump(ctx, t, snap.ID, p, w)
}

// RestoreRequest restores paths of a snapshot to where they were on this
// server: next to the current version ("beside"), or replacing it ("replace";
// the current version goes to the Recycle Bin).
type RestoreRequest struct {
	PlanID   int64    `json:"plan_id"`
	Snapshot string   `json:"snapshot"`
	Paths    []string `json:"paths"`
	Mode     string   `json:"mode"`
}

// Restore starts a restore job.
func (m *Manager) Restore(ctx context.Context, req RestoreRequest) (*Job, error) {
	if req.Mode != "beside" && req.Mode != "replace" {
		return nil, errors.New("choose how to restore")
	}
	if len(req.Paths) == 0 || len(req.Paths) > 500 {
		return nil, errors.New("choose what to restore")
	}
	r, t, _, err := m.planTarget(ctx, req.PlanID)
	if err != nil {
		return nil, err
	}
	snap, err := m.snapshotOf(ctx, r, t, req.PlanID, req.Snapshot)
	if err != nil {
		return nil, err
	}
	var items []restoreItem
	for _, p := range req.Paths {
		p = filepath.ToSlash(filepath.Clean(p))
		if !inside(p, snap.Paths) {
			return nil, fmt.Errorf("%s isn't in this backup", p)
		}
		root, rel, ok := m.locate(p)
		if !ok {
			return nil, fmt.Errorf("%s isn't in a storage location on this server; download it instead", p)
		}
		items = append(items, restoreItem{p, root, rel})
	}
	return m.startJob("restore", req.PlanID, func(ctx context.Context, j *Job) (string, error) {
		stamp := time.Now().Format("2006-01-02 1504")
		var placed []string
		for n, it := range items {
			m.update(j, func(j *Job) {
				j.Phase = fmt.Sprintf("Restoring %s", filepath.Base(it.snapPath))
				j.Progress.Percent = float64(n) * 100 / float64(len(items))
			})
			tmp := filepath.Join(it.root.Path, fmt.Sprintf(".nocap-restore-%s-%d", j.ID, n))
			if err := r.Restore(ctx, t, snap.ID, tmp, []string{it.snapPath}); err != nil {
				_ = os.RemoveAll(tmp)
				return strings.Join(placed, "\n"), err
			}
			got := filepath.Join(tmp, filepath.FromSlash(strings.TrimPrefix(it.snapPath, "/")))
			dest := filepath.Join(it.root.Path, filepath.FromSlash(it.rel))
			if req.Mode == "replace" {
				if _, err := os.Lstat(dest); err == nil {
					if err := m.files.Delete(it.root.ID, []string{it.rel}, false); err != nil {
						_ = os.RemoveAll(tmp)
						return strings.Join(placed, "\n"), fmt.Errorf("move the current %s to the Recycle Bin: %w", filepath.Base(dest), err)
					}
				}
			} else if _, err := os.Lstat(dest); err == nil {
				dest = besideName(dest, stamp)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				_ = os.RemoveAll(tmp)
				return strings.Join(placed, "\n"), err
			}
			if err := os.Rename(got, dest); err != nil {
				_ = os.RemoveAll(tmp)
				return strings.Join(placed, "\n"), fmt.Errorf("put %s back: %w", filepath.Base(dest), err)
			}
			_ = os.RemoveAll(tmp)
			placed = append(placed, it.root.ID+":"+filepath.ToSlash(strings.TrimPrefix(dest, it.root.Path)))
		}
		m.update(j, func(j *Job) { j.Progress.Percent = 100; j.Phase = "Restored" })
		return strings.Join(placed, "\n"), nil
	}), nil
}

// restoreItem is one path to restore and where it goes on this server.
type restoreItem struct {
	snapPath string
	root     files.Root
	rel      string // inside root
}

// besideName picks "name (restored 2026-10-09 1504).ext" next to dest.
func besideName(dest, stamp string) string {
	dir, base := filepath.Split(dest)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if ext == base { // ".bashrc"
		stem, ext = base, ""
	}
	for i := 1; ; i++ {
		suffix := fmt.Sprintf(" (restored %s)", stamp)
		if i > 1 {
			suffix = fmt.Sprintf(" (restored %s %d)", stamp, i)
		}
		cand := filepath.Join(dir, stem+suffix+ext)
		if _, err := os.Lstat(cand); errors.Is(err, os.ErrNotExist) {
			return cand
		}
	}
}

// label names a backed-up path the way NoCapOS shows it ("Drive › Documents").
func (m *Manager) label(p string) string {
	if p == filepath.ToSlash(filepath.Join(m.dataDir, "backup-staging", "nocapos")) {
		return "NoCapOS settings & accounts"
	}
	native := filepath.FromSlash(p)
	for _, r := range m.files.Roots() {
		if filepath.Clean(r.Path) == native {
			return r.Name
		}
	}
	if root, rel, ok := m.locate(p); ok {
		return root.Name + " › " + strings.ReplaceAll(rel, "/", " › ")
	}
	return p
}

// locate finds the storage location holding an absolute path (most specific wins).
func (m *Manager) locate(abs string) (files.Root, string, bool) {
	var best files.Root
	bestLen := -1
	native := filepath.FromSlash(abs)
	for _, r := range m.files.Roots() {
		base := filepath.Clean(r.Path)
		if native == base || strings.HasPrefix(native, strings.TrimSuffix(base, string(filepath.Separator))+string(filepath.Separator)) {
			if len(base) > bestLen {
				best, bestLen = r, len(base)
			}
		}
	}
	if bestLen < 0 {
		return files.Root{}, "", false
	}
	rel := filepath.ToSlash(strings.TrimPrefix(native, filepath.Clean(best.Path)))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return files.Root{}, "", false // restoring a whole location isn't supported; pick folders in it
	}
	return best, rel, true
}
