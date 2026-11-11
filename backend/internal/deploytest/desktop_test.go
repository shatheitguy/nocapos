package deploytest

// Tests for deploy/desktop/lib.sh, NoCap Desktop's pure helpers: which
// distribution gets which packages, how each machine boots, the boot menu
// edits (and their undo), and the settings files every account gets.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func libPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "deploy", "desktop", "lib.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// lib calls a lib.sh function with stdin and args and returns its output.
func lib(t *testing.T, stdin string, fn string, args ...string) string {
	t.Helper()
	out, err := libErr(t, stdin, fn, args...)
	if err != nil {
		t.Fatalf("%s: %v: %s", fn, err, out)
	}
	return out
}

func libErr(t *testing.T, stdin string, fn string, args ...string) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	src, err := os.ReadFile(libPath(t))
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(src), "\r\n", "\n") + "\n" + fn + ` "$@"` + "\n"
	// A script file, not bash -c: a long script doesn't survive a Windows command line.
	f := filepath.Join(t.TempDir(), "run.sh")
	if err := os.WriteFile(f, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, append([]string{filepath.ToSlash(f)}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	b, err := cmd.CombinedOutput()
	return strings.ReplaceAll(string(b), "\r\n", "\n"), err
}

// root makes a fake machine: path -> contents (a trailing "/" makes a folder).
func root(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			_ = os.MkdirAll(full, 0o755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.ToSlash(dir)
}

func osRelease(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "os-release", name))
	if err != nil {
		t.Fatal(err)
	}
	return root(t, map[string]string{"etc/os-release": string(b)})
}

func TestDesktopFamily(t *testing.T) {
	for fixture, want := range map[string]string{
		"debian-12":              "debian",
		"debian-13":              "debian",
		"raspios-bookworm-arm64": "debian",
		"raspbian-11-armhf":      "debian",
		"armbian-bookworm":       "debian",
		"ubuntu-24.04":           "ubuntu",
		"linuxmint-22":           "ubuntu",
		"fedora-41":              "fedora",
		"arch":                   "arch",
		"alpine-3.20":            "none",
	} {
		if got := strings.TrimSpace(lib(t, "", "desktop_family", osRelease(t, fixture))); got != want {
			t.Errorf("%s: family %q, want %q", fixture, got, want)
		}
	}
	if got := strings.TrimSpace(lib(t, "", "desktop_family", root(t, map[string]string{"etc/": ""}))); got != "none" {
		t.Errorf("no os-release: %q", got)
	}
}

func TestDesktopPackages(t *testing.T) {
	// Never the full GNOME meta packages (games, office, ...), never
	// NetworkManager (it would take over a server's network), never the
	// distribution's branding packages.
	banned := regexp.MustCompile(`(^|[|?])(gnome|gnome-core|task-gnome-desktop|ubuntu-desktop|gnome-games|aisleriot|network-manager|NetworkManager|desktop-base|ubuntu-wallpapers|yaru-theme-gtk|libreoffice\S*|evolution|totem|rhythmbox)($|\|)`)
	required := map[string][]string{
		"debian": {"gdm3", "gnome-session", "gnome-shell", "gnome-control-center", "nautilus", "gnome-console|gnome-terminal", "gnome-text-editor|gedit", "loupe|eog", "papers|evince", "file-roller", "gnome-system-monitor", "xdg-desktop-portal-gnome", "flatpak", "?gnome-software", "?gnome-software-plugin-flatpak", "?gnome-shell-extension-dashtodock|gnome-shell-extension-ubuntu-dock", "gnome-remote-desktop", "x11vnc", "pipewire", "dconf-cli", "libsecret-tools"},
		"fedora": {"gdm", "gnome-shell", "gnome-session", "nautilus", "flatpak", "gnome-remote-desktop", "x11vnc", "pipewire"},
		"arch":   {"gdm", "gnome-shell", "gnome-session", "nautilus", "flatpak", "gnome-remote-desktop", "x11vnc", "pipewire"},
	}
	required["ubuntu"] = required["debian"]
	for family, want := range required {
		lines := strings.Split(strings.TrimSpace(lib(t, "", "desktop_packages", family)), "\n")
		have := map[string]bool{}
		for _, l := range lines {
			if banned.MatchString(l) {
				t.Errorf("%s: unwanted package line %q", family, l)
			}
			if strings.TrimSpace(l) != l || l == "" || strings.Contains(l, " ") {
				t.Errorf("%s: malformed line %q", family, l)
			}
			have[l] = true
		}
		for _, w := range want {
			if !have[w] {
				t.Errorf("%s: missing %q", family, w)
			}
		}
	}
	if got := lib(t, "", "desktop_packages", "none"); got != "" {
		t.Errorf("unsupported family should have no packages: %q", got)
	}
}

