package webapps

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func testSession(spec StreamSpec) *streamSession {
	return &streamSession{id: "0123456789abcdef01234567", spec: spec, slot: slotAt(3)}
}

func alex() *RunAs {
	return &RunAs{Username: "alex", UID: 1000, GID: 1000, Home: "/home/alex"}
}

func TestStreamPlanRunsAsThePersonOutsideTheService(t *testing.T) {
	s := testSession(StreamSpec{Key: "flatpak:org.videolan.VLC", Title: "VLC", RunAs: alex(),
		Command: []string{"flatpak", "run", "org.videolan.VLC"}})
	prep, unit, err := streamPlan(s, "/var/lib/nocapos/streams/x.log", "en_US.UTF-8")
	if err != nil {
		t.Fatal(err)
	}
	args := SystemdRunArgs(unit)
	for _, want := range []string{
		"--unit=nocap-app-alex-0123456789abcdef01234567", "--collect", "--wait",
		"--uid=alex", "--gid=1000",
		"--property=WorkingDirectory=-/home/alex",
		"--property=KillMode=control-group",
		"--property=StandardOutput=append:/var/lib/nocapos/streams/x.log",
		"--setenv=HOME=/home/alex", "--setenv=USER=alex",
		"--setenv=XDG_RUNTIME_DIR=/run/user/1000",
		"--setenv=XDG_DATA_DIRS=" + streamDataDirs,
		"--setenv=NOCAP_DISPLAY=:123", "--setenv=NOCAP_VNC_PORT=6203", "--setenv=NOCAP_WEB_PORT=6303",
		"--setenv=NOCAP_MODE=app", "--setenv=LANG=en_US.UTF-8",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %q in %q", want, args)
		}
	}
	// None of alfad's sandbox settings are carried over.
	for _, a := range args {
		if strings.Contains(a, "PrivateTmp") || strings.Contains(a, "Protect") || strings.Contains(a, "--pipe") {
			t.Errorf("unexpected %q", a)
		}
	}
	sep := slices.Index(args, "--")
	if sep < 0 || !slices.Equal(args[sep+1:], []string{"/bin/bash", StreamScriptPath, "app", "flatpak", "run", "org.videolan.VLC"}) {
		t.Fatalf("command = %q", args[sep+1:])
	}

	// The root setup: X socket folder, display lock, runtime folder.
	if prep == nil || prep.User != "" || !prep.Pipe {
		t.Fatalf("prep = %+v", prep)
	}
	pa := SystemdRunArgs(*prep)
	if !slices.Contains(pa, "--unit=nocap-prep-0123456789abcdef01234567") || slices.ContainsFunc(pa, func(a string) bool { return strings.HasPrefix(a, "--uid=") }) {
		t.Fatalf("prep args = %q", pa)
	}
	sep = slices.Index(pa, "--")
	tailArgs := pa[sep+1:]
	if tailArgs[0] != "/bin/sh" || !slices.Equal(tailArgs[3:], []string{"nocap-prep", "123", "1000", "1000", "/run/user/1000", ""}) {
		t.Fatalf("prep command = %q", tailArgs)
	}
	script := tailArgs[2]
	for _, want := range []string{"chown 0:0 \"$$d\"", "chmod 1777 \"$$d\"", "d=/tmp/.X11-unix", "mkdir -m 0700", "is a link; refusing"} {
		if !strings.Contains(script, want) {
			t.Errorf("prep script lacks %q", want)
		}
	}
}

func TestStreamPlanSharedAccountAndModes(t *testing.T) {
	shared := &RunAs{Username: "nocap-apps", UID: 990, GID: 990, Home: "/var/lib/nocap-apps/homes/u1",
		Runtime: AppsRuntimeBase + "/u1", OwnHome: true}
	s := testSession(StreamSpec{Key: "desktop", Title: "Linux Desktop", Mode: ModeDesktop, RunAs: shared})
	prep, unit, err := streamPlan(s, "/l", "C.UTF-8")
	if err != nil {
		t.Fatal(err)
	}
	pa := SystemdRunArgs(*prep)
	if !slices.Equal(pa[len(pa)-5:], []string{"123", "990", "990", AppsRuntimeBase + "/u1", "/var/lib/nocap-apps/homes/u1"}) {
		t.Fatalf("prep tail = %q", pa[len(pa)-5:])
	}
	args := SystemdRunArgs(unit)
	if !slices.Contains(args, "--uid=nocap-apps") || !slices.Contains(args, "--setenv=XDG_RUNTIME_DIR="+AppsRuntimeBase+"/u1") || args[len(args)-1] != "desktop" {
		t.Fatalf("args = %q", args)
	}

	// The server's screen: no setup unit, the screen's own environment.
	screen := &RunAs{Username: "nocap-desktop", UID: 991, GID: 991, Home: "/var/lib/nocap-desktop", Runtime: "/run/user/991"}
	s = testSession(StreamSpec{Key: "screen", Mode: ModeScreen, RunAs: screen, Env: []string{"WAYLAND_DISPLAY=wayland-0"}})
	prep, unit, err = streamPlan(s, "/l", "C.UTF-8")
	if err != nil || prep != nil {
		t.Fatalf("screen prep = %+v %v", prep, err)
	}
	args = SystemdRunArgs(unit)
	if !slices.Contains(args, "--setenv=WAYLAND_DISPLAY=wayland-0") || slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "--setenv=NOCAP_DISPLAY") }) {
		t.Fatalf("screen args = %q", args)
	}
}

