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
// NoCap Desktop is what the server's own monitor shows. install.sh sets up
// up to two of these, and Settings switches between them:
//
//	full   the native desktop: NoCapOS's GDM sign-in with the machine's
//	       Linux accounts, then a GNOME session with NoCapOS's look
//	kiosk  NoCapOS full screen (nocapos-desktop.service: a small compositor
//	       and a browser on tty1, behind NoCapOS's own sign-in)
//	off    the text console

// Modes.
const (
	ModeFull  = "full"
	ModeKiosk = "kiosk"
	ModeOff   = "off"
)

// DesktopUnit is the systemd unit install.sh writes for the kiosk.
const DesktopUnit = "nocapos-desktop.service"

// DesktopUnitPath is where it lives.
const DesktopUnitPath = "/etc/systemd/system/" + DesktopUnit

// DesktopEnvFile names the session on the screen (KEY=VALUE lines: the
// user, the Wayland/X display and runtime folder), so native apps can be
// opened there. The kiosk writes it; for the full desktop, alfad does
// (WatchSession).
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
	// Mode is full, kiosk or off; Modes are the ones this machine has.
	Mode  string   `json:"mode"`
	Modes []string `json:"modes"`
	// Signed in on the screen right now (full desktop), if anyone.
	SessionUser string `json:"session_user,omitempty"`
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

const notSetUp = "Not set up yet. Plug in a screen, then run on the server: curl -fsSL https://github.com/shatheitguy/nocapos/releases/latest/download/install.sh | sudo bash -s -- --desktop"

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

func (d *Desktop) hasKiosk() bool { return d.exists(DesktopUnitPath) }

// Modes lists what this machine can switch between (always ending in off).
func (d *Desktop) Modes() []string {
	var m []string
	if d.conf().Full {
		m = append(m, ModeFull)
	}
	if d.hasKiosk() {
		m = append(m, ModeKiosk)
	}
	if len(m) == 0 {
		return nil
	}
	return append(m, ModeOff)
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
	switch {
	case c.Full && d.systemctl(ctx, "is-enabled", c.DMUnit) == "enabled":
		st.Mode = ModeFull
		st.Active = d.systemctl(ctx, "is-active", c.DMUnit) == "active"
	case d.hasKiosk() && d.systemctl(ctx, "is-enabled", DesktopUnit) == "enabled":
		st.Mode = ModeKiosk
		st.Active = d.systemctl(ctx, "is-active", DesktopUnit) == "active"
	}
	st.Enabled = st.Mode != ModeOff
	if c.Full {
		if s, ok := d.ActiveSession(ctx); ok {
			st.SessionUser = s.User
		}
		if c.GDMConf != "" {
			st.AutologinSupported = true
			if b, err := d.readFile(c.GDMConf); err == nil {
				st.Autologin, st.AutologinUser = ParseAutologin(string(b))
			}
		}
	}
	st.Remote = st.Active && d.remoteSupported(st.Mode)
	return st
}

// ModeCommands are the systemctl calls that switch to mode (the same ones
// install.sh's apply_desktop_mode makes).
func ModeCommands(mode string, c DesktopConf, kiosk bool) [][]string {
	var cmds [][]string
	add := func(args ...string) { cmds = append(cmds, args) }
	switch mode {
	case ModeFull:
		if kiosk {
			add("disable", "--now", DesktopUnit)
		}
		add("set-default", "graphical.target")
		add("enable", "--force", c.DMUnit)
		add("restart", c.DMUnit)
	case ModeKiosk:
		if c.Full {
			add("disable", "--now", c.DMUnit)
			add("set-default", "multi-user.target")
		}
		add("enable", "--now", DesktopUnit)
	case ModeOff:
		if c.Full {
			add("disable", "--now", c.DMUnit)
			add("set-default", "multi-user.target")
		}
		if kiosk {
			add("disable", "--now", DesktopUnit)
		}
		// The desktop had the screen; give the text console its login back.
		add("--no-block", "start", "getty@tty1.service")
	}
	return cmds
}

// SetMode switches NoCap Desktop to full, kiosk or off.
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
	for _, args := range ModeCommands(mode, d.conf(), d.hasKiosk()) {
		out, err := d.Run(ctx, "systemctl", args...)
		if err != nil && args[0] != "--no-block" {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), msg)
		}
	}
	// Remembered for updates (install.sh keeps this mode).
	_ = d.writeFile(DesktopModePath, []byte(mode+"\n"), 0o644)
	return nil
}

// Set is the older on/off switch: on means the full desktop where there is
// one, else NoCapOS full screen.
func (d *Desktop) Set(ctx context.Context, on bool) error {
	if !on {
		return d.SetMode(ctx, ModeOff)
	}
	modes := d.Modes()
	if len(modes) == 0 {
		return d.SetMode(ctx, ModeFull) // ErrDesktopUnavailable
	}
	return d.SetMode(ctx, modes[0])
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
