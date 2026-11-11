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
		return []byte("7\n"), nil
	case name == "loginctl" && args[0] == "show-session":
		return []byte(f.session), nil
	case strings.Contains(line, "systemctl --user restart gnome-remote-desktop"):
		f.open[SharePort] = true
	case name == "systemctl" && len(args) > 2 && args[2] == "gnome-remote-desktop.service":
		f.open[RemoteLoginPort] = true
	case name == "systemd-run":
		f.open[VNCPort] = true
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
		mode  string
		conf  DesktopConf
		kiosk bool
		want  string
	}{
		{ModeFull, c, true, "disable --now nocapos-desktop.service | set-default graphical.target | enable --force gdm3.service | restart gdm3.service"},
		{ModeKiosk, c, true, "disable --now gdm3.service | set-default multi-user.target | enable --now nocapos-desktop.service"},
		{ModeOff, c, true, "disable --now gdm3.service | set-default multi-user.target | disable --now nocapos-desktop.service | --no-block start getty@tty1.service"},
		// A kiosk-only machine (no full desktop): exactly the older switches.
		{ModeKiosk, DesktopConf{}, true, "enable --now nocapos-desktop.service"},
		{ModeOff, DesktopConf{}, true, "disable --now nocapos-desktop.service | --no-block start getty@tty1.service"},
	} {
		if got := join(ModeCommands(tc.mode, tc.conf, tc.kiosk)); got != tc.want {
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
	f.files[DesktopUnitPath] = "[Unit]"
	f.files["/etc/gdm3/daemon.conf"] = "[daemon]\nAutomaticLoginEnable=true\nAutomaticLogin=sam\n"
	f.enabled["gdm3.service"], f.active["gdm3.service"] = true, true
	f.session = "Name=sam\nUser=1000\nType=wayland\nClass=user\nRemote=no\nState=active\n"
	st := d.State(context.Background())
	if !st.Available || st.Mode != ModeFull || !st.Active || !st.Enabled || st.SessionUser != "sam" {
		t.Fatalf("state = %+v", st)
	}
	if strings.Join(st.Modes, ",") != "full,kiosk,off" {
		t.Fatalf("modes = %v", st.Modes)
	}
	if !st.AutologinSupported || !st.Autologin || st.AutologinUser != "sam" {
		t.Fatalf("autologin = %+v", st)
	}

	f.calls = nil
	if err := d.SetMode(context.Background(), ModeKiosk); err != nil {
		t.Fatal(err)
	}
	want := "systemctl disable --now gdm3.service|systemctl set-default multi-user.target|systemctl enable --now nocapos-desktop.service"
	if got := strings.Join(f.calls, "|"); got != want {
		t.Fatalf("calls = %s", got)
	}
	if f.files[DesktopModePath] != "kiosk\n" {
		t.Fatalf("mode file = %q", f.files[DesktopModePath])
	}
	if err := d.SetMode(context.Background(), "party"); !errors.Is(err, ErrBadMode) {
		t.Fatalf("bad mode = %v", err)
	}

	// The older on/off switch: on means the full desktop when there is one.
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
	// The kiosk's own file is never touched.
	f.files[env] = "USER=nocap-desktop\nWAYLAND_DISPLAY=wayland-0\n"
	d.SyncSessionEnv(context.Background(), env)
	if f.files[env] != "USER=nocap-desktop\nWAYLAND_DISPLAY=wayland-0\n" {
		t.Fatal("kiosk file overwritten")
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

// The remote screen's password reaches programs on stdin or in files only,
// never in their arguments (which anyone on the machine can read in ps).
func TestRemoteViewKeepsPasswordOutOfArgv(t *testing.T) {
	check := func(f *fakeHost, pw string) {
		t.Helper()
		for _, c := range f.calls {
			if strings.Contains(c, pw) {
				t.Fatalf("password in argv: %s", c)
			}
		}
	}

	// Somebody signed in: their session is shared.
	f := newFakeHost()
	d := f.desktop()
	f.files[DesktopConfPath] = fullConf
	f.enabled["gdm3.service"], f.active["gdm3.service"] = true, true
	f.tools["grdctl"] = true
	f.session = "Name=sam\nUser=1000\nType=wayland\nClass=user\nRemote=no\nState=active\n"
	tgt, err := d.RemoteView(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Protocol != "rdp" || tgt.Port != SharePort || tgt.Username != "nocap" || len(tgt.Password) != 32 {
		t.Fatalf("target = %+v", tgt)
	}
	check(f, tgt.Password)
	if len(f.stdins) == 0 || !strings.Contains(f.stdins[0], tgt.Password) {
		t.Fatalf("password not given on stdin: %q", f.stdins)
	}
	if !strings.Contains(f.files["/var/lib/nocapos/desktop-remote/credentials.json"], tgt.Password) {
		t.Fatal("credentials not kept")
	}
	if !strings.Contains(f.files["/home/sam/.local/share/nocapos-remote/nocapos-tls.key"], "PRIVATE KEY") {
		t.Fatal("TLS key not placed for the session")
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"runuser -u sam -- env XDG_RUNTIME_DIR=/run/user/1000", "grdctl rdp enable", "grdctl rdp disable-view-only", "grdctl rdp set-credentials"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}

	// Older GNOME: grdctl won't read stdin, so the keyring gets it (stdin too).
	remoteMu.shared = nil
	f2 := newFakeHost()
	d2 := f2.desktop()
	f2.files = f.files
	f2.enabled, f2.active, f2.tools, f2.session = f.enabled, f.active, f.tools, f.session
	f2.failRun["grdctl rdp set-credentials"] = true
	tgt2, err := d2.RemoteView(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tgt2.Password != tgt.Password {
		t.Fatal("credentials should be kept between views")
	}
	check(f2, tgt2.Password)
	if len(f2.stdins) < 2 || !strings.Contains(f2.stdins[1], "'password': <'"+tgt.Password+"'>") {
		t.Fatalf("keyring fallback stdin = %q", f2.stdins)
	}

	// Nobody signed in, GNOME 46+: remote sign-in through the system daemon.
	remoteMu.shared = nil
	f3 := newFakeHost()
	d3 := f3.desktop()
	f3.files = f.files
	f3.files["/usr/lib/systemd/system/gnome-remote-desktop.service"] = "[Unit]"
	f3.enabled, f3.active, f3.tools = f.enabled, f.active, f.tools
	tgt3, err := d3.RemoteView(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tgt3.Port != RemoteLoginPort {
		t.Fatalf("remote sign-in port = %d", tgt3.Port)
	}
	check(f3, tgt3.Password)

	// Nobody signed in and no remote sign-in: say so.
	delete(f3.files, "/usr/lib/systemd/system/gnome-remote-desktop.service")
	f3.open = map[int]bool{}
	if _, err := d3.RemoteView(context.Background()); !errors.Is(err, ErrSignInFirst) {
		t.Fatalf("no session = %v", err)
	}
}

func TestRemoteViewKiosk(t *testing.T) {
	f := newFakeHost()
	d := f.desktop()
	f.files[DesktopUnitPath] = "[Unit]"
	f.enabled[DesktopUnit], f.active[DesktopUnit] = true, true
	f.files[DesktopEnvFile] = "USER=nocap-desktop\nWAYLAND_DISPLAY=wayland-0\nXDG_RUNTIME_DIR=/run/nocap-desktop\n"
	f.tools["wayvnc"] = true
	tgt, err := d.RemoteView(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Protocol != "vnc" || tgt.Port != VNCPort {
		t.Fatalf("target = %+v", tgt)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "systemd-run --unit=nocapos-desktop-vnc --collect --quiet --uid=nocap-desktop --setenv=XDG_RUNTIME_DIR=/run/nocap-desktop --setenv=WAYLAND_DISPLAY=wayland-0 wayvnc 127.0.0.1 5905") {
		t.Fatalf("calls = %v", f.calls)
	}
	f.enabled[DesktopUnit] = false
	if _, err := d.RemoteView(context.Background()); !errors.Is(err, ErrRemoteOff) {
		t.Fatalf("off = %v", err)
	}
}
