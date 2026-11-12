package hostaccess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeHost is a machine for Desktop: files in a map, systemctl/loginctl
// answers from tables, every command recorded with its stdin.
type fakeHost struct {
	files   map[string]string
	enabled map[string]bool
	active  map[string]bool
	tools   map[string]bool
	session string // loginctl show-session output ("" = nobody)
	failRun map[string]bool
	calls   []string
	stdins  []string
	open    map[int]bool
}

func newFakeHost() *fakeHost {
	return &fakeHost{files: map[string]string{}, enabled: map[string]bool{}, active: map[string]bool{}, tools: map[string]bool{}, failRun: map[string]bool{}, open: map[int]bool{}}
}

func (f *fakeHost) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	for k := range f.failRun {
		if strings.Contains(line, k) {
			return []byte("failed: " + k), errors.New("exit 1")
		}
	}
	switch {
	case name == "systemctl" && args[0] == "is-enabled":
		if f.enabled[args[1]] {
			return []byte("enabled\n"), nil
		}
		return []byte("disabled\n"), errors.New("exit 1")
	case name == "systemctl" && args[0] == "is-active":
		if f.active[args[1]] {
			return []byte("active\n"), nil
		}
		return []byte("inactive\n"), errors.New("exit 3")
	case name == "loginctl" && args[0] == "show-seat":
		if f.session == "" {
			return []byte("\n"), nil
		}
		if strings.Contains(f.session, "Class=greeter") {
			return []byte("c1\n"), nil
		}
		return []byte("7\n"), nil
	case name == "loginctl" && args[0] == "show-session":
		return []byte(f.session), nil
	case name == "systemctl" && args[0] == "stop" && args[1] == ScreenUnit:
		f.open[ScreenRDPPort], f.open[ScreenVNCPort] = false, false
	case name == "systemd-run" && strings.Contains(line, "--unit=nocapos-screen "):
		if strings.Contains(line, " x11vnc ") {
			f.open[ScreenVNCPort] = true
		} else {
			f.open[ScreenRDPPort] = true
		}
	}
	return nil, nil
}

func (f *fakeHost) runInput(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	f.stdins = append(f.stdins, string(stdin))
	return f.run(ctx, name, args...)
}

func (f *fakeHost) desktop() *Desktop {
	return &Desktop{
		Native: true, DataDir: "/var/lib/nocapos", DRMDir: "/nonexistent", RuntimeRoot: "/run/user",
		Run: f.run, RunInput: f.runInput,
		Exists: func(p string) bool { _, ok := f.files[p]; return ok },
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := f.files[p]; ok {
				return []byte(s), nil
			}
			return nil, os.ErrNotExist
		},
		WriteFile: func(p string, b []byte, _ os.FileMode) error { f.files[p] = string(b); return nil },
		LookPath:  func(n string) bool { return f.tools[n] },
		LookupAccount: func(n string) (Account, error) {
			return Account{UID: 1000, GID: 1000, Home: "/home/" + n}, nil
		},
		Chown: func(string, int, int) error { return nil },
		Dial: func(addr string, _ time.Duration) error {
			for p, ok := range f.open {
				if ok && strings.HasSuffix(addr, ":"+strconv.Itoa(p)) {
					return nil
				}
			}
			return errors.New("refused")
		},
	}
}

const fullConf = "# NoCap Desktop\nFULL=1\nDM_UNIT=gdm3.service\nGDM_CONF=/etc/gdm3/daemon.conf\n"

func TestParseDesktopConf(t *testing.T) {
	c := ParseDesktopConf(fullConf)
	if !c.Full || c.DMUnit != "gdm3.service" || c.GDMConf != "/etc/gdm3/daemon.conf" {
		t.Fatalf("conf = %+v", c)
	}
	// Anything that doesn't look like a unit or a file under /etc is dropped.
	c = ParseDesktopConf("FULL=1\nDM_UNIT=gdm; rm -rf /\nGDM_CONF=/tmp/../etc/shadow\n")
	if c.DMUnit != "gdm.service" || c.GDMConf != "" {
		t.Fatalf("unsafe values kept: %+v", c)
	}
	if ParseDesktopConf("").Full {
		t.Fatal("empty conf is not full")
	}
}

