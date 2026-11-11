package webapps

import (
	"errors"
	"strconv"
	"strings"
)

// Streamed apps run outside nocapos.service.
//
// alfad's unit is hardened (PrivateTmp, ProtectKernelLogs, …), and every
// child of alfad inherits that: its own /tmp (so Xvfb's /tmp/.X11-unix is a
// private folder the person's account ends up owning) and a /proc with
// masked files (so bubblewrap, which every `flatpak run` uses, can't mount
// a fresh /proc in its sandbox). So each stream runs in a transient systemd
// unit of its own (systemd-run, like the updater): the host's real /tmp and
// /proc, the person's account, and nothing of alfad's sandbox. alfad's own
// hardening stays as it is.
//
// Before the stream starts, a short root unit gets the host ready:
// /tmp/.X11-unix owned by root with mode 1777 (X refuses anything else), a
// stale display lock cleared, and the account's runtime folder (and, for
// the shared apps account, the person's home) made with the right owner.

const (
	// AppUnitPrefix starts every stream's unit name (nocap-app-<user>-<id>).
	AppUnitPrefix = "nocap-app-"
	// prepUnitPrefix starts the root setup unit's name.
	prepUnitPrefix = "nocap-prep-"
	// StreamScriptPath is where alfad puts app-stream.sh for the units: on
	// the host's /run (not alfad's private /tmp), root-owned, world-readable.
	StreamScriptPath = "/run/nocapos/app-stream.sh"
	// AppsRuntimeBase holds the shared apps account's per-person runtime folders.
	AppsRuntimeBase = "/run/nocap-apps"
	// streamPath is PATH inside a stream.
	streamPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	// streamDataDirs lets menus and file pickers see system-wide Flatpak apps.
	streamDataDirs = "/var/lib/flatpak/exports/share:/usr/local/share:/usr/share"
)

// UnitSpec is one transient systemd unit.
type UnitSpec struct {
	Unit        string
	Description string
	// User runs the unit (name or uid); empty = root (setup units only).
	User    string
	GID     uint32
	WorkDir string
	Env     []string // KEY=VALUE
	// LogPath gets the unit's output (appended); empty = the journal.
	LogPath string
	// Wait keeps systemd-run running until the unit ends (its exit is
	// how alfad notices the stream closed).
	Wait bool
	// Pipe connects the unit's output to systemd-run's (short setup units).
	Pipe bool
	Argv []string
}

// SystemdRunArgs is systemd-run's argv (without "systemd-run") for u.
func SystemdRunArgs(u UnitSpec) []string {
	args := []string{"--unit=" + u.Unit, "--collect", "--quiet"}
	if u.Description != "" {
		args = append(args, "--description="+u.Description)
	}
	if u.Wait || u.Pipe {
		args = append(args, "--wait")
	}
	if u.Pipe {
		args = append(args, "--pipe")
	}
	if u.User != "" {
		args = append(args, "--uid="+u.User, "--gid="+strconv.FormatUint(uint64(u.GID), 10))
	}
	if u.WorkDir != "" {
		// "-": a missing folder isn't fatal (the app starts in /).
		args = append(args, "--property=WorkingDirectory=-"+u.WorkDir)
	}
	args = append(args, "--property=Type=exec", "--property=KillMode=control-group", "--property=TimeoutStopSec=10")
	if u.LogPath != "" {
		args = append(args, "--property=StandardOutput=append:"+u.LogPath, "--property=StandardError=append:"+u.LogPath)
	}
	for _, e := range u.Env {
		args = append(args, "--setenv="+e)
	}
	args = append(args, "--")
	for _, a := range u.Argv {
		args = append(args, systemdLiteral(a))
	}
	return args
}

// systemdLiteral keeps systemd from expanding $VAR / ${VAR} in an argument
// ("$$" is a literal "$" to it).
func systemdLiteral(s string) string { return strings.ReplaceAll(s, "$", "$$") }

// UnitName is a unit name made of safe characters only.
func UnitName(prefix, user, id string) string {
	clean := func(s string) string {
		var b strings.Builder
		for _, c := range s {
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
				b.WriteRune(c)
			default:
				b.WriteRune('_')
			}
		}
		return b.String()
	}
	u := clean(user)
	if len(u) > 32 {
		u = u[:32]
	}
	if u == "" {
		u = "x"
	}
	return prefix + u + "-" + clean(id)
}

