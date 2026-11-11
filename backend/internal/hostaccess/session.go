package hostaccess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- who's signed in on the screen ----

// Session is a graphical login session on the machine's own screen (seat0).
type Session struct {
	ID      string
	User    string
	UID     int
	Type    string // wayland | x11
	Display string // X display (x11 sessions)
}

var (
	sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	userNameRe  = regexp.MustCompile(`^[a-z_][a-z0-9_.-]*[$]?$`)
	xDisplayRe  = regexp.MustCompile(`^:\d{1,4}(\.\d+)?$`)
)

// ParseLoginctlProps reads `loginctl show-session` KEY=VALUE output.
func ParseLoginctlProps(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			m[k] = v
		}
	}
	return m
}

// SessionFromProps turns a session's properties into a Session when it's a
// person's own graphical session (not the sign-in screen, not remote, not
// NoCapOS full screen, never root).
func SessionFromProps(id string, p map[string]string) (Session, bool) {
	s := Session{ID: id, User: p["Name"], Type: p["Type"], Display: p["Display"]}
	uid, err := strconv.Atoi(p["User"])
	if err != nil || uid <= 0 {
		return Session{}, false
	}
	s.UID = uid
	if p["Class"] != "user" || p["Remote"] == "yes" || (p["State"] != "" && p["State"] != "active" && p["State"] != "online") {
		return Session{}, false
	}
	if s.Type != "wayland" && s.Type != "x11" {
		return Session{}, false
	}
	if !userNameRe.MatchString(s.User) || len(s.User) > 32 || s.User == "nocap-desktop" || s.User == "gdm" || s.User == "Debian-gdm" {
		return Session{}, false
	}
	if s.Display != "" && !xDisplayRe.MatchString(s.Display) {
		s.Display = ""
	}
	return s, true
}

// ActiveSession is the person signed in on the screen right now, if any.
func (d *Desktop) ActiveSession(ctx context.Context) (Session, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := d.Run(ctx, "loginctl", "show-seat", "seat0", "-p", "ActiveSession", "--value")
	id := strings.TrimSpace(string(out))
	if err != nil || !sessionIDRe.MatchString(id) {
		return Session{}, false
	}
	out, err = d.Run(ctx, "loginctl", "show-session", id, "-p", "Name", "-p", "User", "-p", "Type", "-p", "Class", "-p", "Display", "-p", "Remote", "-p", "State")
	if err != nil {
		return Session{}, false
	}
	return SessionFromProps(id, ParseLoginctlProps(string(out)))
}

func (d *Desktop) runtimeDir(uid int) string {
	root := d.RuntimeRoot
	if root == "" {
		root = "/run/user"
	}
	return filepath.Join(root, strconv.Itoa(uid))
}

// waylandSocket finds the session's Wayland socket (wayland-0, ...).
func waylandSocket(dir string) string {
	m, _ := filepath.Glob(filepath.Join(dir, "wayland-*"))
	sort.Strings(m)
	for _, p := range m {
		name := filepath.Base(p)
		if _, err := strconv.Atoi(strings.TrimPrefix(name, "wayland-")); err == nil {
			return name
		}
	}
	return ""
}

// envMarker starts desktop.env when alfad wrote it (for the full desktop),
// so it never removes the kiosk's own file.
const envMarker = "# NoCap Desktop session (written by NoCapOS)"

// SessionEnvFile renders desktop.env for a full-desktop session.
func SessionEnvFile(s Session, runtimeDir, wayland string) string {
	var b strings.Builder
	b.WriteString(envMarker + "\n")
	fmt.Fprintf(&b, "USER=%s\n", s.User)
	if wayland != "" {
		fmt.Fprintf(&b, "WAYLAND_DISPLAY=%s\n", wayland)
	}
	fmt.Fprintf(&b, "XDG_RUNTIME_DIR=%s\n", runtimeDir)
	if s.Display != "" {
		fmt.Fprintf(&b, "DISPLAY=%s\n", s.Display)
	}
	return b.String()
}

