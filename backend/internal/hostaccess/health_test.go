package hostaccess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func codes(h DesktopHealth) string {
	var c []string
	for _, p := range h.Problems {
		c = append(c, p.Code)
	}
	return strings.Join(c, ",")
}

func TestEvaluateHealth(t *testing.T) {
	good := HealthInputs{
		Full: true, WantOn: true, DMUnit: "greetd.service", LinkedUnit: "greetd.service", DefaultTarget: "graphical.target",
		DMActive: "active", Display: true, ShellRunning: true, HaveCompositor: true, HaveNocapShell: true, HaveGreeter: true,
		HaveSessions: true, HaveXwayland: true,
	}
	for _, tc := range []struct {
		name   string
		edit   func(*HealthInputs)
		status string
		codes  string
	}{
		{"healthy", func(*HealthInputs) {}, "ok", ""},
		{"not set up", func(in *HealthInputs) { in.Full = false }, "unavailable", ""},
		{"switched off", func(in *HealthInputs) { in.WantOn = false; in.DMActive = "inactive" }, "off", ""},
		{"no boot link", func(in *HealthInputs) { in.LinkedUnit = "" }, "broken", ProblemNotAtBoot},
		{"earlier sign-in service linked", func(in *HealthInputs) { in.LinkedUnit = "gdm3.service" }, "broken", ProblemNotAtBoot},
		{"text target", func(in *HealthInputs) { in.DefaultTarget = "multi-user.target" }, "broken", ProblemNotGraphical},
		{"greetd stopped", func(in *HealthInputs) { in.DMActive = "inactive"; in.ShellRunning = false }, "broken", ProblemDMStopped},
		{"greetd failed", func(in *HealthInputs) { in.DMActive = "failed"; in.Hints = []string{"gpu"} }, "broken", ProblemDMFailed},
		// The black screen: greetd up, the compositor never came.
		{"black screen", func(in *HealthInputs) { in.ShellRunning = false; in.NoShellFor = time.Minute }, "broken", ProblemNoShell},
		{"starting", func(in *HealthInputs) { in.ShellRunning = false; in.NoShellFor = 5 * time.Second }, "starting", ""},
		// No monitor: nothing to show, nothing wrong.
		{"headless", func(in *HealthInputs) { in.ShellRunning = false; in.Display = false; in.NoShellFor = time.Hour }, "ok", ""},
		{"no xwayland", func(in *HealthInputs) { in.HaveXwayland = false }, "warning", ProblemNoXwayland},
		{"leftovers", func(in *HealthInputs) { in.KioskLeft, in.ServicesHeld = true, true }, "broken", ProblemKioskLeft + "," + ProblemServicesHeld},
		{"missing parts", func(in *HealthInputs) {
			in.HaveCompositor, in.HaveNocapShell, in.HaveGreeter, in.HaveSessions = false, false, false, false
		}, "broken", ProblemNoCompositor + "," + ProblemNoNocapShell + "," + ProblemNoGreeter + "," + ProblemNoSession},
	} {
		in := good
		tc.edit(&in)
		h := EvaluateHealth(in)
		if h.Status != tc.status || codes(h) != tc.codes {
			t.Errorf("%s: got %s [%s], want %s [%s]", tc.name, h.Status, codes(h), tc.status, tc.codes)
		}
	}
	in := good
	in.DMActive, in.Hints = "failed", []string{"gpu", "seat"}
	if h := EvaluateHealth(in); h.Problems[0].Detail != "gpu, seat" {
		t.Errorf("hints = %+v", h.Problems)
	}
}

func TestJournalHints(t *testing.T) {
	text := `[    9.1] labwc[812]: [backend/backend.c:220] Failed to create backend
[   10.0] greetd[700]: error: greeter exited without creating a session
[   11.0] systemd[1]: greetd.service: Start request repeated too quickly.
[   12.0] labwc[900]: [libseat] Could not open target tty: Permission denied
[   13.0] kernel: labwc[1201]: segfault at 0 ip 00007f
[   15.0] kernel: Out of memory: Killed process 1401 (WebKitWebProces)
`
	got := JournalHints(text)
	want := []string{"gave_up", "gpu", "greeter_crash", "memory", "seat", "shell_crash"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hints = %v, want %v", got, want)
	}
	if got := JournalHints("greetd[1]: starting\n"); len(got) != 0 {
		t.Fatalf("quiet journal = %v", got)
	}
}