// Every machine gets the full desktop: there's no hardware check left, only
// a memory profile that tunes it.
func TestNoHardwareFallback(t *testing.T) {
	if _, err := libErr(t, "", "desktop_feasible", "armv7", "900000"); err == nil {
		t.Fatal("desktop_feasible is back: small and 32-bit machines get the full desktop")
	}
	for mem, want := range map[string]string{
		"8000000": "normal",
		"3900000": "normal", // Pi 4 4 GB
		"1900000": "low",    // Pi 4 2 GB
		"900000":  "low",    // Pi 3, 1 GB
		"":        "normal",
	} {
		if got := strings.TrimSpace(lib(t, "", "memory_profile", mem)); got != want {
			t.Errorf("memory_profile %s = %s, want %s", mem, got, want)
		}
	}
	for mem, want := range map[string]string{"900000": "878", "1900000": "1855", "8000000": "3906", "64000000": "4096", "200000": "256"} {
		if got := strings.TrimSpace(lib(t, "", "zram_size_mb", mem)); got != want {
			t.Errorf("zram_size_mb %s = %s, want %s", mem, got, want)
		}
	}
	if got := strings.TrimSpace(lib(t, "", "swapfile_mb", "900000")); got != "1024" {
		t.Errorf("1 GB machine swap file = %s", got)
	}
	if got := strings.TrimSpace(lib(t, "", "swapfile_mb", "8000000")); got != "0" {
		t.Errorf("8 GB machine swap file = %s", got)
	}
	zg := keyfile(t, "zram-generator", strings.ReplaceAll(lib(t, "", "render_zram_generator", "900000"), " = ", "="))
	if zg["zram0"]["zram-size"] != "878" || zg["zram0"]["compression-algorithm"] != "zstd" {
		t.Errorf("zram-generator.conf: %v", zg)
	}
	if z := lib(t, "", "render_zramswap", "900000"); !strings.Contains(z, "\nSIZE=878\n") || !strings.Contains(z, "ALGO=zstd") {
		t.Errorf("zramswap: %s", z)
	}
	low := keyfile(t, "lowmem", lib(t, "", "render_dconf_lowmem"))
	if low["org/gnome/desktop/interface"]["enable-animations"] != "false" {
		t.Error("animations stay on in the low-memory profile")
	}
	if low["org/freedesktop/tracker/miner/files"]["crawling-interval"] != "-2" || low["org/freedesktop/localsearch/miner/files"]["enable-monitors"] != "false" {
		t.Error("file indexing stays on in the low-memory profile")
	}
	units := lib(t, "", "eval", `echo "$LOWMEM_USER_UNITS"`)
	for _, u := range []string{"tracker-miner-fs-3.service", "localsearch-3.service", "evolution-source-registry.service", "evolution-calendar-factory.service"} {
		if !strings.Contains(units, u) {
			t.Errorf("%s isn't masked in the low-memory profile", u)
		}
	}
	if hidden := lib(t, "", "render_autostart_hidden"); !strings.Contains(hidden, "Hidden=true") {
		t.Errorf("autostart: %s", hidden)
	}
}

