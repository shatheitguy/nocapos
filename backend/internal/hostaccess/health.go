package hostaccess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- is NoCap Desktop really on the screen? ----
//
// "gdm is enabled" isn't the same as "the screen shows NoCap Desktop". Each
// of these ends in a black screen, and Health finds them:
//
//   - display-manager.service doesn't point at GDM: graphical.target starts
//     whatever that link names, and Debian's gdm3.service has no [Install]
//     section, so "systemctl enable gdm3" never makes it.
//   - the default target isn't graphical.target.
//   - GDM runs but no GNOME Shell does (the sign-in screen crashed: Wayland
//     can't run on this graphics card, no GL driver, a broken theme...).
//   - GNOME Shell, the NoCapOS session files or Xorg (the fallback) are
//     missing, or the old web full screen or the installer's service hold
//     was left behind.
//
// alfad checks every few seconds (Ensure): it puts the boot link and the
// target back, starts the sign-in screen when nothing is on the screen, and
// when the sign-in screen keeps failing it steps down a fallback ladder:
// Xorg instead of Wayland, then Xorg with software rendering and the
// system's own sign-in theme. Still native GNOME, still NoCapOS's session.

// DisplayManagerLink is what graphical.target starts.
const DisplayManagerLink = "/etc/systemd/system/display-manager.service"

// Fallback stages (the file desktop-fallback in the data folder).
const (
	FallbackNone     = ""
	FallbackXorg     = "xorg"
	FallbackSoftware = "software"
)

// FallbackFileName is in the data folder (gdm-theme reads it too).
const FallbackFileName = "desktop-fallback"

// ShellGrace: how long GDM may run without GNOME Shell before it counts as
// a crashed sign-in screen.
const ShellGrace = 45 * time.Second

// autoFallbackEvery: the least time between two automatic fallback steps.
const autoFallbackEvery = 3 * time.Minute

// startDelay: alfad starts with the machine; GDM may still be on its way.
const startDelay = 60 * time.Second

// NoCapOS's session files (install.sh writes them; Repair puts them back).
var sessionFiles = map[string]string{
	"/usr/share/wayland-sessions/nocapos.desktop": sessionDesktop("NoCapOS"),
	"/usr/share/xsessions/nocapos.desktop":        sessionDesktop("NoCapOS (X11)"),
}

// sessionDesktop is lib.sh's render_session_desktop.
func sessionDesktop(name string) string {
	return "[Desktop Entry]\n# The NoCapOS desktop (written by NoCapOS's install.sh).\nName=" + name +
		"\nComment=The NoCapOS desktop\nExec=gnome-session\nTryExec=gnome-session\nType=Application\nDesktopNames=GNOME\nX-GDM-SessionRegisters=true\n"
}

const swRenderMark = "# NoCap Desktop software rendering (NoCapOS repair)"

// Problem codes.
const (
	ProblemNotAtBoot    = "not_at_boot"
	ProblemNotGraphical = "not_graphical"
	ProblemDMStopped    = "dm_stopped"
	ProblemDMFailed     = "dm_failed"
	ProblemNoShell      = "no_shell"
	ProblemNoGnomeShell = "no_gnome_shell"
	ProblemNoSession    = "no_session"
	ProblemNoXorg       = "no_xorg"
	ProblemKioskLeft    = "kiosk_left"
	ProblemServicesHeld = "services_held"
)

// HealthProblem is one thing wrong.
type HealthProblem struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// DesktopHealth is what Settings shows: ok, starting (the sign-in screen is
// on its way), warning (works, but something's missing), broken, off or
// unavailable.
type DesktopHealth struct {
	Status   string          `json:"status"`
	Problems []HealthProblem `json:"problems,omitempty"`
	// Fallback in use: "", xorg or software.
	Fallback string `json:"fallback,omitempty"`
}

// HealthInputs is everything Health looks at, gathered from the machine.
type HealthInputs struct {
	Full          bool // install.sh set the desktop up
	WantOn        bool // not switched off in Settings
	DMUnit        string
	LinkedUnit    string // what display-manager.service is ("" = nothing)
	DefaultTarget string
	DMActive      string // systemctl is-active
	Display       bool   // a monitor is plugged in
	ShellRunning  bool
	NoShellFor    time.Duration
	HaveShell     bool
	HaveSessions  bool
	HaveXorg      bool
	KioskLeft     bool
	ServicesHeld  bool
	Fallback      string
	Hints         []string // from the journal (JournalHints)
}

