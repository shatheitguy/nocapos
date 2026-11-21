package hostaccess

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActiveUnits(t *testing.T) {
	units := []string{"gdm3.service", "gdm.service", "lightdm.service"}
	if got := ActiveUnits(units, "active\nactive\ninactive\n"); !reflect.DeepEqual(got, []string{"gdm3.service", "gdm.service"}) {
		t.Fatalf("got %v", got)
	}
	if got := ActiveUnits(units, "inactive\nactivating\n"); !reflect.DeepEqual(got, []string{"gdm.service"}) {
		t.Fatalf("short output: %v", got)
	}
	if got := ActiveUnits(units, ""); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
}

func TestOldDesktopUser(t *testing.T) {
	p := func(s string) map[string]string { return ParseLoginctlProps(s) }
	for _, tc := range []struct {
		props string
		want  bool
	}{
		{"Name=sam\nClass=user\nType=wayland\nRemote=no\nState=active\nService=gdm-password\n", true},
		{"Name=sam\nClass=user\nType=x11\nRemote=no\nState=online\nService=gdm-autologin\n", true},
		// NoCap Desktop's own session.
		{"Name=sam\nClass=user\nType=wayland\nRemote=no\nState=active\nService=greetd\n", false},
		// The earlier sign-in screen itself: nobody to sign out.
		{"Name=Debian-gdm\nClass=greeter\nType=wayland\nRemote=no\nState=active\nService=gdm-launch-environment\n", false},
		// A console or an SSH login.
		{"Name=sam\nClass=user\nType=tty\nRemote=no\nState=active\nService=login\n", false},
		{"Name=sam\nClass=user\nType=x11\nRemote=yes\nState=active\nService=sshd\n", false},
		{"Name=sam\nClass=user\nType=wayland\nRemote=no\nState=closing\nService=gdm-password\n", false},
	} {
		if got := OldDesktopUser(p(tc.props)); got != tc.want {
			t.Errorf("%q: got %v", tc.props, got)
		}
	}
}

// oldDesktopHost: the update switched the boot link to greetd but left the
// earlier sign-in service running (someone was signed in through it).
func oldDesktopHost(t *testing.T, signedIn bool) (*fakeHost, *Desktop, *time.Time) {
	t.Helper()
	f, d, now := screenHost(t)
	f.linked, f.target = "greetd.service", "graphical.target"
	f.active["greetd.service"] = false
	f.active["gdm3.service"], f.active["gdm.service"] = true, true
	f.sessions = map[string]string{"c1": "Name=Debian-gdm\nClass=greeter\nType=wayland\nRemote=no\nState=online\nService=gdm-launch-environment\n"}
	if signedIn {
		f.sessions["2"] = "Name=sam\nClass=user\nType=wayland\nRemote=no\nState=active\nService=gdm-password\n"
		f.session = f.sessions["2"]
	}
	return f, d, now
}

func callIndex(calls []string, want string) int {
	for i, c := range calls {
		if c == want {
			return i
		}
	}
	return -1
}

func TestHealthSaysRestartWhileSomeoneUsesTheOldDesktop(t *testing.T) {
	_, d, _ := oldDesktopHost(t, true)
	h := d.Health(context.Background())
	if h.Status != "broken" || !h.Has(ProblemOtherDM) || !h.RestartNeeded || !reflect.DeepEqual(h.OldSessionUsers, []string{"sam"}) {
		t.Fatalf("health = %+v", h)
	}
}

func TestRepairNeverSignsOutTheOldDesktop(t *testing.T) {
	f, d, _ := oldDesktopHost(t, true)
	res, err := d.Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.RestartNeeded || !reflect.DeepEqual(res.OldSessionUsers, []string{"sam"}) {
		t.Fatalf("result = %+v", res)
	}
	all := strings.Join(f.calls, "\n")
	for _, never := range []string{"systemctl restart greetd.service", "systemctl start greetd.service", "systemctl stop gdm3.service", "systemctl stop gdm.service"} {
		if strings.Contains(all, never) {
			t.Errorf("ran %q while sam is signed in:\n%s", never, all)
		}
	}
	// Off from the next restart, and greetd conflicts with it from now on.
	if callIndex(f.calls, "systemctl disable gdm3.service") < 0 {
		t.Errorf("gdm3 not disabled:\n%s", all)
	}
	if f.files[GreetdDropInPath] != GreetdDropIn() {
		t.Errorf("drop-in = %q", f.files[GreetdDropInPath])
	}
	if f.linked != "greetd.service" {
		t.Errorf("linked = %q", f.linked)
	}
}

func TestRepairStopsAnIdleOldSignInScreenFirst(t *testing.T) {
	f, d, _ := oldDesktopHost(t, false)
	res, err := d.Repair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.RestartNeeded {
		t.Fatalf("result = %+v", res)
	}
	stop, restart := callIndex(f.calls, "systemctl stop gdm3.service"), callIndex(f.calls, "systemctl restart greetd.service")
	if stop < 0 || restart < 0 || stop > restart {
		t.Fatalf("want gdm3 stopped before greetd starts:\n%s", strings.Join(f.calls, "\n"))
	}
	if callIndex(f.calls, "systemctl disable gdm3.service") < 0 || !strings.Contains(strings.Join(res.Actions, "|"), "stopped gdm3.service") {
		t.Fatalf("actions = %v", res.Actions)
	}
}

func TestEnsureSwitchesFromAnIdleOldSignInScreen(t *testing.T) {
	ctx := context.Background()
	// Someone signed in on the earlier desktop: never touched.
	f, d, now := oldDesktopHost(t, true)
	d.Ensure(ctx)
	*now = now.Add(2 * time.Minute)
	f.calls = nil
	d.Ensure(ctx)
	for _, c := range f.calls {
		if strings.Contains(c, "greetd.service") && (strings.Contains(c, " start") || strings.Contains(c, "restart")) || strings.HasPrefix(c, "systemctl stop gdm") {
			t.Fatalf("touched the screen: %v", f.calls)
		}
	}
	// Only its sign-in screen: stopped, then NoCap's starts.
	f, d, now = oldDesktopHost(t, false)
	d.Ensure(ctx)
	*now = now.Add(2 * time.Minute)
	f.calls = nil
	did := d.Ensure(ctx)
	stop, start := callIndex(f.calls, "systemctl stop gdm3.service"), callIndex(f.calls, "systemctl --no-block start greetd.service")
	if stop < 0 || start < 0 || stop > start {
		t.Fatalf("calls = %v (did %v)", f.calls, did)
	}
	if f.files[GreetdDropInPath] != GreetdDropIn() {
		t.Fatal("no drop-in")
	}
}

func TestGreetdDropIn(t *testing.T) {
	g := GreetdDropIn()
	for _, u := range OldDisplayManagers {
		if !strings.Contains(g, "Conflicts=") || !strings.Contains(g, u) {
			t.Fatalf("drop-in lacks %s:\n%s", u, g)
		}
	}
	if strings.Contains(g, "greetd.service ") {
		t.Fatal("greetd conflicts with itself")
	}
}