func TestEnvironmentWithSoftwareRendering(t *testing.T) {
	base := "PATH=/usr/bin\n"
	on := EnvironmentWithSoftwareRendering(base, true)
	if on != base+swRenderMark+"\nLIBGL_ALWAYS_SOFTWARE=1\n" {
		t.Fatalf("on = %q", on)
	}
	if again := EnvironmentWithSoftwareRendering(on, true); again != on {
		t.Fatalf("twice = %q", again)
	}
	if off := EnvironmentWithSoftwareRendering(on, false); off != base {
		t.Fatalf("off = %q", off)
	}
	// Someone's own LIBGL line stays.
	own := "LIBGL_ALWAYS_SOFTWARE=1\n"
	if got := EnvironmentWithSoftwareRendering(own, false); got != own {
		t.Fatalf("own line = %q", got)
	}
	if got := EnvironmentWithSoftwareRendering("", false); got != "" {
		t.Fatalf("empty = %q", got)
	}
}

// screenHost: a set-up machine with a monitor plugged in and no compositor
// running (the black screen), on a clock the test moves.
func screenHost(t *testing.T) (*fakeHost, *Desktop, *time.Time) {
	t.Helper()
	f, d := fullHost()
	drm := t.TempDir()
	_ = os.MkdirAll(filepath.Join(drm, "card0-HDMI-A-1"), 0o755)
	_ = os.WriteFile(filepath.Join(drm, "card0-HDMI-A-1", "status"), []byte("connected\n"), 0o644)
	d.DRMDir = drm
	d.ProcRoot = t.TempDir()
	d.DataDir = "/var/lib/nocapos"
	f.target = "multi-user.target" // what the image left
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	d.Now = func() time.Time { return now }
	return f, d, &now
}

func TestEnsureRelinksAndFallsBackToSoftware(t *testing.T) {
	f, d, now := screenHost(t)
	ctx := context.Background()

	// Right after boot: the boot link and target come back at once.
	d.Ensure(ctx)
	if f.linked != "greetd.service" || f.target != "graphical.target" {
		t.Fatalf("linked=%q target=%q", f.linked, f.target)
	}
	if strings.Contains(strings.Join(f.calls, "\n"), "restart greetd") {
		t.Fatal("no fallback before the sign-in screen had its chance")
	}

	// A minute later, still nothing on the screen: software rendering.
	*now = now.Add(61 * time.Second)
	f.calls = nil
	d.Ensure(ctx)
	if d.Fallback() != FallbackSoftware || !strings.Contains(strings.Join(f.calls, "\n"), "systemctl restart greetd.service") {
		t.Fatalf("fallback = %q, calls = %v", d.Fallback(), f.calls)
	}
	if _, ok := f.files[SoftwareRenderingFlag]; !ok || !strings.Contains(f.files["/etc/environment"], "LIBGL_ALWAYS_SOFTWARE=1") {
		t.Fatalf("software rendering not switched on: %v", f.files)
	}

	// The end of the ladder: it stays there (and says so in Health).
	*now = now.Add(10 * time.Minute)
	f.calls = nil
	d.Ensure(ctx)
	*now = now.Add(10 * time.Minute)
	d.Ensure(ctx)
	if strings.Contains(strings.Join(f.calls, "\n"), "restart") {
		t.Fatalf("no stage left, yet: %v", f.calls)
	}
	if h := d.Health(ctx); h.Status != "broken" || !h.Has(ProblemNoShell) || h.Fallback != FallbackSoftware {
		t.Fatalf("health = %+v", h)
	}

	// Back to normal.
	if err := d.ResetFallback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.files[SoftwareRenderingFlag]; ok || d.Fallback() != FallbackNone || strings.Contains(f.files["/etc/environment"], "LIBGL") {
		t.Fatalf("reset: %q %q", d.Fallback(), f.files["/etc/environment"])
	}

	// An earlier version's "xorg" stage starts over on the normal setup.
	f.files["/var/lib/nocapos/desktop-fallback"] = "xorg\n"
	if d.Fallback() != FallbackNone {
		t.Fatal("old stage kept")
	}
}

// The USB stick's setup screen: greetd runs the installer full screen, with
// no NoCap Desktop. That's no black screen; greetd is never restarted under
// it (setup would start over at its first page).
func TestEnsureLeavesTheSetupScreenAlone(t *testing.T) {
	f, d, now := screenHost(t)
	ctx := context.Background()
	f.linked, f.target = "greetd.service", "graphical.target"
	f.files[GreetdConfPath] = "[terminal]\nvt = 1\n\n[default_session]\ncommand = \"/usr/lib/nocapos/image/live/nocapos-install-screen\"\nuser = \"root\"\n"
	for i := 0; i < 5; i++ {
		*now = now.Add(2 * time.Minute)
		d.Ensure(ctx)
	}
	if calls := strings.Join(f.calls, "\n"); strings.Contains(calls, "restart greetd") || strings.Contains(calls, "start greetd") {
		t.Fatalf("greetd restarted under the setup screen: %v", f.calls)
	}
	if d.Fallback() != FallbackNone {
		t.Fatalf("fallback = %q", d.Fallback())
	}
	if h := d.Health(ctx); h.Has(ProblemNoShell) {
		t.Fatalf("health = %+v", h)
	}
}

