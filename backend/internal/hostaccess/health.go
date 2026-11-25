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
// "greetd is enabled" isn't the same as "the screen shows NoCap Desktop".
// Each of these ends in a black screen (or no sign-in screen), and Health
// finds them:
//
//   - display-manager.service doesn't point at greetd: graphical.target
//     starts whatever that link names.
//   - the default target isn't graphical.target.
//   - greetd runs but no compositor (labwc) does: the sign-in screen or the
//     desktop couldn't start on this graphics hardware.
//   - labwc, NoCap Shell (its programs and pages), the session file or
//     greetd's settings for NoCap's sign-in screen are missing, or the old
//     web full screen or the installer's service hold was left behind.
//
// alfad checks every few seconds (Ensure): it puts the boot link and the
// target back, starts the sign-in screen when nothing is on the screen, and
// when the screen keeps failing it switches to software rendering (labwc
// with WLR_RENDERER=pixman, which every machine can draw). Still NoCap
// Desktop, just drawn by the processor.

// DisplayManagerLink is what graphical.target starts.
const DisplayManagerLink = "/etc/systemd/system/display-manager.service"

// Fallback stages (the file desktop-fallback in the data folder).
const (
	FallbackNone     = ""
	FallbackSoftware = "software"
)

// FallbackFileName is in the data folder.
const FallbackFileName = "desktop-fallback"

// SoftwareRenderingFlag makes the session scripts start labwc with
// software rendering (nocap-session, nocap-greeter-session).
const SoftwareRenderingFlag = "/etc/nocapos/desktop-software-rendering"

// NoCap Shell's pieces on the machine.
const (
	ShellLibDir     = "/usr/libexec/nocapos"
	ShellUIDir      = "/usr/share/nocapos/shell"
	GreetdConfPath  = "/etc/greetd/config.toml"
	DefaultDMUnit   = "greetd.service"
	greeterCmd      = ShellLibDir + "/nocap-greeter-session"
	sessionCmd      = ShellLibDir + "/nocap-session"
	sessionFilePath = "/usr/share/wayland-sessions/nocapos.desktop"
)

// ShellGrace: how long greetd may run without a compositor before it
// counts as a black screen.
const ShellGrace = 45 * time.Second

// autoFallbackEvery: the least time between two automatic fallback steps.
const autoFallbackEvery = 3 * time.Minute

// startDelay: alfad starts with the machine; greetd may still be on its way.
const startDelay = 60 * time.Second

// NoCapOS's session file (install.sh writes it; Repair puts it back).
var sessionFiles = map[string]string{sessionFilePath: SessionDesktop()}

// SessionDesktop is lib.sh's render_session_desktop.
func SessionDesktop() string {
	return "[Desktop Entry]\n# The NoCapOS desktop (written by NoCapOS's install.sh).\nName=NoCapOS\nComment=The NoCapOS desktop\nExec=" +
		sessionCmd + "\nTryExec=" + sessionCmd + "\nType=Application\nDesktopNames=NoCap;wlroots\n"
}

const swRenderMark = "# NoCap Desktop software rendering (NoCapOS repair)"

// Problem codes.
const (
	ProblemNotAtBoot    = "not_at_boot"
	ProblemNotGraphical = "not_graphical"
	ProblemDMStopped    = "dm_stopped"
	ProblemDMFailed     = "dm_failed"
	ProblemNoShell      = "no_shell"      // the sign-in service runs, nothing is on the screen
	ProblemNoCompositor = "no_compositor" // labwc isn't installed
	ProblemNoNocapShell = "no_nocap_shell"
	ProblemNoGreeter    = "no_greeter" // greetd isn't set to show NoCap's sign-in screen
	ProblemNoSession    = "no_session"
	ProblemNoXwayland   = "no_xwayland" // works; older X11 apps don't open
	ProblemKioskLeft    = "kiosk_left"
	ProblemServicesHeld = "services_held"
	// Another sign-in service (the earlier desktop's) still runs.
	ProblemOtherDM = "other_dm"
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
	// Fallback in use: "" or software.
	Fallback string `json:"fallback,omitempty"`
	// RestartNeeded: someone is still signed in on the earlier desktop;
	// the screen moves to NoCap Desktop when the machine restarts.
	RestartNeeded   bool     `json:"restart_needed,omitempty"`
	OldSessionUsers []string `json:"old_session_users,omitempty"`
}

