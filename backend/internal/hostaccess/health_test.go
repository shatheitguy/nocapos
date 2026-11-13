package hostaccess

import (
	"context"
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
		Full: true, WantOn: true, DMUnit: "gdm3.service", LinkedUnit: "gdm3.service", DefaultTarget: "graphical.target",
		DMActive: "active", Display: true, ShellRunning: true, HaveShell: true, HaveSessions: true, HaveXorg: true,
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
		// Debian: gdm3 "enabled" but nothing starts it at boot.
		{"no boot link", func(in *HealthInputs) { in.LinkedUnit = "" }, "broken", ProblemNotAtBoot},
		{"text target", func(in *HealthInputs) { in.DefaultTarget = "multi-user.target" }, "broken", ProblemNotGraphical},
		{"gdm stopped", func(in *HealthInputs) { in.DMActive = "inactive"; in.ShellRunning = false }, "broken", ProblemDMStopped},
		{"gdm failed", func(in *HealthInputs) { in.DMActive = "failed"; in.Hints = []string{"gpu"} }, "broken", ProblemDMFailed},
		// The black screen: GDM up, GNOME Shell never came.
		{"greeter crashed", func(in *HealthInputs) { in.ShellRunning = false; in.NoShellFor = time.Minute }, "broken", ProblemNoShell},
		{"greeter starting", func(in *HealthInputs) { in.ShellRunning = false; in.NoShellFor = 5 * time.Second }, "starting", ""},
		// No monitor: nothing to show, nothing wrong.
		{"headless", func(in *HealthInputs) { in.ShellRunning = false; in.Display = false; in.NoShellFor = time.Hour }, "ok", ""},
		{"no xorg", func(in *HealthInputs) { in.HaveXorg = false }, "warning", ProblemNoXorg},
		{"leftovers", func(in *HealthInputs) { in.KioskLeft, in.ServicesHeld = true, true }, "broken", ProblemKioskLeft + "," + ProblemServicesHeld},
		{"missing parts", func(in *HealthInputs) { in.HaveShell, in.HaveSessions = false, false }, "broken", ProblemNoGnomeShell + "," + ProblemNoSession},
	} {
		in := good
		tc.edit(&in)
		h := EvaluateHealth(in)
		if h.Status != tc.status || codes(h) != tc.codes {
			t.Errorf("%s: got %s [%s], want %s [%s]", tc.name, h.Status, codes(h), tc.status, tc.codes)
		}
	}
	h := EvaluateHealth(HealthInputs{Full: true, WantOn: true, DMUnit: "gdm3.service", DMActive: "failed", Hints: []string{"gpu", "xorg"}, HaveShell: true, HaveSessions: true, HaveXorg: true, LinkedUnit: "gdm3.service", DefaultTarget: "graphical.target"})
	if h.Problems[0].Detail != "gpu, xorg" {
		t.Errorf("hints = %+v", h.Problems)
	}
}