// EvaluateHealth turns the inputs into a verdict.
func EvaluateHealth(in HealthInputs) DesktopHealth {
	h := DesktopHealth{Fallback: in.Fallback}
	switch {
	case !in.Full:
		h.Status = "unavailable"
		return h
	case !in.WantOn:
		h.Status = "off"
		return h
	}
	add := func(code, detail string) { h.Problems = append(h.Problems, HealthProblem{Code: code, Detail: detail}) }
	if in.LinkedUnit != in.DMUnit {
		got := in.LinkedUnit
		if got == "" {
			got = "nothing"
		}
		add(ProblemNotAtBoot, "display-manager.service is "+got+", not "+in.DMUnit)
	}
	if in.DefaultTarget != "graphical.target" {
		add(ProblemNotGraphical, "the default target is "+orNone(in.DefaultTarget))
	}
	if !in.HaveShell {
		add(ProblemNoGnomeShell, "")
	}
	if !in.HaveSessions {
		add(ProblemNoSession, "")
	}
	if in.KioskLeft {
		add(ProblemKioskLeft, "")
	}
	if in.ServicesHeld {
		add(ProblemServicesHeld, "/usr/sbin/policy-rc.d")
	}
	starting := false
	switch in.DMActive {
	case "active", "reloading":
		if !in.ShellRunning && in.Display {
			if in.NoShellFor >= ShellGrace {
				add(ProblemNoShell, strings.Join(in.Hints, ", "))
			} else {
				starting = true
			}
		}
	case "activating":
		starting = true
	case "failed":
		add(ProblemDMFailed, strings.Join(in.Hints, ", "))
	default:
		add(ProblemDMStopped, "")
	}
	if !in.HaveXorg {
		add(ProblemNoXorg, "")
	}
	switch {
	case len(h.Problems) == 1 && h.Problems[0].Code == ProblemNoXorg:
		h.Status = "warning"
	case len(h.Problems) > 0:
		h.Status = "broken"
	case starting:
		h.Status = "starting"
	default:
		h.Status = "ok"
	}
	return h
}

func orNone(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// Has reports whether the verdict has that problem.
func (h DesktopHealth) Has(code string) bool {
	for _, p := range h.Problems {
		if p.Code == code {
			return true
		}
	}
	return false
}

// journal patterns that explain a black screen.
var journalHints = []struct {
	code string
	re   *regexp.Regexp
}{
	{"gpu", regexp.MustCompile(`(?i)failed to create backend|no gpus? found|failed to open gpu|could not open device|failed to (initialize|create) (renderer|egl|gbm)|eglInitialize failed|no suitable egl`)},
	{"xorg", regexp.MustCompile(`(?i)unable to run x server|no screens found|\(EE\) .*fatal server error|failed to start x server|xorg.*(failed|exited)`)},
	{"greeter_crash", regexp.MustCompile(`(?i)session never registered|child process -?\d+ was already dead|greeter.*(crash|exited|died)|gdm-(wayland|x)-session.*(exited|failed)`)},
	{"gave_up", regexp.MustCompile(`(?i)maximum number of (x )?display failures reached|too many (display )?failures|giving up`)},
	{"shell_crash", regexp.MustCompile(`(?i)gnome-shell.*(segfault|segmentation fault|core dumped|aborted|dumped core|trap)|traps: gnome-shell`)},
	{"extension", regexp.MustCompile(`(?i)extension .* had error|JS ERROR`)},
	{"theme", regexp.MustCompile(`(?i)(failed to load|parsing).*(stylesheet|theme)|gdm-theme\.gresource`)},
	{"memory", regexp.MustCompile(`(?i)out of memory|oom-kill|killed process \d+ \(gnome-shell\)`)},
}

// JournalHints finds the known causes in journal text (codes, sorted).
func JournalHints(text string) []string {
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		for _, h := range journalHints {
			if !seen[h.code] && h.re.MatchString(line) {
				seen[h.code] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// healthState is what Ensure remembers between checks.
type healthState struct {
	mu           sync.Mutex
	started      time.Time
	noShellSince time.Time
	lastFallback time.Time
}

func (d *Desktop) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Desktop) remove(p string) error {
	if d.Remove != nil {
		return d.Remove(p)
	}
	return os.Remove(p)
}

func (d *Desktop) symlink(target, link string) error {
	if d.Symlink != nil {
		return d.Symlink(target, link)
	}
	_ = os.Remove(link)
	return os.Symlink(target, link)
}

func (d *Desktop) procRoot() string {
	if d.ProcRoot != "" {
		return d.ProcRoot
	}
	return "/proc"
}

func (d *Desktop) fallbackPath() string {
	dir := d.DataDir
	if dir == "" {
		dir = "/var/lib/nocapos"
	}
	return path.Join(filepath.ToSlash(dir), FallbackFileName)
}

// Fallback is the fallback stage in use.
func (d *Desktop) Fallback() string {
	b, err := d.readFile(d.fallbackPath())
	if err != nil {
		return FallbackNone
	}
	switch s := strings.TrimSpace(string(b)); s {
	case FallbackXorg, FallbackSoftware:
		return s
	}
	return FallbackNone
}

// wantOn: the owner didn't switch NoCap Desktop off.
func (d *Desktop) wantOn() bool {
	b, _ := d.readFile(DesktopModePath)
	return strings.TrimSpace(string(b)) != "user-off"
}

// linkedUnit is the unit display-manager.service is ("" = none).
func (d *Desktop) linkedUnit(ctx context.Context) string {
	id := d.systemctl(ctx, "show", "display-manager.service", "-p", "Id", "--value")
	if id == "" || id == "display-manager.service" || !unitRe.MatchString(id) {
		return ""
	}
	return id
}

// processRunning: some process named name runs (from /proc).
func processRunning(procRoot, name string) bool {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "comm"))
		if err == nil && strings.TrimSpace(string(b)) == name {
			return true
		}
	}
	return false
}