// Package selection on every architecture, armhf included: the same list,
// picked from what the repositories have.
func TestDesktopSelect(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", "packages", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for name, want := range map[string][]string{
		// Debian 12 / Raspberry Pi OS (32-bit): older apps where the new
		// ones don't exist yet.
		"debian-12-armhf": {"gdm3", "gnome-console", "gnome-text-editor", "eog", "evince", "gnome-remote-desktop", "x11vnc", "pipewire", "wireplumber", "systemd-zram-generator", "libguac-client-rdp0"},
		"debian-13-arm64": {"gdm3", "loupe", "papers", "gnome-remote-desktop", "x11vnc", "wayvnc"},
	} {
		got := lib(t, read(name), "desktop_select", "debian")
		picks := map[string]bool{}
		for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
			if strings.HasPrefix(l, "!") {
				t.Errorf("%s: required package missing: %s", name, l)
			}
			picks[l] = true
		}
		for _, w := range want {
			if !picks[w] {
				t.Errorf("%s: %s not picked:\n%s", name, w, got)
			}
		}
		if picks["loupe"] && picks["eog"] {
			t.Errorf("%s: both alternatives picked", name)
		}
	}
	// Optional lines with nothing available are listed as such; required
	// ones make the selection fail.
	got := lib(t, "gdm3\n", "desktop_select", "debian")
	if !strings.Contains(got, "\n!gnome-shell\n") || !strings.Contains(got, "\n?wayvnc\n") || !strings.HasPrefix(got, "gdm3\n") {
		t.Errorf("selection with almost nothing:\n%s", got)
	}
}

func TestAccentCSS(t *testing.T) {
	g4 := lib(t, "", "render_gtk4_accent")
	if !strings.Contains(g4, "@define-color accent_bg_color #e5383b;") || !strings.Contains(g4, "@define-color accent_fg_color #ffffff;") {
		t.Errorf("gtk4: %s", g4)
	}
	g3 := lib(t, "", "render_gtk3_accent")
	if !strings.Contains(g3, "button.suggested-action") || !strings.Contains(g3, "switch:checked") || !strings.Contains(g3, "#e5383b") {
		t.Errorf("gtk3: %s", g3)
	}
	for _, css := range []string{g4, g3} {
		if strings.Count(css, "{") != strings.Count(css, "}") {
			t.Errorf("unbalanced CSS: %s", css)
		}
	}
	block := filepath.Join(t.TempDir(), "block.css")
	if err := os.WriteFile(block, []byte(g4), 0o644); err != nil {
		t.Fatal(err)
	}
	b := filepath.ToSlash(block)
	// Someone's own CSS stays; the block is added once and replaced, not
	// added again.
	own := "window { font-size: 12px; }\n"
	once := lib(t, own, "css_with_accent", b)
	if !strings.HasPrefix(once, own) || !strings.HasSuffix(once, g4) {
		t.Fatalf("merge:\n%s", once)
	}
	if twice := lib(t, once, "css_with_accent", b); twice != once {
		t.Fatalf("not idempotent:\n%s", twice)
	}
	if fresh := lib(t, "", "css_with_accent", b); fresh != g4 {
		t.Fatalf("empty file:\n%q", fresh)
	}
	if back := lib(t, once, "css_without_accent"); strings.TrimSpace(back) != strings.TrimSpace(own) {
		t.Fatalf("undo:\n%s", back)
	}
	// libadwaita gets the CSS up to GNOME 46; 47+ has the accent setting.
	for major, want := range map[string]bool{"0": true, "43": true, "46": true, "47": false, "48": false} {
		_, err := libErr(t, "", "gtk4_accent_wanted", major)
		if (err == nil) != want {
			t.Errorf("gtk4_accent_wanted %s = %v", major, err == nil)
		}
	}
}