func TestEnsureStartsAStoppedSignInScreen(t *testing.T) {
	f, d, now := screenHost(t)
	ctx := context.Background()
	f.active["greetd.service"] = false
	f.linked, f.target = "greetd.service", "graphical.target"
	d.Ensure(ctx)
	if strings.Contains(strings.Join(f.calls, "\n"), "start greetd") {
		t.Fatal("started during boot")
	}
	// Someone is on the console: leave them be.
	*now = now.Add(2 * time.Minute)
	f.session = "Name=sam\nUser=1000\nType=tty\nClass=user\nRemote=no\nState=active\n"
	f.calls = nil
	d.Ensure(ctx)
	if strings.Contains(strings.Join(f.calls, "\n"), "start greetd") {
		t.Fatalf("took the console away: %v", f.calls)
	}
	f.session = ""
	f.calls = nil
	d.Ensure(ctx)
	if !strings.Contains(strings.Join(f.calls, "\n"), "systemctl --no-block start greetd.service") {
		t.Fatalf("calls = %v", f.calls)
	}

	// Switched off in Settings: never started.
	f.files[DesktopModePath] = "user-off\n"
	f.calls = nil
	d.Ensure(ctx)
	if len(f.calls) != 0 {
		t.Fatalf("off, yet: %v", f.calls)
	}
}