func TestStreamPlanNeverRoot(t *testing.T) {
	for _, a := range []*RunAs{
		nil,
		{Username: "root", UID: 0, GID: 0, Home: "/root"},
		{Username: "toor", UID: 0, GID: 0, Home: "/root"},
		{Username: "root", UID: 5, Home: "/x"},
		{Username: "alex", UID: 1000, Home: ""},
	} {
		s := testSession(StreamSpec{Key: "k", Command: []string{"app"}, RunAs: a})
		if _, _, err := streamPlan(s, "/l", "C"); !errors.Is(err, ErrNoAccount) {
			t.Errorf("%+v: err = %v", a, err)
		}
	}
	s := testSession(StreamSpec{Key: "k", RunAs: alex()})
	if _, _, err := streamPlan(s, "/l", "C"); err == nil {
		t.Error("an app stream with nothing to run should fail")
	}
}

func TestSystemdLiteralAndUnitNames(t *testing.T) {
	u := UnitSpec{Unit: "u", Argv: []string{"sh", "-c", `echo "$HOME" ${X}`}}
	args := SystemdRunArgs(u)
	if args[len(args)-1] != `echo "$$HOME" $${X}` {
		t.Fatalf("not escaped: %q", args[len(args)-1])
	}
	if got := UnitName(AppUnitPrefix, "Jo Doe/../x", "ab12"); got != "nocap-app-Jo_Doe____x-ab12" {
		t.Fatalf("unit = %q", got)
	}
	if got := UnitName(AppUnitPrefix, "", "id"); got != "nocap-app-x-id" {
		t.Fatalf("unit = %q", got)
	}
}

func TestCheckUserNamespaces(t *testing.T) {
	files := func(m map[string]string) func(string) ([]byte, error) {
		return func(p string) ([]byte, error) {
			if v, ok := m[p]; ok {
				return []byte(v), nil
			}
			return nil, os.ErrNotExist
		}
	}
	if c := CheckUserNamespaces(files(map[string]string{usernsClonePath: "1\n", usernsMaxPath: "63000\n"}), false); !c.OK {
		t.Fatalf("enabled: %+v", c)
	}
	c := CheckUserNamespaces(files(map[string]string{usernsClonePath: "0\n"}), false)
	if c.OK || !strings.Contains(c.Problem, "unprivileged_userns_clone") || !strings.Contains(c.Fix, "kernel.unprivileged_userns_clone=1") {
		t.Fatalf("clone off: %+v", c)
	}
	if c := CheckUserNamespaces(files(map[string]string{usernsClonePath: "0"}), true); !c.OK {
		t.Fatal("setuid bwrap needs no user namespaces")
	}
	c = CheckUserNamespaces(files(map[string]string{usernsMaxPath: "0"}), false)
	if c.OK || !strings.Contains(c.Fix, "user.max_user_namespaces") {
		t.Fatalf("max 0: %+v", c)
	}
	c = CheckUserNamespaces(files(map[string]string{usernsAppArmorPath: "1"}), false)
	if c.OK || !strings.Contains(c.Problem, "AppArmor") {
		t.Fatalf("apparmor: %+v", c)
	}
	if c := CheckUserNamespaces(files(map[string]string{usernsAppArmorPath: "1", "/etc/apparmor.d/bwrap-userns-restrict": "profile"}), false); !c.OK {
		t.Fatalf("apparmor with a bwrap profile: %+v", c)
	}
	// No /proc files at all (older kernels): assume it works.
	if c := CheckUserNamespaces(files(nil), false); !c.OK {
		t.Fatal("unknown should pass")
	}

	bad := CheckUserNamespaces(files(map[string]string{usernsClonePath: "0"}), false)
	log := "_XSERVTransmkdir: ok\nbwrap: Can't mount proc on /proc: Operation not permitted\n"
	if h := sandboxHint(log, bad); !strings.Contains(h, "Fix:") {
		t.Fatalf("hint = %q", h)
	}
	if h := sandboxHint("Xvfb is missing", bad); h != "" {
		t.Fatalf("unrelated failure got a hint: %q", h)
	}
}