// prepScript runs as root, outside alfad's sandbox, before a stream:
//
//	$1 display number ("" = none)  $2 uid  $3 gid
//	$4 runtime folder ("" = leave)  $5 home to make for the account ("" = leave)
//
// It never follows a link it would hand to the account.
const prepScript = `set -eu
n="$1"; uid="$2"; gid="$3"; rt="$4"; home="$5"
d=/tmp/.X11-unix
if [ -L "$d" ] || { [ -e "$d" ] && [ ! -d "$d" ]; }; then rm -f "$d"; fi
mkdir -p "$d"
chown 0:0 "$d"
chmod 1777 "$d"
if [ -n "$n" ] && [ -e "/tmp/.X$n-lock" ]; then
  pid="$(tr -cd 0-9 <"/tmp/.X$n-lock" 2>/dev/null || true)"
  if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then rm -f "/tmp/.X$n-lock" "$d/X$n"; fi
fi
own_dir() {
  if [ -L "$1" ]; then echo "$1 is a link; refusing" >&2; exit 1; fi
  parent="$(dirname "$1")"
  if [ ! -d "$parent" ]; then mkdir -p "$parent"; chmod 0755 "$parent"; fi
  if [ ! -d "$1" ]; then mkdir -m 0700 "$1"; fi
  chown "$uid:$gid" "$1"
  chmod 0700 "$1"
}
if [ -n "$rt" ]; then own_dir "$rt"; fi
if [ -n "$home" ]; then own_dir "$home"; fi
`

// PrepArgv is the root setup command for a stream (see prepScript).
func PrepArgv(display string, a *RunAs) []string {
	home := ""
	if a.OwnHome {
		home = a.Home
	}
	return []string{"/bin/sh", "-c", prepScript, "nocap-prep",
		strings.TrimPrefix(display, ":"),
		strconv.FormatUint(uint64(a.UID), 10), strconv.FormatUint(uint64(a.GID), 10),
		a.Runtime, home}
}

// PrepUnit is the root setup unit for a session.
func PrepUnit(id, display string, a *RunAs) UnitSpec {
	return UnitSpec{
		Unit: prepUnitPrefix + id, Description: "NoCapOS app stream setup",
		Pipe: true, Argv: PrepArgv(display, a),
	}
}

// StreamUnit is the unit a session runs in: the person's account, their
// home and runtime folder, a session bus made inside, never root.
func StreamUnit(id string, spec StreamSpec, slot streamSlot, logPath, lang string) UnitSpec {
	a := spec.RunAs
	mode := spec.Mode
	if mode == "" {
		mode = ModeApp
	}
	env := []string{
		"PATH=" + streamPath,
		"LANG=" + lang,
		"HOME=" + a.Home,
		"USER=" + a.Username,
		"LOGNAME=" + a.Username,
		"XDG_RUNTIME_DIR=" + a.Runtime,
		"XDG_DATA_DIRS=" + streamDataDirs,
		"XDG_CONFIG_DIRS=/etc/xdg",
		"NOCAP_MODE=" + mode,
		"NOCAP_VNC_PORT=" + strconv.Itoa(slot.VNCPort),
		"NOCAP_WEB_PORT=" + strconv.Itoa(slot.WebPort),
	}
	if mode != ModeScreen {
		env = append(env, "NOCAP_DISPLAY="+slot.Display)
	}
	env = append(env, spec.Env...)
	argv := append([]string{"/bin/bash", StreamScriptPath, mode}, spec.Command...)
	title := spec.Title
	if title == "" {
		title = spec.Key
	}
	return UnitSpec{
		Unit:        UnitName(AppUnitPrefix, a.Username, id),
		Description: "NoCapOS stream: " + title,
		User:        a.Username, GID: a.GID,
		WorkDir: a.Home, Env: env, LogPath: logPath, Wait: true, Argv: argv,
	}
}

// ErrNoAccount: a stream needs an unprivileged account to run as.
var ErrNoAccount = errors.New("this app needs an unprivileged Linux account to run as (never root)")

// streamPlan checks a session and builds its units: the root setup (none
// for the server's screen, whose session is already set up) and the stream
// itself. The account is never root.
func streamPlan(s *streamSession, logPath, lang string) (prep *UnitSpec, unit UnitSpec, err error) {
	a := s.spec.RunAs
	if a == nil || a.UID == 0 || a.Username == "" || a.Username == "root" || a.Home == "" {
		return nil, UnitSpec{}, ErrNoAccount
	}
	spec := s.spec
	if spec.Mode == "" {
		spec.Mode = ModeApp
	}
	switch spec.Mode {
	case ModeApp:
		if len(spec.Command) == 0 {
			return nil, UnitSpec{}, errors.New("nothing to run")
		}
	case ModeDesktop, ModeScreen:
	default:
		return nil, UnitSpec{}, errors.New("unknown stream mode " + spec.Mode)
	}
	run := *a
	if run.Runtime == "" {
		run.Runtime = "/run/user/" + strconv.FormatUint(uint64(a.UID), 10)
	}
	spec.RunAs = &run
	if spec.Mode != ModeScreen {
		p := PrepUnit(s.id, s.slot.Display, &run)
		prep = &p
	}
	return prep, StreamUnit(s.id, spec, s.slot, logPath, lang), nil
}