func (d *Desktop) haveXorg() bool {
	return d.lookPath("Xorg") || d.exists("/usr/lib/xorg/Xorg") || d.exists("/usr/libexec/Xorg")
}

// gather reads the machine (and tracks how long GNOME Shell has been gone).
func (d *Desktop) gather(ctx context.Context) HealthInputs {
	c := d.conf()
	in := HealthInputs{Full: d.Native && c.Full, WantOn: d.wantOn(), DMUnit: c.DMUnit, Fallback: d.Fallback()}
	in.KioskLeft = d.exists("/etc/systemd/system/nocapos-desktop.service")
	if b, err := d.readFile("/usr/sbin/policy-rc.d"); err == nil && strings.Contains(string(b), "NoCapOS install.sh") {
		in.ServicesHeld = true
	}
	if !in.Full || !in.WantOn {
		return in
	}
	drm := d.DRMDir
	if drm == "" {
		drm = "/sys/class/drm"
	}
	in.Display = DisplayConnected(drm)
	in.LinkedUnit = d.linkedUnit(ctx)
	in.DefaultTarget = d.systemctl(ctx, "get-default")
	in.DMActive = d.systemctl(ctx, "is-active", c.DMUnit)
	in.ShellRunning = processRunning(d.procRoot(), "gnome-shell")
	in.HaveShell = d.lookPath("gnome-shell")
	in.HaveSessions = true
	for p := range sessionFiles {
		in.HaveSessions = in.HaveSessions && d.exists(p)
	}
	in.HaveXorg = d.haveXorg()
	now := d.now()
	d.hs.mu.Lock()
	if in.ShellRunning || (in.DMActive != "active" && in.DMActive != "reloading") {
		d.hs.noShellSince = time.Time{}
	} else if d.hs.noShellSince.IsZero() {
		d.hs.noShellSince = now
	}
	if !d.hs.noShellSince.IsZero() {
		in.NoShellFor = now.Sub(d.hs.noShellSince)
	}
	d.hs.mu.Unlock()
	if (in.DMActive == "failed" || in.NoShellFor >= ShellGrace) && in.Display {
		in.Hints = JournalHints(d.journal(ctx, "-u", c.DMUnit, "-n", "300") + d.journal(ctx, "_COMM=gnome-shell", "-n", "200"))
	}
	return in
}

