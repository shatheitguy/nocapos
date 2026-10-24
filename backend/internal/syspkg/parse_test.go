package syspkg

import (
	"context"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseDpkg(t *testing.T) {
	out := "bash\t5.2.15-2+b2\tamd64\t7164\tii \tGNU Bourne Again SHell\n" +
		"zlib1g:i386\t1:1.2.13\ti386\t160\thi \tcompression library - runtime\n" +
		"oldpkg\t1.0\tall\t10\trc \tremoved, config left\n" +
		"broken line\n"
	got := parseDpkg(out)
	want := []Package{
		{Name: "bash", Version: "5.2.15-2+b2", Arch: "amd64", Size: 7164 * 1024, Description: "GNU Bourne Again SHell"},
		{Name: "zlib1g:i386", Version: "1:1.2.13", Arch: "i386", Size: 160 * 1024, Description: "compression library - runtime", Held: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseRPM(t *testing.T) {
	out := "gpg-pubkey\tabc-def\t(none)\t0\tgpg(Fedora)\n" +
		"bash\t5.2.26-3.fc40\tx86_64\t8401744\tThe GNU Bourne Again shell\n" +
		"tzdata\t2024a-5.fc40\tnoarch\t1717045\tTimezone data\n"
	got := parseRPM(out)
	if len(got) != 2 || got[0].Name != "bash" || got[0].Size != 8401744 || got[0].Arch != "x86_64" || got[1].Version != "2024a-5.fc40" {
		t.Fatalf("got %+v", got)
	}
}

func TestParsePacmanQi(t *testing.T) {
	out := `Name            : bash
Version         : 5.2.026-2
Description     : The GNU Bourne Again shell
Architecture    : x86_64
Installed Size  : 9.37 MiB
Depends On      : readline  libreadline.so=8-64
                  glibc  ncurses

Name            : zstd
Version         : 1.5.6-1
Description     : Zstandard - Fast real-time compression algorithm
Architecture    : x86_64
Installed Size  : 3.12 KiB
`
	got := parsePacmanQi(out)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "bash" || got[0].Size != 9825157 || got[0].Description != "The GNU Bourne Again shell" {
		t.Errorf("bash: %+v", got[0])
	}
	if got[1].Name != "zstd" || got[1].Size != 3194 {
		t.Errorf("zstd: %+v", got[1])
	}
}

func TestParseAptUpgradable(t *testing.T) {
	out := `Listing... Done
bash/jammy-updates 5.1-6ubuntu1.1 amd64 [upgradable from: 5.1-6ubuntu1]
libssl3/jammy-security,jammy-updates 3.0.2-0ubuntu1.18 amd64 [upgradable from: 3.0.2-0ubuntu1.15]
WARNING: apt does not have a stable CLI interface. Use with caution in scripts.
`
	got := parseAptUpgradable(out)
	want := []Update{
		{Name: "bash", Available: "5.1-6ubuntu1.1", Arch: "amd64", Current: "5.1-6ubuntu1"},
		{Name: "libssl3", Available: "3.0.2-0ubuntu1.18", Arch: "amd64", Current: "3.0.2-0ubuntu1.15"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseDnfCheckUpdate(t *testing.T) {
	out := `
Last metadata expiration check: 0:12:01 ago on Sat 17 Oct 2026.

bash.x86_64                       5.2.26-4.fc40                 updates
a-very-long-package-name-that-wraps.noarch
                                  1.2.3-1.fc40                  updates
kernel.x86_64                     6.10.3-200.fc40               updates
Obsoleting Packages
grub2-tools.x86_64                1:2.06-121.fc40               updates
`
	got := parseDnfCheckUpdate(out)
	want := []Update{
		{Name: "bash", Arch: "x86_64", Available: "5.2.26-4.fc40"},
		{Name: "a-very-long-package-name-that-wraps", Arch: "noarch", Available: "1.2.3-1.fc40"},
		{Name: "kernel", Arch: "x86_64", Available: "6.10.3-200.fc40"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParsePacmanQuAndZypper(t *testing.T) {
	p := parsePacmanQu("bash 5.2.026-1 -> 5.2.026-2\nlinux 6.9.1.arch1-1 -> 6.9.2.arch1-1 [ignored]\n")
	if len(p) != 2 || p[0].Name != "bash" || p[0].Current != "5.2.026-1" || p[0].Available != "5.2.026-2" || !p[1].Held {
		t.Errorf("pacman: %+v", p)
	}
	z := parseZypperUpdates(`S | Repository | Name | Current Version | Available Version | Arch
--+------------+------+-----------------+-------------------+-------
v | Main       | bash | 5.2.15-1.1      | 5.2.15-1.2        | x86_64
`)
	if len(z) != 1 || z[0].Name != "bash" || z[0].Current != "5.2.15-1.1" || z[0].Available != "5.2.15-1.2" {
		t.Errorf("zypper: %+v", z)
	}
}

func TestParseHolds(t *testing.T) {
	if got := parseDnfVersionlock("Last metadata expiration check: 1:00:00 ago.\nbash-0:5.2.26-3.fc40.*\npython3-dnf-plugin-versionlock-4.4.4-1.fc40.*\n"); !reflect.DeepEqual(got, []string{"bash", "python3-dnf-plugin-versionlock"}) {
		t.Errorf("versionlock: %v", got)
	}
	if got := parsePacmanIgnore("[options]\n#IgnorePkg = nope\nIgnorePkg   = linux linux-headers\nIgnorePkg = vim\n"); !reflect.DeepEqual(got, []string{"linux", "linux-headers", "vim"}) {
		t.Errorf("pacman ignore: %v", got)
	}
	if got := parseZypperLocks("# | Name | Matches | Type    | Repository\n--+------+---------+---------+-----------\n1 | vim  | 1       | package | (any)\n"); !reflect.DeepEqual(got, []string{"vim"}) {
		t.Errorf("zypper locks: %v", got)
	}
	if got := parseAptSimRemovals("NOTE: simulation\nRemv nano [7.2-1]\nRemv sudo [1.9]\nInst x\n"); !reflect.DeepEqual(got, []string{"nano", "sudo"}) {
		t.Errorf("sim removals: %v", got)
	}
}

func TestPlan(t *testing.T) {
	p, err := Plan("apt", ActRemove, "nano")
	if err != nil || len(p) != 1 || p[0][0] != "apt-get" || p[0][len(p[0])-1] != "nano" {
		t.Fatalf("apt remove: %v %v", p, err)
	}
	if p, _ := Plan("dnf", ActHold, "bash"); !reflect.DeepEqual(p, [][]string{{"dnf", "versionlock", "add", "bash"}}) {
		t.Errorf("dnf hold: %v", p)
	}
	if _, err := Plan("pacman", ActHold, "bash"); err == nil || !strings.Contains(err.Error(), "IgnorePkg") {
		t.Errorf("pacman hold should explain IgnorePkg: %v", err)
	}
	if _, err := Plan("zypper", ActAutoremove, ""); err != ErrUnsupported {
		t.Errorf("zypper autoremove: %v", err)
	}
	for _, bad := range []string{"-y", "--purge", "a b", "x;rm", "", "$(id)"} {
		if _, err := Plan("apt", ActInstall, bad); err == nil {
			t.Errorf("Plan accepted %q", bad)
		}
	}
	if _, err := Plan("apt", ActInstallFile, "relative.deb"); err == nil {
		t.Error("install-file needs an absolute path")
	}
	if p, _ := Plan("apt", ActInstallFile, "/var/lib/nocapos/package-uploads/up-1/x.deb"); p[0][len(p[0])-1] != "/var/lib/nocapos/package-uploads/up-1/x.deb" {
		t.Errorf("install-file: %v", p)
	}
	if _, err := Plan("apk", ActRefresh, ""); err == nil {
		t.Error("unknown manager should fail")
	}
}

func TestProtected(t *testing.T) {
	for _, n := range []string{"systemd", "libc6", "bash:amd64", "linux-image-6.1.0-18-amd64", "kernel-core", "docker-ce", "grub-efi-amd64", "openssh-server"} {
		if !Protected(n) {
			t.Errorf("%s should be protected", n)
		}
	}
	for _, n := range []string{"nano", "htop", "vlc", "linuxlogo"} {
		if Protected(n) {
			t.Errorf("%s shouldn't be protected", n)
		}
	}
}

// The installer's PKG_TABLE and Features must list the same packages.
func TestInstallerTableMatchesFeatures(t *testing.T) {
	b, err := os.ReadFile("../../../deploy/install.sh")
	if err != nil {
		t.Skip("installer not found:", err)
	}
	s := string(b)
	start := strings.Index(s, "PKG_TABLE='")
	if start < 0 {
		t.Fatal("install.sh has no PKG_TABLE")
	}
	body := s[start+len("PKG_TABLE='"):]
	body = body[:strings.Index(body, "'")]
	managers := []string{"apt", "dnf", "pacman", "zypper"}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		cols := strings.Split(strings.TrimSpace(line), "|")
		if len(cols) != 1+len(managers) {
			t.Fatalf("bad PKG_TABLE line %q", line)
		}
		f, ok := FeatureByID(cols[0])
		if !ok {
			t.Errorf("PKG_TABLE has unknown feature %q", cols[0])
			continue
		}
		seen[f.ID] = true
		for i, m := range managers {
			var got []string
			if c := strings.TrimSpace(cols[i+1]); c != "-" {
				got = strings.Fields(c)
			}
			if !reflect.DeepEqual(got, f.Packages[m]) {
				t.Errorf("%s/%s: installer has %v, Features has %v", f.ID, m, got, f.Packages[m])
			}
		}
	}
	for _, f := range Features {
		if !seen[f.ID] {
			t.Errorf("feature %s missing from PKG_TABLE", f.ID)
		}
	}
}

func TestJobsRunStepsAndSkipOptional(t *testing.T) {
	js := NewJobs()
	js.exec = func(ctx context.Context, args []string) *exec.Cmd {
		// Every command "fails" except the Go steps below.
		return exec.CommandContext(ctx, "this-command-does-not-exist-nocap")
	}
	done := make(chan Job, 1)
	steps := []Step{
		{Args: []string{"apt-get", "install", "-y", "missing"}, Optional: true},
		{Label: "say hi", Fn: func(_ context.Context, out io.Writer) error { _, err := io.WriteString(out, "hi\n"); return err }},
	}
	j, err := js.Start("feature", "media", steps, func(j Job) { done <- j })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case end := <-done:
		if !end.OK || !strings.Contains(end.Output, "hi") || !strings.Contains(end.Output, "skipped") {
			t.Errorf("job: %+v", end)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("job didn't finish")
	}
	if got, ok := js.Get(j.ID); !ok || got.Running {
		t.Errorf("Get: %+v %v", got, ok)
	}

	steps = []Step{{Args: []string{"apt-get", "remove", "x"}}}
	if _, err := js.Start("remove", "x", steps, func(j Job) { done <- j }); err != nil {
		t.Fatal(err)
	}
	if end := <-done; end.OK || end.Error == "" {
		t.Errorf("a failing required step should fail the job: %+v", end)
	}
}

func TestJobsOneAtATime(t *testing.T) {
	js := NewJobs()
	release := make(chan struct{})
	done := make(chan Job, 1)
	block := Step{Label: "wait", Fn: func(context.Context, io.Writer) error { <-release; return nil }}
	if _, err := js.Start("refresh", "", []Step{block}, func(j Job) { done <- j }); err != nil {
		t.Fatal(err)
	}
	if _, err := js.Start("refresh", "", nil, nil); err != ErrBusy {
		t.Errorf("second job: %v, want ErrBusy", err)
	}
	close(release)
	<-done
}

func TestFeatureSteps(t *testing.T) {
	f, _ := FeatureByID("sharing")
	steps := FeatureSteps(f, "apt")
	if len(steps) != 1+len(f.Packages["apt"]) {
		t.Fatalf("steps: %+v", steps)
	}
	for _, s := range steps {
		if !s.Optional {
			t.Errorf("feature steps are best effort: %+v", s)
		}
	}
	if last := steps[len(steps)-1].Args; last[len(last)-1] != "nfs-common" || last[0] != "apt-get" {
		t.Errorf("last step: %v", last)
	}
	rd, _ := FeatureByID("remote-desktop")
	if len(FeatureSteps(rd, "pacman")) != 1 { // only the refresh
		t.Error("pacman has no guacd packages")
	}
}
