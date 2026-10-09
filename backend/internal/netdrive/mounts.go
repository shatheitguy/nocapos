// Package netdrive connects network drives (SMB and NFS shares that NoCapOS
// mounts as root and offers as storage locations) and shares NoCapOS folders
// with other devices over SMB (Samba) and WebDAV.
package netdrive

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// Runner runs a host command (swapped out in tests).
type Runner func(ctx context.Context, stdin string, name string, args ...string) (string, error)

func execRunner(ctx context.Context, stdin, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, errors.New(lastLine(text))
	}
	return text, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// Tools that features need, and the packages that provide them.
var (
	toolPackages = map[string]syspkg.Names{
		"smb":   {"apt": {"cifs-utils"}, "dnf": {"cifs-utils"}, "pacman": {"cifs-utils"}, "zypper": {"cifs-utils"}, "apk": {"cifs-utils"}},
		"nfs":   {"apt": {"nfs-common"}, "dnf": {"nfs-utils"}, "pacman": {"nfs-utils"}, "zypper": {"nfs-client"}, "apk": {"nfs-utils"}},
		"samba": {"apt": {"samba"}, "dnf": {"samba"}, "pacman": {"samba"}, "zypper": {"samba"}, "apk": {"samba"}},
	}
	toolBinary = map[string]string{"smb": "mount.cifs", "nfs": "mount.nfs", "samba": "smbd"}
)

type Manager struct {
	st        *store.Store
	box       Sealer
	files     *files.Service
	dataDir   string
	mountBase string
	log       *slog.Logger

	// Host access, replaceable in tests.
	run      Runner
	mounted  func(path string) bool
	lookPath func(string) (string, error)
	native   func() bool
	sambaDir string // /etc/samba

	mu    sync.Mutex
	state map[int64]*driveState
	dav   *davAuth
}

type driveState struct {
	mounted bool
	err     string
	paused  bool // disconnected by the user: don't reconnect on our own
}

func NewManager(st *store.Store, box Sealer, fsvc *files.Service, dataDir string, log *slog.Logger) *Manager {
	base := os.Getenv("ALFA_MOUNT_DIR")
	if base == "" {
		base = "/mnt/nocapos"
	}
	return &Manager{
		st: st, box: box, files: fsvc, dataDir: dataDir, mountBase: base, log: log,
		run: execRunner, mounted: isMountPoint, lookPath: exec.LookPath, native: syspkg.Native, sambaDir: "/etc/samba",
		state: map[int64]*driveState{}, dav: newDavAuth(),
	}
}

// Support says what this server can do.
type Support struct {
	Native     bool `json:"native"`      // runs as root on Linux: can mount and configure Samba
	CanInstall bool `json:"can_install"` // can install missing tools
	SMB        bool `json:"smb"`         // mount.cifs present
	NFS        bool `json:"nfs"`         // mount.nfs present
	Samba      bool `json:"samba"`       // smbd present
}

func (m *Manager) has(tool string) bool {
	_, err := m.lookPath(toolBinary[tool])
	return err == nil
}

func (m *Manager) Support() Support {
	return Support{Native: m.native(), CanInstall: m.native() && syspkg.Manager() != "", SMB: m.has("smb"), NFS: m.has("nfs"), Samba: m.has("samba")}
}

// Install installs a missing tool ("smb", "nfs" or "samba").
func (m *Manager) Install(ctx context.Context, tool string) error {
	names, ok := toolPackages[tool]
	if !ok {
		return fmt.Errorf("unknown tool %q", tool)
	}
	if err := syspkg.Install(ctx, names); err != nil {
		return err
	}
	if !m.has(tool) {
		return fmt.Errorf("%s still isn't available after installing", toolBinary[tool])
	}
	if tool == "samba" {
		return m.applySamba(ctx)
	}
	return nil
}

// Drive is a network drive as the UI sees it.
type Drive struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Host      string    `json:"host"`
	Share     string    `json:"share"`
	Username  string    `json:"username,omitempty"`
	Auto      bool      `json:"auto"`
	Mounted   bool      `json:"mounted"`
	Error     string    `json:"error,omitempty"`
	RootID    string    `json:"root_id"`
	Address   string    `json:"address"`
	CreatedAt time.Time `json:"created_at"`
}

