package hostaccess

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ---- NoCap Desktop ----
//
// NoCap Desktop is what the server's own monitor shows: a native desktop
// (NoCapOS's GDM sign-in with the machine's Linux accounts, then the
// NoCapOS session: GNOME Shell with NoCapOS's look and native apps), or,
// when it's off, the text console. There is no web UI on the screen.

// Modes.
const (
	ModeFull = "full"
	ModeOff  = "off"
)

// DesktopEnvFile names the session on the screen (KEY=VALUE lines: the
// user, the Wayland/X display and runtime folder), so native apps can be
// opened there. alfad writes it (WatchSession).
const DesktopEnvFile = "/run/nocapos/desktop.env"

// DesktopConfPath is written by install.sh when the full desktop is set up.
const DesktopConfPath = "/etc/nocapos/desktop.conf"

// DesktopModePath keeps the last mode chosen, so updates keep it.
const DesktopModePath = "/etc/nocapos/desktop-mode"

// Runner runs a command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// InputRunner runs a command with stdin (how secrets reach a program: never
// in its arguments, which every user on the machine can read in ps).
type InputRunner func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)

// ExecRunner runs real commands.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// ExecInputRunner runs real commands with stdin.
func ExecInputRunner(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	return cmd.CombinedOutput()
}

// DesktopConf is install.sh's record of the full desktop.
type DesktopConf struct {
	Full    bool
	DMUnit  string // gdm3.service | gdm.service
	GDMConf string // GDM's settings file
}

var unitRe = regexp.MustCompile(`^[A-Za-z0-9@._-]+\.service$`)

// ParseDesktopConf reads desktop.conf. Values that don't look right are
// dropped (they end up in systemctl's arguments and a file path).
func ParseDesktopConf(s string) DesktopConf {
	var c DesktopConf
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "FULL":
			c.Full = v == "1" || v == "yes" || v == "true"
		case "DM_UNIT":
			if unitRe.MatchString(v) {
				c.DMUnit = v
			}
		case "GDM_CONF":
			if strings.HasPrefix(v, "/etc/") && !strings.Contains(v, "..") {
				c.GDMConf = path.Clean(v)
			}
		}
	}
	if c.Full && c.DMUnit == "" {
		c.DMUnit = "gdm.service"
	}
	return c
}

// Desktop controls NoCap Desktop.
type Desktop struct {
	Run       Runner
	RunInput  InputRunner
	Exists    func(string) bool
	ReadFile  func(string) ([]byte, error)
	WriteFile func(string, []byte, os.FileMode) error
	// Native: NoCapOS runs on Linux as root outside a container.
	Native bool
	// DataDir holds the remote screen's credentials and certificate.
	DataDir string
	// DRMDir is /sys/class/drm (monitors), RuntimeRoot /run/user.
	DRMDir      string
	RuntimeRoot string
	// Dial checks that a local port answers (the remote screen).
	Dial func(addr string, timeout time.Duration) error
	// LookPath reports whether a program is installed.
	LookPath func(string) bool
	// LookupAccount and Chown: Linux accounts (real ones when nil).
	LookupAccount func(string) (Account, error)
	Chown         func(path string, uid, gid int) error
	// ProcRoot is /proc (finding a session's environment).
	ProcRoot string
	// SessionWait: how long RemoteView waits for the screen to have a
	// session (0 = 15 seconds).
	SessionWait time.Duration

	mirror screenMirror
}

// NewDesktop wires the real machine.
func NewDesktop(native bool, dataDir string) *Desktop {
	return &Desktop{Run: ExecRunner, RunInput: ExecInputRunner, Native: native, DataDir: dataDir}
}

// DesktopState is what Settings shows.
type DesktopState struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Enabled: some mode other than off is on (older UIs read this).
	Enabled bool `json:"enabled"`
	Active  bool `json:"active"`
	Display bool `json:"display"` // a monitor is plugged in
	// Mode is full or off; Modes are the ones this machine has.
	Mode  string   `json:"mode"`
	Modes []string `json:"modes"`
	// Signed in on the screen right now (full desktop), if anyone.
	SessionUser string `json:"session_user,omitempty"`
	// SignInScreen: the screen shows the sign-in screen (nobody signed in).
	SignInScreen bool `json:"sign_in_screen"`
	// Autologin: the sign-in screen opens this account without a password.
	Autologin          bool   `json:"autologin"`
	AutologinUser      string `json:"autologin_user,omitempty"`
	AutologinSupported bool   `json:"autologin_supported"`
	// Remote: the screen can be opened in the browser (NoCap Desktop window).
	Remote bool `json:"remote"`
}

// ErrDesktopUnavailable means NoCap Desktop isn't set up on this server.
var ErrDesktopUnavailable = errors.New("NoCap Desktop isn't set up on this server")

// ErrBadMode is a mode this machine doesn't have.
var ErrBadMode = errors.New("this server doesn't have that NoCap Desktop mode")

const notSetUp = "Not set up yet. Run Update everything in Settings → Glow Up and NoCap Desktop is set up automatically."