func TestRepair(t *testing.T) {
	f, d, _ := screenHost(t)
	ctx := context.Background()
	delete(f.files, sessionFilePath)
	f.files[GreetdConfPath] = "[terminal]\nvt = 1\n[default_session]\ncommand = \"agreety --cmd /bin/sh\"\nuser = \"_greetd\"\n[initial_session]\ncommand = \"x\"\nuser = \"sam\"\n"
	f.files["/etc/systemd/system/nocapos-desktop.service"] = "[Unit]"
	f.files["/usr/sbin/policy-rc.d"] = "#!/bin/sh\n# NoCapOS install.sh: no service starts while the desktop installs.\nexit 101\n"
	f.files[DesktopModePath] = "user-off\n"
	res, err := d.Repair(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(res.Actions, "|")
	for _, want := range []string{"removed the old web full screen", "released the installer's service hold", "sign-in screen starts at boot", "session file", "sign-in screen settings", "restarted greetd.service"} {
		if !strings.Contains(all, want) {
			t.Errorf("actions %q: missing %q", all, want)
		}
	}
	if f.linked != "greetd.service" || f.target != "graphical.target" || f.files[DesktopModePath] != "full\n" {
		t.Fatalf("linked=%q target=%q mode=%q", f.linked, f.target, f.files[DesktopModePath])
	}
	if f.files[sessionFilePath] != SessionDesktop() {
		t.Fatal("session file not put back")
	}
	// greetd shows NoCap's sign-in screen again; automatic sign-in is kept.
	if g := f.files[GreetdConfPath]; !strings.Contains(g, greeterCmd) || GreetdAutologin(g) != "sam" || GreetdGreeterUser(g) != "_greetd" {
		t.Fatalf("greetd conf = %q", g)
	}
	if _, ok := f.files["/usr/sbin/policy-rc.d"]; ok {
		t.Fatal("service hold left")
	}

	f.files[DesktopConfPath] = ""
	if _, err := d.Repair(ctx); err == nil {
		t.Fatal("repair without a desktop")
	}
}

func TestDiagnostics(t *testing.T) {
	f, d, _ := screenHost(t)
	for _, tool := range []string{"journalctl", "systemctl", "loginctl", "uname", "labwc"} {
		f.tools[tool] = true
	}
	text := d.Diagnostics(context.Background())
	for _, want := range []string{"===== NoCap Desktop health =====", "journalctl -b -u greetd.service", "===== Compositor (this boot) =====", "===== NoCap Shell (this boot) =====", "===== Sessions =====", "===== Graphics card =====", "card0-HDMI-A-1: connected", sessionFilePath + ": present", "(lspci isn't installed)"} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnostics lack %q", want)
		}
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "journalctl -b --no-pager -o short-monotonic -u greetd.service -n 300") {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestGPULines(t *testing.T) {
	lspci := "00:00.0 Host bridge [0600]: Intel\n\tKernel driver in use: skl_uncore\n00:02.0 VGA compatible controller [0300]: Intel UHD [8086:3e92]\n\tSubsystem: Dell\n\tKernel driver in use: i915\n00:1f.3 Audio device [0403]: Intel\n"
	want := "00:02.0 VGA compatible controller [0300]: Intel UHD [8086:3e92]\n\tSubsystem: Dell\n\tKernel driver in use: i915"
	if got := gpuLines(lspci); got != want {
		t.Fatalf("gpu = %q", got)
	}
}

// Software rendering chosen on one boot is taken back on the next one, so a
// machine whose graphics card works isn't stuck drawing with the processor;
// a graphics card that keeps failing keeps it after a few boots.
func TestSoftwareRenderingIsRetriedOnTheNextBoot(t *testing.T) {
	f, d, now := screenHost(t)
	ctx := context.Background()
	const bootFile = "/proc/sys/kernel/random/boot_id"
	f.files[bootFile] = "boot-1\n"
	d.Ensure(ctx)
	*now = now.Add(61 * time.Second)
	d.Ensure(ctx)
	if r := d.fallbackRecord(); r.stage != FallbackSoftware || r.tries != 1 || r.boot != "boot-1" {
		t.Fatalf("record = %+v", r)
	}
	// alfad restarts on the same boot (an update): nothing changes.
	d.hs = healthState{}
	d.Ensure(ctx)
	if d.Fallback() != FallbackSoftware {
		t.Fatal("retried on the same boot")
	}

	// The next boots: the graphics card again, then software when the screen
	// stays empty, until maxHardwareRetries boots have failed.
	for boot := 2; boot <= maxHardwareRetries+1; boot++ {
		f.files[bootFile] = fmt.Sprintf("boot-%d\n", boot)
		d.hs = healthState{}
		f.calls = nil
		d.Ensure(ctx)
		if boot > maxHardwareRetries {
			if d.Fallback() != FallbackSoftware {
				t.Fatalf("boot %d: gave software up after %d failures", boot, maxHardwareRetries)
			}
			break
		}
		if d.Fallback() != FallbackNone {
			t.Fatalf("boot %d: not retried: %+v", boot, d.fallbackRecord())
		}
		if _, ok := f.files[SoftwareRenderingFlag]; ok || strings.Contains(f.files["/etc/environment"], "LIBGL") {
			t.Fatalf("boot %d: software switches left on", boot)
		}
		if strings.Contains(strings.Join(f.calls, "\n"), "restart") {
			t.Fatalf("boot %d: the retry restarted the screen: %v", boot, f.calls)
		}
		*now = now.Add(61 * time.Second)
		d.Ensure(ctx)
		if r := d.fallbackRecord(); r.stage != FallbackSoftware || r.tries != boot {
			t.Fatalf("boot %d: record = %+v", boot, r)
		}
	}

	// An earlier version's record (no boot, no count) is retried too.
	f.files["/var/lib/nocapos/desktop-fallback"] = "software\n"
	f.files[SoftwareRenderingFlag] = ""
	f.files["/etc/environment"] = swRenderMark + "\nLIBGL_ALWAYS_SOFTWARE=1\n"
	d.hs = healthState{}
	d.Ensure(ctx)
	if d.Fallback() != FallbackNone || f.files["/etc/environment"] != "" {
		t.Fatalf("legacy: %q %q", d.Fallback(), f.files["/etc/environment"])
	}
}

// A hardware retry that keeps working for a while ends: the failed boots
// are forgotten.
func TestHardwareRetryProven(t *testing.T) {
	f, d, now := screenHost(t)
	ctx := context.Background()
	f.linked, f.target = "greetd.service", "graphical.target"
	const rec = "/var/lib/nocapos/desktop-fallback"
	f.files[rec] = "hardware-retry\nboot=b\ntries=2\n"
	proc := filepath.Join(d.ProcRoot, "812")
	_ = os.MkdirAll(proc, 0o755)
	_ = os.WriteFile(filepath.Join(proc, "comm"), []byte("labwc\n"), 0o644)
	_ = os.WriteFile(filepath.Join(proc, "environ"), []byte("HOME=/home/sam\x00WLR_RENDERER=pixman\x00"), 0o644)
	d.Ensure(ctx)
	*now = now.Add(20 * time.Minute)
	d.Ensure(ctx)
	if _, ok := f.files[rec]; !ok {
		t.Fatal("a compositor drawing in software proved the graphics card")
	}
	_ = os.WriteFile(filepath.Join(proc, "environ"), []byte("HOME=/home/sam\x00"), 0o644)
	d.Ensure(ctx)
	*now = now.Add(11 * time.Minute)
	d.Ensure(ctx)
	if _, ok := f.files[rec]; ok {
		t.Fatalf("record kept: %q", f.files[rec])
	}
}