// rootID can't clash with configured locations, whose ids never contain ":".
func rootID(id int64) string { return fmt.Sprintf("net:%d", id) }

func address(d *store.NetMount) string {
	if d.Kind == "nfs" {
		return d.Host + ":" + d.Share
	}
	return `\\` + d.Host + `\` + strings.ReplaceAll(d.Share, "/", `\`)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func (m *Manager) mountPoint(d *store.NetMount) string {
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(d.Name), "-"), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug == "" {
		slug = "drive"
	}
	return filepath.Join(m.mountBase, fmt.Sprintf("%d-%s", d.ID, slug))
}

func (m *Manager) view(d *store.NetMount) Drive {
	m.mu.Lock()
	st := m.state[d.ID]
	m.mu.Unlock()
	v := Drive{ID: d.ID, Name: d.Name, Kind: d.Kind, Host: d.Host, Share: d.Share, Username: d.Username, Auto: d.Auto, RootID: rootID(d.ID), Address: address(d), CreatedAt: d.CreatedAt}
	if st != nil {
		v.Mounted, v.Error = st.mounted, st.err
	}
	return v
}

func (m *Manager) Drives(ctx context.Context) ([]Drive, error) {
	rows, err := m.st.NetMounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Drive, 0, len(rows))
	for _, d := range rows {
		out = append(out, m.view(d))
	}
	return out, nil
}

// NewDrive is a network drive to add.
type NewDrive struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // smb | nfs
	Host     string `json:"host"`
	Share    string `json:"share"`
	Username string `json:"username"`
	Password string `json:"password"`
	Auto     bool   `json:"auto"`
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9._:\[\]-]{1,253}$`)

func validate(in *NewDrive) error {
	in.Name, in.Host, in.Share, in.Username = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host), strings.TrimSpace(in.Share), strings.TrimSpace(in.Username)
	in.Host = strings.TrimPrefix(strings.TrimPrefix(in.Host, `\\`), "smb://")
	if in.Name == "" || len(in.Name) > 60 {
		return errors.New("give the drive a name (up to 60 characters)")
	}
	if !hostRe.MatchString(in.Host) {
		return errors.New("enter the server's name or IP address, e.g. 192.168.1.20 or nas.local")
	}
	if strings.ContainsAny(in.Username+in.Password, "\n\r") || strings.ContainsAny(in.Username, ",=") {
		return errors.New("the user name or password has characters that can't be used")
	}
	switch in.Kind {
	case "smb":
		in.Share = strings.Trim(strings.ReplaceAll(in.Share, `\`, "/"), "/")
		if in.Share == "" || strings.Contains(in.Share, "..") || strings.ContainsAny(in.Share, `:*?"<>|,`) {
			return errors.New("enter the share name, e.g. Media")
		}
	case "nfs":
		if !strings.HasPrefix(in.Share, "/") || strings.Contains(in.Share, "..") || strings.ContainsAny(in.Share, " ,") {
			return errors.New("enter the export path, e.g. /volume1/media")
		}
		in.Username, in.Password = "", ""
	default:
		return errors.New("choose SMB or NFS")
	}
	return nil
}

// Add saves a network drive after connecting to it once (so typos fail early).
func (m *Manager) Add(ctx context.Context, in NewDrive) (Drive, error) {
	if !m.native() {
		return Drive{}, errors.New("network drives need NoCapOS installed on Linux (it mounts them as root)")
	}
	if err := validate(&in); err != nil {
		return Drive{}, err
	}
	if !m.has(in.Kind) {
		return Drive{}, fmt.Errorf("install %s support first", strings.ToUpper(in.Kind))
	}
	rec := &store.NetMount{Name: in.Name, Kind: in.Kind, Host: in.Host, Share: in.Share, Username: in.Username, Auto: in.Auto}
	if in.Password != "" {
		rec.Password = m.box.Seal(in.Password)
	}
	id, err := m.st.CreateNetMount(ctx, rec)
	if err != nil {
		return Drive{}, err
	}
	rec.ID, rec.CreatedAt = id, time.Now().UTC()
	if err := m.mount(ctx, rec); err != nil {
		_ = m.st.DeleteNetMount(context.Background(), id)
		_ = os.Remove(m.credFile(id))
		m.mu.Lock()
		delete(m.state, id)
		m.mu.Unlock()
		return Drive{}, err
	}
	return m.view(rec), nil
}