func (d *Desktop) exists(p string) bool {
	if d.Exists != nil {
		return d.Exists(p)
	}
	_, err := os.Stat(p)
	return err == nil
}

func (d *Desktop) readFile(p string) ([]byte, error) {
	if d.ReadFile != nil {
		return d.ReadFile(p)
	}
	return os.ReadFile(p)
}

func (d *Desktop) writeFile(p string, b []byte, mode os.FileMode) error {
	if d.WriteFile != nil {
		return d.WriteFile(p, b, mode)
	}
	return writeFileAtomic(p, b, mode)
}

func (d *Desktop) conf() DesktopConf {
	b, err := d.readFile(DesktopConfPath)
	if err != nil {
		return DesktopConf{}
	}
	return ParseDesktopConf(string(b))
}

// Modes lists what this machine can switch between: full and off once
// install.sh has set the desktop up, else nothing.
func (d *Desktop) Modes() []string {
	if !d.conf().Full {
		return nil
	}
	return []string{ModeFull, ModeOff}
}

func (d *Desktop) systemctl(ctx context.Context, args ...string) string {
	out, _ := d.Run(ctx, "systemctl", args...)
	return strings.TrimSpace(string(out))
}

// State reports the modes, which one is on and whether it's running.
func (d *Desktop) State(ctx context.Context) DesktopState {
	drm := d.DRMDir
	if drm == "" {
		drm = "/sys/class/drm"
	}
	st := DesktopState{Display: DisplayConnected(drm), Mode: ModeOff}
	if !d.Native {
		st.Reason = "NoCap Desktop needs NoCapOS installed straight on Linux (not in Docker)."
		return st
	}
	st.Modes = d.Modes()
	if len(st.Modes) == 0 {
		st.Reason = notSetUp
		return st
	}
	st.Available = true
	c := d.conf()
	if d.systemctl(ctx, "is-enabled", c.DMUnit) == "enabled" {
		st.Mode = ModeFull
		st.Active = d.systemctl(ctx, "is-active", c.DMUnit) == "active"
	}
	st.Enabled = st.Mode != ModeOff
	if s, ok := d.ScreenSession(ctx); ok {
		if s.Greeter {
			st.SignInScreen = true
		} else {
			st.SessionUser = s.User
		}
	}
	if c.GDMConf != "" {
		st.AutologinSupported = true
		if b, err := d.readFile(c.GDMConf); err == nil {
			st.Autologin, st.AutologinUser = ParseAutologin(string(b))
		}
	}
	st.Remote = st.Active && d.remoteSupported(st.Mode)
	return st
}

// ModeCommands are the systemctl calls that switch to mode (the same ones
// install.sh's apply_desktop_mode makes).
func ModeCommands(mode string, c DesktopConf) [][]string {
	var cmds [][]string
	add := func(args ...string) { cmds = append(cmds, args) }
	switch mode {
	case ModeFull:
		add("set-default", "graphical.target")
		add("enable", "--force", c.DMUnit)
		add("restart", c.DMUnit)
	case ModeOff:
		add("disable", "--now", c.DMUnit)
		add("set-default", "multi-user.target")
		// The desktop had the screen; give the text console its login back.
		add("--no-block", "start", "getty@tty1.service")
	}
	return cmds
}

// SetMode switches NoCap Desktop on (full) or off.
func (d *Desktop) SetMode(ctx context.Context, mode string) error {
	if !d.Native {
		return ErrDesktopUnavailable
	}
	modes := d.Modes()
	if len(modes) == 0 {
		return ErrDesktopUnavailable
	}
	ok := false
	for _, m := range modes {
		ok = ok || m == mode
	}
	if !ok {
		return ErrBadMode
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if mode != ModeFull {
		d.StopScreen(ctx)
	}
	for _, args := range ModeCommands(mode, d.conf()) {
		out, err := d.Run(ctx, "systemctl", args...)
		if err != nil && args[0] != "--no-block" {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), msg)
		}
	}
	// Remembered for updates: install.sh turns the desktop on unless the
	// owner switched it off here ("user-off").
	saved := mode
	if mode == ModeOff {
		saved = "user-off"
	}
	_ = d.writeFile(DesktopModePath, []byte(saved+"\n"), 0o644)
	return nil
}

// Set is the on/off switch.
func (d *Desktop) Set(ctx context.Context, on bool) error {
	if !on {
		return d.SetMode(ctx, ModeOff)
	}
	return d.SetMode(ctx, ModeFull)
}

// DisplayConnected reports whether any graphics output under drmDir (normally
// /sys/class/drm) has a monitor plugged in.
func DisplayConnected(drmDir string) bool {
	matches, _ := filepath.Glob(filepath.Join(drmDir, "*", "status"))
	for _, m := range matches {
		if b, err := os.ReadFile(m); err == nil && strings.TrimSpace(string(b)) == "connected" {
			return true
		}
	}
	return false
}

// writeFileAtomic replaces a file in one step (a reader never sees half).
func writeFileAtomic(p string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".nocapos-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
