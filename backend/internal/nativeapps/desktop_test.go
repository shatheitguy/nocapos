package nativeapps

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"alfaos/alfad/internal/webapps"
)

func TestRunAsMapsAccountsNeverRoot(t *testing.T) {
	s, _ := newFakeService(t, true)
	linux := map[string]*webapps.RunAs{
		"alex": {Username: "alex", UID: 1000, GID: 1000, Home: "/home/alex"},
		"root": {Username: "root", UID: 0, GID: 0, Home: "/root"},
		"toor": {Username: "toor", UID: 0, GID: 0, Home: "/root"},
	}
	s.Account = func(n string) *webapps.RunAs { return linux[n] }

	// Their own Linux account, their own home and runtime folder.
	a, err := s.runAs(Person{ID: "u1", Username: "alex"})
	if err != nil || a.Username != "alex" || a.Home != "/home/alex" || a.Runtime != "/run/user/1000" || a.OwnHome {
		t.Fatalf("alex = %+v %v", a, err)
	}
	// Root (by name or uid), no account, or no Linux sign-in: the shared
	// unprivileged account, with a home per person.
	for _, who := range []Person{{ID: "u2", Username: "root"}, {ID: "u2", Username: "toor"}, {ID: "u2", Username: "ghost"}, {ID: "u2"}} {
		a, err := s.runAs(who)
		if err != nil || a.UID == 0 || a.Username != AppsUser || a.Home != AppsHomeBase+"/u2" || a.Runtime != webapps.AppsRuntimeBase+"/u2" || !a.OwnHome {
			t.Fatalf("%+v → %+v %v", who, a, err)
		}
	}
	// A broken shared account is refused, never replaced by root.
	s.AppsAccount = func() (*webapps.RunAs, error) { return &webapps.RunAs{Username: "root", Home: "/root"}, nil }
	if _, err := s.runAs(Person{ID: "u3"}); !errors.Is(err, webapps.ErrNoAccount) {
		t.Fatalf("root shared account: %v", err)
	}
	// Odd ids can't leave the homes folder.
	s.AppsAccount = func() (*webapps.RunAs, error) {
		return &webapps.RunAs{Username: AppsUser, UID: 990, GID: 990, Home: "/var/lib/nocap-apps"}, nil
	}
	if a, _ := s.runAs(Person{ID: "../../etc"}); a.Home != AppsHomeBase+"/etc" {
		t.Fatalf("home = %q", a.Home)
	}
}

func TestSandboxProblemStopsLaunchWithFix(t *testing.T) {
	s, _ := newFakeService(t, true)
	s.Sandbox = func() webapps.SandboxCheck {
		return webapps.SandboxCheck{Problem: "user namespaces are off", Fix: "sysctl kernel.unprivileged_userns_clone=1"}
	}
	_, err := s.Launch(context.Background(), Person{ID: "u1"}, "org.gimp.GIMP")
	var sb ErrSandbox
	if !errors.As(err, &sb) || !strings.Contains(err.Error(), "Fix: sysctl") {
		t.Fatalf("err = %v", err)
	}
	if st := s.Status(); st.Sandbox.OK || st.Sandbox.Fix == "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestDesktopAndServerScreen(t *testing.T) {
	s, h := newFakeService(t, true)
	got, err := s.Desktop(Person{ID: "u1"})
	if err != nil || got.Stream == nil {
		t.Fatalf("desktop = %+v %v", got, err)
	}
	fs := s.Streams.(*fakeStreams)
	if sp := fs.specs[0]; sp.Mode != webapps.ModeDesktop || sp.Key != DesktopKey || sp.RunAs.Username != AppsUser {
		t.Fatalf("spec = %+v", sp)
	}
	if _, err := s.ViewScreen(Person{ID: "u1"}); !errors.Is(err, ErrNoScreen) {
		t.Fatalf("screen off: %v", err)
	}
	h.mu.Lock()
	h.envFile = "USER=nocap-desktop\nWAYLAND_DISPLAY=wayland-0\nXDG_RUNTIME_DIR=/run/user/991\n"
	h.sockets["/run/user/991/wayland-0"] = true
	h.mu.Unlock()
	s.Account = func(n string) *webapps.RunAs {
		if n == "nocap-desktop" {
			return &webapps.RunAs{Username: n, UID: 991, GID: 991, Home: "/var/lib/nocap-desktop"}
		}
		return nil
	}
	if _, err := s.ViewScreen(Person{ID: "u1"}); err != nil {
		t.Fatal(err)
	}
	sp := fs.specs[len(fs.specs)-1]
	if sp.Mode != webapps.ModeScreen || sp.RunAs.Username != "nocap-desktop" || sp.RunAs.Runtime != "/run/user/991" || !slices.Contains(sp.Env, "WAYLAND_DISPLAY=wayland-0") {
		t.Fatalf("screen spec = %+v", sp)
	}
}

func TestScreenUnitAndAppsAccountArgs(t *testing.T) {
	env := DesktopEnv{User: "nocap-desktop", WaylandDisplay: "wayland-0", XDGRuntimeDir: "/run/user/991"}
	a := &webapps.RunAs{Username: "nocap-desktop", UID: 991, GID: 991, Home: "/var/lib/nocap-desktop"}
	u := ScreenUnit(env, a, []string{"flatpak", "run", "org.videolan.VLC"}, "abc", "C.UTF-8", func(p string) bool { return p == "/run/user/991/bus" })
	args := webapps.SystemdRunArgs(u)
	for _, want := range []string{"--uid=nocap-desktop", "--wait", "--setenv=WAYLAND_DISPLAY=wayland-0", "--setenv=XDG_RUNTIME_DIR=/run/user/991",
		"--setenv=DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/991/bus", "--unit=nocap-screen-nocap-desktop-abc"} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %q in %q", want, args)
		}
	}
	if !slices.Equal(args[len(args)-3:], []string{"flatpak", "run", "org.videolan.VLC"}) {
		t.Fatalf("argv = %q", args)
	}
	ua := AppsAccountArgs()
	if ua[len(ua)-1] != AppsUser || !slices.Contains(ua, "--system") || !slices.Contains(ua, "/usr/sbin/nologin") {
		t.Fatalf("useradd = %q", ua)
	}
}
