// Package cloudimport copies files from cloud accounts (Google Drive,
// Dropbox, OneDrive, S3-compatible storage, Nextcloud/WebDAV, SFTP) into
// NoCapOS storage with rclone, once or on a schedule. It only ever copies:
// nothing is deleted in the cloud or on the server.
package cloudimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/syspkg"
)

// Sealer encrypts secrets at rest (the server's secret box).
type Sealer interface {
	Seal(plain string) []byte
	Open(sealed []byte) (string, error)
}

// Kinds of accounts and how they sign in.
var oauthKinds = map[string]bool{"drive": true, "dropbox": true, "onedrive": true}
var formKinds = map[string]bool{"s3": true, "webdav": true, "sftp": true}

type Manager struct {
	st      *store.Store
	box     Sealer
	files   *files.Service
	dataDir string
	log     *slog.Logger
	auth    authorizer
	graph   func(ctx context.Context, token string) (id, kind string, err error) // OneDrive lookup (tests replace it)

	mu        sync.Mutex
	rclone    *Rclone
	rcloneErr error
	jobs      map[string]*Job
	base      context.Context // alfad's lifetime, set by RunScheduler
}

func NewManager(st *store.Store, box Sealer, fsvc *files.Service, dataDir string, log *slog.Logger) *Manager {
	m := &Manager{st: st, box: box, files: fsvc, dataDir: dataDir, log: log, jobs: map[string]*Job{}, graph: oneDriveDrive}
	m.Refind()
	return m
}

func (m *Manager) Refind() {
	r, err := findRclone(filepath.Join(m.dataDir, "secrets", "rclone"))
	m.mu.Lock()
	m.rclone, m.rcloneErr = r, err
	m.mu.Unlock()
}

func (m *Manager) tool() (*Rclone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rclone, m.rcloneErr
}

// Status describes the import engine.
type Status struct {
	Installed  bool   `json:"installed"`
	Version    string `json:"version,omitempty"`
	Error      string `json:"error,omitempty"`
	CanInstall bool   `json:"can_install"`
}

func (m *Manager) Status(ctx context.Context) Status {
	st := Status{CanInstall: syspkg.CanInstall()}
	r, err := m.tool()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	v, err := r.Version(ctx)
	if err != nil {
		st.Error = "rclone is installed but won't run: " + err.Error()
		return st
	}
	st.Installed, st.Version = true, v
	return st
}

// InstallRclone installs rclone from the distribution's packages.
func (m *Manager) InstallRclone(ctx context.Context) error {
	if err := syspkg.Install(ctx, syspkg.Names{"apt": {"rclone"}, "dnf": {"rclone"}, "pacman": {"rclone"}, "zypper": {"rclone"}, "apk": {"rclone"}}); err != nil {
		return err
	}
	m.Refind()
	_, err := m.tool()
	return err
}

// ---------- accounts ----------

// Account is a connected cloud account as the UI sees it (no secrets).
type Account struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Detail    string    `json:"detail"` // e.g. the server or bucket
	CreatedAt time.Time `json:"created_at"`
}

func detail(kind string, rem Remote) string {
	switch kind {
	case "s3":
		return rem["endpoint"]
	case "webdav":
		return rem["url"]
	case "sftp":
		return rem["user"] + "@" + rem["host"]
	}
	return ""
}

func (m *Manager) remote(a *store.CloudAccount) (Remote, error) {
	raw, err := m.box.Open(a.Config)
	if err != nil {
		return nil, fmt.Errorf("unlock the account's settings: %w", err)
	}
	var rem Remote
	if err := json.Unmarshal([]byte(raw), &rem); err != nil {
		return nil, err
	}
	return rem, nil
}

func (m *Manager) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := m.st.CloudAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(rows))
	for _, a := range rows {
		v := Account{ID: a.ID, Name: a.Name, Kind: a.Kind, CreatedAt: a.CreatedAt}
		if rem, err := m.remote(a); err == nil {
			v.Detail = detail(a.Kind, rem)
		}
		out = append(out, v)
	}
	return out, nil
}