func TestJournalHints(t *testing.T) {
	text := `[    9.1] gnome-shell[812]: Failed to create backend: No GPUs found
[   10.0] gdm3[700]: Gdm: GdmDisplay: Session never registered, failing
[   11.0] gdm3[700]: Gdm: GdmLocalDisplayFactory: maximum number of X display failures reached: check X server log for errors
[   12.0] /usr/libexec/gdm-x-session[900]: (EE) no screens found(EE)
[   13.0] kernel: traps: gnome-shell[1201] trap int3 ip:7f
[   14.0] gnome-shell[1301]: JS ERROR: TypeError: x is undefined
[   15.0] kernel: Out of memory: Killed process 1401 (gnome-shell)
`
	got := JournalHints(text)
	want := []string{"extension", "gave_up", "gpu", "greeter_crash", "memory", "shell_crash", "xorg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hints = %v, want %v", got, want)
	}
	if got := JournalHints("gdm3[1]: Gdm: starting\n"); len(got) != 0 {
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

// screenHost: a set-up machine with a monitor plugged in and no GNOME
// Shell running (the black screen), on a clock the test moves.
func screenHost(t *testing.T) (*fakeHost, *Desktop, *time.Time) {
	t.Helper()
	f, d := fullHost()
	drm := t.TempDir()
	_ = os.MkdirAll(filepath.Join(drm, "card0-HDMI-A-1"), 0o755)
	_ = os.WriteFile(filepath.Join(drm, "card0-HDMI-A-1", "status"), []byte("connected\n"), 0o644)
	d.DRMDir = drm
	d.ProcRoot = t.TempDir()
	d.DataDir = "/var/lib/nocapos"
	f.enabled["gdm3.service"] = false // Debian: gdm3.service has no [Install]
	f.target = "multi-user.target"    // what the image left
	f.tools["gnome-shell"], f.tools["Xorg"] = true, true
	for p := range sessionFiles {
		f.files[p] = sessionFiles[p]
	}
	f.files["/etc/gdm3/daemon.conf"] = "[daemon]\n#WaylandEnable=false\n"
	f.files["/usr/share/gnome-shell/gnome-shell-theme.gresource"] = ""
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	d.Now = func() time.Time { return now }
	return f, d, &now
}

func TestEnsureRelinksAndStepsDownTheLadder(t *testing.T) {
	f, d, now := screenHost(t)
	ctx := context.Background()

	// Right after boot: the boot link and target come back at once.
	d.Ensure(ctx)
	if f.linked != "gdm3.service" || f.target != "graphical.target" {
		t.Fatalf("linked=%q target=%q", f.linked, f.target)
	}
	if st := d.State(ctx); st.Mode != ModeFull {
		t.Fatalf("a linked gdm3 (no [Install]) is on: %+v", st)
	}
	if strings.Contains(strings.Join(f.calls, "\n"), "restart gdm3") {
		t.Fatal("no fallback before the sign-in screen had its chance")
	}

	// A minute later, still no GNOME Shell: Xorg instead of Wayland.
	*now = now.Add(61 * time.Second)
	f.calls = nil
	d.Ensure(ctx)
	if !strings.Contains(f.files["/etc/gdm3/daemon.conf"], "WaylandEnable=false") || strings.Contains(f.files["/etc/gdm3/daemon.conf"], "#WaylandEnable") {
		t.Fatalf("gdm conf = %q", f.files["/etc/gdm3/daemon.conf"])
	}
	if d.Fallback() != FallbackXorg || !strings.Contains(strings.Join(f.calls, "\n"), "systemctl restart gdm3.service") {
		t.Fatalf("fallback = %q, calls = %v", d.Fallback(), f.calls)
	}

	// Not again straight away.
	*now = now.Add(10 * time.Second)
	f.calls = nil
	d.Ensure(ctx)
	if strings.Contains(strings.Join(f.calls, "\n"), "restart") {
		t.Fatalf("stepped again too soon: %v", f.calls)
	}

	// Still black a few minutes on: software rendering, the system's theme.
	*now = now.Add(4 * time.Minute)
	f.calls = nil
	d.Ensure(ctx)
	if d.Fallback() != FallbackSoftware || !strings.Contains(f.files["/etc/environment"], "LIBGL_ALWAYS_SOFTWARE=1") {
		t.Fatalf("fallback = %q env = %q", d.Fallback(), f.files["/etc/environment"])
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "update-alternatives --set gdm-theme.gresource /usr/share/gnome-shell/gnome-shell-theme.gresource") {
		t.Fatalf("calls = %v", f.calls)
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
	if d.Fallback() != FallbackNone || strings.Contains(f.files["/etc/environment"], "LIBGL") || !strings.Contains(f.files["/etc/gdm3/daemon.conf"], "WaylandEnable=true") {
		t.Fatalf("reset: %q %q %q", d.Fallback(), f.files["/etc/environment"], f.files["/etc/gdm3/daemon.conf"])
	}
}

func TestEnsureStartsAStoppedSignInScreen(t *testing.T) {
	f, d, now := screenHost(t)
	ctx := context.Background()
	f.active["gdm3.service"] = false
	f.linked, f.target = "gdm3.service", "graphical.target"
	d.Ensure(ctx)
	if strings.Contains(strings.Join(f.calls, "\n"), "start gdm3") {
		t.Fatal("started during boot")
	}
	// Someone is on the console: leave them be.
	*now = now.Add(2 * time.Minute)
	f.session = "Name=sam\nUser=1000\nType=tty\nClass=user\nRemote=no\nState=active\n"
	f.calls = nil
	d.Ensure(ctx)
	if strings.Contains(strings.Join(f.calls, "\n"), "start gdm3") {
		t.Fatalf("took the console away: %v", f.calls)
	}
	f.session = ""
	f.calls = nil
	d.Ensure(ctx)
	if !strings.Contains(strings.Join(f.calls, "\n"), "systemctl --no-block start gdm3.service") {
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
	delete(f.files, "/usr/share/xsessions/nocapos.desktop")
	f.files["/etc/systemd/system/nocapos-desktop.service"] = "[Unit]"
	f.files["/usr/sbin/policy-rc.d"] = "#!/bin/sh\n# NoCapOS install.sh: no service starts while the desktop installs.\nexit 101\n"
	f.files[DesktopModePath] = "user-off\n"
	res, err := d.Repair(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(res.Actions, "|")
	for _, want := range []string{"removed the old web full screen", "released the installer's service hold", "sign-in screen starts at boot", "session file xsessions", "restarted gdm3.service"} {
		if !strings.Contains(all, want) {
			t.Errorf("actions %q: missing %q", all, want)
		}
	}
	if f.linked != "gdm3.service" || f.target != "graphical.target" || f.files[DesktopModePath] != "full\n" {
		t.Fatalf("linked=%q target=%q mode=%q", f.linked, f.target, f.files[DesktopModePath])
	}
	if f.files["/usr/share/xsessions/nocapos.desktop"] != sessionFiles["/usr/share/xsessions/nocapos.desktop"] {
		t.Fatal("session file not put back")
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
	for _, tool := range []string{"journalctl", "systemctl", "loginctl", "uname"} {
		f.tools[tool] = true
	}
	text := d.Diagnostics(context.Background())
	for _, want := range []string{"===== NoCap Desktop health =====", "journalctl -b -u gdm3.service", "===== GNOME Shell (this boot) =====", "===== Sessions =====", "===== Graphics card =====", "card0-HDMI-A-1: connected", "/usr/share/xsessions/nocapos.desktop: present", "(lspci isn't installed)"} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnostics lack %q", want)
		}
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "journalctl -b --no-pager -o short-monotonic -u gdm3.service -n 300") {
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
