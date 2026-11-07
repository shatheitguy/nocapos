// Package deploytest tests the pure helper functions in deploy/install.sh
// (kernel command line editing for the boot splash) by running them in bash.
package deploytest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	beginMark = "# >>> cmdline helpers"
	endMark   = "# <<< cmdline helpers"
)

func helpers(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	i, j := strings.Index(s, beginMark), strings.Index(s, endMark)
	if i < 0 || j < i {
		t.Fatal("cmdline helpers markers not found in install.sh")
	}
	return s[i:j]
}

// run calls one helper with stdin and args and returns its output.
func run(t *testing.T, fn, stdin string, args ...string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	script := helpers(t) + "\n" + fn + ` "$@"`
	cmd := exec.Command(bash, append([]string{"-c", script, "_"}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v: %s", fn, err, out)
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n")
}

func TestCmdlineWith(t *testing.T) {
	pi := "console=serial0,115200 console=tty1 root=PARTUUID=4e639091-02 rootfstype=ext4 fsck.repair=yes rootwait cfg80211.ieee80211_regdom=GB\n"
	got := run(t, "cmdline_with", pi, "quiet", "splash", "plymouth.ignore-serial-consoles")
	want := "console=serial0,115200 console=tty1 root=PARTUUID=4e639091-02 rootfstype=ext4 fsck.repair=yes rootwait cfg80211.ieee80211_regdom=GB quiet splash plymouth.ignore-serial-consoles\n"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if strings.Count(got, "\n") != 1 {
		t.Error("cmdline.txt must stay one line")
	}
	// Already there: unchanged; a stray second line and CRLF are folded in.
	if got := run(t, "cmdline_with", "a quiet b\r\nsplash\n", "quiet", "splash"); got != "a quiet b splash\n" {
		t.Errorf("idempotent: %q", got)
	}
	// Globs are never expanded.
	if got := run(t, "cmdline_with", "x=* y\n", "splash"); got != "x=* y splash\n" {
		t.Errorf("glob: %q", got)
	}
	// "quiet" inside another token doesn't count as having it.
	if got := run(t, "cmdline_with", "loglevel=3 noquiet\n", "quiet"); got != "loglevel=3 noquiet quiet\n" {
		t.Errorf("substring: %q", got)
	}
	if got := run(t, "cmdline_without", want, "quiet", "splash", "plymouth.ignore-serial-consoles"); got != pi {
		t.Errorf("without: %q", got)
	}
}

func TestGrubDefaultWith(t *testing.T) {
	grub := `# If you change this file, run 'update-grub' afterwards
GRUB_DEFAULT=0
GRUB_TIMEOUT=5
GRUB_DISTRIBUTOR=` + "`lsb_release -i -s 2> /dev/null || echo Debian`" + `
GRUB_CMDLINE_LINUX_DEFAULT="quiet"
GRUB_CMDLINE_LINUX=""
`
	got := run(t, "grub_default_with", grub, "splash")
	want := strings.Replace(grub, `GRUB_CMDLINE_LINUX_DEFAULT="quiet"`, `GRUB_CMDLINE_LINUX_DEFAULT="quiet splash"`, 1)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if again := run(t, "grub_default_with", got, "quiet", "splash"); again != got {
		t.Errorf("not idempotent:\n%s", again)
	}
	// Single quotes and extra options.
	if got := run(t, "grub_default_with", "GRUB_CMDLINE_LINUX_DEFAULT='loglevel=3 nowatchdog'\n", "quiet", "splash"); got != "GRUB_CMDLINE_LINUX_DEFAULT=\"loglevel=3 nowatchdog quiet splash\"\n" {
		t.Errorf("single quotes: %q", got)
	}
	// Missing line: added; GRUB_CMDLINE_LINUX itself is left alone.
	if got := run(t, "grub_default_with", "GRUB_CMDLINE_LINUX=\"console=ttyS0\"\n", "quiet", "splash"); got != "GRUB_CMDLINE_LINUX=\"console=ttyS0\"\nGRUB_CMDLINE_LINUX_DEFAULT=\"quiet splash\"\n" {
		t.Errorf("missing line: %q", got)
	}
	if back := run(t, "grub_default_without", want, "splash"); back != grub {
		t.Errorf("without:\n%s", back)
	}
}

func TestTokensMissing(t *testing.T) {
	if got := run(t, "tokens_missing", "", "quiet loglevel=3", "quiet", "splash"); got != "splash\n" {
		t.Errorf("got %q", got)
	}
	if got := run(t, "tokens_missing", "", "quiet splash", "quiet", "splash"); got != "\n" {
		t.Errorf("none missing: %q", got)
	}
}