func TestBootMethods(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"pc-grub-bios":      {map[string]string{"etc/default/grub": "GRUB_DEFAULT=0\n"}, "grub"},
		"pc-grub-uefi":      {map[string]string{"etc/default/grub": "", "boot/efi/EFI/debian/grubx64.efi": ""}, "grub"},
		"pc-systemd-boot":   {map[string]string{"boot/efi/loader/loader.conf": "timeout 3\n", "etc/kernel/cmdline": "root=UUID=x rw\n"}, "systemd-boot"},
		"pi-bookworm":       {map[string]string{"boot/firmware/cmdline.txt": "console=tty1 root=PARTUUID=1-02\n", "boot/firmware/config.txt": "[all]\n"}, "pi"},
		"pi-bullseye":       {map[string]string{"boot/cmdline.txt": "console=tty1\n"}, "pi"},
		"arm64-u-boot-menu": {map[string]string{"etc/default/u-boot": "U_BOOT_PARAMETERS=\"ro\"\n", "boot/extlinux/extlinux.conf": "label l0\n  append ro\n"}, "uboot-menu"},
		"arm64-extlinux":    {map[string]string{"boot/extlinux/extlinux.conf": "label l0\n  append ro\n"}, "extlinux"},
		"arm64-armbian":     {map[string]string{"boot/armbianEnv.txt": "verbosity=1\nbootlogo=true\n"}, "armbian"},
		"arm64-efi-grub":    {map[string]string{"etc/default/grub": "", "boot/efi/EFI/BOOT/BOOTAA64.EFI": ""}, "grub"},
		"unknown":           {map[string]string{"etc/": ""}, ""},
	} {
		if got := strings.TrimSpace(lib(t, "", "boot_methods", root(t, tc.files))); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestBootTokenEdits(t *testing.T) {
	toks := []string{"quiet", "splash", "loglevel=3", "logo.nologo"}

	// Debian's u-boot-menu and Armbian: a KEY="value" line.
	uboot := "# U-Boot menu\nU_BOOT_PROMPT=\"1\"\nU_BOOT_PARAMETERS=\"ro rootwait\"\n"
	got := lib(t, uboot, "var_tokens_with", append([]string{"U_BOOT_PARAMETERS"}, toks...)...)
	if !strings.Contains(got, "U_BOOT_PARAMETERS=\"ro rootwait quiet splash loglevel=3 logo.nologo\"\n") || !strings.Contains(got, "U_BOOT_PROMPT=\"1\"") {
		t.Fatalf("u-boot:\n%s", got)
	}
	if again := lib(t, got, "var_tokens_with", append([]string{"U_BOOT_PARAMETERS"}, toks...)...); again != got {
		t.Fatalf("not idempotent:\n%s", again)
	}
	if back := lib(t, got, "var_tokens_without", append([]string{"U_BOOT_PARAMETERS"}, toks...)...); back != uboot {
		t.Fatalf("undo:\n%s", back)
	}
	armbian := "verbosity=1\nbootlogo=true\nrootdev=UUID=abc\n"
	got = lib(t, armbian, "var_tokens_with", "extraargs", "quiet", "splash")
	if !strings.HasSuffix(got, "extraargs=\"quiet splash\"\n") {
		t.Fatalf("armbian (missing key):\n%s", got)
	}
	got = lib(t, "extraargs=cma=256M\n", "var_tokens_with", "extraargs", "quiet")
	if got != "extraargs=\"cma=256M quiet\"\n" {
		t.Fatalf("armbian unquoted: %q", got)
	}

	// extlinux and systemd-boot entries: every append/options line.
	ext := "default l0\nmenu title U-Boot menu\nlabel l0\n\tmenu label Debian\n\tlinux /vmlinuz\n\tappend root=/dev/mmcblk0p2 ro quiet\nlabel l0r\n\tappend root=/dev/mmcblk0p2 ro single\n"
	got = lib(t, ext, "keyword_tokens_with", append([]string{"append"}, toks...)...)
	if strings.Count(got, "splash loglevel=3 logo.nologo") != 2 || strings.Count(got, " quiet") != 2 || !strings.Contains(got, "\tappend root=/dev/mmcblk0p2 ro quiet splash") {
		t.Fatalf("extlinux:\n%s", got)
	}
	if back := lib(t, got, "keyword_tokens_without", "append", "splash", "loglevel=3", "logo.nologo"); !strings.Contains(back, "\tappend root=/dev/mmcblk0p2 ro quiet\n") || !strings.Contains(back, "\tappend root=/dev/mmcblk0p2 ro single quiet\n") {
		t.Fatalf("extlinux undo:\n%s", back)
	}
	entry := "title Debian\nlinux /vmlinuz\noptions root=UUID=x rw\n"
	if got := lib(t, entry, "keyword_tokens_with", "options", "quiet", "splash"); got != "title Debian\nlinux /vmlinuz\noptions root=UUID=x rw quiet splash\n" {
		t.Fatalf("systemd-boot entry: %q", got)
	}

	// /etc/kernel/cmdline: one line.
	if got := lib(t, "root=UUID=x rw quiet\n", "tokens_line_with", toks...); got != "root=UUID=x rw quiet splash loglevel=3 logo.nologo\n" {
		t.Fatalf("kernel cmdline: %q", got)
	}
	if got := lib(t, "root=UUID=x rw quiet splash loglevel=3 logo.nologo\n", "tokens_line_without", "splash", "loglevel=3", "logo.nologo"); got != "root=UUID=x rw quiet\n" {
		t.Fatalf("kernel cmdline undo: %q", got)
	}

	// Raspberry Pi config.txt: no rainbow splash, added once, undone exactly.
	cfg := "# For more options\ndtparam=audio=on\n\n[cm4]\notg_mode=1\n\n[all]\narm_64bit=1\n"
	got = lib(t, cfg, "config_txt_with", "disable_splash=1")
	if got != cfg+"disable_splash=1\n" {
		t.Fatalf("config.txt (ends in [all]): %q", got)
	}
	cfg2 := "dtparam=audio=on\n[pi4]\narm_boost=1\n"
	got = lib(t, cfg2, "config_txt_with", "disable_splash=1")
	if got != cfg2+"[all]\ndisable_splash=1\n" {
		t.Fatalf("config.txt (ends in [pi4]): %q", got)
	}
	if again := lib(t, got, "config_txt_with", "disable_splash=1"); again != got {
		t.Fatalf("config.txt twice: %q", again)
	}
	if kept := lib(t, "disable_splash=0\n", "config_txt_with", "disable_splash=1"); kept != "disable_splash=0\n" {
		t.Fatalf("someone's own setting changed: %q", kept)
	}
	if back := lib(t, cfg+"disable_splash=1\n", "config_txt_without", "disable_splash=1"); back != cfg {
		t.Fatalf("config.txt undo: %q", back)
	}
}

// keyfile checks a GLib key file's shape: [groups], key=value lines and
// comments only, every group non-empty.
func keyfile(t *testing.T, name, s string) map[string]map[string]string {
	t.Helper()
	groups := map[string]map[string]string{}
	var cur string
	for i, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			cur = line[1 : len(line)-1]
			if _, dup := groups[cur]; dup {
				t.Errorf("%s:%d: group %s twice", name, i+1, cur)
			}
			groups[cur] = map[string]string{}
		default:
			k, v, ok := strings.Cut(line, "=")
			if !ok || cur == "" || k == "" || strings.ContainsAny(k, " \t") {
				t.Fatalf("%s:%d: not a key file line: %q", name, i+1, line)
			}
			groups[cur][k] = v
		}
	}
	for g, kv := range groups {
		if len(kv) == 0 {
			t.Errorf("%s: empty group [%s]", name, g)
		}
	}
	return groups
}

