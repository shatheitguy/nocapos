package deploytest

// Tests for the parts of lib.sh/setup.sh and the session files that keep
// the screen from going black: the Raspberry Pi's KMS driver, the packages
// the screen needs, the boot link, the session scripts' software rendering
// fallback, the labwc and systemd files, and every shell script parsing.

import (
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPiKMS(t *testing.T) {
	for cfg, missing := range map[string]bool{
		"[all]\ndtoverlay=vc4-kms-v3d\nmax_framebuffers=2\n": false,
		"[pi4]\ndtoverlay=vc4-fkms-v3d\n":                    false,
		"dtoverlay=vc4-kms-v3d-pi5\n":                        false,
		"#dtoverlay=vc4-kms-v3d\ndtoverlay=dwc2\n":           true,
		"": true,
	} {
		_, err := libErr(t, cfg, "pi_kms_missing")
		if got := err == nil; got != missing {
			t.Errorf("pi_kms_missing(%q) = %v, want %v", cfg, got, missing)
		}
	}
	got := lib(t, "[pi4]\narm_boost=1\ndtoverlay=dwc2\n", "config_txt_append", "dtoverlay=vc4-kms-v3d")
	if got != "[pi4]\narm_boost=1\ndtoverlay=dwc2\n[all]\ndtoverlay=vc4-kms-v3d\n" {
		t.Errorf("config_txt_append:\n%s", got)
	}
	// Uninstall takes exactly that line out again.
	if back := lib(t, got, "config_txt_without", "dtoverlay=vc4-kms-v3d"); back != "[pi4]\narm_boost=1\ndtoverlay=dwc2\n[all]\n" {
		t.Errorf("config_txt_without:\n%s", back)
	}
}

// Installs skip recommended packages: what the screen needs to light up
// (GL drivers with the software renderer, logind's PAM module, the
// compositor and the sign-in service) must be listed, and required.
func TestScreenPackages(t *testing.T) {
	for family, want := range map[string][]string{
		"debian": {"labwc", "greetd", "libgl1-mesa-dri", "libegl-mesa0", "libgbm1", "libpam-systemd", "xwayland"},
		"fedora": {"labwc", "greetd", "mesa-dri-drivers", "mesa-libEGL"},
		"arch":   {"labwc", "greetd", "mesa"},
	} {
		lines := "\n" + lib(t, "", "desktop_packages", family)
		for _, w := range want {
			if !strings.Contains(lines, "\n"+w+"\n") {
				t.Errorf("%s: %s isn't a required package", family, w)
			}
		}
	}
}

func readRepo(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// Switching the desktop on makes the display-manager.service link to
// greetd (an earlier display manager's link is in the way of "enable").
func TestDesktopOnLinksTheDisplayManager(t *testing.T) {
	setup := readRepo(t, "deploy/desktop/setup.sh")
	start := strings.Index(setup, "apply_desktop_mode() {")
	if start < 0 {
		t.Fatal("apply_desktop_mode is gone")
	}
	body := setup[start:]
	body = body[:strings.Index(body, "\n}\n")]
	full := body[strings.Index(body, "full)"):strings.Index(body, "off)")]
	if !strings.Contains(full, `desk_link_display_manager "$dm"`) || !strings.Contains(full, "desk_verify") {
		t.Errorf("full mode doesn't link the display manager:\n%s", full)
	}
	if !strings.Contains(body[strings.Index(body, "off)"):], "desk_unlink_display_manager") {
		t.Error("off mode leaves the link")
	}
	if !strings.Contains(setup, `ln -sfn "$path" "$link"`) || !strings.Contains(setup, "DESK_DM=greetd.service") {
		t.Error("desk_link_display_manager doesn't make the greetd link")
	}
	// Someone signed in on the earlier desktop is never signed out by an update.
	if !strings.Contains(full, "desk_screen_in_use") {
		t.Error("the switch doesn't check for a signed-in screen")
	}
	// The earlier desktop's packages are never removed by NoCapOS.
	if regexp.MustCompile(`apt-get (remove|purge)[^\n]*(gdm|gnome)`).MatchString(setup) {
		t.Error("setup.sh removes the earlier desktop's packages")
	}
	// No third-party default apps or browser. (desk_app_windows only gives a
	// browser the person installed NoCap's title bar; it installs nothing.)
	installs := regexp.MustCompile(`(?s)FIREFOX_POLICY=[^\n]*\n|desk_app_windows\(\) \{.*?\n\}\n`).ReplaceAllString(setup, "")
	if regexp.MustCompile(`(?m)^\s*(pkg_install|apt-get install|flatpak install)[^\n]*firefox`).MatchString(setup) {
		t.Error("setup.sh installs a browser")
	}
	for _, never := range []string{"desk_install_browser", "firefox", "thunar", "mousepad"} {
		if strings.Contains(installs, never) {
			t.Errorf("setup.sh still has %q", never)
		}
	}
}

// The session scripts: never as root, NoCap Desktop's own labwc settings,
// and software rendering when the compositor can't start (no black screen).
func TestSessionScripts(t *testing.T) {
	for _, s := range []string{"deploy/desktop/session/nocap-session", "deploy/desktop/session/nocap-greeter-session"} {
		src := readRepo(t, s)
		for _, want := range []string{"WLR_RENDERER=pixman", "/etc/nocapos/desktop-software-rendering", "-C \"$CONF\"", "refusing to run as root"} {
			if !strings.Contains(src, want) {
				t.Errorf("%s lacks %q", s, want)
			}
		}
	}
	if !strings.Contains(readRepo(t, "deploy/desktop/session/nocap-session"), "CONF=/usr/share/nocapos/labwc") {
		t.Error("the session doesn't use NoCap Desktop's labwc settings")
	}
	if !strings.Contains(readRepo(t, "deploy/desktop/session/nocap-greeter-session"), "nocap-shell --greeter") {
		t.Error("the sign-in screen isn't NoCap's")
	}
	// The programs every file points at are the ones setup.sh installs.
	setup := readRepo(t, "deploy/desktop/setup.sh") + readRepo(t, "deploy/desktop/lib.sh")
	for _, f := range []string{"deploy/desktop/labwc/rc.xml", "deploy/desktop/labwc/autostart", "deploy/desktop/session/nocap-shell.service", "deploy/desktop/session/nocap-idle.service", "deploy/desktop/session/nocap-lock-start", "deploy/desktop/session/nocap-open"} {
		for _, m := range regexp.MustCompile(`/usr/libexec/nocapos/([a-z-]+)`).FindAllStringSubmatch(readRepo(t, f), -1) {
			prog := m[1]
			if prog != "nocap-shell" && prog != "nocap-lock" && !strings.Contains(setup, prog) {
				t.Errorf("%s runs %s, which setup.sh doesn't install", f, prog)
			}
		}
	}
	for _, unit := range []string{"nocap-session.target", "nocap-shell.service", "nocap-idle.service", "nocapos-accent.service"} {
		if !strings.Contains(setup, unit) {
			t.Errorf("setup.sh doesn't install %s", unit)
		}
		if !strings.Contains(readRepo(t, "deploy/desktop/uninstall-desktop.sh"), unit) {
			t.Errorf("uninstall-desktop.sh leaves %s", unit)
		}
	}
	if !strings.Contains(readRepo(t, "deploy/desktop/session/nocap-session.target"), "nocap-shell.service") {
		t.Error("the session target doesn't start NoCap Shell")
	}
}

// labwc's settings are XML: a broken file means a session without them.
func TestLabwcFiles(t *testing.T) {
	for _, f := range []string{"deploy/desktop/labwc/rc.xml", "deploy/desktop/labwc/menu.xml", "deploy/desktop/greeter/rc.xml"} {
		d := xml.NewDecoder(strings.NewReader(readRepo(t, f)))
		for {
			_, err := d.Token()
			if err != nil {
				if err.Error() != "EOF" {
					t.Errorf("%s: %v", f, err)
				}
				break
			}
		}
	}
	rc := readRepo(t, "deploy/desktop/labwc/rc.xml")
	for _, want := range []string{"--toggle launcher", "nocap-lock", "<theme>", "<name>NoCap</name>", "SnapToEdge"} {
		if !strings.Contains(rc, want) {
			t.Errorf("rc.xml lacks %q", want)
		}
	}
	// NoCap OS's app windows draw their own title bar.
	if !strings.Contains(rc, `identifier="io.nocapos.App.*"`) || !strings.Contains(rc, `serverDecoration="no"`) {
		t.Error("rc.xml lets labwc decorate NoCap OS's app windows")
	}
}

// Every shell script the installer, uninstaller, session and images run parses.
func TestShellScriptsParse(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	root := filepath.Join("..", "..", "..")
	scripts := []string{
		"deploy/install.sh", "deploy/uninstall.sh",
		"deploy/desktop/lib.sh", "deploy/desktop/setup.sh", "deploy/desktop/uninstall-desktop.sh",
		"deploy/desktop/remote-guard", "deploy/desktop/nocapos-accent", "deploy/desktop/os-release-sync",
		"deploy/desktop/session/nocap-session", "deploy/desktop/session/nocap-greeter-session",
		"deploy/desktop/session/nocap-lock-start", "deploy/desktop/session/nocap-open", "deploy/desktop/labwc/autostart",
		"image/common/layer.sh", "image/common/stage-overlay.sh",
		"image/common/rootfs/usr/lib/nocapos/image/desktop-packages.sh",
		"image/common/rootfs/usr/lib/nocapos/image/desktop-brand.sh",
		"image/common/rootfs/usr/lib/nocapos/image/firstboot.sh",
		"scripts/package.sh", "shell/scripts/build-release.sh",
	}
	for _, s := range scripts {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(s)))
		if err != nil {
			t.Errorf("%s: %v", s, err)
			continue
		}
		f := filepath.Join(t.TempDir(), "s.sh")
		_ = os.WriteFile(f, []byte(strings.ReplaceAll(string(src), "\r\n", "\n")), 0o644)
		if out, err := exec.Command(bash, "-n", filepath.ToSlash(f)).CombinedOutput(); err != nil {
			t.Errorf("%s doesn't parse: %v\n%s", s, err, out)
		}
	}
	if sc, err := exec.LookPath("shellcheck"); err == nil {
		args := []string{"-S", "error", "-s", "bash"}
		for _, s := range scripts {
			args = append(args, filepath.Join(root, filepath.FromSlash(s)))
		}
		if out, err := exec.Command(sc, args...).CombinedOutput(); err != nil {
			t.Errorf("shellcheck errors:\n%s", out)
		}
	}
}