// HealthInputs is everything Health looks at, gathered from the machine.
type HealthInputs struct {
	Full           bool // install.sh set the desktop up
	WantOn         bool // not switched off in Settings
	DMUnit         string
	LinkedUnit     string // what display-manager.service is ("" = nothing)
	DefaultTarget  string
	DMActive       string // systemctl is-active
	Display        bool   // a monitor is plugged in
	ShellRunning   bool   // a compositor runs (the sign-in screen's or a session's)
	NoShellFor     time.Duration
	HaveCompositor bool
	HaveNocapShell bool
	HaveGreeter    bool
	HaveSessions   bool
	HaveXwayland   bool
	KioskLeft      bool
	ServicesHeld   bool
	Fallback       string
	Hints          []string // from the journal (JournalHints)
	// OtherDMs: other sign-in services running; OldUsers: people signed in
	// through them.
	OtherDMs []string
	OldUsers []string
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
	if !in.HaveCompositor {
		add(ProblemNoCompositor, "")
	}
	if !in.HaveNocapShell {
		add(ProblemNoNocapShell, "")
	}
	if !in.HaveGreeter {
		add(ProblemNoGreeter, GreetdConfPath)
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
	if len(in.OtherDMs) > 0 {
		add(ProblemOtherDM, strings.Join(in.OtherDMs, ", "))
		if len(in.OldUsers) > 0 {
			h.RestartNeeded = true
			h.OldSessionUsers = in.OldUsers
		}
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
	if !in.HaveXwayland {
		add(ProblemNoXwayland, "")
	}
	switch {
	case len(h.Problems) == 1 && h.Problems[0].Code == ProblemNoXwayland:
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
	{"gpu", regexp.MustCompile(`(?i)failed to create (backend|renderer|allocator)|no gpus? found|failed to open gpu|could not open device|failed to (initialize|create) (renderer|egl|gbm)|eglInitialize failed|no suitable egl|no DRM device|drm.*(permission denied|failed)`)},
	{"seat", regexp.MustCompile(`(?i)libseat|could not (open|take) (seat|session)|failed to open seat|no seat`)},
	{"greeter_crash", regexp.MustCompile(`(?i)greetd.*(error|failed)|nocap-greeter-session.*(failed|exited)|greeter.*(crash|exited|died)`)},
	{"gave_up", regexp.MustCompile(`(?i)start request repeated too quickly|too many (restarts|failures)|giving up`)},
	{"shell_crash", regexp.MustCompile(`(?i)(labwc|nocap-shell|WebKitWebProcess).*(segfault|segmentation fault|core dumped|aborted|dumped core|trap)|traps: (labwc|nocap-shell)`)},
	{"memory", regexp.MustCompile(`(?i)out of memory|oom-kill|killed process \d+ \((labwc|nocap-shell|WebKitWebProces)`)},
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
	// hardwareSince: since when the compositor has drawn with the graphics
	// card during a hardware retry.
	hardwareSince time.Time
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

// Fallback is the fallback stage in use. (Earlier versions' "xorg" stage
// doesn't exist any more: the screen starts over on the normal setup.)
func (d *Desktop) Fallback() string {
	return d.fallbackRecord().stage
}

// fallbackRecord is what the desktop-fallback file says: the stage, the
// boot it was chosen on and how many boots in a row the graphics card
// failed. A stage of "hardware-retry" means the graphics card is being
// tried again after a software fallback (no fallback in use).
type fallbackRecord struct {
	stage string
	boot  string
	tries int
}

const stageHardwareRetry = "hardware-retry"

// maxHardwareRetries: after this many boots on which the graphics card
// failed again, software rendering stays (until Settings resets it).
const maxHardwareRetries = 3

func (d *Desktop) fallbackRecord() fallbackRecord {
	var r fallbackRecord
	b, err := d.readFile(d.fallbackPath())
	if err != nil {
		return r
	}
	for i, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case i == 0 && l == FallbackSoftware:
			r.stage = FallbackSoftware
		case strings.HasPrefix(l, "boot="):
			r.boot = strings.TrimPrefix(l, "boot=")
		case strings.HasPrefix(l, "tries="):
			r.tries, _ = strconv.Atoi(strings.TrimPrefix(l, "tries="))
		}
	}
	return r
}

func (r fallbackRecord) text(stage string) string {
	return fmt.Sprintf("%s\nboot=%s\ntries=%d\n", stage, r.boot, r.tries)
}

// bootID tells this boot from the earlier ones ("" when unknown).
func (d *Desktop) bootID() string {
	b, err := d.readFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(b))
	if len(id) > 64 || strings.ContainsAny(id, " \n=") {
		return ""
	}
	return id
}

// retryHardware: software rendering chosen on an earlier boot is taken back
// once per boot (up to maxHardwareRetries boots that fail again), so a
// machine whose graphics card works isn't left drawing with the processor
// after one bad start (a slow first boot or first sign-in, a driver an
// update has fixed since). Nothing restarts: the next sign-in screen or
// sign-in uses the graphics card, the session scripts still fall back by
// themselves when it can't start, and Ensure falls back again (counting
// the try) when the screen stays empty.
func (d *Desktop) retryHardware(ctx context.Context) bool {
	r := d.fallbackRecord()
	boot := d.bootID()
	if r.stage != FallbackSoftware || boot == "" || r.boot == boot || r.tries >= maxHardwareRetries {
		return false
	}
	if err := d.setRendering(false); err != nil {
		return false
	}
	r.boot = boot
	return d.writeFile(d.fallbackPath(), []byte(r.text(stageHardwareRetry)), 0o644) == nil
}

// hardwareProvenAfter: how long the compositor must have drawn with the
// graphics card before a hardware retry counts as a success.
const hardwareProvenAfter = 10 * time.Minute

// hardwareProven: a hardware retry that has worked for a while ends it (the
// failed boots are forgotten). Reports whether it did.
func (d *Desktop) hardwareProven(now time.Time) bool {
	if d.fallbackRecord().stage != FallbackNone {
		return false
	}
	b, err := d.readFile(d.fallbackPath())
	if err != nil || !strings.HasPrefix(string(b), stageHardwareRetry) {
		return false
	}
	d.hs.mu.Lock()
	defer d.hs.mu.Unlock()
	if !hardwareCompositor(d.procRoot()) {
		d.hs.hardwareSince = time.Time{}
		return false
	}
	if d.hs.hardwareSince.IsZero() {
		d.hs.hardwareSince = now
	}
	if now.Sub(d.hs.hardwareSince) < hardwareProvenAfter {
		return false
	}
	return d.remove(d.fallbackPath()) == nil
}

// hardwareCompositor: a compositor (labwc) runs, drawing with the graphics
// card (not started with WLR_RENDERER=pixman).
func hardwareCompositor(procRoot string) bool {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "comm"))
		if err != nil || strings.TrimSpace(string(b)) != "labwc" {
			continue
		}
		env, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "environ"))
		if err != nil {
			continue
		}
		soft := false
		for _, kv := range strings.Split(string(env), "\x00") {
			if kv == "WLR_RENDERER=pixman" {
				soft = true
			}
		}
		if !soft {
			return true
		}
	}
	return false
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