func (d *Desktop) journal(ctx context.Context, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, _ := d.Run(ctx, "journalctl", append([]string{"-b", "--no-pager", "-o", "short-monotonic"}, args...)...)
	return string(out)
}

// Health checks NoCap Desktop on the screen.
func (d *Desktop) Health(ctx context.Context) DesktopHealth {
	return EvaluateHealth(d.gather(ctx))
}

// unitFile finds a unit's file (for the display-manager.service link).
func (d *Desktop) unitFile(ctx context.Context, unit string) string {
	if p := d.systemctl(ctx, "show", "-p", "FragmentPath", "--value", unit); strings.HasPrefix(p, "/") && !strings.Contains(p, "..") {
		return p
	}
	for _, dir := range []string{"/usr/lib/systemd/system", "/lib/systemd/system", "/etc/systemd/system"} {
		if p := dir + "/" + unit; d.exists(p) {
			return p
		}
	}
	return ""
}

// linkDisplayManager makes graphical.target start unit.
func (d *Desktop) linkDisplayManager(ctx context.Context, unit string) error {
	p := d.unitFile(ctx, unit)
	if p == "" {
		return fmt.Errorf("%s isn't installed", unit)
	}
	if err := d.symlink(p, DisplayManagerLink); err != nil {
		return fmt.Errorf("display-manager.service: %w", err)
	}
	if unit == "gdm3.service" && d.exists("/usr/sbin/gdm3") && d.exists("/etc/X11") {
		_ = d.writeFile("/etc/X11/default-display-manager", []byte("/usr/sbin/gdm3\n"), 0o644)
	}
	_, _ = d.Run(ctx, "systemctl", "daemon-reload")
	return nil
}

// unlinkDisplayManager: off takes GDM's boot link away.
func (d *Desktop) unlinkDisplayManager(ctx context.Context, unit string) {
	if d.linkedUnit(ctx) == unit {
		_ = d.remove(DisplayManagerLink)
		_, _ = d.Run(ctx, "systemctl", "daemon-reload")
	}
}

// nextFallback is the stage after cur ("" when there's none left).
func nextFallback(cur string) string {
	switch cur {
	case FallbackNone:
		return FallbackXorg
	case FallbackXorg:
		return FallbackSoftware
	}
	return ""
}