func (m *Manager) credFile(id int64) string {
	return filepath.Join(m.dataDir, "secrets", fmt.Sprintf("netdrive-%d.cred", id))
}

func (m *Manager) setState(id int64, f func(*driveState)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.state[id]
	if st == nil {
		st = &driveState{}
		m.state[id] = st
	}
	f(st)
}

// mount mounts a drive and offers it as a storage location.
func (m *Manager) mount(ctx context.Context, d *store.NetMount) (err error) {
	defer func() {
		m.setState(d.ID, func(s *driveState) {
			s.mounted = err == nil
			s.err = ""
			if err != nil {
				s.err = err.Error()
			}
		})
	}()
	mp := m.mountPoint(d)
	if err := os.MkdirAll(mp, 0o755); err != nil {
		return err
	}
	if !m.mounted(mp) {
		var args []string
		switch d.Kind {
		case "smb":
			opts := "uid=0,gid=0,file_mode=0664,dir_mode=0775,iocharset=utf8,noperm"
			if d.Username == "" {
				opts += ",guest"
			} else {
				pw := ""
				if len(d.Password) > 0 {
					if pw, err = m.box.Open(d.Password); err != nil {
						return fmt.Errorf("unlock the saved password: %w", err)
					}
				}
				user, domain := d.Username, ""
				if i := strings.IndexAny(user, `\/`); i > 0 {
					domain, user = user[:i], user[i+1:]
				}
				cred := "username=" + user + "\npassword=" + pw + "\n"
				if domain != "" {
					cred += "domain=" + domain + "\n"
				}
				if err := os.MkdirAll(filepath.Dir(m.credFile(d.ID)), 0o700); err != nil {
					return err
				}
				if err := os.WriteFile(m.credFile(d.ID), []byte(cred), 0o600); err != nil {
					return err
				}
				opts += ",credentials=" + m.credFile(d.ID)
			}
			args = []string{"-t", "cifs", "//" + d.Host + "/" + d.Share, mp, "-o", opts}
		case "nfs":
			args = []string{"-t", "nfs", d.Host + ":" + d.Share, mp, "-o", "soft,timeo=150,retrans=2"}
		}
		cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		if out, err := m.run(cctx, "", "mount", args...); err != nil {
			_ = os.Remove(mp) // only removes an empty folder
			return fmt.Errorf("couldn't connect to %s: %s", address(d), friendlyMountError(out, err))
		}
	}
	m.files.AddRoot(files.Root{ID: rootID(d.ID), Name: d.Name, Path: mp})
	m.log.Info("network drive connected", "name", d.Name, "address", address(d), "path", mp)
	return nil
}

// friendlyMountError explains the usual mount failures in plain words.
func friendlyMountError(out string, err error) string {
	low := strings.ToLower(out + " " + err.Error())
	switch {
	case strings.Contains(low, "permission denied") || strings.Contains(low, "error(13)"):
		return "the user name or password was refused"
	case strings.Contains(low, "no such file or directory") || strings.Contains(low, "error(2)"):
		return "the server doesn't have that share or folder"
	case strings.Contains(low, "host is down") || strings.Contains(low, "error(112)") || strings.Contains(low, "no route to host"):
		return "the server can't be reached"
	case strings.Contains(low, "could not resolve") || strings.Contains(low, "resolve address"):
		return "the server name couldn't be found; try its IP address"
	case strings.Contains(low, "timed out") || strings.Contains(low, "deadline"):
		return "the server didn't answer in time"
	case strings.Contains(low, "access denied by server"):
		return "the server doesn't allow this computer to connect (check the NFS export's allowed clients)"
	}
	return lastLine(out)
}