func TestModeCommands(t *testing.T) {
	c := ParseDesktopConf(fullConf)
	join := func(cmds [][]string) string {
		var s []string
		for _, c := range cmds {
			s = append(s, strings.Join(c, " "))
		}
		return strings.Join(s, " | ")
	}
	for _, tc := range []struct {
		mode string
		conf DesktopConf
		want string
	}{
		{ModeFull, c, "set-default graphical.target | enable --force gdm3.service | restart gdm3.service"},
		{ModeOff, c, "disable --now gdm3.service | set-default multi-user.target | --no-block start getty@tty1.service"},
		// There is no web full screen mode any more.
		{"kiosk", c, ""},
	} {
		if got := join(ModeCommands(tc.mode, tc.conf)); got != tc.want {
			t.Errorf("%s: got  %s\nwant %s", tc.mode, got, tc.want)
		}
	}
}

func TestDesktopStateAndSetMode(t *testing.T) {
	f := newFakeHost()
	d := f.desktop()
	if st := d.State(context.Background()); st.Available || st.Reason == "" {
		t.Fatalf("nothing set up: %+v", st)
	}
	if err := d.SetMode(context.Background(), ModeFull); !errors.Is(err, ErrDesktopUnavailable) {
		t.Fatalf("SetMode without a desktop = %v", err)
	}

	f.files[DesktopConfPath] = fullConf
	f.files["/etc/gdm3/daemon.conf"] = "[daemon]\nAutomaticLoginEnable=true\nAutomaticLogin=sam\n"
	f.enabled["gdm3.service"], f.active["gdm3.service"] = true, true
	f.session = "Name=sam\nUser=1000\nType=wayland\nClass=user\nRemote=no\nState=active\n"
	st := d.State(context.Background())
	if !st.Available || st.Mode != ModeFull || !st.Active || !st.Enabled || st.SessionUser != "sam" {
		t.Fatalf("state = %+v", st)
	}
	if strings.Join(st.Modes, ",") != "full,off" {
		t.Fatalf("modes = %v", st.Modes)
	}
	if !st.AutologinSupported || !st.Autologin || st.AutologinUser != "sam" {
		t.Fatalf("autologin = %+v", st)
	}

	f.calls = nil
	if err := d.SetMode(context.Background(), ModeOff); err != nil {
		t.Fatal(err)
	}
	want := "systemctl disable --now gdm3.service|systemctl set-default multi-user.target|systemctl --no-block start getty@tty1.service"
	if got := strings.Join(f.calls, "|"); got != want {
		t.Fatalf("calls = %s", got)
	}
	if f.files[DesktopModePath] != "off\n" {
		t.Fatalf("mode file = %q", f.files[DesktopModePath])
	}
	for _, bad := range []string{"party", "kiosk"} {
		if err := d.SetMode(context.Background(), bad); !errors.Is(err, ErrBadMode) {
			t.Fatalf("%s mode = %v", bad, err)
		}
	}

	// The on/off switch: on is the native desktop.
	f.calls = nil
	if err := d.Set(context.Background(), true); err != nil || !strings.Contains(strings.Join(f.calls, "|"), "enable --force gdm3.service") {
		t.Fatalf("Set(true) = %v, %v", err, f.calls)
	}

	// A failing step stops the switch and says which.
	f.failRun["enable --force"] = true
	if err := d.SetMode(context.Background(), ModeFull); err == nil || !strings.Contains(err.Error(), "enable --force gdm3.service") {
		t.Fatalf("failure = %v", err)
	}

	d.Native = false
	if st := d.State(context.Background()); st.Available {
		t.Fatal("not native: unavailable")
	}
}

func TestSessionFromProps(t *testing.T) {
	ok := map[string]string{"Name": "sam", "User": "1000", "Type": "wayland", "Class": "user", "Remote": "no", "State": "active"}
	if s, good := SessionFromProps("3", ok); !good || s.User != "sam" || s.UID != 1000 {
		t.Fatalf("session = %+v %v", s, good)
	}
	for name, change := range map[string][2]string{
		"greeter": {"Class", "greeter"},
		"tty":     {"Type", "tty"},
		"remote":  {"Remote", "yes"},
		"root":    {"User", "0"},
		"kiosk":   {"Name", "nocap-desktop"},
		"bad":     {"Name", "../x"},
		"closing": {"State", "closing"},
	} {
		p := map[string]string{}
		for k, v := range ok {
			p[k] = v
		}
		p[change[0]] = change[1]
		if _, good := SessionFromProps("3", p); good {
			t.Errorf("%s session accepted", name)
		}
	}
}