func (d *Desktop) haveCompositor() bool {
	return d.lookPath("labwc") || d.exists(ShellLibDir+"/labwc")
}

func (d *Desktop) haveNocapShell() bool {
	for _, p := range []string{ShellLibDir + "/nocap-shell", ShellLibDir + "/nocap-lock", sessionCmd, greeterCmd, ShellUIDir + "/shell.html"} {
		if !d.exists(p) {
			return false
		}
	}
	return true
}

func (d *Desktop) greetdConf() string {
	c := d.conf()
	if c.GreetdConf != "" {
		return c.GreetdConf
	}
	return GreetdConfPath
}

func (d *Desktop) haveGreeter() bool {
	b, err := d.readFile(d.greetdConf())
	return err == nil && strings.Contains(string(b), greeterCmd)
}

// gather reads the machine (and tracks how long the screen has been empty).
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
	in.ShellRunning = processRunning(d.procRoot(), "labwc")
	in.HaveCompositor = d.haveCompositor()
	in.HaveNocapShell = d.haveNocapShell()
	in.HaveGreeter = d.haveGreeter()
	in.HaveSessions = true
	for p := range sessionFiles {
		in.HaveSessions = in.HaveSessions && d.exists(p)
	}
	in.HaveXwayland = d.lookPath("Xwayland")
	if in.OtherDMs = d.otherDMs(ctx, c.DMUnit); len(in.OtherDMs) > 0 {
		in.OldUsers = d.oldDesktopUsers(ctx)
	}
	now := d.now()
	d.hs.mu.Lock()
	// greetd may run something else full screen on purpose (the USB stick's
	// setup screen runs the installer there): no NoCap Desktop then isn't a
	// black screen, and restarting greetd would throw that program away.
	if in.ShellRunning || !in.HaveGreeter || (in.DMActive != "active" && in.DMActive != "reloading") {
		d.hs.noShellSince = time.Time{}
	} else if d.hs.noShellSince.IsZero() {
		d.hs.noShellSince = now
	}
	if !d.hs.noShellSince.IsZero() {
		in.NoShellFor = now.Sub(d.hs.noShellSince)
	}
	d.hs.mu.Unlock()
	if (in.DMActive == "failed" || in.NoShellFor >= ShellGrace) && in.Display {
		in.Hints = JournalHints(d.journal(ctx, "-u", c.DMUnit, "-n", "300") + d.journal(ctx, "_COMM=labwc", "-n", "200"))
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
	if unit == DefaultDMUnit && d.exists("/etc/X11") {
		_ = d.writeFile("/etc/X11/default-display-manager", []byte("/usr/sbin/greetd\n"), 0o644)
	}
	_, _ = d.Run(ctx, "systemctl", "daemon-reload")
	return nil
}

