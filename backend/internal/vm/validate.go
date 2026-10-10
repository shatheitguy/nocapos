package vm

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Preset is a starting point for a new machine of one kind.
type Preset struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CPUs      int    `json:"cpus"`
	MemoryMiB int    `json:"memory_mib"`
	DiskGiB   int    `json:"disk_gib"`
	UEFI      bool   `json:"uefi"`
	TPM       bool   `json:"tpm"`
	// Virtio: paravirtual disk and network. Fast, but Windows needs drivers
	// for it during setup, so Windows starts on SATA + e1000e.
	Virtio bool   `json:"virtio"`
	Video  string `json:"video"`
}

var presets = []Preset{
	{ID: OSWindows11, Title: "Windows 11", CPUs: 4, MemoryMiB: 8192, DiskGiB: 80, UEFI: true, TPM: true, Video: "vga"},
	{ID: OSWindows10, Title: "Windows 10", CPUs: 4, MemoryMiB: 4096, DiskGiB: 64, UEFI: true, TPM: true, Video: "vga"},
	{ID: OSLinux, Title: "Ubuntu, Debian or other Linux", CPUs: 2, MemoryMiB: 4096, DiskGiB: 32, Virtio: true, Video: "virtio"},
	{ID: OSAndroid, Title: "Android-x86", CPUs: 4, MemoryMiB: 4096, DiskGiB: 16, Video: "vga"},
	{ID: OSOther, Title: "Other", CPUs: 2, MemoryMiB: 2048, DiskGiB: 20, Video: "vga"},
}

// Presets returns a copy of the presets.
func Presets() []Preset { return append([]Preset(nil), presets...) }

func presetFor(os string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == os {
			return p, true
		}
	}
	return Preset{}, false
}

func isWindows(os string) bool { return os == OSWindows11 || os == OSWindows10 }

// Limits for one machine.
const (
	maxCPUs      = 64
	minMemoryMiB = 256
	maxMemoryMiB = 1 << 20 // 1 TiB
	minDiskGiB   = 1
	maxDiskGiB   = 64 << 10 // 64 TiB
	maxDisks     = 8
)

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9 _.-]{0,46}[A-Za-z0-9_.-])?$`)
	snapRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	ifRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,14}$`)
)

// ValidName checks a new machine name. It is also the folder name of its disks.
func ValidName(n string) error {
	if !nameRe.MatchString(n) || strings.Contains(n, "..") || strings.Contains(n, "  ") {
		return bad("names start with a letter or number and use letters, numbers, spaces and _ . - (up to 48 characters)")
	}
	switch strings.ToLower(n) {
	case "status", "stats", "install", "isos": // words the API uses
		return bad("%q is a reserved name; pick another", n)
	}
	return nil
}

// ValidSnapshotName checks a snapshot name.
func ValidSnapshotName(n string) error {
	if !snapRe.MatchString(n) {
		return bad("snapshot names start with a letter or number and use letters, numbers and _ . - (up to 64 characters)")
	}
	return nil
}

func validNetwork(n Network, ifaces []HostIface) error {
	switch n.Mode {
	case NetNAT, NetNone:
		return nil
	case NetBridge, NetDirect:
	default:
		return bad("network must be nat, bridge, direct or none")
	}
	if !ifRe.MatchString(n.Source) {
		return bad("pick a host network interface")
	}
	for _, i := range ifaces {
		if i.Name == n.Source {
			if n.Mode == NetBridge && i.Kind != "bridge" {
				return bad("%s isn't a bridge; use \"Direct on a host interface\" for it, or create a bridge first", n.Source)
			}
			return nil
		}
	}
	return bad("there's no network interface called %s on this server", n.Source)
}

func validModel(m string) error {
	switch m {
	case "", "virtio", "e1000e", "e1000", "rtl8139":
		return nil
	}
	return bad("network card must be virtio, e1000e, e1000 or rtl8139")
}

// Image kinds an admin can pick.
var (
	isoExts  = []string{".iso"}
	diskExts = []string{".qcow2", ".img", ".raw", ".vmdk", ".vdi", ".vhd", ".vhdx"}
)

// checkImage validates a host path picked for an ISO or a disk image: it must
// be absolute, a regular file once links are resolved, inside one of the
// allowed folders (the Files locations and Virtual Desk's own folder), and
// have one of the expected extensions. It returns the resolved path.
func checkImage(p string, roots []string, exts []string, what string) (string, error) {
	if p == "" {
		return "", bad("pick the %s", what)
	}
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsAny(p, "\x00\n\r") {
		return "", bad("the %s path must be a full path like /srv/isos/setup.iso", what)
	}
	ext := strings.ToLower(filepath.Ext(p))
	okExt := false
	for _, e := range exts {
		if ext == e {
			okExt = true
		}
	}
	if !okExt {
		return "", bad("the %s must be a %s file", what, strings.Join(exts, ", "))
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", bad("there's no file at %s", p)
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.Mode().IsRegular() {
		return "", bad("%s isn't a regular file", p)
	}
	for _, r := range roots {
		if r == "" {
			continue
		}
		rr, err := filepath.EvalSymlinks(r)
		if err != nil {
			rr = filepath.Clean(r)
		}
		if inside(real, rr) {
			return real, nil
		}
	}
	return "", bad("the %s must be inside one of your Files locations", what)
}

// inside reports whether p is dir or below it (clean, native paths).
func inside(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// diskTarget picks the next free device name for a bus (vda, vdb… or sda…).
func diskTarget(bus string, used map[string]bool) string {
	prefix := "sd"
	if bus == "virtio" {
		prefix = "vd"
	}
	for c := 'a'; c <= 'z'; c++ {
		t := prefix + string(c)
		if !used[t] {
			used[t] = true
			return t
		}
	}
	return ""
}

func gib(n int) int64 { return int64(n) << 30 }