func TestSyncSessionEnv(t *testing.T) {
	dir := t.TempDir()
	rt := filepath.Join(dir, "1000")
	_ = os.MkdirAll(rt, 0o755)
	_ = os.WriteFile(filepath.Join(rt, "wayland-0"), nil, 0o600)
	_ = os.WriteFile(filepath.Join(rt, "wayland-0.lock"), nil, 0o600)
	f := newFakeHost()
	d := f.desktop()
	d.RuntimeRoot = dir
	f.files[DesktopConfPath] = fullConf
	f.session = "Name=sam\nUser=1000\nType=wayland\nClass=user\nRemote=no\nState=active\n"
	env := "/run/nocapos/desktop.env"
	d.SyncSessionEnv(context.Background(), env)
	got := f.files[env]
	if !strings.HasPrefix(got, envMarker) || !strings.Contains(got, "USER=sam\n") || !strings.Contains(got, "WAYLAND_DISPLAY=wayland-0\n") ||
		!strings.Contains(got, "XDG_RUNTIME_DIR="+rt+"\n") {
		t.Fatalf("env = %q", got)
	}
	// A file alfad didn't write is never touched.
	f.files[env] = "USER=nocap-desktop\nWAYLAND_DISPLAY=wayland-0\n"
	d.SyncSessionEnv(context.Background(), env)
	if f.files[env] != "USER=nocap-desktop\nWAYLAND_DISPLAY=wayland-0\n" {
		t.Fatal("someone else's file overwritten")
	}
}

func TestSetINIKeyAndAutologin(t *testing.T) {
	debian := "# GDM configuration storage\n[daemon]\n# Uncomment the line below to force the login screen to use Xorg\n#WaylandEnable=false\n\n# Enabling automatic login\n#  AutomaticLoginEnable = true\n#  AutomaticLogin = user1\n\n[security]\n\n[xdmcp]\n"
	on := SetINIKey(SetINIKey(debian, "daemon", "AutomaticLoginEnable", "true"), "daemon", "AutomaticLogin", "sam")
	if a, who := ParseAutologin(on); !a || who != "sam" {
		t.Fatalf("on:\n%s", on)
	}
	if !strings.Contains(on, "[security]") || strings.Count(on, "AutomaticLogin=") != 1 {
		t.Fatalf("on:\n%s", on)
	}
	off := SetINIKey(on, "daemon", "AutomaticLoginEnable", "false")
	if a, _ := ParseAutologin(off); a {
		t.Fatalf("off:\n%s", off)
	}
	// No [daemon] yet (Fedora's custom.conf can be empty).
	if got := SetINIKey("", "daemon", "AutomaticLoginEnable", "false"); got != "[daemon]\nAutomaticLoginEnable=false\n" {
		t.Fatalf("empty: %q", got)
	}
	// Keys in another section don't count.
	if a, _ := ParseAutologin("[security]\nAutomaticLoginEnable=true\nAutomaticLogin=x\n"); a {
		t.Fatal("wrong section counted")
	}

	f := newFakeHost()
	d := f.desktop()
	f.files[DesktopConfPath] = fullConf
	f.files["/etc/gdm3/daemon.conf"] = debian
	old := LookupUID
	defer func() { LookupUID = old }()
	LookupUID = func(n string) (int, error) {
		if n == "sam" {
			return 1000, nil
		}
		return 0, errors.New("unknown user")
	}
	if err := d.SetAutologin("sam", true); err != nil {
		t.Fatal(err)
	}
	if a, who := ParseAutologin(f.files["/etc/gdm3/daemon.conf"]); !a || who != "sam" {
		t.Fatal("autologin not written")
	}
	if err := d.SetAutologin("admin", true); !errors.Is(err, ErrNotLinuxUser) {
		t.Fatalf("NoCapOS-only account = %v", err)
	}
	if err := d.SetAutologin("sam", false); err != nil {
		t.Fatal(err)
	}
	if a, _ := ParseAutologin(f.files["/etc/gdm3/daemon.conf"]); a {
		t.Fatal("autologin still on")
	}
}

const (
	samSession     = "Name=sam\nUser=1000\nType=wayland\nClass=user\nRemote=no\nState=active\n"
	greeterSession = "Name=Debian-gdm\nUser=116\nType=wayland\nClass=greeter\nRemote=no\nState=active\n"
)

