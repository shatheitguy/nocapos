package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, ok := range []string{"Windows 11", "ubuntu-server", "a", "web_01.test", "Android 14"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("ValidName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", " lead", "trail ", "-x", "a/b", "../x", "a..b", "two  spaces", "status", "Stats",
		"x\ny", strings.Repeat("a", 49), "ünicode"} {
		if err := ValidName(bad); err == nil {
			t.Errorf("ValidName(%q) should fail", bad)
		}
	}
	if err := ValidSnapshotName("before-update.1"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"", "has space", "-x", "a/b", "x@y"} {
		if err := ValidSnapshotName(bad); err == nil {
			t.Errorf("ValidSnapshotName(%q) should fail", bad)
		}
	}
}

func TestValidNetwork(t *testing.T) {
	ifs := []HostIface{{Name: "enp3s0", Kind: "ethernet"}, {Name: "br0", Kind: "bridge"}}
	for _, ok := range []Network{{Mode: NetNAT}, {Mode: NetNone}, {Mode: NetBridge, Source: "br0"}, {Mode: NetDirect, Source: "enp3s0"}} {
		if err := validNetwork(ok, ifs); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []Network{{Mode: "host"}, {Mode: NetBridge, Source: "enp3s0"}, {Mode: NetDirect, Source: "eth9"}, {Mode: NetDirect, Source: "-x"}} {
		if err := validNetwork(bad, ifs); err == nil {
			t.Errorf("%+v should fail", bad)
		}
	}
}

func TestCheckImage(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	write := func(p string) string {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	iso := write(filepath.Join(root, "isos", "Setup.ISO"))
	outside := write(filepath.Join(other, "evil.iso"))
	notISO := write(filepath.Join(root, "notes.txt"))
	if err := os.MkdirAll(filepath.Join(root, "folder.iso"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots := []string{root}

	got, err := checkImage(iso, roots, isoExts, "ISO")
	if err != nil {
		t.Fatalf("a file in a root: %v", err)
	}
	if want, _ := filepath.EvalSymlinks(iso); got != want {
		t.Errorf("resolved %q, want %q", got, want)
	}
	for name, p := range map[string]string{
		"outside the roots":  outside,
		"wrong extension":    notISO,
		"a folder":           filepath.Join(root, "folder.iso"),
		"missing":            filepath.Join(root, "nope.iso"),
		"relative":           filepath.Join("isos", "Setup.ISO"),
		"not clean":          root + string(filepath.Separator) + "isos" + string(filepath.Separator) + ".." + string(filepath.Separator) + "isos" + string(filepath.Separator) + "Setup.ISO",
		"empty":              "",
		"dot-dot out of one": filepath.Join(root, "..", filepath.Base(other), "evil.iso"),
	} {
		if _, err := checkImage(p, roots, isoExts, "ISO"); err == nil {
			t.Errorf("%s (%q) should be refused", name, p)
		}
	}

	// A link inside a root that points outside it is judged by its target.
	link := filepath.Join(root, "link.iso")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := checkImage(link, roots, isoExts, "ISO"); err == nil {
			t.Error("a link out of the roots should be refused")
		}
	}

	disk := write(filepath.Join(root, "old.qcow2"))
	if _, err := checkImage(disk, roots, diskExts, "disk image"); err != nil {
		t.Errorf("disk image: %v", err)
	}
	if _, err := checkImage(disk, roots, isoExts, "ISO"); err == nil {
		t.Error("a qcow2 isn't an ISO")
	}
}

func TestInside(t *testing.T) {
	sep := string(filepath.Separator)
	base := filepath.Join(sep+"data", "vms")
	for p, want := range map[string]bool{
		base:                            true,
		filepath.Join(base, "a", "b"):   true,
		filepath.Join(sep+"data", "vm"): false,
		base + "2":                      false,
		filepath.Join(sep + "data"):     false,
	} {
		if inside(p, base) != want {
			t.Errorf("inside(%q) != %v", p, want)
		}
	}
}

func TestDiskTarget(t *testing.T) {
	used := map[string]bool{"sda": true}
	if got := diskTarget("sata", used); got != "sdb" {
		t.Errorf("sata after sda = %q", got)
	}
	if got := diskTarget("virtio", used); got != "vda" {
		t.Errorf("virtio = %q", got)
	}
}