// Connect mounts a saved drive (and lets it reconnect automatically again).
func (m *Manager) Connect(ctx context.Context, id int64) (Drive, error) {
	d, err := m.st.NetMount(ctx, id)
	if err != nil {
		return Drive{}, err
	}
	m.setState(id, func(s *driveState) { s.paused = false })
	if err := m.mount(ctx, d); err != nil {
		return m.view(d), err
	}
	return m.view(d), nil
}

// Disconnect unmounts a drive; it stays saved.
func (m *Manager) Disconnect(ctx context.Context, id int64) (Drive, error) {
	d, err := m.st.NetMount(ctx, id)
	if err != nil {
		return Drive{}, err
	}
	m.setState(id, func(s *driveState) { s.paused = true })
	if err := m.unmount(ctx, d); err != nil {
		return m.view(d), err
	}
	return m.view(d), nil
}

func (m *Manager) unmount(ctx context.Context, d *store.NetMount) error {
	mp := m.mountPoint(d)
	m.files.RemoveRoot(rootID(d.ID))
	if m.mounted(mp) {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if out, err := m.run(cctx, "", "umount", mp); err != nil {
			// Something still has files open; detach it now and finish when they close.
			if _, lerr := m.run(cctx, "", "umount", "-l", mp); lerr != nil {
				m.files.AddRoot(files.Root{ID: rootID(d.ID), Name: d.Name, Path: mp})
				return fmt.Errorf("couldn't disconnect: %s", lastLine(out))
			}
		}
	}
	_ = os.Remove(mp)
	m.setState(d.ID, func(s *driveState) { s.mounted, s.err = false, "" })
	m.log.Info("network drive disconnected", "name", d.Name)
	return nil
}

// SetAuto turns connecting at startup on or off.
func (m *Manager) SetAuto(ctx context.Context, id int64, auto bool) error {
	return m.st.SetNetMountAuto(ctx, id, auto)
}

// Delete disconnects a drive and forgets it (the files on the server stay).
func (m *Manager) Delete(ctx context.Context, id int64) error {
	d, err := m.st.NetMount(ctx, id)
	if err != nil {
		return err
	}
	if err := m.unmount(ctx, d); err != nil {
		return err
	}
	if err := m.st.DeleteNetMount(ctx, id); err != nil {
		return err
	}
	_ = os.Remove(m.credFile(id))
	m.mu.Lock()
	delete(m.state, id)
	m.mu.Unlock()
	return nil
}

// Run connects drives marked "at startup" and keeps retrying the ones that
// fail (the network or NAS may come up after NoCapOS), until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	if !m.native() {
		return
	}
	for {
		drives, err := m.st.NetMounts(ctx)
		if err == nil {
			for _, d := range drives {
				m.mu.Lock()
				st := m.state[d.ID]
				skip := !d.Auto || (st != nil && (st.mounted || st.paused))
				m.mu.Unlock()
				if skip || !m.has(d.Kind) {
					continue
				}
				if err := m.mount(ctx, d); err != nil {
					m.log.Warn("network drive: connect", "name", d.Name, "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Minute):
		}
	}
}

// Exports lists what an NFS server shares (showmount -e).
func (m *Manager) Exports(ctx context.Context, host string) ([]string, error) {
	host = strings.TrimSpace(host)
	if !hostRe.MatchString(host) {
		return nil, errors.New("enter the server's name or IP address")
	}
	if _, err := m.lookPath("showmount"); err != nil {
		return nil, errors.New("install NFS support first")
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := m.run(cctx, "", "showmount", "-e", "--no-headers", host)
	if err != nil {
		return nil, fmt.Errorf("couldn't ask %s for its shares: %s", host, lastLine(out))
	}
	var exports []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) > 0 && strings.HasPrefix(f[0], "/") {
			exports = append(exports, f[0])
		}
	}
	return exports, nil
}

// isMountPoint reports whether path is a mount point (from /proc/self/mountinfo).
func isMountPoint(path string) bool {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 4 && unescapeMount(fields[4]) == path {
			return true
		}
	}
	return false
}

// unescapeMount decodes \040-style escapes in mountinfo paths.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			var c int
			if _, err := fmt.Sscanf(s[i+1:i+4], "%03o", &c); err == nil {
				b.WriteByte(byte(c))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
