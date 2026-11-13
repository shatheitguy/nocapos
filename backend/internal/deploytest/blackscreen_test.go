package deploytest

// Tests for the parts of lib.sh/setup.sh that keep the screen from going
// black: the sign-in screen's dconf profile, the Raspberry Pi's KMS driver,
// the packages the screen needs, the boot link, and every shell script
// parsing at all.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGDMProfile(t *testing.T) {
	// Debian keeps the greeter's defaults in /var/lib/gdm3: kept, with
	// NoCapOS's database before them.
	deb := "user-db:user\nfile-db:/var/lib/gdm3/greeter-dconf-defaults\n"
	if got := lib(t, deb, "render_gdm_profile"); got != "user-db:user\nsystem-db:gdm\nfile-db:/var/lib/gdm3/greeter-dconf-defaults\n" {
		t.Errorf("debian profile:\n%s", got)
	}
	// Already there: unchanged.
	done := "user-db:user\nsystem-db:gdm\nfile-db:/usr/share/gdm/greeter-dconf-defaults\n"
	if got := lib(t, done, "render_gdm_profile"); got != done {
		t.Errorf("unchanged profile:\n%s", got)
	}
	// No distribution profile: upstream's.
	if got := lib(t, "", "render_gdm_profile"); got != done {
		t.Errorf("default profile:\n%s", got)
	}
	// No file-db at all: added at the end.
	if got := lib(t, "user-db:user\r\n", "render_gdm_profile"); got != "user-db:user\nsystem-db:gdm\n" {
		t.Errorf("no file-db:\n%q", got)
	}
}

func TestPiKMS(t *testing.T) {
	for cfg, missing := range map[string]bool{
		"[all]\ndtoverlay=vc4-kms-v3d\nmax_framebuffers=2\n": false,
		"[pi4]\ndtoverlay=vc4-fkms-v3d\n":                    false,
		"dtoverlay=vc4-kms-v3d-pi5\n":                        false,
		"#dtoverlay=vc4-kms-v3d\ndtoverlay=dwc2\n":           true,
		"":                                                   true,
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
// (GL drivers with the software renderer, Xorg for GDM's fallback, logind's
// PAM module) must be listed, and required, everywhere.
func TestScreenPackages(t *testing.T) {
	for family, want := range map[string][]string{
		"debian": {"xserver-xorg-core", "libgl1-mesa-dri", "libegl-mesa0", "libgbm1", "libpam-systemd", "xwayland"},
		"fedora": {"xorg-x11-server-Xorg", "mesa-dri-drivers", "mesa-libEGL"},
		"arch":   {"xorg-server", "mesa"},
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

// "systemctl enable gdm3" does nothing on Debian (no [Install] section):
// switching the desktop on must make the display-manager.service link.
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
		t.Error("off mode leaves GDM's link")
	}
	if !strings.Contains(setup, `ln -sfn "$path" "$link"`) {
		t.Error("desk_link_display_manager doesn't make the link")
	}
	// The old profile with Debian's missing file-db is never written again.
	if strings.Contains(setup, `printf 'user-db:user\nsystem-db:gdm\nfile-db:`) {
		t.Error("setup.sh still writes the fixed sign-in screen profile")
	}
}

// Every shell script the installer, uninstaller and images run parses.
func TestShellScriptsParse(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	root := filepath.Join("..", "..", "..")
	scripts := []string{
		"deploy/install.sh", "deploy/uninstall.sh",
		"deploy/desktop/lib.sh", "deploy/desktop/setup.sh", "deploy/desktop/uninstall-desktop.sh",
		"deploy/desktop/gdm-theme", "deploy/desktop/remote-guard", "deploy/desktop/nocapos-accent", "deploy/desktop/os-release-sync",
		"image/common/layer.sh", "image/common/stage-overlay.sh",
		"image/common/rootfs/usr/lib/nocapos/image/desktop-packages.sh",
		"image/common/rootfs/usr/lib/nocapos/image/firstboot.sh",
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
