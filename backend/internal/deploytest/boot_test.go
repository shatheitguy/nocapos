package deploytest

// The silent boot: the GRUB settings NoCapOS writes (rendered by lib.sh),
// nocap-boot against fake machines (an update from an earlier version, the
// Show boot details switch, a failing update-grub rolled back, a Raspberry
// Pi, the early graphics driver), the sign-in screen's hand-off units and
// the splash theme's files. testdata/boot-trixie.sh runs the same against
// Debian's real update-grub scripts and Plymouth.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func deployPath(t *testing.T, parts ...string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join(append([]string{"..", "..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var quietTokens = []string{"quiet", "splash", "loglevel=3", "rd.systemd.show_status=false", "systemd.show_status=false", "rd.udev.log_level=3", "vt.global_cursor_default=0"}

// sourceDropin: GRUB_CMDLINE_LINUX_DEFAULT after /etc/default/grub (with
// the given value) and the rendered drop-in are read, as update-grub does.
func sourceDropin(t *testing.T, shell, mode, cmdline string, times int) string {
	t.Helper()
	dropin := lib(t, "", "render_grub_dropin", mode)
	f := filepath.Join(t.TempDir(), "nocapos.cfg")
	if err := os.WriteFile(f, []byte(dropin), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "GRUB_CMDLINE_LINUX_DEFAULT='" + cmdline + "'\n" + strings.Repeat(". '"+filepath.ToSlash(f)+"'\n", times) +
		`printf '%s|%s|%s|%s|%s\n' "$GRUB_CMDLINE_LINUX_DEFAULT" "$GRUB_TIMEOUT_STYLE" "$GRUB_TIMEOUT" "$GRUB_BACKGROUND" "$GRUB_GFXPAYLOAD_LINUX"` + "\n" +
		`case $- in *f*) echo "glob left off" ;; esac` + "\n"
	out, err := exec.Command(shell, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v: %s", shell, err, out)
	}
	return strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n"))
}

func TestGrubDropin(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	shells := []string{bash}
	if dash, err := exec.LookPath("dash"); err == nil {
		shells = append(shells, dash) // what update-grub reads it with on Debian
	}
	quiet := strings.Join(quietTokens, " ") + " logo.nologo"
	for _, sh := range shells {
		// An install from the 0.13/0.14 ISO: Debian's "quiet" plus the tokens
		// earlier installers added to /etc/default/grub.
		got := sourceDropin(t, sh, "silent", "quiet splash loglevel=3 logo.nologo resume=UUID=abc", 1)
		want := "resume=UUID=abc " + quiet + "|hidden|0|/usr/share/grub/themes/nocapos/boot.png|keep"
		if got != want {
			t.Errorf("%s silent:\n got %q\nwant %q", sh, got, want)
		}
		// Read twice (or after another update): still each token once.
		if again := sourceDropin(t, sh, "silent", "quiet splash loglevel=3 logo.nologo resume=UUID=abc", 2); again != want {
			t.Errorf("%s twice: %q", sh, again)
		}
		if got := sourceDropin(t, sh, "silent", "", 1); got != quiet+"|hidden|0|/usr/share/grub/themes/nocapos/boot.png|keep" {
			t.Errorf("%s empty line: %q", sh, got)
		}
		// Show boot details: every quiet token out, the rest kept; the menu shows.
		if got := sourceDropin(t, sh, "details", "quiet splash loglevel=3 nvidia-drm.modeset=1", 1); got != "nvidia-drm.modeset=1|menu|5|/usr/share/grub/themes/nocapos/boot.png|keep" {
			t.Errorf("%s details: %q", sh, got)
		}
		// Globs in someone's kernel line are never expanded.
		if got := sourceDropin(t, sh, "details", "a=* quiet", 1); !strings.HasPrefix(got, "a=*|") {
			t.Errorf("%s glob: %q", sh, got)
		}
	}
	silent := lib(t, "", "render_grub_dropin", "silent")
	for _, want := range []string{"GRUB_DISTRIBUTOR=\"NoCapOS\"\n", "GRUB_THEME=\"/usr/share/grub/themes/nocapos/theme.txt\"\n", "GRUB_TIMEOUT_STYLE=hidden\nGRUB_TIMEOUT=0\n", "GRUB_GFXPAYLOAD_LINUX=keep\n"} {
		if !strings.Contains(silent, want) {
			t.Errorf("silent drop-in lacks %q:\n%s", want, silent)
		}
	}
	if m := strings.TrimSpace(lib(t, silent, "grub_dropin_mode")); m != "silent" {
		t.Errorf("mode = %q", m)
	}
	if m := strings.TrimSpace(lib(t, lib(t, "", "render_grub_dropin", "details"), "grub_dropin_mode")); m != "details" {
		t.Errorf("mode = %q", m)
	}
	// The drop-in from 0.14 and earlier (no hidden menu yet).
	if m := strings.TrimSpace(lib(t, "GRUB_DISTRIBUTOR=\"NoCapOS\"\nGRUB_THEME=\"/usr/share/grub/themes/nocapos/theme.txt\"\n", "grub_dropin_mode")); m != "" {
		t.Errorf("old drop-in mode = %q", m)
	}
}

func TestKMSModules(t *testing.T) {
	pci := func(class, vendor string) map[string]string {
		return map[string]string{"class": class, "vendor": vendor}
	}
	machine := func(devs map[string]map[string]string, extra map[string]string) string {
		files := map[string]string{}
		for id, d := range devs {
			for k, v := range d {
				files["sys/bus/pci/devices/"+id+"/"+k] = v + "\n"
			}
		}
		for k, v := range extra {
			files[k] = v
		}
		return root(t, files)
	}
	for name, tc := range map[string]struct {
		devs  map[string]map[string]string
		extra map[string]string
		want  string
	}{
		"intel":           {map[string]map[string]string{"0000-00-02.0": pci("0x030000", "0x8086"), "0000-00-1f.3": pci("0x040300", "0x8086")}, nil, "i915"},
		"amd":             {map[string]map[string]string{"0000-03-00.0": pci("0x030000", "0x1002")}, nil, "amdgpu"},
		"hybrid":          {map[string]map[string]string{"0000-00-02.0": pci("0x030000", "0x8086"), "0000-01-00.0": pci("0x030200", "0x10de")}, nil, "i915\nnouveau"},
		"nvidia's driver": {map[string]map[string]string{"0000-01-00.0": pci("0x030000", "0x10de")}, map[string]string{"sys/module/nvidia/": ""}, ""},
		"vm":              {map[string]map[string]string{"0000-00-01.0": pci("0x030000", "0x1234")}, nil, ""},
		"none":            {nil, map[string]string{"etc/": ""}, ""},
	} {
		if got := strings.TrimSpace(lib(t, "", "kms_modules", machine(tc.devs, tc.extra))); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if got := lib(t, "", "render_kms_modules", "amdgpu", "i915"); !strings.HasSuffix(got, "\namdgpu\ni915\n") || !strings.HasPrefix(got, "#") {
		t.Errorf("module list:\n%s", got)
	}
}

// The sign-in screen takes over the first console from the splash.
func TestSplashHandOff(t *testing.T) {
	g := lib(t, "", "render_greetd_dropin")
	for _, want := range []string{"Conflicts=getty@tty1.service", "OnFailure=getty@tty1.service", "After=plymouth-quit-wait.service"} {
		if !strings.Contains(g, want) {
			t.Errorf("greetd drop-in lacks %q:\n%s", want, g)
		}
	}
	// greetd's own unit waits for plymouth-quit-wait: the splash must still
	// quit (it only keeps its last frame), or the sign-in screen never starts.
	if strings.Contains(g, "plymouth-quit.service") {
		t.Errorf("greetd must not hold back plymouth-quit:\n%s", g)
	}
	p := lib(t, "", "render_plymouth_quit_dropin")
	if !strings.Contains(p, "ExecStart=\nExecStart=-/bin/sh -c '") || !strings.Contains(p, "plymouth quit --retain-splash") ||
		!strings.Contains(p, "else exec plymouth quit; fi'") {
		t.Errorf("plymouth-quit drop-in:\n%s", p)
	}
	// systemd expands $VAR in ExecStart: the shell's $( must be written $$(.
	if regexp.MustCompile(`[^$]\$\(`).MatchString(p) {
		t.Errorf("unescaped $( in a unit file:\n%s", p)
	}
}

// ---- nocap-boot on fake machines ----

type bootMachine struct {
	t    *testing.T
	root string
	bin  string
}

const fakeUpdateGrub = `#!/bin/sh
# update-grub, as far as the test needs: reads the settings the way
# grub-mkconfig does and writes the kernel line and menu style.
echo "update-grub" >>"$NOCAP_ROOT/calls"
. "$NOCAP_ROOT/etc/default/grub"
for f in "$NOCAP_ROOT"/etc/default/grub.d/*.cfg; do [ -e "$f" ] && . "$f"; done
[ -x "$NOCAP_ROOT/etc/grub.d/06_nocapos" ] && extra=yes
style="$GRUB_TIMEOUT_STYLE"
if [ -n "$FAIL_GRUB" ] && [ "$style" = "$FAIL_GRUB" ]; then echo "grub-mkconfig: error: simulated" >&2; exit 1; fi
printf 'set timeout_style=%s\nlinux /vmlinuz root=UUID=r ro %s\n# 06=%s\n' "$style" "$GRUB_CMDLINE_LINUX_DEFAULT" "$extra" >"$NOCAP_ROOT/boot/grub/grub.cfg"
`

const fakeScriptCheck = `#!/bin/sh
echo "grub-script-check" >>"$NOCAP_ROOT/calls"
if [ -n "$FAIL_CHECK" ] && grep -q "timeout_style=$FAIL_CHECK" "$1"; then echo "syntax error" >&2; exit 1; fi
exit 0
`

const fakeInitramfs = `#!/bin/sh
echo "update-initramfs $*" >>"$NOCAP_ROOT/calls"
`

func newBootMachine(t *testing.T, files map[string]string) *bootMachine {
	t.Helper()
	m := &bootMachine{t: t, root: root(t, files), bin: t.TempDir()}
	for name, body := range map[string]string{"update-grub": fakeUpdateGrub, "grub-script-check": fakeScriptCheck, "update-initramfs": fakeInitramfs} {
		if err := os.WriteFile(filepath.Join(m.bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

// boot runs nocap-boot with ARGS; env: extra KEY=VALUE settings.
func (m *bootMachine) boot(env []string, args ...string) (string, error) {
	m.t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		m.t.Skip("no bash")
	}
	tool := filepath.ToSlash(deployPath(m.t, "deploy", "desktop", "nocap-boot"))
	wrap := filepath.Join(m.t.TempDir(), "wrap.sh")
	script := `export PATH="$(cd "$FAKEBIN" && pwd):$PATH"` + "\nexec bash '" + tool + `' "$@"` + "\n"
	if err := os.WriteFile(wrap, []byte(script), 0o644); err != nil {
		m.t.Fatal(err)
	}
	cmd := exec.Command(bash, append([]string{filepath.ToSlash(wrap)}, args...)...)
	cmd.Env = append(os.Environ(), append([]string{"NOCAP_ROOT=" + m.root, "FAKEBIN=" + filepath.ToSlash(m.bin), "NOCAP_ARCH=x86_64"}, env...)...)
	out, err := cmd.CombinedOutput()
	return strings.ReplaceAll(string(out), "\r\n", "\n"), err
}

func (m *bootMachine) read(p string) string {
	b, err := os.ReadFile(filepath.Join(m.root, filepath.FromSlash(p)))
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func (m *bootMachine) calls() string {
	c := m.read("calls")
	_ = os.Remove(filepath.Join(m.root, "calls"))
	return c
}

func (m *bootMachine) exists(p string) bool {
	_, err := os.Stat(filepath.Join(m.root, filepath.FromSlash(p)))
	return err == nil
}

// An installed PC updating from 0.14: Debian's /etc/default/grub with the
// tokens earlier installers added, 0.14's drop-in (menu shown, no hidden
// style), a working grub.cfg.
func pcFrom014(t *testing.T) *bootMachine {
	return newBootMachine(t, map[string]string{
		"etc/default/grub":                        "GRUB_DEFAULT=0\nGRUB_TIMEOUT=5\nGRUB_DISTRIBUTOR=`lsb_release -i -s 2> /dev/null || echo Debian`\nGRUB_CMDLINE_LINUX_DEFAULT=\"quiet splash loglevel=3 logo.nologo\"\nGRUB_CMDLINE_LINUX=\"\"\n",
		"etc/default/grub.d/nocapos.cfg":          "# NoCapOS boot menu (written by install.sh, removed by uninstall.sh).\nGRUB_DISTRIBUTOR=\"NoCapOS\"\nGRUB_THEME=\"/usr/share/grub/themes/nocapos/theme.txt\"\nGRUB_GFXMODE=auto\n",
		"boot/grub/grub.cfg":                      "set timeout_style=menu\nlinux /vmlinuz root=UUID=r ro quiet splash loglevel=3 logo.nologo\n",
		"var/lib/nocapos/desktop-backup/edits":    "grub-theme\ngrub-cfg\n",
		"usr/share/initramfs-tools/":              "",
		"sys/bus/pci/devices/0000-00-02.0/class":  "0x030000\n",
		"sys/bus/pci/devices/0000-00-02.0/vendor": "0x8086\n",
	})
}

func TestNocapBootUpdateFrom014(t *testing.T) {
	m := pcFrom014(t)
	out, err := m.boot(nil, "apply")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	cfg := m.read("boot/grub/grub.cfg")
	line := "linux /vmlinuz root=UUID=r ro " + strings.Join(quietTokens, " ") + " logo.nologo\n"
	if !strings.Contains(cfg, "set timeout_style=hidden\n") || !strings.Contains(cfg, line) || !strings.Contains(cfg, "# 06=yes") {
		t.Fatalf("grub.cfg after the update:\n%s\n(output: %s)", cfg, out)
	}
	if mode := strings.TrimSpace(lib(t, m.read("etc/default/grub.d/nocapos.cfg"), "grub_dropin_mode")); mode != "silent" {
		t.Errorf("drop-in mode %q", mode)
	}
	if st, err := os.Stat(filepath.Join(m.root, "etc", "grub.d", "06_nocapos")); err != nil || (st.Mode()&0o100 == 0 && os.PathSeparator == '/') {
		t.Errorf("06_nocapos not installed executable: %v", err)
	}
	c := m.calls()
	if strings.Count(c, "update-grub") != 1 || !strings.Contains(c, "grub-script-check") || !strings.Contains(c, "update-initramfs -u") {
		t.Errorf("calls:\n%s", c)
	}
	if kms := m.read("usr/share/initramfs-tools/modules.d/nocapos-kms"); !strings.HasSuffix(kms, "\ni915\n") {
		t.Errorf("kms list:\n%s", kms)
	}
	edits := m.read("var/lib/nocapos/desktop-backup/edits")
	for _, want := range []string{"grub-cfg\n", "grub-script\n", "kms\n"} {
		if strings.Count(edits, want) != 1 {
			t.Errorf("edits record %q %d times:\n%s", want, strings.Count(edits, want), edits)
		}
	}
	// /etc/default/grub itself is never touched.
	if !strings.Contains(m.read("etc/default/grub"), "GRUB_CMDLINE_LINUX_DEFAULT=\"quiet splash loglevel=3 logo.nologo\"") {
		t.Error("/etc/default/grub changed")
	}
	for _, left := range []string{"etc/default/grub.d/nocapos.cfg.nocap-prev", "etc/grub.d/06_nocapos.nocap-absent", "boot/grub/grub.cfg.nocap-prev"} {
		if m.exists(left) {
			t.Errorf("leftover %s", left)
		}
	}

	// The next update changes nothing and rebuilds nothing.
	if out, err := m.boot(nil, "apply"); err != nil {
		t.Fatalf("again: %v\n%s", err, out)
	}
	if c := m.calls(); c != "" {
		t.Errorf("second apply ran:\n%s", c)
	}
	// ... unless the installer changed the theme files.
	if out, err := m.boot([]string{"NOCAP_GRUB_CHANGED=1"}, "apply"); err != nil || !strings.Contains(m.calls(), "update-grub") {
		t.Errorf("theme change didn't rebuild the menu: %v\n%s", err, out)
	}
}

func TestNocapBootDetailsSwitch(t *testing.T) {
	m := pcFrom014(t)
	if out, err := m.boot(nil, "apply"); err != nil {
		t.Fatal(err, out)
	}
	m.calls()
	if out, _ := m.boot(nil, "status"); strings.TrimSpace(out) != "off" {
		t.Fatalf("status %q", out)
	}
	if out, err := m.boot(nil, "details", "on"); err != nil {
		t.Fatalf("details on: %v\n%s", err, out)
	}
	cfg := m.read("boot/grub/grub.cfg")
	if !strings.Contains(cfg, "set timeout_style=menu\n") || strings.Contains(cfg, "quiet") || strings.Contains(cfg, "splash") {
		t.Fatalf("details on:\n%s", cfg)
	}
	if out, _ := m.boot(nil, "status"); strings.TrimSpace(out) != "on" || strings.TrimSpace(m.read("etc/nocapos/boot-details")) != "on" {
		t.Fatalf("status after on: %q", out)
	}
	// An update keeps the choice.
	if out, err := m.boot(nil, "apply"); err != nil || strings.Contains(m.read("boot/grub/grub.cfg"), "quiet") {
		t.Fatalf("update dropped the choice: %v\n%s", err, out)
	}
	if out, err := m.boot(nil, "details", "off"); err != nil || !strings.Contains(m.read("boot/grub/grub.cfg"), "set timeout_style=hidden\n") {
		t.Fatalf("details off: %v\n%s", err, out)
	}
	if out, err := m.boot(nil, "details", "sideways"); err == nil {
		t.Fatalf("bad argument accepted: %s", out)
	}
}

// update-grub failing, or a grub.cfg GRUB's checker rejects, leaves the
// boot menu exactly as it was, and the switch too.
func TestNocapBootRollback(t *testing.T) {
	for _, env := range [][]string{{"FAIL_GRUB=menu"}, {"FAIL_CHECK=menu"}} {
		m := pcFrom014(t)
		if out, err := m.boot(nil, "apply"); err != nil {
			t.Fatal(err, out)
		}
		before, dropin, script := m.read("boot/grub/grub.cfg"), m.read("etc/default/grub.d/nocapos.cfg"), m.read("etc/grub.d/06_nocapos")
		out, err := m.boot(env, "details", "on")
		if err == nil {
			t.Fatalf("%v: a failed update-grub reported success:\n%s", env, out)
		}
		if !strings.Contains(out, "previous one stays") {
			t.Errorf("%v: no explanation:\n%s", env, out)
		}
		if got := m.read("boot/grub/grub.cfg"); got != before {
			t.Errorf("%v: grub.cfg changed:\n%s", env, got)
		}
		if m.read("etc/default/grub.d/nocapos.cfg") != dropin || m.read("etc/grub.d/06_nocapos") != script {
			t.Errorf("%v: GRUB's settings weren't put back", env)
		}
		if out, _ := m.boot(nil, "status"); strings.TrimSpace(out) != "off" {
			t.Errorf("%v: switch says %q after the failure", env, out)
		}
	}
	// A first install whose update-grub fails: nocapos.cfg and 06_nocapos
	// didn't exist, and don't afterwards.
	m := newBootMachine(t, map[string]string{
		"etc/default/grub":    "GRUB_CMDLINE_LINUX_DEFAULT=\"quiet\"\n",
		"etc/default/grub.d/": "",
		"boot/grub/grub.cfg":  "set timeout_style=menu\nlinux /vmlinuz quiet\n",
	})
	if out, err := m.boot([]string{"FAIL_GRUB=hidden"}, "apply"); err == nil {
		t.Fatalf("failure not reported:\n%s", out)
	}
	if m.exists("etc/default/grub.d/nocapos.cfg") || m.exists("etc/grub.d/06_nocapos") {
		t.Error("new GRUB files left behind after a failed update-grub")
	}
	// grub.cfg is rebuilt from the files as they were: no hidden menu.
	if got := m.read("boot/grub/grub.cfg"); strings.Contains(got, "hidden") || !strings.Contains(got, "linux /vmlinuz root=UUID=r ro quiet\n") {
		t.Errorf("grub.cfg after the rollback:\n%s", got)
	}
}

func TestNocapBootNoUpdate(t *testing.T) {
	m := pcFrom014(t)
	if out, err := m.boot(nil, "apply", "--no-update"); err != nil {
		t.Fatal(err, out)
	}
	if c := m.calls(); c != "" {
		t.Errorf("--no-update ran:\n%s", c)
	}
	if mode := strings.TrimSpace(lib(t, m.read("etc/default/grub.d/nocapos.cfg"), "grub_dropin_mode")); mode != "silent" || !m.exists("etc/grub.d/06_nocapos") {
		t.Errorf("files not written (mode %q)", mode)
	}
}

func TestNocapBootRaspberryPi(t *testing.T) {
	orig := "console=serial0,115200 console=tty1 root=PARTUUID=4e639091-02 rootfstype=ext4 fsck.repair=yes rootwait\n"
	m := newBootMachine(t, map[string]string{
		"boot/firmware/cmdline.txt":            orig,
		"boot/firmware/config.txt":             "[all]\ndtoverlay=vc4-kms-v3d\n",
		"var/lib/nocapos/desktop-backup/edits": "",
		"usr/share/initramfs-tools/":           "",
	})
	env := []string{"NOCAP_ARCH=aarch64"}
	if out, err := m.boot(env, "apply"); err != nil {
		t.Fatal(err, out)
	}
	line := m.read("boot/firmware/cmdline.txt")
	if strings.Count(line, "\n") != 1 || !strings.HasPrefix(line, strings.TrimSpace(orig)+" ") {
		t.Fatalf("cmdline.txt: %q", line)
	}
	for _, tok := range append(quietTokens, "plymouth.ignore-serial-consoles") {
		if strings.Count(" "+line, " "+tok+" ")+strings.Count(" "+line, " "+tok+"\n") != 1 {
			t.Errorf("%s not there exactly once: %q", tok, line)
		}
	}
	if m.exists("usr/share/initramfs-tools/modules.d/nocapos-kms") || strings.Contains(m.calls(), "update-grub") {
		t.Error("a Pi got PC boot changes")
	}
	if !strings.Contains(m.read("var/lib/nocapos/desktop-backup/edits"), "line|/boot/firmware/cmdline.txt|quiet splash") {
		t.Errorf("not recorded:\n%s", m.read("var/lib/nocapos/desktop-backup/edits"))
	}
	again := line
	if out, err := m.boot(env, "apply"); err != nil || m.read("boot/firmware/cmdline.txt") != again {
		t.Fatalf("not idempotent: %v %s", err, out)
	}
	if out, err := m.boot(env, "details", "on"); err != nil {
		t.Fatal(err, out)
	}
	if got := m.read("boot/firmware/cmdline.txt"); got != strings.TrimSpace(orig)+" plymouth.ignore-serial-consoles\n" {
		t.Errorf("details on: %q", got)
	}
}

// The splash theme names only files it ships, and every shine frame.
func TestSplashThemeFiles(t *testing.T) {
	dir := deployPath(t, "deploy", "plymouth")
	b, err := os.ReadFile(filepath.Join(dir, "nocapos.script"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, m := range regexp.MustCompile(`Image\("([^"+]+)"\)`).FindAllStringSubmatch(s, -1) {
		if _, err := os.Stat(filepath.Join(dir, m[1])); err != nil {
			t.Errorf("the script loads %s, which isn't there", m[1])
		}
	}
	n := regexp.MustCompile(`shine_n = (\d+);`).FindStringSubmatch(s)
	if n == nil {
		t.Fatal("no shine_n")
	}
	frames, _ := filepath.Glob(filepath.Join(dir, "shine-*.png"))
	if len(frames) == 0 || len(frames) != atoi(n[1]) {
		t.Errorf("%d shine frames, the script expects %s", len(frames), n[1])
	}
	// Balanced braces and parentheses (Plymouth drops a script that isn't).
	for _, p := range [][2]string{{"{", "}"}, {"(", ")"}} {
		if strings.Count(s, p[0]) != strings.Count(s, p[1]) {
			t.Errorf("unbalanced %s%s", p[0], p[1])
		}
	}
	theme, _ := os.ReadFile(filepath.Join(dir, "nocapos.plymouth"))
	if !strings.Contains(string(theme), "ModuleName=script") || !strings.Contains(string(theme), "ScriptFile=/usr/share/plymouth/themes/nocapos/nocapos.script") {
		t.Errorf("nocapos.plymouth:\n%s", theme)
	}
	for _, f := range []string{"grub-background.png", "grub-boot.png"} {
		if _, err := os.Stat(deployPath(t, "deploy", "desktop", "art", f)); err != nil {
			t.Errorf("GRUB art %s missing", f)
		}
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