func TestSettingsFiles(t *testing.T) {
	const uuid = "nocapos-brand@shatheitguy.github.io"
	favs := "['org.gnome.Nautilus.desktop', 'firefox-esr.desktop', 'org.gnome.Console.desktop', 'org.gnome.Settings.desktop']"
	exts := []string{favs, uuid, "dash-to-dock@micxgx.gmail.com", "appindicatorsupport@rgcjonas.gmail.com"}
	gs := keyfile(t, "gschema", lib(t, "", "render_gschema_override", exts...))
	if gs["org.gnome.shell"]["enabled-extensions"] != "['"+uuid+"', 'dash-to-dock@micxgx.gmail.com', 'appindicatorsupport@rgcjonas.gmail.com']" {
		t.Errorf("extensions = %s", gs["org.gnome.shell"]["enabled-extensions"])
	}
	if fav := gs["org.gnome.shell"]["favorite-apps"]; fav != favs {
		t.Errorf("dock = %s", fav)
	}
	if gs["org.gnome.desktop.interface"]["color-scheme"] != "'prefer-dark'" || !strings.Contains(gs["org.gnome.desktop.background"]["picture-uri"], "/usr/share/backgrounds/nocapos/") {
		t.Errorf("look: %v", gs)
	}
	// Only keys every GNOME since 43 has go in the override (an unknown key
	// there makes glib-compile-schemas complain); newer ones go to dconf.
	for g, kv := range gs {
		if !strings.HasPrefix(g, "org.gnome.") || strings.Contains(g, "dash-to-dock") {
			t.Errorf("override has schema %s", g)
		}
		if _, ok := kv["accent-color"]; ok {
			t.Error("accent-color (GNOME 47+) belongs in dconf, not the override")
		}
	}

	// Older Ubuntu: its dock, no app indicators, the older terminal.
	db := keyfile(t, "dconf", lib(t, "", "render_dconf_local", "['org.gnome.Terminal.desktop', 'org.gnome.Settings.desktop']", uuid, "ubuntu-dock@ubuntu.com"))
	if db["org/gnome/desktop/interface"]["accent-color"] != "'red'" {
		t.Error("NoCap red accent missing")
	}
	if db["org/gnome/shell/extensions/dash-to-dock"]["dock-fixed"] != "true" {
		t.Error("the dock must always be visible")
	}
	if db["org/gnome/shell"]["enabled-extensions"] != "['"+uuid+"', 'ubuntu-dock@ubuntu.com']" || !strings.Contains(db["org/gnome/shell"]["favorite-apps"], "'org.gnome.Terminal.desktop'") {
		t.Errorf("shell: %v", db["org/gnome/shell"])
	}
	if db["org/gnome/settings-daemon/plugins/power"]["sleep-inactive-ac-type"] != "'nothing'" {
		t.Error("a server must never put itself to sleep")
	}
	if db["org/gnome/software"]["allow-updates"] != "false" {
		t.Error("updates come from NoCapOS")
	}
	// No web apps on the desktop: nothing anywhere opens NoCapOS's web UI.
	for _, out := range []string{lib(t, "", "render_dconf_local", "['org.gnome.Nautilus.desktop']", uuid), lib(t, "", "render_gschema_override", "['org.gnome.Nautilus.desktop']", uuid)} {
		if strings.Contains(out, "nocap-") || strings.Contains(out, "127.0.0.1") {
			t.Errorf("web app in the desktop's settings:\n%s", out)
		}
	}
	locks := strings.Split(strings.TrimSpace(lib(t, "", "render_dconf_locks")), "\n")
	for _, l := range locks[1:] {
		if !strings.HasPrefix(l, "/org/gnome/") || strings.Contains(l, " ") {
			t.Errorf("bad lock line %q", l)
		}
	}
	if len(locks) > 6 {
		t.Errorf("lock only what's needed (%d locks)", len(locks)-1)
	}
	gdm := keyfile(t, "gdm", lib(t, "", "render_dconf_gdm"))
	if gdm["org/gnome/login-screen"]["logo"] != "'/usr/share/nocapos/desktop/login-logo.png'" {
		t.Errorf("sign-in logo: %v", gdm["org/gnome/login-screen"])
	}
	if css := lib(t, "", "render_gdm_css"); !strings.Contains(css, "#lockDialogGroup") || !strings.Contains(css, "resource:///org/gnome/shell/theme/nocapos-login.jpg") {
		t.Errorf("sign-in css: %s", css)
	}
	if got := lib(t, "", "gv_list", "a", "", "b"); got != "['a', 'b']\n" {
		t.Errorf("gv_list: %q", got)
	}
}