// applyFallback switches the sign-in screen (and sessions) to stage.
func (d *Desktop) applyFallback(ctx context.Context, stage string) error {
	c := d.conf()
	wayland := "true"
	if stage == FallbackXorg || stage == FallbackSoftware {
		wayland = "false"
	}
	if c.GDMConf != "" {
		b, err := d.readFile(c.GDMConf)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := d.writeFile(c.GDMConf, []byte(SetINIKey(string(b), "daemon", "WaylandEnable", wayland)), 0o644); err != nil {
			return err
		}
	}
	env, _ := d.readFile("/etc/environment")
	newEnv := EnvironmentWithSoftwareRendering(string(env), stage == FallbackSoftware)
	if newEnv != string(env) {
		if err := d.writeFile("/etc/environment", []byte(newEnv), 0o644); err != nil {
			return err
		}
	}
	if stage == FallbackSoftware {
		// The system's own sign-in theme: a broken theme is one less suspect.
		if d.exists("/usr/share/gnome-shell/gnome-shell-theme.gresource") {
			_, _ = d.Run(ctx, "update-alternatives", "--set", "gdm-theme.gresource", "/usr/share/gnome-shell/gnome-shell-theme.gresource")
		}
	} else if d.exists("/usr/local/lib/nocapos/desktop/gdm-theme") {
		_, _ = d.Run(ctx, "/usr/local/lib/nocapos/desktop/gdm-theme")
	}
	if stage == FallbackNone {
		if err := d.remove(d.fallbackPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return d.writeFile(d.fallbackPath(), []byte(stage+"\n"), 0o644)
}

// EnvironmentWithSoftwareRendering is /etc/environment with NoCapOS's
// software rendering line on or off (other lines kept as they are).
func EnvironmentWithSoftwareRendering(env string, on bool) string {
	var out []string
	lines := strings.Split(strings.TrimRight(env, "\n"), "\n")
	if env == "" {
		lines = nil
	}
	skip := false
	for _, l := range lines {
		if l == swRenderMark {
			skip = true
			continue
		}
		if skip && strings.TrimSpace(l) == "LIBGL_ALWAYS_SOFTWARE=1" {
			skip = false
			continue
		}
		skip = false
		out = append(out, l)
	}
	if on {
		out = append(out, swRenderMark, "LIBGL_ALWAYS_SOFTWARE=1")
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// Ensure keeps NoCap Desktop on the screen (WatchSession runs it every few
// seconds): what it fixed, if anything.
func (d *Desktop) Ensure(ctx context.Context) []string {
	if !d.Native {
		return nil
	}
	now := d.now()
	d.hs.mu.Lock()
	if d.hs.started.IsZero() {
		d.hs.started = now
	}
	settled := now.Sub(d.hs.started) >= startDelay
	d.hs.mu.Unlock()
	in := d.gather(ctx)
	h := EvaluateHealth(in)
	if h.Status != "broken" {
		return nil
	}
	c := d.conf()
	var did []string
	if h.Has(ProblemNotAtBoot) {
		if err := d.linkDisplayManager(ctx, c.DMUnit); err == nil {
			did = append(did, "boot link")
		}
	}
	if h.Has(ProblemNotGraphical) {
		_, _ = d.Run(ctx, "systemctl", "set-default", "graphical.target")
		did = append(did, "graphical target")
	}
	if h.Has(ProblemServicesHeld) {
		_ = d.remove("/usr/sbin/policy-rc.d")
		did = append(did, "service hold")
	}
	if !settled {
		return did
	}
	// Nothing on the screen: start the sign-in screen (never over someone
	// signed in on the console).
	if h.Has(ProblemDMStopped) || h.Has(ProblemDMFailed) {
		if _, _, busy := d.seatProps(ctx); !busy {
			_, _ = d.Run(ctx, "systemctl", "reset-failed", c.DMUnit)
			_, _ = d.Run(ctx, "systemctl", "--no-block", "start", c.DMUnit)
			did = append(did, "started "+c.DMUnit)
		}
		return did
	}
	// The sign-in screen keeps failing: one step down the fallback ladder.
	if h.Has(ProblemNoShell) && in.Display {
		d.hs.mu.Lock()
		due := d.hs.lastFallback.IsZero() || now.Sub(d.hs.lastFallback) >= autoFallbackEvery
		if due {
			d.hs.lastFallback = now
		}
		d.hs.mu.Unlock()
		next := nextFallback(in.Fallback)
		if due && next != "" && d.applyFallback(ctx, next) == nil {
			_, _ = d.Run(ctx, "systemctl", "reset-failed", c.DMUnit)
			_, _ = d.Run(ctx, "systemctl", "restart", c.DMUnit)
			d.resetShellTimer()
			did = append(did, "fallback "+next)
		}
	}
	return did
}

func (d *Desktop) resetShellTimer() {
	d.hs.mu.Lock()
	d.hs.noShellSince = time.Time{}
	d.hs.mu.Unlock()
}

// RepairResult is what Repair did and how it looks now.
type RepairResult struct {
	Actions []string      `json:"actions"`
	Health  DesktopHealth `json:"health"`
}

// Repair puts NoCap Desktop back on the screen: the boot link, the
// graphical target, NoCapOS's session files, settings databases, leftovers
// of the old web full screen, and, when the sign-in screen wasn't coming
// up, the next fallback (Xorg, then software rendering). Then the sign-in
// screen restarts (whoever is signed in on the screen is signed out).
func (d *Desktop) Repair(ctx context.Context) (RepairResult, error) {
	c := d.conf()
	if !d.Native || !c.Full {
		return RepairResult{}, ErrDesktopUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	in := d.gather(ctx)
	h := EvaluateHealth(in)
	var did []string
	note := func(s string) { did = append(did, s) }
	if in.KioskLeft {
		_, _ = d.Run(ctx, "systemctl", "disable", "--now", "nocapos-desktop.service")
		_ = d.remove("/etc/systemd/system/nocapos-desktop.service")
		note("removed the old web full screen")
	}
	if in.ServicesHeld {
		_ = d.remove("/usr/sbin/policy-rc.d")
		note("released the installer's service hold")
	}
	if err := d.linkDisplayManager(ctx, c.DMUnit); err != nil {
		return RepairResult{Actions: did, Health: h}, err
	}
	note("sign-in screen starts at boot")
	if _, err := d.Run(ctx, "systemctl", "set-default", "graphical.target"); err == nil {
		note("graphical boot")
	}
	for p, body := range sessionFiles {
		if b, err := d.readFile(p); err != nil || string(b) != body {
			if err := d.writeFile(p, []byte(body), 0o644); err == nil {
				note("session file " + path.Base(path.Dir(p)))
			}
		}
	}
	if d.lookPath("glib-compile-schemas") {
		_, _ = d.Run(ctx, "glib-compile-schemas", "/usr/share/glib-2.0/schemas")
	}
	if d.lookPath("dconf") {
		_, _ = d.Run(ctx, "dconf", "update")
	}
	if h.Has(ProblemNoShell) || h.Has(ProblemDMFailed) || len(in.Hints) > 0 {
		if next := nextFallback(in.Fallback); next != "" {
			if err := d.applyFallback(ctx, next); err == nil {
				note("fallback " + next)
			}
		}
	}
	_ = d.writeFile(DesktopModePath, []byte(ModeFull+"\n"), 0o644)
	_, _ = d.Run(ctx, "systemctl", "reset-failed", c.DMUnit)
	if out, err := d.Run(ctx, "systemctl", "restart", c.DMUnit); err != nil {
		return RepairResult{Actions: did, Health: d.Health(ctx)}, fmt.Errorf("systemctl restart %s: %s", c.DMUnit, strings.TrimSpace(string(out)))
	}
	note("restarted " + c.DMUnit)
	d.resetShellTimer()
	return RepairResult{Actions: did, Health: d.Health(ctx)}, nil
}

// ResetFallback goes back to the normal setup (Wayland, hardware rendering,
// NoCapOS's sign-in theme) and restarts the sign-in screen.
func (d *Desktop) ResetFallback(ctx context.Context) error {
	c := d.conf()
	if !d.Native || !c.Full {
		return ErrDesktopUnavailable
	}
	if err := d.applyFallback(ctx, FallbackNone); err != nil {
		return err
	}
	_, _ = d.Run(ctx, "systemctl", "restart", c.DMUnit)
	d.resetShellTimer()
	return nil
}

// ---- diagnostics ----

const diagSectionMax = 64 << 10

func tail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[len(s)-max:]
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return "[...]\n" + s
}

var gpuClassRe = regexp.MustCompile(`(?i)VGA|3D controller|Display controller`)

// gpuLines keeps lspci -nnk's graphics devices (and their driver lines).
func gpuLines(lspci string) string {
	var out []string
	keep := false
	for _, l := range strings.Split(lspci, "\n") {
		if l == "" {
			continue
		}
		if !strings.HasPrefix(l, "\t") && !strings.HasPrefix(l, " ") {
			keep = gpuClassRe.MatchString(l)
		}
		if keep {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// Diagnostics is the text bundle the owner can read or copy: health, the
// sign-in screen's and GNOME Shell's journal, sessions, the GPU, screens.
func (d *Desktop) Diagnostics(ctx context.Context) string {
	c := d.conf()
	unit := c.DMUnit
	if unit == "" {
		unit = "gdm.service"
	}
	var b strings.Builder
	section := func(title, body string) {
		fmt.Fprintf(&b, "===== %s =====\n%s\n\n", title, strings.TrimRight(tail(body, diagSectionMax), "\n"))
	}
	run := func(name string, args ...string) string {
		if !d.lookPath(name) && !strings.HasPrefix(name, "/") {
			return "(" + name + " isn't installed)"
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, err := d.Run(cctx, name, args...)
		s := string(out)
		if err != nil && strings.TrimSpace(s) == "" {
			s = "(" + err.Error() + ")"
		}
		return s
	}
	file := func(p string, lines int) string {
		bb, err := d.readFile(p)
		if err != nil {
			return "(" + p + ": not there)"
		}
		ls := strings.Split(strings.TrimRight(string(bb), "\n"), "\n")
		if lines > 0 && len(ls) > lines {
			ls = append([]string{"[...]"}, ls[len(ls)-lines:]...)
		}
		return strings.Join(ls, "\n")
	}
	in := d.gather(ctx)
	h := EvaluateHealth(in)
	var hs strings.Builder
	fmt.Fprintf(&hs, "status: %s\nfallback: %s\n", h.Status, orNone(h.Fallback))
	for _, p := range h.Problems {
		fmt.Fprintf(&hs, "problem: %s %s\n", p.Code, p.Detail)
	}
	fmt.Fprintf(&hs, "display manager: %s (linked: %s, %s)\ndefault target: %s\nmonitor connected: %v\ngnome-shell running: %v\n",
		unit, orNone(in.LinkedUnit), orNone(in.DMActive), orNone(in.DefaultTarget), in.Display, in.ShellRunning)
	section("NoCap Desktop health", hs.String())
	logs := run("journalctl", "-b", "--no-pager", "-o", "short-monotonic", "-u", unit, "-n", "300")
	shell := run("journalctl", "-b", "--no-pager", "-o", "short-monotonic", "_COMM=gnome-shell", "-n", "300")
	if hints := JournalHints(logs + "\n" + shell); len(hints) > 0 {
		section("Likely causes (from the journal)", strings.Join(hints, "\n"))
	}
	section("NoCapOS desktop settings", file(DesktopConfPath, 0)+"\nmode: "+strings.TrimSpace(file(DesktopModePath, 0)))
	if c.GDMConf != "" {
		section("GDM settings "+c.GDMConf, file(c.GDMConf, 0))
	}
	section("systemctl status "+unit, run("systemctl", "status", unit, "--no-pager", "-l", "-n", "0"))
	section("Boot target and display manager", run("systemctl", "get-default")+run("systemctl", "show", "display-manager.service", "-p", "Id", "-p", "FragmentPath", "-p", "LoadState"))
	section("journalctl -b -u "+unit, logs)
	section("GNOME Shell (this boot)", shell)
	section("GNOME session (this boot)", run("journalctl", "-b", "--no-pager", "-o", "short-monotonic", "_COMM=gnome-session-binary", "-n", "150"))
	section("Xorg (this boot)", run("journalctl", "-b", "--no-pager", "-o", "short-monotonic", "_COMM=Xorg", "-n", "150"))
	for _, p := range []string{"/var/lib/gdm3/.local/share/xorg/Xorg.0.log", "/var/lib/gdm/.local/share/xorg/Xorg.0.log", "/var/log/Xorg.0.log"} {
		if d.exists(p) {
			section(p, file(p, 150))
		}
	}
	section("Kernel graphics messages", run("journalctl", "-b", "-k", "--no-pager", "-o", "short-monotonic", "-n", "150", "--grep", "drm|gpu|vc4|v3d|amdgpu|radeon|i915|nouveau|nvidia|simpledrm|virtio|qxl|bochs|vmwgfx|firmware|framebuffer"))
	section("Sessions", run("loginctl", "list-sessions", "--no-pager")+"\n"+run("loginctl", "show-seat", "seat0", "--no-pager"))
	gpu := "(lspci isn't installed)"
	if d.lookPath("lspci") {
		gpu = gpuLines(run("lspci", "-nnk"))
	}
	section("Graphics card", gpu)
	drm := d.DRMDir
	if drm == "" {
		drm = "/sys/class/drm"
	}
	var screens []string
	matches, _ := filepath.Glob(filepath.Join(drm, "*", "status"))
	for _, m := range matches {
		if st, err := os.ReadFile(m); err == nil {
			screens = append(screens, filepath.Base(filepath.Dir(m))+": "+strings.TrimSpace(string(st)))
		}
	}
	if len(screens) == 0 {
		screens = []string{"(no graphics outputs: no KMS driver is loaded)"}
	}
	section("Screens", strings.Join(screens, "\n"))
	section("Kernel command line", file("/proc/cmdline", 0))
	section("GNOME Shell version", run("gnome-shell", "--version"))
	var sess []string
	for p := range sessionFiles {
		state := "missing"
		if d.exists(p) {
			state = "present"
		}
		sess = append(sess, p+": "+state)
	}
	sort.Strings(sess)
	section("NoCapOS session files", strings.Join(sess, "\n"))
	mem := file("/proc/meminfo", 0)
	section("Memory", mem[:min(400, len(mem))])
	section("System", run("uname", "-a"))
	return b.String()
}