func fullHost() (*fakeHost, *Desktop) {
	f := newFakeHost()
	d := f.desktop()
	f.files[DesktopConfPath] = fullConf
	f.enabled["gdm3.service"], f.active["gdm3.service"] = true, true
	f.files["/usr/libexec/gnome-remote-desktop-daemon"] = ""
	f.tools["x11vnc"] = true
	d.SessionWait = time.Millisecond
	d.ProcRoot = "/nonexistent"
	return f, d
}

func TestScreenSessionFromProps(t *testing.T) {
	g := ParseLoginctlProps(greeterSession)
	s, ok := ScreenSessionFromProps("c1", g)
	if !ok || !s.Greeter || s.User != "Debian-gdm" || s.UID != 116 {
		t.Fatalf("greeter = %+v %v", s, ok)
	}
	if _, ok := SessionFromProps("c1", g); ok {
		t.Fatal("the sign-in screen isn't a person's session")
	}
	for name, change := range map[string][2]string{"root": {"User", "0"}, "tty": {"Type", "tty"}, "remote": {"Remote", "yes"}, "bad": {"Name", "../x"}} {
		p := ParseLoginctlProps(greeterSession)
		p[change[0]] = change[1]
		if _, ok := ScreenSessionFromProps("c1", p); ok {
			t.Errorf("%s greeter accepted", name)
		}
	}
	if s, ok := ScreenSessionFromProps("3", ParseLoginctlProps(samSession)); !ok || s.Greeter || s.User != "sam" {
		t.Fatalf("user = %+v", s)
	}
}

func TestFindSessionEnv(t *testing.T) {
	root := t.TempDir()
	proc := func(pid, comm string, uid int, env ...string) {
		dir := filepath.Join(root, pid)
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "status"), []byte("Name:\t"+comm+"\nUid:\t"+strconv.Itoa(uid)+"\t"+strconv.Itoa(uid)+"\t0\t0\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "environ"), []byte(strings.Join(env, "\x00")+"\x00"), 0o644)
	}
	proc("10", "bash", 1000, "XDG_SESSION_ID=3")
	proc("11", "gnome-shell", 1001, "XDG_SESSION_ID=3")
	proc("12", "gnome-shell", 1000, "XDG_SESSION_ID=2", "DISPLAY=:1")
	proc("13", "gnome-shell", 1000, "XDG_SESSION_ID=3", "DISPLAY=:0", "XAUTHORITY=/run/user/1000/gdm/Xauthority")
	_ = os.MkdirAll(filepath.Join(root, "self"), 0o755)
	env := FindSessionEnv(root, 1000, "3")
	if env["DISPLAY"] != ":0" || env["XAUTHORITY"] != "/run/user/1000/gdm/Xauthority" {
		t.Fatalf("env = %v", env)
	}
	if env := FindSessionEnv(root, 1000, "9"); env["DISPLAY"] == "" {
		t.Fatal("any shell of the account when no session matches")
	}
	if env := FindSessionEnv(root, 1002, "3"); env != nil {
		t.Fatalf("other account: %v", env)
	}
}