func TestGDMHelpers(t *testing.T) {
	deb := root(t, map[string]string{"etc/gdm3/daemon.conf": "[daemon]\n", "lib/systemd/system/gdm3.service": ""})
	if got := strings.TrimSpace(lib(t, "", "gdm_config", deb)); got != "/etc/gdm3/daemon.conf" {
		t.Errorf("debian conf %q", got)
	}
	if got := strings.TrimSpace(lib(t, "", "gdm_unit", deb)); got != "gdm3.service" {
		t.Errorf("debian unit %q", got)
	}
	ubuntu := root(t, map[string]string{"etc/gdm3/custom.conf": "", "usr/lib/systemd/system/gdm.service": ""})
	if got := strings.TrimSpace(lib(t, "", "gdm_config", ubuntu)); got != "/etc/gdm3/custom.conf" {
		t.Errorf("ubuntu conf %q", got)
	}
	if got := strings.TrimSpace(lib(t, "", "gdm_unit", ubuntu)); got != "gdm.service" {
		t.Errorf("ubuntu unit %q", got)
	}
	fedora := root(t, map[string]string{"etc/gdm/": ""})
	if got := strings.TrimSpace(lib(t, "", "gdm_config", fedora)); got != "/etc/gdm/custom.conf" {
		t.Errorf("fedora conf %q", got)
	}
	for in, want := range map[string]string{"GNOME Shell 48.4": "48", "GNOME Shell 43.9": "43", "GNOME Shell 49.beta": "0", "": "0"} {
		if got := strings.TrimSpace(lib(t, "", "gnome_major", in)); got != want {
			t.Errorf("gnome_major(%q) = %q, want %s", in, got, want)
		}
	}
	got := lib(t, "[daemon]\n#WaylandEnable=false\n\n[security]\n", "ini_set", "daemon", "WaylandEnable", "false")
	if got != "[daemon]\nWaylandEnable=false\n\n[security]\n" {
		t.Errorf("ini_set: %q", got)
	}
}