// SyncSessionEnv keeps desktop.env pointing at whoever is signed in on the
// full desktop, so the App Store's Linux apps (and approved AI agents) open
// on this screen. The kiosk writes its own file; that one is left alone.
func (d *Desktop) SyncSessionEnv(ctx context.Context, envFile string) {
	if !d.Native || !d.conf().Full {
		return
	}
	cur, _ := d.readFile(envFile)
	ours := strings.HasPrefix(string(cur), envMarker)
	if len(cur) > 0 && !ours {
		return // the kiosk's
	}
	s, ok := d.ActiveSession(ctx)
	if !ok {
		if ours {
			_ = os.Remove(envFile)
		}
		return
	}
	dir := d.runtimeDir(s.UID)
	want := SessionEnvFile(s, dir, waylandSocket(dir))
	if string(cur) != want {
		_ = d.writeFile(envFile, []byte(want), 0o644)
	}
}

// WatchSession runs SyncSessionEnv every few seconds until ctx ends.
func (d *Desktop) WatchSession(ctx context.Context, envFile string) {
	t := time.NewTicker(4 * time.Second)
	defer t.Stop()
	for {
		d.SyncSessionEnv(ctx, envFile)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- automatic sign-in ----

// ParseAutologin reads GDM's [daemon] AutomaticLoginEnable/AutomaticLogin.
func ParseAutologin(conf string) (bool, string) {
	on, who, inDaemon := false, "", false
	for _, line := range strings.Split(conf, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "[") {
			inDaemon = s == "[daemon]"
			continue
		}
		if !inDaemon || strings.HasPrefix(s, "#") {
			continue
		}
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "AutomaticLoginEnable":
			on = strings.EqualFold(strings.TrimSpace(v), "true")
		case "AutomaticLogin":
			who = strings.TrimSpace(v)
		}
	}
	return on && who != "", who
}

// SetINIKey sets key=value in [section] of an ini file's text: an existing
// or commented-out key line is replaced, else the line is added at the end
// of the section; a missing section is added at the end.
func SetINIKey(conf, section, key, value string) string {
	lines := strings.Split(strings.TrimRight(conf, "\n"), "\n")
	if conf == "" {
		lines = nil
	}
	var out []string
	in, done, seen := false, false, false
	lastInSection := -1
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "[") {
			in = s == "["+section+"]"
			seen = seen || in
			out = append(out, line)
			if in {
				lastInSection = len(out) - 1
			}
			continue
		}
		if in {
			c := strings.TrimSpace(strings.TrimLeft(s, "#"))
			if k, _, ok := strings.Cut(c, "="); ok && strings.TrimSpace(k) == key {
				if !done {
					out = append(out, key+"="+value)
					done = true
					lastInSection = len(out) - 1
				}
				continue
			}
			if s != "" {
				lastInSection = len(out)
			}
		}
		out = append(out, line)
	}
	switch {
	case done:
	case seen:
		out = append(out[:lastInSection+1], append([]string{key + "=" + value}, out[lastInSection+1:]...)...)
	default:
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, "["+section+"]", key+"="+value)
	}
	return strings.Join(out, "\n") + "\n"
}

// ErrNotLinuxUser: automatic sign-in needs a person's own Linux account.
var ErrNotLinuxUser = errors.New("automatic sign-in needs your own Linux account on this server (NoCapOS accounts in system mode are)")

// LookupUID finds a Linux account's UID.
var LookupUID = func(name string) (int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}

// SetAutologin turns the sign-in screen's automatic sign-in on (for
// username) or off. It takes effect the next time the sign-in screen starts.
func (d *Desktop) SetAutologin(username string, on bool) error {
	c := d.conf()
	if !d.Native || !c.Full || c.GDMConf == "" {
		return ErrDesktopUnavailable
	}
	if on {
		uid, err := LookupUID(username)
		if err != nil || uid < 1000 || !userNameRe.MatchString(username) || len(username) > 32 {
			return ErrNotLinuxUser
		}
	}
	b, err := d.readFile(c.GDMConf)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	conf := string(b)
	if on {
		conf = SetINIKey(conf, "daemon", "AutomaticLoginEnable", "true")
		conf = SetINIKey(conf, "daemon", "AutomaticLogin", username)
	} else {
		conf = SetINIKey(conf, "daemon", "AutomaticLoginEnable", "false")
	}
	return d.writeFile(c.GDMConf, []byte(conf), 0o644)
}