// The browser window always shows the real screen: the sign-in screen when
// nobody is signed in, the person's session after. Passwords only ever
// reach programs in files, never in their arguments (which anyone on the
// machine can read in ps).
func TestRemoteViewFollowsTheScreen(t *testing.T) {
	f, d := fullHost()
	noArgv := func(pw string) {
		t.Helper()
		for _, c := range f.calls {
			if strings.Contains(c, pw) {
				t.Fatalf("password in argv: %s", c)
			}
		}
	}

	// Nobody signed in: the sign-in screen, as GDM's account.
	f.session = greeterSession
	tgt, err := d.RemoteView(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Protocol != "rdp" || tgt.Port != ScreenRDPPort || !tgt.SignIn || tgt.Username != "nocap" || len(tgt.Password) != 32 {
		t.Fatalf("target = %+v", tgt)
	}
	noArgv(tgt.Password)
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{
		"systemd-run --unit=nocapos-screen --collect --quiet --property=KillMode=mixed --uid=Debian-gdm --setenv=XDG_RUNTIME_DIR=/run/user/116 --setenv=DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/116/bus",
		"--setenv=GSETTINGS_BACKEND=keyfile",
		"/usr/libexec/gnome-remote-desktop-daemon --headless",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	creds := f.files["/run/nocapos/screen/116/data/gnome-remote-desktop/credentials.ini"]
	if !strings.Contains(creds, "'password': <'"+tgt.Password+"'>") {
		t.Fatalf("credentials = %q", creds)
	}
	kf := f.files["/run/nocapos/screen/116/config/glib-2.0/settings/keyfile"]
	for _, want := range []string{"[org/gnome/desktop/remote-desktop/rdp]", "port=3391", "view-only=false", "screen-share-mode='mirror-primary'", "tls-key='/run/nocapos/screen/116/tls.key'"} {
		if !strings.Contains(kf, want) {
			t.Errorf("settings missing %q:\n%s", want, kf)
		}
	}
	if !strings.Contains(f.files["/run/nocapos/screen/116/tls.key"], "PRIVATE KEY") {
		t.Fatal("no TLS key")
	}

	// Still the sign-in screen: the same server.
	f.calls = nil
	if again, err := d.RemoteView(context.Background()); err != nil || again.Password != tgt.Password {
		t.Fatalf("again = %+v, %v", again, err)
	}
	if strings.Contains(strings.Join(f.calls, "\n"), "systemd-run") {
		t.Fatal("restarted for the same session")
	}

	// Someone signs in (on the screen or through the window): the old
	// server stops, so the window reconnects to the new session.
	f.session = samSession
	f.calls = nil
	d.FollowScreen(context.Background())
	if !strings.Contains(strings.Join(f.calls, "\n"), "systemctl stop nocapos-screen.service") {
		t.Fatalf("not stopped: %v", f.calls)
	}
	tgt2, err := d.RemoteView(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tgt2.SignIn || tgt2.Password == tgt.Password {
		t.Fatalf("new session target = %+v", tgt2)
	}
	noArgv(tgt2.Password)
	if !strings.Contains(strings.Join(f.calls, "\n"), "--uid=sam") {
		t.Fatalf("calls = %v", f.calls)
	}
	// Following a session that didn't change does nothing.
	f.calls = nil
	d.FollowScreen(context.Background())
	if strings.Contains(strings.Join(f.calls, "\n"), "stop") {
		t.Fatalf("stopped the current session's server: %v", f.calls)
	}

	// An Xorg session: x11vnc on its display, password in a file.
	f.session = "Name=sam\nUser=1000\nType=x11\nClass=user\nRemote=no\nState=active\nDisplay=:1\n"
	f.calls = nil
	tgt3, err := d.RemoteView(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tgt3.Protocol != "vnc" || tgt3.Port != ScreenVNCPort || len(tgt3.Password) != 8 {
		t.Fatalf("x11 target = %+v", tgt3)
	}
	noArgv(tgt3.Password)
	if !strings.Contains(strings.Join(f.calls, "\n"), "x11vnc -display :1 -auth /run/user/1000/gdm/Xauthority -localhost -rfbport 5906") {
		t.Fatalf("calls = %v", f.calls)
	}
	if f.files["/run/nocapos/screen/vnc.pass"] != tgt3.Password+"\n" {
		t.Fatal("vnc password file")
	}

	// Nobody on the screen for a moment (between sign-in and desktop).
	f.session = ""
	d.FollowScreen(context.Background())
	if _, err := d.RemoteView(context.Background()); !errors.Is(err, ErrScreenStarting) {
		t.Fatalf("no session = %v", err)
	}

	// Switching to another mode stops the screen server.
	f.session = samSession
	if _, err := d.RemoteView(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := d.SetMode(context.Background(), ModeOff); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "systemctl stop nocapos-screen.service") {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestStateShowsSignInScreen(t *testing.T) {
	f, d := fullHost()
	f.session = greeterSession
	st := d.State(context.Background())
	if !st.SignInScreen || st.SessionUser != "" || !st.Remote {
		t.Fatalf("state = %+v", st)
	}
}

// An old web full screen unit left on a machine (before the update that
// removes it) is no mode at all: the screen view needs the native desktop.
func TestKioskIsNoMode(t *testing.T) {
	f := newFakeHost()
	d := f.desktop()
	f.files["/etc/systemd/system/nocapos-desktop.service"] = "[Unit]"
	f.enabled["nocapos-desktop.service"], f.active["nocapos-desktop.service"] = true, true
	if st := d.State(context.Background()); st.Available || st.Mode != ModeOff || len(st.Modes) != 0 {
		t.Fatalf("state = %+v", st)
	}
	if _, err := d.RemoteView(context.Background()); !errors.Is(err, ErrDesktopUnavailable) {
		t.Fatalf("view = %v", err)
	}
}