// unlinkDisplayManager: off takes the sign-in service's boot link away.
func (d *Desktop) unlinkDisplayManager(ctx context.Context, unit string) {
	if d.linkedUnit(ctx) == unit {
		_ = d.remove(DisplayManagerLink)
		_, _ = d.Run(ctx, "systemctl", "daemon-reload")
	}
}

// nextFallback is the stage after cur ("" when there's none left).
func nextFallback(cur string) string {
	if cur == FallbackNone {
		return FallbackSoftware
	}
	return ""
}

// applyFallback switches the sign-in screen and sessions to stage. Software
// rendering counts one more boot on which the graphics card failed.
func (d *Desktop) applyFallback(ctx context.Context, stage string) error {
	if err := d.setRendering(stage == FallbackSoftware); err != nil {
		return err
	}
	if stage == FallbackNone {
		if err := d.remove(d.fallbackPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	r := d.fallbackRecord()
	r.boot = d.bootID()
	r.tries++
	return d.writeFile(d.fallbackPath(), []byte(r.text(stage)), 0o644)
}

// setRendering puts the software rendering switches (the flag the session
// scripts and NoCap Shell read, /etc/environment's line) on or off.
func (d *Desktop) setRendering(soft bool) error {
	if soft {
		if err := d.writeFile(SoftwareRenderingFlag, []byte("# NoCap Desktop draws with the processor here (NoCapOS repair).\n"), 0o644); err != nil {
			return err
		}
	} else if err := d.remove(SoftwareRenderingFlag); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	env, _ := d.readFile("/etc/environment")
	newEnv := EnvironmentWithSoftwareRendering(string(env), soft)
	if newEnv != string(env) {
		if err := d.writeFile("/etc/environment", []byte(newEnv), 0o644); err != nil {
			return err
		}
	}
	return nil
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
	var did []string
	if d.retryHardware(ctx) {
		did = append(did, "graphics card retried")
	}
	in := d.gather(ctx)
	h := EvaluateHealth(in)
	if h.Status != "broken" {
		if h.Status == "ok" && d.hardwareProven(now) {
			did = append(did, "graphics card works")
		}
		return did
	}
	c := d.conf()
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
	// The earlier sign-in service still runs (an update left it for the
	// next restart, or something started it). Someone signed in through
	// it: leave the screen alone until the restart, and never start greetd
	// next to it. Only its sign-in screen: switch to NoCap Desktop now.
	if h.Has(ProblemOtherDM) {
		if len(in.OldUsers) > 0 {
			return did
		}
		did = append(did, d.retireOldDMs(ctx, in.OtherDMs, true)...)
		d.ensureGreetdDropIn(ctx)
		_, _ = d.Run(ctx, "systemctl", "reset-failed", c.DMUnit)
		verb := "start"
		if in.DMActive == "active" || in.DMActive == "reloading" {
			verb = "restart" // it lost the screen to the other one
		}
		_, _ = d.Run(ctx, "systemctl", "--no-block", verb, c.DMUnit)
		d.resetShellTimer()
		return append(did, verb+"ed "+c.DMUnit)
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
	// The screen stays empty: software rendering.
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
	// RestartNeeded: someone is signed in on the earlier desktop, so the
	// screen wasn't restarted; NoCap Desktop starts with the next restart.
	RestartNeeded   bool     `json:"restart_needed,omitempty"`
	OldSessionUsers []string `json:"old_session_users,omitempty"`
}

// Repair puts NoCap Desktop back on the screen: the boot link, the
// graphical target, the session file, greetd's settings for NoCap's sign-in
// screen, leftovers of the old web full screen, and, when the screen
// wasn't coming up, software rendering. An earlier sign-in service that
// still runs is stopped and switched off first, unless someone is signed
// in through it: then nothing on the screen changes and the result says a
// restart is needed. Otherwise the sign-in screen restarts (whoever is
// signed in on NoCap Desktop is signed out).
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
				note("session file")
			}
		}
	}
	if !in.HaveGreeter {
		conf := d.greetdConf()
		old, _ := d.readFile(conf)
		if err := d.writeFile(conf, []byte(GreetdConfig(d.greeterUser(), GreetdAutologin(string(old)))), 0o644); err == nil {
			note("sign-in screen settings")
		}
	}
	if h.Has(ProblemNoShell) || h.Has(ProblemDMFailed) || len(in.Hints) > 0 {
		if next := nextFallback(in.Fallback); next != "" {
			if err := d.applyFallback(ctx, next); err == nil {
				note("fallback " + next)
			}
		}
	}
	if d.ensureGreetdDropIn(ctx) {
		note("sign-in service settings")
	}
	_ = d.writeFile(DesktopModePath, []byte(ModeFull+"\n"), 0o644)
	if len(in.OtherDMs) > 0 {
		if len(in.OldUsers) > 0 {
			// Never sign anyone out: the earlier sign-in service is off from
			// the next restart, and NoCap Desktop starts then.
			d.retireOldDMs(ctx, in.OtherDMs, false)
			note("the earlier sign-in screen is off from the next restart")
			return RepairResult{Actions: did, Health: d.Health(ctx), RestartNeeded: true, OldSessionUsers: in.OldUsers}, nil
		}
		did = append(did, d.retireOldDMs(ctx, in.OtherDMs, true)...)
	}
	_, _ = d.Run(ctx, "systemctl", "reset-failed", c.DMUnit)
	if out, err := d.Run(ctx, "systemctl", "restart", c.DMUnit); err != nil {
		return RepairResult{Actions: did, Health: d.Health(ctx)}, fmt.Errorf("systemctl restart %s: %s", c.DMUnit, strings.TrimSpace(string(out)))
	}
	note("restarted " + c.DMUnit)
	d.resetShellTimer()
	return RepairResult{Actions: did, Health: d.Health(ctx)}, nil
}

