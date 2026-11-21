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

	"alfaos/alfad/internal/hostaccess"
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

// NoCap Desktop is NoCap OS's own: the compositor, the sign-in service and
// what NoCap Shell and the screen need. No third-party default apps (Files,
// Terminal, Text Editor, Photos, Settings and the App Store are NoCap OS's
// own), no web browser, nothing from the earlier desktop.
func TestDesktopPackages(t *testing.T) {
	banned := regexp.MustCompile(`(^|[|?])(gdm3?|gnome-shell|gnome-session|gnome-remote-desktop|nautilus|gnome-console|gnome-terminal|task-gnome-desktop|thunar|Thunar|pcmanfm|foot|mousepad|ristretto|firefox(-esr)?|chromium(-browser)?|brave-browser|lightdm|sddm|network-manager|NetworkManager|desktop-base|libreoffice\S*|x11vnc|xclip|dconf-cli)($|\|)`)
	required := map[string][]string{
		"debian": {"labwc", "greetd", "xwayland", "wayvnc", "swayidle", "libgtk-3-0t64|libgtk-3-0", "libgtk-layer-shell0", "libwebkit2gtk-4.1-0", "libjson-glib-1.0-0", "libpolkit-agent-1-0", "libpam0g", "xdg-desktop-portal", "xdg-desktop-portal-wlr", "xdg-desktop-portal-gtk", "wl-clipboard", "pipewire", "flatpak", "libgl1-mesa-dri", "libpam-systemd"},
		"fedora": {"labwc", "greetd", "wayvnc", "gtk-layer-shell", "webkit2gtk4.1", "xdg-desktop-portal", "wl-clipboard", "flatpak"},
		"arch":   {"labwc", "greetd", "wayvnc", "gtk-layer-shell", "webkit2gtk-4.1", "xdg-desktop-portal", "wl-clipboard", "flatpak"},
	}
	required["ubuntu"] = required["debian"]
	for family, want := range required {
		lines := strings.Split(strings.TrimSpace(lib(t, "", "desktop_packages", family)), "\n")
		have := map[string]bool{}
		for _, l := range lines {
			if banned.MatchString(l) {
				t.Errorf("%s: unwanted package line %q", family, l)
			}
			if strings.TrimSpace(l) != l || l == "" || strings.Contains(l, " ") || strings.Contains(l, "#") {
				t.Errorf("%s: malformed line %q", family, l)
			}
			have[l] = true
		}
		for _, w := range want {
			if !have[w] {
				t.Errorf("%s: missing %q", family, w)
			}
		}
		// Every package says why it's there.
		for _, l := range strings.Split(strings.TrimSpace(lib(t, "", "desktop_package_list", family)), "\n") {
			if pkg, why, ok := strings.Cut(l, "#"); !ok || strings.TrimSpace(pkg) == "" || len(strings.TrimSpace(why)) < 5 {
				t.Errorf("%s: no reason given for %q", family, l)
			}
		}
	}
	if got := lib(t, "", "desktop_packages", "none"); got != "" {
		t.Errorf("unsupported family should have no packages: %q", got)
	}
	// The earlier desktop's packages are known (they stay installed, unused).
	if old := lib(t, "", "eval", `echo "$OLD_DESKTOP_PACKAGES"`); !strings.Contains(old, "gdm3") || !strings.Contains(old, "gnome-shell") {
		t.Errorf("OLD_DESKTOP_PACKAGES = %q", old)
	}
	// Building NoCap Shell on the machine itself names real build packages.
	for _, f := range []string{"debian", "fedora", "arch"} {
		if !strings.Contains(lib(t, "", "shell_build_packages", f), "webkit2gtk") && !strings.Contains(lib(t, "", "shell_build_packages", f), "webkit2gtk-4.1") {
			t.Errorf("%s: shell build packages lack WebKitGTK", f)
		}
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
}

// Package selection on every architecture: the same list, picked from what
// the repositories have. Debian 12 has no labwc: the selection says so (and
// install.sh asks for Debian 13); everywhere else it's complete.
func TestDesktopSelect(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", "packages", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for name, want := range map[string][]string{
		"debian-13-arm64":      {"labwc", "greetd", "libgtk-3-0t64", "polkitd", "wayvnc", "wl-clipboard", "guacd", "libguac-client-vnc0", "systemd-zram-generator"},
		"debian-13-amd64":      {"labwc", "greetd", "libgtk-3-0t64", "libwebkit2gtk-4.1-0", "firmware-amd-graphics"},
		"raspios-trixie-armhf": {"labwc", "greetd", "libgtk-3-0t64", "libgtk-layer-shell0", "wayvnc"},
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
		for _, never := range []string{"gdm3", "gnome-shell", "thunar", "foot", "mousepad", "firefox-esr", "chromium", "xclip"} {
			if picks[never] {
				t.Errorf("%s: %s picked", name, never)
			}
		}
	}
	got := lib(t, read("debian-12-armhf"), "desktop_select", "debian")
	if !strings.Contains(got, "\n!labwc\n") && !strings.HasPrefix(got, "!labwc\n") {
		t.Errorf("Debian 12 has no labwc, yet:\n%s", got)
	}
	if strings.Count(got, "\n!") > 0 && strings.Count(got, "!") != 1 {
		t.Errorf("Debian 12: only labwc should be missing:\n%s", got)
	}
	// Optional lines with nothing available are listed as such; required
	// ones make the selection fail.
	got = lib(t, "labwc\n", "desktop_select", "debian")
	if !strings.Contains(got, "\n!greetd\n") || !strings.Contains(got, "\n?grim\n") || !strings.HasPrefix(got, "labwc\n") {
		t.Errorf("selection with almost nothing:\n%s", got)
	}
}

