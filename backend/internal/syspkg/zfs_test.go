package syspkg

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestContribList(t *testing.T) {
	in := strings.Join([]string{
		"# Debian, as installed",
		"deb http://deb.debian.org/debian bookworm main non-free-firmware",
		"deb-src http://deb.debian.org/debian bookworm main",
		"deb [arch=arm64 signed-by=/usr/share/keyrings/x.gpg] http://deb.debian.org/debian-security/ bookworm-security main # security",
		"deb http://deb.debian.org/debian bookworm-updates main contrib non-free",
		"#deb http://deb.debian.org/debian bookworm-backports main",
		"deb http://archive.raspberrypi.com/debian/ bookworm main",
		"deb [arch=arm64] https://brave-browser-apt-release.s3.brave.com/ stable main",
		"deb https://download.docker.com/linux/debian bookworm stable",
		"deb cdrom:[Debian_GNU/Linux_12]/ bookworm main",
		"",
	}, "\n")
	want := strings.Join([]string{
		"# Debian, as installed",
		"deb http://deb.debian.org/debian bookworm main contrib non-free-firmware",
		"deb-src http://deb.debian.org/debian bookworm main contrib",
		"deb [arch=arm64 signed-by=/usr/share/keyrings/x.gpg] http://deb.debian.org/debian-security/ bookworm-security main contrib # security",
		"deb http://deb.debian.org/debian bookworm-updates main contrib non-free",
		"#deb http://deb.debian.org/debian bookworm-backports main",
		"deb http://archive.raspberrypi.com/debian/ bookworm main",
		"deb [arch=arm64] https://brave-browser-apt-release.s3.brave.com/ stable main",
		"deb https://download.docker.com/linux/debian bookworm stable",
		"deb cdrom:[Debian_GNU/Linux_12]/ bookworm main",
		"",
	}, "\n")
	got, changed := contribList(in)
	if !changed || got != want {
		t.Fatalf("changed=%v\n got:\n%s\nwant:\n%s", changed, got, want)
	}
	if _, again := contribList(got); again {
		t.Fatal("second run should change nothing")
	}
	// Raspbian (32-bit Raspberry Pi OS) already has contrib.
	if _, c := contribList("deb http://raspbian.raspberrypi.org/raspbian/ bullseye main contrib non-free rpi\n"); c {
		t.Fatal("raspbian line already has contrib")
	}
}

func TestContribDeb822(t *testing.T) {
	in := `Types: deb deb-src
URIs: http://deb.debian.org/debian
Suites: bookworm bookworm-updates
Components: main non-free-firmware
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg

Types: deb
URIs: http://deb.debian.org/debian-security
Suites: bookworm-security
Components: main contrib
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg

Types: deb
URIs: http://archive.raspberrypi.com/debian/
Suites: bookworm
Components: main

Enabled: no
Types: deb
URIs: http://deb.debian.org/debian
Suites: bookworm-backports
Components: main
`
	got, changed := contribDeb822(in)
	if !changed {
		t.Fatal("expected a change")
	}
	if !strings.Contains(got, "Components: main non-free-firmware contrib\n") {
		t.Errorf("first stanza not fixed:\n%s", got)
	}
	if strings.Count(got, "contrib") != 2 {
		t.Errorf("only the first stanza should change:\n%s", got)
	}
	if _, again := contribDeb822(got); again {
		t.Fatal("second run should change nothing")
	}
	crlf := strings.ReplaceAll("Types: deb\nURIs: http://deb.debian.org/debian\nSuites: trixie\nComponents: main\n", "\n", "\r\n")
	if got, _ := contribDeb822(crlf); !strings.Contains(got, "Components: main contrib\r\n") {
		t.Errorf("CRLF file: %q", got)
	}
}

func TestEnableContribFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sources.list.d"), 0o755)
	list := filepath.Join(dir, "sources.list")
	src := filepath.Join(dir, "sources.list.d", "debian.sources")
	rpi := filepath.Join(dir, "sources.list.d", "raspi.list")
	_ = os.WriteFile(list, []byte("deb http://deb.debian.org/debian bookworm main\n"), 0o644)
	_ = os.WriteFile(src, []byte("Types: deb\nURIs: http://deb.debian.org/debian\nSuites: bookworm\nComponents: main\n"), 0o644)
	_ = os.WriteFile(rpi, []byte("deb http://archive.raspberrypi.com/debian/ bookworm main\n"), 0o644)
	changed, err := EnableContrib(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed = %v", changed)
	}
	if b, _ := os.ReadFile(list + ".nocapos.bak"); string(b) != "deb http://deb.debian.org/debian bookworm main\n" {
		t.Errorf("backup = %q", b)
	}
	if b, _ := os.ReadFile(rpi); strings.Contains(string(b), "contrib") {
		t.Error("Raspberry Pi's own repository must be left alone")
	}
	if again, _ := EnableContrib(dir); len(again) != 0 {
		t.Errorf("second run changed %v", again)
	}
	if none, err := EnableContrib(filepath.Join(dir, "missing")); err != nil || len(none) != 0 {
		t.Errorf("missing apt dir: %v %v", none, err)
	}
}

func TestHeaderCandidates(t *testing.T) {
	cases := []struct {
		rel, arch string
		pi        bool
		want      []string
	}{
		{"6.1.0-26-amd64", "amd64", false, []string{"linux-headers-6.1.0-26-amd64", "linux-headers-amd64"}},
		{"6.1.0-26-arm64", "arm64", false, []string{"linux-headers-6.1.0-26-arm64", "linux-headers-arm64"}},
		{"6.6.51+rpt-rpi-v8", "arm64", true, []string{"linux-headers-6.6.51+rpt-rpi-v8", "linux-headers-rpi-v8", "linux-headers-rpi-2712", "raspberrypi-kernel-headers"}},
		{"6.6.51+rpt-rpi-2712", "arm64", true, []string{"linux-headers-6.6.51+rpt-rpi-2712", "linux-headers-rpi-2712", "linux-headers-rpi-v8", "raspberrypi-kernel-headers"}},
		{"6.1.21-v8+", "arm64", false, []string{"linux-headers-6.1.21-v8+", "linux-headers-rpi-v8", "linux-headers-rpi-2712", "raspberrypi-kernel-headers"}},
		{"6.1.21-v7l+", "arm", false, []string{"linux-headers-6.1.21-v7l+", "linux-headers-rpi-v7l", "linux-headers-rpi-v7", "raspberrypi-kernel-headers"}},
	}
	for _, c := range cases {
		if got := HeaderCandidates(c.rel, c.arch, c.pi); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.rel, got, c.want)
		}
	}
	have := map[string]bool{"linux-headers-rpi-v8": true, "raspberrypi-kernel-headers": true}
	if got := PickHeaders(HeaderCandidates("6.6.51+rpt-rpi-v8", "arm64", true), func(p string) bool { return have[p] }); got != "linux-headers-rpi-v8" {
		t.Errorf("PickHeaders = %q", got)
	}
	if got := PickHeaders([]string{"a", "b"}, func(string) bool { return false }); got != "" {
		t.Errorf("nothing available = %q", got)
	}
}

func TestZFSDebianSteps(t *testing.T) {
	steps := ZFSDebianSteps()
	var last []string
	for _, s := range steps {
		if s.Args != nil {
			last = s.Args
		}
	}
	if strings.Join(last, " ") != "modprobe zfs" {
		t.Fatalf("last step = %v", last)
	}
	found := false
	for _, s := range steps {
		if strings.Join(s.Args, " ") == "apt-get install -y zfs-dkms zfsutils-linux" {
			found = !s.Optional
		}
	}
	if !found {
		t.Fatal("the zfs-dkms install must be a required step")
	}
	if ZFSNeedsBuild("dnf") {
		t.Fatal("only apt systems build ZFS this way")
	}
}