// ResetFallback goes back to the normal setup (hardware rendering) and
// restarts the sign-in screen.
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
// sign-in service's, compositor's and NoCap Shell's journal, sessions, the
// GPU, screens.
func (d *Desktop) Diagnostics(ctx context.Context) string {
	c := d.conf()
	unit := c.DMUnit
	if unit == "" {
		unit = DefaultDMUnit
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
	if rec, err := d.readFile(d.fallbackPath()); err == nil {
		fmt.Fprintf(&hs, "fallback record: %s\n", strings.Join(strings.Fields(string(rec)), " "))
	}
	for _, p := range h.Problems {
		fmt.Fprintf(&hs, "problem: %s %s\n", p.Code, p.Detail)
	}
	fmt.Fprintf(&hs, "sign-in service: %s (linked: %s, %s)\ndefault target: %s\nmonitor connected: %v\ncompositor running: %v\n",
		unit, orNone(in.LinkedUnit), orNone(in.DMActive), orNone(in.DefaultTarget), in.Display, in.ShellRunning)
	section("NoCap Desktop health", hs.String())
	logs := run("journalctl", "-b", "--no-pager", "-o", "short-monotonic", "-u", unit, "-n", "300")
	comp := run("journalctl", "-b", "--no-pager", "-o", "short-monotonic", "_COMM=labwc", "-n", "300")
	shellLog := run("journalctl", "-b", "--no-pager", "-o", "short-monotonic", "_COMM=nocap-shell", "-n", "200")
	if hints := JournalHints(logs + "\n" + comp + "\n" + shellLog); len(hints) > 0 {
		section("Likely causes (from the journal)", strings.Join(hints, "\n"))
	}
	section("NoCapOS desktop settings", file(DesktopConfPath, 0)+"\nmode: "+strings.TrimSpace(file(DesktopModePath, 0)))
	section("Sign-in service settings "+d.greetdConf(), file(d.greetdConf(), 0))
	section("systemctl status "+unit, run("systemctl", "status", unit, "--no-pager", "-l", "-n", "0"))
	section("Boot target and display manager", run("systemctl", "get-default")+run("systemctl", "show", "display-manager.service", "-p", "Id", "-p", "FragmentPath", "-p", "LoadState"))
	section("journalctl -b -u "+unit, logs)
	section("Compositor (this boot)", comp)
	section("NoCap Shell (this boot)", shellLog)
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
	section("Versions", run("labwc", "--version")+run(ShellLibDir+"/nocap-shell", "--version"))
	var parts []string
	for _, p := range []string{sessionFilePath, sessionCmd, greeterCmd, ShellLibDir + "/nocap-shell", ShellLibDir + "/nocap-lock", ShellUIDir + "/shell.html"} {
		state := "missing"
		if d.exists(p) {
			state = "present"
		}
		parts = append(parts, p+": "+state)
	}
	section("NoCap Desktop files", strings.Join(parts, "\n"))
	mem := file("/proc/meminfo", 0)
	section("Memory", mem[:min(400, len(mem))])
	section("System", run("uname", "-a"))
	return b.String()
}