func TestAccentCSS(t *testing.T) {
	g4 := lib(t, "", "render_gtk4_accent")
	if !strings.Contains(g4, "@define-color accent_bg_color #e8232b;") || !strings.Contains(g4, "@define-color accent_fg_color #ffffff;") {
		t.Errorf("gtk4: %s", g4)
	}
	g3 := lib(t, "", "render_gtk3_accent")
	if !strings.Contains(g3, "button.suggested-action") || !strings.Contains(g3, "switch:checked") || !strings.Contains(g3, "#e8232b") {
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

// keyfile checks a key file's shape: [groups], key=value lines and
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

// The screen: greetd shows NoCap's sign-in screen as its own account, then
// starts the NoCap Desktop session.
func TestSignInService(t *testing.T) {
	plain := lib(t, "", "render_greetd_config", "_greetd")
	for _, want := range []string{"[default_session]\ncommand = \"/usr/libexec/nocapos/nocap-greeter-session\"\nuser = \"_greetd\"\n", "[terminal]\nvt = 1\n"} {
		if !strings.Contains(plain, want) {
			t.Errorf("greetd config lacks %q:\n%s", want, plain)
		}
		// alfad writes the same file when automatic sign-in changes.
		if !strings.Contains(hostaccess.GreetdConfig("_greetd", ""), want) {
			t.Errorf("hostaccess.GreetdConfig lacks %q", want)
		}
	}
	if strings.Contains(plain, "initial_session") {
		t.Errorf("automatic sign-in on by default:\n%s", plain)
	}
	auto := lib(t, "", "render_greetd_config", "_greetd", "sam")
	if !strings.Contains(auto, "[initial_session]\ncommand = \"/usr/libexec/nocapos/nocap-session\"\nuser = \"sam\"\n") {
		t.Errorf("autologin config:\n%s", auto)
	}
	if got := strings.TrimSpace(lib(t, auto, "greetd_autologin")); got != "sam" {
		t.Errorf("greetd_autologin = %q", got)
	}
	if got := strings.TrimSpace(lib(t, plain, "greetd_autologin")); got != "" {
		t.Errorf("greetd_autologin (off) = %q", got)
	}
	for passwd, want := range map[string]string{
		"root:x:0:0::/root:/bin/bash\n_greetd:x:110:115::/var/lib/greetd:/usr/sbin/nologin\n": "_greetd",
		"greetd:x:980:980::/var/lib/greetd:/sbin/nologin\n":                                   "greetd",
		"greeter:x:964:964::/var/lib/greeter:/usr/bin/nologin\n":                              "greeter",
		"root:x:0:0::/root:/bin/bash\n":                                                       "_greetd",
	} {
		if got := strings.TrimSpace(lib(t, "", "greeter_user", root(t, map[string]string{"etc/passwd": passwd}))); got != want {
			t.Errorf("greeter_user(%q) = %q, want %q", passwd, got, want)
		}
	}
	// The lock screen checks the account's own password; the keyring opens at sign-in.
	if p := lib(t, "", "render_pam_lock", "debian"); !strings.Contains(p, "@include common-auth") {
		t.Errorf("pam (debian): %s", p)
	}
	if p := lib(t, "", "render_pam_lock", "fedora"); !strings.Contains(p, "auth include system-auth") {
		t.Errorf("pam (fedora): %s", p)
	}
	pam := "#%PAM-1.0\nauth include common-auth\n"
	with := lib(t, pam, "pam_with_keyring")
	if !strings.Contains(with, "auth       optional   pam_gnome_keyring.so") || lib(t, with, "pam_with_keyring") != with {
		t.Errorf("keyring pam:\n%s", with)
	}
	portals := keyfile(t, "portals", lib(t, "", "render_portals_conf"))
	if portals["preferred"]["org.freedesktop.impl.portal.ScreenCast"] != "wlr" || portals["preferred"]["default"] != "gtk" {
		t.Errorf("portals: %v", portals)
	}
}

// The session entry, for tools that list sessions: NoCapOS's own.
func TestNoCapSession(t *testing.T) {
	f := lib(t, "", "render_session_desktop")
	if !strings.HasPrefix(f, "[Desktop Entry]\n") || !strings.Contains(f, "\nExec=/usr/libexec/nocapos/nocap-session\n") || !strings.Contains(f, "\nDesktopNames=NoCap;wlroots\n") {
		t.Errorf("session:\n%s", f)
	}
	// alfad's repair writes the very same file.
	if want := "[Desktop Entry]\n# The NoCapOS desktop (written by NoCapOS's install.sh).\nName=NoCapOS\nComment=The NoCapOS desktop\nExec=/usr/libexec/nocapos/nocap-session\nTryExec=/usr/libexec/nocapos/nocap-session\nType=Application\nDesktopNames=NoCap;wlroots\n"; f != want {
		t.Errorf("session file differs from hostaccess.SessionDesktop:\n%q", f)
	}
}

// The desktop clipboard agent: a user service in every graphical session.
func TestClipboardUnit(t *testing.T) {
	u := lib(t, "", "render_clipboard_unit", "/usr/local/bin/cap")
	for _, want := range []string{"\nExecStart=/usr/local/bin/cap clipboard agent\n", "\nPartOf=graphical-session.target\n", "\nWantedBy=graphical-session.target\n", "\nRestart=on-failure\n"} {
		if !strings.Contains(u, want) {
			t.Errorf("clipboard unit lacks %q:\n%s", want, u)
		}
	}
}