// saveToken stores a token rclone renewed during a run.
func (m *Manager) saveToken(ctx context.Context, id int64, rem Remote, tok string) {
	if tok == "" {
		return
	}
	rem["token"] = tok
	raw, _ := json.Marshal(rem)
	if err := m.st.SetCloudAccountConfig(ctx, id, m.box.Seal(string(raw))); err != nil {
		m.log.Warn("cloud import: save renewed token", "account", id, "err", err)
	}
}

func cleanName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || len(n) > 60 {
		return "", errors.New("give it a name (up to 60 characters)")
	}
	return n, nil
}

// save checks the account works (lists its top folder) and stores it.
func (m *Manager) save(ctx context.Context, name, kind string, rem Remote) (Account, error) {
	r, err := m.tool()
	if err != nil {
		return Account{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if _, tok, err := r.Folders(cctx, rem, ""); err != nil {
		return Account{}, fmt.Errorf("couldn't open the account: %w", err)
	} else if tok != "" {
		rem["token"] = tok
	}
	raw, _ := json.Marshal(rem)
	id, err := m.st.CreateCloudAccount(ctx, &store.CloudAccount{Name: name, Kind: kind, Config: m.box.Seal(string(raw))})
	if err != nil {
		return Account{}, err
	}
	return Account{ID: id, Name: name, Kind: kind, Detail: detail(kind, rem), CreatedAt: time.Now().UTC()}, nil
}

// NewAccount is an account that signs in with a key or password.
type NewAccount struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // s3 | webdav | sftp
	Endpoint string `json:"endpoint"`
	KeyID    string `json:"key_id"`
	Secret   string `json:"secret"`
	URL      string `json:"url"`
	Vendor   string `json:"vendor"` // nextcloud | owncloud | other
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func noBreaks(vals ...string) error {
	for _, v := range vals {
		if strings.ContainsAny(v, "\r\n") {
			return errors.New("the settings can't contain line breaks")
		}
	}
	return nil
}

// AddAccount adds an S3, WebDAV or SFTP account.
func (m *Manager) AddAccount(ctx context.Context, in NewAccount) (Account, error) {
	r, err := m.tool()
	if err != nil {
		return Account{}, err
	}
	name, err := cleanName(in.Name)
	if err != nil {
		return Account{}, err
	}
	if !formKinds[in.Kind] {
		return Account{}, fmt.Errorf("unknown account type %q", in.Kind)
	}
	if err := noBreaks(in.Endpoint, in.KeyID, in.Secret, in.URL, in.Host, in.User, in.Password); err != nil {
		return Account{}, err
	}
	rem := Remote{"type": in.Kind}
	switch in.Kind {
	case "s3":
		if !strings.HasPrefix(in.Endpoint, "https://") && !strings.HasPrefix(in.Endpoint, "http://") {
			return Account{}, errors.New("the endpoint must start with https://")
		}
		if in.KeyID == "" || in.Secret == "" {
			return Account{}, errors.New("enter the access key and secret")
		}
		rem["provider"], rem["endpoint"], rem["access_key_id"], rem["secret_access_key"] = "Other", strings.TrimSpace(in.Endpoint), strings.TrimSpace(in.KeyID), in.Secret
	case "webdav":
		if !strings.HasPrefix(in.URL, "https://") && !strings.HasPrefix(in.URL, "http://") {
			return Account{}, errors.New("enter the WebDAV address, e.g. https://cloud.example.com/remote.php/dav/files/me")
		}
		vendor := in.Vendor
		if vendor != "nextcloud" && vendor != "owncloud" {
			vendor = "other"
		}
		rem["url"], rem["vendor"], rem["user"] = strings.TrimSpace(in.URL), vendor, strings.TrimSpace(in.User)
		if in.Password != "" {
			if rem["pass"], err = r.Obscure(ctx, in.Password); err != nil {
				return Account{}, err
			}
		}
	case "sftp":
		if in.Host == "" || in.User == "" || strings.ContainsAny(in.Host+in.User, " @/") {
			return Account{}, errors.New("enter the server and user name")
		}
		rem["host"], rem["user"] = strings.TrimSpace(in.Host), strings.TrimSpace(in.User)
		if in.Port > 0 && in.Port != 22 {
			rem["port"] = fmt.Sprint(in.Port)
		}
		if in.Password == "" {
			return Account{}, errors.New("enter the password")
		}
		if rem["pass"], err = r.Obscure(ctx, in.Password); err != nil {
			return Account{}, err
		}
	}
	return m.save(ctx, name, in.Kind, rem)
}

// SignInStart begins a Google Drive, Dropbox or OneDrive sign-in.
func (m *Manager) SignInStart(ctx context.Context, kind string) (session, signInURL string, err error) {
	r, err := m.tool()
	if err != nil {
		return "", "", err
	}
	if !oauthKinds[kind] {
		return "", "", fmt.Errorf("unknown account type %q", kind)
	}
	s, link, err := m.auth.start(r, kind)
	if err != nil {
		return "", "", err
	}
	return s.id, link, nil
}

// SignInFinish completes a sign-in with the address the browser landed on and saves the account.
func (m *Manager) SignInFinish(ctx context.Context, session, pasted, name string) (Account, error) {
	name, err := cleanName(name)
	if err != nil {
		return Account{}, err
	}
	m.auth.mu.Lock()
	cur := m.auth.current
	m.auth.mu.Unlock()
	if cur == nil || cur.id != session {
		return Account{}, errors.New("this sign-in has expired; start again")
	}
	kind := cur.kind
	tok, err := m.auth.finish(session, pasted)
	if err != nil {
		return Account{}, err
	}
	rem := Remote{"type": kind, "token": tok}
	switch kind {
	case "drive":
		rem["scope"] = "drive"
	case "onedrive":
		var t struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.Unmarshal([]byte(tok), &t)
		id, typ, err := m.graph(ctx, t.AccessToken)
		if err != nil {
			return Account{}, fmt.Errorf("find your OneDrive: %w", err)
		}
		rem["drive_id"], rem["drive_type"] = id, typ
	}
	return m.save(ctx, name, kind, rem)
}

// oneDriveDrive asks Microsoft Graph for the signed-in user's drive.
func oneDriveDrive(ctx context.Context, token string) (string, string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.microsoft.com/v1.0/me/drive", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("Microsoft said %s", res.Status)
	}
	var d struct {
		ID        string `json:"id"`
		DriveType string `json:"driveType"`
	}
	if err := json.Unmarshal(body, &d); err != nil || d.ID == "" {
		return "", "", errors.New("no OneDrive found for this account")
	}
	return d.ID, d.DriveType, nil
}

func (m *Manager) DeleteAccount(ctx context.Context, id int64) error {
	return m.st.DeleteCloudAccount(ctx, id)
}

// Folders lists folders in an account (to choose what to import).
func (m *Manager) Folders(ctx context.Context, accountID int64, dir string) ([]Entry, error) {
	r, err := m.tool()
	if err != nil {
		return nil, err
	}
	a, err := m.st.CloudAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	rem, err := m.remote(a)
	if err != nil {
		return nil, err
	}
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if strings.Contains(dir, "..") || strings.ContainsAny(dir, "\r\n") {
		return nil, errors.New("invalid folder")
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	entries, tok, err := r.Folders(cctx, rem, dir)
	m.saveToken(ctx, a.ID, rem, tok)
	if entries == nil {
		entries = []Entry{}
	}
	return entries, err
}

// ---------- imports ----------

// Schedule says when an import repeats: "manual", "day" or "week".
type Schedule struct {
	Every   string `json:"every"`
	At      string `json:"at,omitempty"`
	Weekday int    `json:"weekday,omitempty"`
}

// Import is an import as the UI sees it.
type Import struct {
	ID         int64     `json:"id"`
	AccountID  int64     `json:"account_id"`
	Name       string    `json:"name"`
	Source     string    `json:"source"`
	DestRoot   string    `json:"dest_root"`
	DestPath   string    `json:"dest_path"`
	Schedule   Schedule  `json:"schedule"`
	LastRun    time.Time `json:"last_run"`
	LastStatus string    `json:"last_status"`
	LastError  string    `json:"last_error,omitempty"`
	LastBytes  int64     `json:"last_bytes"`
	LastFiles  int64     `json:"last_files"`
	NextRun    time.Time `json:"next_run"`
	Job        *Job      `json:"job,omitempty"`
}

func importView(c *store.CloudImport) Import {
	v := Import{ID: c.ID, AccountID: c.AccountID, Name: c.Name, Source: c.Source, DestRoot: c.DestRoot, DestPath: c.DestPath,
		LastRun: c.LastRun, LastStatus: c.LastStatus, LastError: c.LastError, LastBytes: c.LastBytes, LastFiles: c.LastFiles}
	_ = json.Unmarshal([]byte(c.Schedule), &v.Schedule)
	if v.Schedule.Every == "" {
		v.Schedule.Every = "manual"
	}
	v.NextRun = nextRun(v.Schedule, c.LastRun, c.CreatedAt)
	return v
}

func (m *Manager) Imports(ctx context.Context) ([]Import, error) {
	rows, err := m.st.CloudImports(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Import, 0, len(rows))
	for _, c := range rows {
		v := importView(c)
		v.Job = m.importJob(c.ID)
		out = append(out, v)
	}
	return out, nil
}

// SaveImport validates and stores an import (ID 0 creates it).
func (m *Manager) SaveImport(ctx context.Context, in Import) (int64, error) {
	name, err := cleanName(in.Name)
	if err != nil {
		return 0, err
	}
	if _, err := m.st.CloudAccount(ctx, in.AccountID); err != nil {
		return 0, errors.New("choose a cloud account")
	}
	src := strings.Trim(strings.TrimSpace(in.Source), "/")
	if strings.Contains(src, "..") || strings.ContainsAny(src, "\r\n") {
		return 0, errors.New("invalid cloud folder")
	}
	if in.DestRoot == "system" {
		return 0, errors.New("import into one of your drives, not the whole-server System view")
	}
	if _, err := m.files.Root(in.DestRoot); err != nil {
		return 0, errors.New("choose where to put the files")
	}
	rel, err := files.Clean(in.DestPath)
	if err != nil || rel == "." {
		return 0, errors.New("choose a folder to import into")
	}
	switch in.Schedule.Every {
	case "manual":
		in.Schedule = Schedule{Every: "manual"}
	case "day", "week":
		if _, err := time.Parse("15:04", in.Schedule.At); err != nil {
			return 0, errors.New("choose a time like 03:00")
		}
		if in.Schedule.Weekday < 0 || in.Schedule.Weekday > 6 {
			return 0, errors.New("choose a day of the week")
		}
	default:
		return 0, errors.New("choose how often to import")
	}
	sch, _ := json.Marshal(in.Schedule)
	return m.st.SaveCloudImport(ctx, &store.CloudImport{ID: in.ID, AccountID: in.AccountID, Name: name, Source: src, DestRoot: in.DestRoot, DestPath: "/" + rel, Schedule: string(sch)})
}

func (m *Manager) DeleteImport(ctx context.Context, id int64) error {
	if m.running(id) {
		return errors.New("stop the running import first")
	}
	return m.st.DeleteCloudImport(ctx, id)
}

// nextRun is when an import is next due (zero for manual ones).
func nextRun(s Schedule, last, created time.Time) time.Time {
	if s.Every != "day" && s.Every != "week" {
		return time.Time{}
	}
	at, err := time.Parse("15:04", s.At)
	if err != nil {
		return time.Time{}
	}
	from := last
	if from.IsZero() {
		from = created
	}
	from = from.In(time.Local)
	t := time.Date(from.Year(), from.Month(), from.Day(), at.Hour(), at.Minute(), 0, 0, time.Local)
	for !t.After(from) || (s.Every == "week" && int(t.Weekday()) != s.Weekday) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// Job is a running (or just finished) import.
type Job struct {
	ID       string    `json:"id"`
	ImportID int64     `json:"import_id"`
	Progress Progress  `json:"progress"`
	Done     bool      `json:"done"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitempty"`

	cancel context.CancelFunc
}

func (m *Manager) importJob(id int64) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ImportID == id && (!j.Done || time.Since(j.Ended) < time.Minute) {
			c := *j
			return &c
		}
	}
	return nil
}

func (m *Manager) running(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ImportID == id && !j.Done {
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

// Run starts an import now (or returns the one already running).
func (m *Manager) Run(ctx context.Context, id int64) (*Job, error) {
	r, err := m.tool()
	if err != nil {
		return nil, err
	}
	c, err := m.st.CloudImport(ctx, id)
	if err != nil {
		return nil, err
	}
	a, err := m.st.CloudAccount(ctx, c.AccountID)
	if err != nil {
		return nil, err
	}
	rem, err := m.remote(a)
	if err != nil {
		return nil, err
	}
	root, err := m.files.Root(c.DestRoot)
	if err != nil {
		return nil, fmt.Errorf("the destination drive isn't available: %w", err)
	}
	dest := filepath.Join(root.Path, filepath.FromSlash(strings.TrimPrefix(c.DestPath, "/")))

	m.mu.Lock()
	for _, j := range m.jobs {
		if j.ImportID == id && !j.Done {
			cp := *j
			m.mu.Unlock()
			return &cp, nil
		}
	}
	for jid, old := range m.jobs {
		if old.Done && time.Since(old.Ended) > time.Hour {
			delete(m.jobs, jid)
		}
	}
	// Jobs run under alfad's lifetime, so rclone is stopped on shutdown.
	base := m.base
	if base == nil {
		base = context.Background()
	}
	jctx, cancel := context.WithCancel(base)
	j := &Job{ID: randHex(8), ImportID: id, Started: time.Now().UTC(), cancel: cancel}
	m.jobs[j.ID] = j
	m.mu.Unlock()

	go func() {
		defer cancel()
		var res Progress
		err := os.MkdirAll(dest, 0o755)
		if err == nil {
			var tok string
			res, tok, err = r.Copy(jctx, rem, c.Source, dest, func(p Progress) {
				m.mu.Lock()
				j.Progress = p
				m.mu.Unlock()
			})
			m.saveToken(context.Background(), a.ID, rem, tok)
		}
		status, msg := "ok", ""
		switch {
		case jctx.Err() != nil:
			status, msg = "canceled", "stopped"
		case err != nil:
			status, msg = "failed", err.Error()
		}
		if e := m.st.SetCloudImportResult(context.Background(), id, j.Started, status, msg, res.Bytes, res.Files); e != nil {
			m.log.Warn("cloud import: save result", "import", id, "err", e)
		}
		m.log.Info("cloud import finished", "import", c.Name, "status", status, "files", res.Files, "bytes", res.Bytes, "err", msg)
		m.mu.Lock()
		j.Done, j.Ended, j.Progress = true, time.Now().UTC(), res
		if status != "ok" {
			j.Error = msg
		}
		m.mu.Unlock()
	}()
	cp := *j
	return &cp, nil
}

// RunScheduler starts due imports until ctx ends.
func (m *Manager) RunScheduler(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()
	tick := time.NewTicker(time.Minute)
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
		rows, err := m.st.CloudImports(ctx)
		if err != nil {
			continue
		}
		for _, c := range rows {
			v := importView(c)
			if v.NextRun.IsZero() || time.Now().Before(v.NextRun) || m.running(c.ID) {
				continue
			}
			if _, err := m.Run(ctx, c.ID); err != nil {
				m.log.Warn("cloud import: scheduled run", "import", c.Name, "err", err)
			}
		}
	}
}