// The dock: native apps only, whichever of each the machine has.
func TestDockApps(t *testing.T) {
	r := root(t, map[string]string{
		"usr/share/applications/org.gnome.Nautilus.desktop":   "",
		"usr/share/applications/org.gnome.Settings.desktop":   "",
		"usr/share/applications/org.gnome.Software.desktop":   "",
		"usr/share/applications/org.gnome.Terminal.desktop":   "",
		"usr/share/applications/org.gnome.TextEditor.desktop": "",
		"usr/share/applications/chromium.desktop":             "",
		"usr/share/applications/firefox-esr.desktop":          "",
	})
	want := "org.gnome.Nautilus.desktop\nfirefox-esr.desktop\norg.gnome.Software.desktop\norg.gnome.Terminal.desktop\norg.gnome.TextEditor.desktop\norg.gnome.Settings.desktop\n"
	if got := lib(t, "", "dock_apps", r); got != want {
		t.Errorf("dock_apps:\n%s", got)
	}
	// Raspberry Pi OS: its own Chromium, Console, gedit, an older Settings.
	pi := root(t, map[string]string{
		"usr/share/applications/chromium-browser.desktop":     "",
		"usr/share/applications/org.gnome.Console.desktop":    "",
		"usr/share/applications/org.gnome.gedit.desktop":      "",
		"usr/share/applications/gnome-control-center.desktop": "",
	})
	want = "org.gnome.Nautilus.desktop\nchromium-browser.desktop\norg.gnome.Console.desktop\norg.gnome.gedit.desktop\ngnome-control-center.desktop\n"
	if got := lib(t, "", "dock_apps", pi); got != want {
		t.Errorf("dock_apps on a Pi:\n%s", got)
	}
}

// The screen's session: NoCapOS's, never a distribution's own desktop.
func TestNoCapSession(t *testing.T) {
	for _, kind := range []string{"wayland", "x11"} {
		f := lib(t, "", "render_session_desktop", kind)
		if !strings.Contains(f, "\nExec=gnome-session\n") || !strings.Contains(f, "\nDesktopNames=GNOME\n") || !strings.HasPrefix(f, "[Desktop Entry]\n") {
			t.Errorf("%s session:\n%s", kind, f)
		}
	}
	r := root(t, map[string]string{
		"usr/share/wayland-sessions/LXDE-pi-labwc.desktop": "",
		"usr/share/wayland-sessions/nocapos.desktop":       "",
		"usr/share/wayland-sessions/gnome.desktop":         "",
		"usr/share/xsessions/LXDE-pi-x.desktop":            "",
		"usr/share/xsessions/nocapos.desktop":              "",
	})
	if got := lib(t, "", "other_sessions", r); got != "/usr/share/wayland-sessions/LXDE-pi-labwc.desktop\n/usr/share/wayland-sessions/gnome.desktop\n/usr/share/xsessions/LXDE-pi-x.desktop\n" {
		t.Errorf("other_sessions:\n%s", got)
	}
	// An account lightdm left on the Pi's desktop moves to NoCapOS's.
	pi := "[User]\nSession=LXDE-pi-labwc\nXSession=LXDE-pi-labwc\nIcon=/home/pi/.face\nSystemAccount=false\n"
	if _, err := libErr(t, pi, "session_file_wants_change"); err != nil {
		t.Error("the Pi desktop's session should change")
	}
	got := lib(t, pi, "session_file_with", "User")
	if got != "[User]\nSession=nocapos\nXSession=nocapos\nIcon=/home/pi/.face\nSystemAccount=false\n" {
		t.Errorf("session_file_with:\n%s", got)
	}
	if _, err := libErr(t, got, "session_file_wants_change"); err == nil {
		t.Error("already NoCapOS's: nothing to change")
	}
	if _, err := libErr(t, "[User]\nSystemAccount=false\n", "session_file_wants_change"); err == nil {
		t.Error("no session saved: nothing to change")
	}
	if got := lib(t, "[User]\nIcon=x\n", "session_file_with", "User"); got != "[User]\nIcon=x\nSession=nocapos\nXSession=nocapos\n" {
		t.Errorf("session_file_with (none saved):\n%s", got)
	}
}
