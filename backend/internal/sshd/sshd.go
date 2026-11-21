// Package sshd is NoCapOS's side of remote login (SSH): one switch that
// installs, configures, starts and stops the system's OpenSSH server, the
// settings NoCapOS writes for it (a drop-in in /etc/ssh/sshd_config.d,
// checked with "sshd -t" and rolled back when the check fails), and each
// person's own authorized_keys.
//
// Programs always run with an argument array, never through a shell.
package sshd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DropIn is the settings file NoCapOS owns, under /etc/ssh/sshd_config.d.
const DropIn = "50-nocapos.conf"

// Who may log in.
const (
	WhoAdmins   = "admins"
	WhoEveryone = "everyone"
)

// Firewall rule comments: how the rules this switch adds are found again.
const (
	CommentLAN     = "NoCapOS remote login (local network)"
	CommentOutside = "NoCapOS remote login (from anywhere)"
)

var (
	// ErrUnsupported: this host can't run the SSH server from NoCapOS.
	ErrUnsupported = errors.New("remote login needs NoCapOS installed straight on Linux as root")
	// ErrNotInstalled: the OpenSSH server isn't installed.
	ErrNotInstalled = errors.New("the SSH server (openssh-server) isn't installed")
	// ErrInvalid marks a request NoCapOS refuses.
	ErrInvalid = errors.New("invalid")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Settings are the choices kept between restarts. Whether SSH is on isn't
// kept here: the system service itself says so.
type Settings struct {
	Who      string `json:"who"`      // admins | everyone
	Password bool   `json:"password"` // password login allowed
	Outside  bool   `json:"outside"`  // reachable from outside the local network
}

// DefaultSettings: admins only, passwords allowed (until a key is added),
// local network only.
func DefaultSettings() Settings { return Settings{Who: WhoAdmins, Password: true} }

// Caps is what this OpenSSH version supports.
type Caps struct {
	Major, Minor int
	// Penalties: PerSourcePenalties (OpenSSH 9.8+), sshd's own brute-force
	// protection. Without it the firewall rate-limits port 22 instead.
	Penalties bool
	// KbdInteractive: the KbdInteractiveAuthentication keyword (8.7+).
	KbdInteractive bool
}

var versionRe = regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)`)

// ParseVersion reads "ssh -V" output.
func ParseVersion(out string) Caps {
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return Caps{}
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	at := func(a, b int) bool { return maj > a || (maj == a && min >= b) }
	return Caps{Major: maj, Minor: min, Penalties: at(9, 8), KbdInteractive: at(8, 7)}
}

// Allow is who sshd lets in: members of Groups, or exactly Users.
type Allow struct {
	Groups []string
	Users  []string
}

var nameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// Render is the drop-in's text. Root login is always off; names that don't
// look like Linux account or group names are left out, and with nobody left
// nobody is let in.
func Render(s Settings, a Allow, c Caps) string {
	yesNo := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	var b strings.Builder
	b.WriteString("# Remote login (SSH), written by NoCapOS (Settings, Security, Remote login).\n")
	b.WriteString("# NoCapOS replaces this file whenever the setting changes.\n")
	b.WriteString("PermitRootLogin no\n")
	b.WriteString("PubkeyAuthentication yes\n")
	b.WriteString("PasswordAuthentication " + yesNo(s.Password) + "\n")
	if c.KbdInteractive {
		b.WriteString("KbdInteractiveAuthentication no\n")
	}
	b.WriteString("PermitEmptyPasswords no\n")
	b.WriteString("MaxAuthTries 3\n")
	b.WriteString("LoginGraceTime 30\n")
	if c.Penalties {
		b.WriteString("PerSourcePenalties yes\n")
	}
	clean := func(in []string) []string {
		var out []string
		for _, n := range in {
			if nameRe.MatchString(n) && n != "root" {
				out = append(out, n)
			}
		}
		sort.Strings(out)
		return slicesCompact(out)
	}
	if s.Who == WhoEveryone {
		if u := clean(a.Users); len(u) > 0 {
			b.WriteString("AllowUsers " + strings.Join(u, " ") + "\n")
			return b.String()
		}
	} else if g := clean(a.Groups); len(g) > 0 {
		b.WriteString("AllowGroups " + strings.Join(g, " ") + "\n")
		return b.String()
	}
	b.WriteString("DenyUsers *\n")
	return b.String()
}

func slicesCompact(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// Runner runs a program with arguments and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) (string, error)

func execRunner(ctx context.Context, name string, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Minute)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil && s == "" {
		s = err.Error()
	}
	return s, err
}

// Manager drives the SSH server. One change runs at a time.
type Manager struct {
	Run    Runner
	Etc    string // "/etc"; a temporary folder in tests
	RunDir string // "/run": sshd -t needs /run/sshd
	Data   string // NoCapOS's data folder (settings, key notes)
	// Have reports whether a program is installed.
	Have func(name string) bool
	// Native: NoCapOS runs on Linux as root, outside a container.
	Native bool
	// Lookup finds a Linux account's numbers and home (keys.go).
	Lookup func(username string) (Account, error)
	// Chown sets a path's owner (never following links).
	Chown func(path string, uid, gid int) error

	mu sync.Mutex
}

// New is the manager for this machine.
func New(dataDir string, native bool) *Manager {
	return &Manager{
		Run: execRunner, Etc: "/etc", RunDir: "/run", Data: dataDir, Native: native,
		Have: func(name string) bool {
			if _, err := exec.LookPath(name); err == nil {
				return true
			}
			for _, d := range []string{"/usr/sbin/", "/sbin/"} {
				if _, err := os.Stat(d + name); err == nil {
					return true
				}
			}
			return false
		},
		Lookup: lookupAccount,
		Chown:  lchown,
	}
}

func (m *Manager) settingsPath() string { return filepath.Join(m.Data, "ssh.json") }
func (m *Manager) dropInPath() string {
	return filepath.Join(m.Etc, "ssh", "sshd_config.d", DropIn)
}
func (m *Manager) mainConfig() string { return filepath.Join(m.Etc, "ssh", "sshd_config") }

// Settings reads the saved choices (the defaults when there are none).
func (m *Manager) Settings() Settings {
	s := DefaultSettings()
	b, err := os.ReadFile(m.settingsPath())
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	if s.Who != WhoEveryone {
		s.Who = WhoAdmins
	}
	return s
}

// SaveSettings keeps the choices.
func (m *Manager) SaveSettings(s Settings) error {
	if err := os.MkdirAll(m.Data, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return writeAtomic(m.settingsPath(), b, 0o600)
}

// Installed reports whether the OpenSSH server is installed.
func (m *Manager) Installed() bool { return m.Have("sshd") }

// Caps asks the installed OpenSSH what it supports.
func (m *Manager) Caps(ctx context.Context) Caps {
	out, _ := m.Run(ctx, "ssh", "-V")
	return ParseVersion(out)
}

// Unit is the system service's name: ssh.service on Debian and Ubuntu,
// sshd.service elsewhere.
func (m *Manager) Unit(ctx context.Context) string {
	out, _ := m.Run(ctx, "systemctl", "show", "-p", "LoadState", "--value", "ssh.service")
	if strings.TrimSpace(out) == "loaded" {
		return "ssh.service"
	}
	return "sshd.service"
}

// socketUnit is Ubuntu's socket activation for the same server.
func socketUnit(unit string) string { return strings.TrimSuffix(unit, ".service") + ".socket" }

// ServiceState says whether the server is switched on and running.
func (m *Manager) ServiceState(ctx context.Context, unit string) (enabled, running bool) {
	active := func(u string) bool {
		out, _ := m.Run(ctx, "systemctl", "is-active", u)
		return strings.TrimSpace(out) == "active"
	}
	isEnabled := func(u string) bool {
		out, _ := m.Run(ctx, "systemctl", "is-enabled", u)
		s := strings.TrimSpace(out)
		return s == "enabled" || s == "enabled-runtime" || s == "alias"
	}
	running = active(unit)
	enabled = running || isEnabled(unit) || isEnabled(socketUnit(unit)) || active(socketUnit(unit))
	return enabled, running
}

// Port is the port sshd listens on (22 unless its settings say otherwise).
func (m *Manager) Port(ctx context.Context) int {
	_ = os.MkdirAll(filepath.Join(m.RunDir, "sshd"), 0o755)
	out, err := m.Run(ctx, "sshd", "-T")
	if err != nil {
		return 22
	}
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "port "); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 && n < 65536 {
				return n
			}
		}
	}
	return 22
}

// Effective lists settings another file overrides (sshd takes the first
// value it reads, so a drop-in sorting before NoCapOS's wins).
func (m *Manager) Effective(ctx context.Context, s Settings) []string {
	_ = os.MkdirAll(filepath.Join(m.RunDir, "sshd"), 0o755)
	out, err := m.Run(ctx, "sshd", "-T")
	if err != nil {
		return nil
	}
	got := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), " ")
		if ok {
			if _, seen := got[k]; !seen {
				got[k] = strings.TrimSpace(v)
			}
		}
	}
	var over []string
	if v, ok := got["permitrootlogin"]; ok && v != "no" {
		over = append(over, "PermitRootLogin")
	}
	want := "no"
	if s.Password {
		want = "yes"
	}
	if v, ok := got["passwordauthentication"]; ok && v != want {
		over = append(over, "PasswordAuthentication")
	}
	return over
}

// HostKeysMissing reports whether the server has no host keys (an image
// built without them: sshd then refuses to start).
func (m *Manager) HostKeysMissing() bool {
	keys, _ := filepath.Glob(filepath.Join(m.Etc, "ssh", "ssh_host_*_key"))
	return len(keys) == 0
}

// EnsureHostKeys creates the missing host keys.
func (m *Manager) EnsureHostKeys(ctx context.Context) error {
	if !m.HostKeysMissing() {
		return nil
	}
	if out, err := m.Run(ctx, "ssh-keygen", "-A"); err != nil {
		return fmt.Errorf("couldn't create the server's SSH host keys: %s", lastLine(out, err))
	}
	return nil
}

func lastLine(out string, err error) string {
	s := strings.TrimSpace(out)
	if s == "" && err != nil {
		s = err.Error()
	}
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	return s
}

// CheckError is "sshd -t" saying no, in plain words.
type CheckError struct {
	Output string
}

func (e *CheckError) Error() string { return Explain(e.Output) }

// Explain turns sshd's complaints into plain words.
func Explain(out string) string {
	low := strings.ToLower(out)
	switch {
	case strings.Contains(low, "no hostkeys available"), strings.Contains(low, "could not load host key"):
		return "The SSH server has no host keys (the identity it shows to computers that connect), so it can't start."
	case strings.Contains(low, "privilege separation directory"):
		return "A folder the SSH server needs (/run/sshd) is missing."
	case strings.Contains(low, "bad configuration option"), strings.Contains(low, "unsupported option"):
		return "The SSH server doesn't understand one of its settings: " + lastLine(out, nil)
	case strings.Contains(low, "address already in use"):
		return "Another program already uses the SSH port."
	case strings.Contains(low, "permission"):
		return "The SSH server can't read one of its files: " + lastLine(out, nil)
	case strings.TrimSpace(out) == "":
		return "The SSH server's settings check failed."
	}
	return "The SSH server's settings check failed: " + lastLine(out, nil)
}

var includeRe = regexp.MustCompile(`(?mi)^\s*Include\s+\S*sshd_config\.d/`)

// Check runs "sshd -t".
func (m *Manager) Check(ctx context.Context) error {
	_ = os.MkdirAll(filepath.Join(m.RunDir, "sshd"), 0o755)
	if out, err := m.Run(ctx, "sshd", "-t"); err != nil {
		return &CheckError{Output: out}
	}
	return nil
}

// WriteConfig writes the drop-in (and, if the main settings file doesn't
// read drop-ins, the Include line that makes it), checks the result with
// "sshd -t", and puts everything back the way it was if the check fails.
func (m *Manager) WriteConfig(ctx context.Context, text string) error {
	dir := filepath.Dir(m.dropInPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	oldDrop, dropErr := os.ReadFile(m.dropInPath())
	oldMain, mainErr := os.ReadFile(m.mainConfig())
	restore := func() {
		if dropErr == nil {
			_ = writeAtomic(m.dropInPath(), oldDrop, 0o644)
		} else {
			_ = os.Remove(m.dropInPath())
		}
		if mainErr == nil {
			_ = writeAtomic(m.mainConfig(), oldMain, 0o644)
		}
	}
	if mainErr == nil && !includeRe.Match(oldMain) {
		inc := "# Added by NoCapOS: read the settings files in sshd_config.d first.\nInclude /etc/ssh/sshd_config.d/*.conf\n\n"
		if err := writeAtomic(m.mainConfig(), append([]byte(inc), oldMain...), 0o644); err != nil {
			return err
		}
	}
	if err := writeAtomic(m.dropInPath(), []byte(text), 0o644); err != nil {
		restore()
		return err
	}
	if err := m.Check(ctx); err != nil {
		restore()
		return err
	}
	return nil
}

// Apply writes the settings and, if the server runs, has it reread them.
func (m *Manager) Apply(ctx context.Context, s Settings, a Allow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.Installed() {
		return ErrNotInstalled
	}
	if err := m.WriteConfig(ctx, Render(s, a, m.Caps(ctx))); err != nil {
		return err
	}
	unit := m.Unit(ctx)
	if _, running := m.ServiceState(ctx, unit); running {
		if out, err := m.Run(ctx, "systemctl", "reload", unit); err != nil {
			return fmt.Errorf("the SSH server didn't take the new settings: %s", lastLine(out, err))
		}
	}
	return nil
}

// Enable writes the settings, makes sure host keys exist, then turns the
// service on and starts it.
func (m *Manager) Enable(ctx context.Context, s Settings, a Allow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.Native {
		return ErrUnsupported
	}
	if !m.Installed() {
		return ErrNotInstalled
	}
	if err := m.EnsureHostKeys(ctx); err != nil {
		return err
	}
	if err := m.WriteConfig(ctx, Render(s, a, m.Caps(ctx))); err != nil {
		return err
	}
	unit := m.Unit(ctx)
	// One way to start it: the service (Ubuntu's socket activation would
	// start a second copy on the same port).
	_, _ = m.Run(ctx, "systemctl", "disable", "--now", socketUnit(unit))
	if out, err := m.Run(ctx, "systemctl", "enable", "--now", unit); err != nil {
		if cerr := m.Check(ctx); cerr != nil {
			return cerr
		}
		return fmt.Errorf("the SSH server didn't start: %s", lastLine(out, err))
	}
	if out, err := m.Run(ctx, "systemctl", "reload-or-restart", unit); err != nil {
		return fmt.Errorf("the SSH server didn't take the new settings: %s", lastLine(out, err))
	}
	return nil
}

// Disable stops the service and turns it off. The package stays.
func (m *Manager) Disable(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.Native {
		return ErrUnsupported
	}
	unit := m.Unit(ctx)
	_, _ = m.Run(ctx, "systemctl", "disable", "--now", socketUnit(unit))
	if out, err := m.Run(ctx, "systemctl", "disable", "--now", unit); err != nil {
		return fmt.Errorf("couldn't stop the SSH server: %s", lastLine(out, err))
	}
	return nil
}

// StartError explains why a switched-on server isn't running, if it can
// tell.
func (m *Manager) StartError(ctx context.Context) string {
	if m.HostKeysMissing() {
		return Explain("no hostkeys available")
	}
	if err := m.Check(ctx); err != nil {
		return err.Error()
	}
	return ""
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}
