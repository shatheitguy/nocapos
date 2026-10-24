package removable

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeHost is a tiny /sys, /proc and mount table.
type fakeHost struct {
	mu      sync.Mutex
	files   map[string]string
	dirs    map[string][]string // folder → names (sub-folders)
	links   map[string]string
	calls   []string
	mounts  map[string]string // device → mount point
	lsblk   string
	failFor map[string]string // command prefix → error output
	tools   map[string]bool
}

type dirEntry struct{ name string }

func (d dirEntry) Name() string               { return d.name }
func (d dirEntry) IsDir() bool                { return true }
func (d dirEntry) Type() fs.FileMode          { return fs.ModeDir }
func (d dirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("n/a") }

func (h *fakeHost) mountinfo() string {
	var b strings.Builder
	i := 30
	for dev, mp := range h.mounts {
		b.WriteString("1 2 8:1 / " + strings.ReplaceAll(mp, " ", `\040`) + " rw,nosuid shared:1 - vfat " + dev + " rw\n")
		i++
	}
	b.WriteString("22 1 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw\n")
	return b.String()
}

func (h *fakeHost) readFile(p string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p == "/proc/self/mountinfo" {
		return []byte(h.mountinfo()), nil
	}
	if s, ok := h.files[p]; ok {
		return []byte(s), nil
	}
	return nil, os.ErrNotExist
}

func (h *fakeHost) readDir(p string) ([]os.DirEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	names, ok := h.dirs[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	var out []os.DirEntry
	for _, n := range names {
		out = append(out, dirEntry{n})
	}
	return out, nil
}

func (h *fakeHost) readlink(p string) (string, error) {
	if t, ok := h.links[p]; ok {
		return t, nil
	}
	return "", os.ErrNotExist
}

func (h *fakeHost) run(_ context.Context, name string, args ...string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	call := name + " " + strings.Join(args, " ")
	h.calls = append(h.calls, call)
	for p, out := range h.failFor {
		if strings.HasPrefix(call, p) {
			return out, errors.New(out)
		}
	}
	switch name {
	case "lsblk":
		return h.lsblk, nil
	case "mount":
		h.mounts[args[len(args)-2]] = args[len(args)-1]
	case "umount":
		for dev, mp := range h.mounts {
			if mp == args[len(args)-1] {
				delete(h.mounts, dev)
			}
		}
	}
	return "", nil
}

func (h *fakeHost) called(prefix string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// plug attaches a USB stick sdb with one partition.
func (h *fakeHost) plug(fstype, label string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dirs["/sys/block"] = []string{"nvme0n1", "loop0", "sda", "sdb"}
	h.files["/sys/block/sdb/removable"] = "0\n"
	h.files["/sys/block/sdb/size"] = "60062500\n"
	h.links["/sys/block/sdb"] = "../devices/pci0000:00/0000:00:14.0/usb2/2-1/2-1:1.0/host6/target6:0:0/6:0:0:0/block/sdb"
	h.links["/sys/class/block/sdb1"] = "../../devices/pci0000:00/0000:00:14.0/usb2/2-1/2-1:1.0/host6/target6:0:0/6:0:0:0/block/sdb/sdb1"
	h.files["/sys/class/block/sdb1/partition"] = "1\n"
	h.files["/sys/class/block/sdb1/dev"] = "8:17\n"
	h.lsblk = `NAME="sdb" FSTYPE="" LABEL="" UUID="" SIZE="30752000000" TYPE="disk" MODEL="SanDisk\x20Ultra  "
NAME="sdb1" FSTYPE="` + fstype + `" LABEL="` + label + `" UUID="1A2B-3C4D" SIZE="30750000000" TYPE="part" MODEL=""
`
}

func (h *fakeHost) unplug() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dirs["/sys/block"] = []string{"nvme0n1", "loop0", "sda"}
	delete(h.files, "/sys/block/sdb/size")
	delete(h.files, "/sys/class/block/sdb1/dev")
	h.lsblk = ""
}

func newFake(t *testing.T) (*Watcher, *fakeHost) {
	t.Helper()
	h := &fakeHost{
		files: map[string]string{
			"/sys/block/sda/removable":     "0\n", // internal SATA disk
			"/sys/block/sda/size":          "1000000\n",
			"/sys/block/nvme0n1/removable": "0\n",
			"/proc/filesystems":            "nodev\tsysfs\n\text4\n\tvfat\n\tntfs3\n",
			"/etc/fstab":                   "UUID=9d8c / ext4 defaults 0 1\n",
		},
		dirs:   map[string][]string{"/sys/block": {"nvme0n1", "loop0", "sda"}},
		links:  map[string]string{"/sys/block/sda": "../devices/pci0000:00/ata1/host0/target0:0:0/0:0:0:0/block/sda"},
		mounts: map[string]string{},
		tools:  map[string]bool{},
	}
	w := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.Exec, w.ReadFile, w.ReadDir, w.Readlink = h.run, h.readFile, h.readDir, h.readlink
	w.MkdirAll = func(string, os.FileMode) error { return nil }
	w.Remove = func(string) error { return nil }
	w.WriteFile = func(p string, b []byte, _ os.FileMode) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.calls = append(h.calls, "write "+p+" "+string(b))
		return nil
	}
	w.LookPath = func(n string) (string, error) {
		if h.tools[n] {
			return "/usr/bin/" + n, nil
		}
		return "", errors.New("not found")
	}
	return w, h
}

func TestPlugOpenEjectUnplug(t *testing.T) {
	w, h := newFake(t)
	var changes []Change
	w.OnChange = func(c Change) { changes = append(changes, c) }
	ctx := context.Background()
	w.Scan(ctx) // start: nothing removable
	if len(w.Drives()) != 0 || len(changes) != 0 {
		t.Fatalf("at start: %+v %+v", w.Drives(), changes)
	}

	h.plug("vfat", "HOLIDAY")
	w.Scan(ctx)
	if c := h.called("mount "); len(c) != 1 || c[0] != "mount -t vfat -o nosuid,nodev,noexec,relatime,utf8,flush /dev/sdb1 /media/nocapos/HOLIDAY" {
		t.Fatalf("mount: %q", c)
	}
	if len(changes) != 1 || len(changes[0].Added) != 1 {
		t.Fatalf("changes: %+v", changes)
	}
	d := changes[0].Added[0]
	if d.Name != "HOLIDAY" || d.Mount != "/media/nocapos/HOLIDAY" || d.Disk != "sdb" || d.Model != "SanDisk Ultra" || d.Device != "/dev/sdb1" {
		t.Errorf("drive: %+v", d)
	}
	// Nothing changed: no second lsblk, no second mount.
	n := len(h.calls)
	w.Scan(ctx)
	if len(h.calls) != n {
		t.Errorf("rescanned an unchanged machine: %v", h.calls[n:])
	}

	// Eject: unmount, then power off through the kernel (no udisks here).
	off, err := w.Eject(ctx, "/media/nocapos/HOLIDAY", "/dev/sdb1")
	if err != nil || !off {
		t.Fatalf("eject: %v %v", off, err)
	}
	if len(h.called("umount /media/nocapos/HOLIDAY")) != 1 || len(h.called("write /sys/block/sdb/device/delete 1")) != 1 {
		t.Errorf("eject calls: %v", h.calls)
	}
	// Still attached for a moment: it isn't opened again.
	w.Scan(ctx)
	if len(h.called("mount ")) != 1 {
		t.Errorf("reopened an ejected drive: %v", h.called("mount "))
	}

	h.unplug()
	w.Scan(ctx)
	if len(changes) != 2 || len(changes[1].Removed) != 1 || changes[1].Removed[0].Name != "HOLIDAY" {
		t.Fatalf("unplug: %+v", changes)
	}
	// Plugged in again: opened again.
	h.plug("vfat", "HOLIDAY")
	w.Scan(ctx)
	if len(h.called("mount ")) != 2 {
		t.Errorf("not reopened after replugging: %v", h.called("mount "))
	}
}

func TestPulledOutWithoutEject(t *testing.T) {
	w, h := newFake(t)
	ctx := context.Background()
	w.Scan(ctx)
	h.plug("exfat", "")
	w.Scan(ctx)
	mp := "/media/nocapos/USB sdb1"
	if c := h.called("mount "); len(c) != 1 || !strings.HasSuffix(c[0], "/dev/sdb1 "+mp) || !strings.Contains(c[0], "-t exfat") {
		t.Fatalf("mount: %q", c)
	}
	h.unplug() // the mount stays behind, its device gone
	w.Scan(ctx)
	if len(h.called("umount -l "+mp)) != 1 {
		t.Errorf("stale mount not detached: %v", h.calls)
	}
}

func TestSkipsWhatItShouldnt(t *testing.T) {
	w, h := newFake(t)
	ctx := context.Background()
	w.Scan(ctx)
	// In /etc/fstab: the system has plans for it.
	h.files["/etc/fstab"] += "UUID=1A2B-3C4D /mnt/backup vfat defaults,nofail 0 0\n"
	h.plug("vfat", "BACKUP")
	w.Scan(ctx)
	// A filesystem it doesn't open by itself.
	h.unplug()
	w.Scan(ctx)
	h.plug("zfs_member", "tank")
	w.Scan(ctx)
	if c := h.called("mount "); len(c) != 0 {
		t.Errorf("mounted: %q", c)
	}
	// Internal disks and loops are never followed.
	for _, d := range w.Drives() {
		if d.Disk != "sdb" {
			t.Errorf("followed %+v", d)
		}
	}
}

func TestMountFailureAndBusyEject(t *testing.T) {
	w, h := newFake(t)
	var changes []Change
	w.OnChange = func(c Change) { changes = append(changes, c) }
	ctx := context.Background()
	w.Scan(ctx)
	h.failFor = map[string]string{"mount ": "mount: /media/nocapos/WIN: wrong fs type, bad option, bad superblock on /dev/sdb1"}
	h.plug("ntfs", "WIN")
	w.Scan(ctx)
	if len(changes) != 1 || changes[0].Added[0].Error == "" || changes[0].Added[0].Mount != "" {
		t.Fatalf("failed mount: %+v", changes)
	}
	if c := h.called("mount "); len(c) != 1 || !strings.Contains(c[0], "-t ntfs3") {
		t.Errorf("ntfs driver: %q", c)
	}
	h.mu.Lock()
	h.lsblk += " " // a change, so it scans again
	h.files["/sys/block/sdb/size"] = "60062501\n"
	h.mu.Unlock()
	w.Scan(ctx)
	if c := h.called("mount "); len(c) != 1 {
		t.Errorf("retried a failing mount: %q", c)
	}

	h.failFor = map[string]string{"umount": "umount: /media/x: target is busy."}
	if _, err := w.Eject(ctx, "/media/x", "/dev/sdb1"); !errors.Is(err, ErrBusy) {
		t.Errorf("busy eject: %v", err)
	}
	if _, err := w.Eject(ctx, "/media/x", "sdb1"); err == nil {
		t.Error("ejected a non-device")
	}
}

func TestParsers(t *testing.T) {
	e := parseLsblkPairs(`NAME="sdc1" FSTYPE="ntfs" LABEL="My\x20Files \"x\"" UUID="AB" SIZE="10" TYPE="part" MODEL=""`)
	if len(e) != 1 || e[0]["LABEL"] != `My Files \"x\"` || e[0]["FSTYPE"] != "ntfs" {
		t.Errorf("lsblk: %+v", e)
	}
	mi := "36 25 8:17 / /media/nocapos/My\\040Stick rw,nosuid,nodev,noexec shared:1 - vfat /dev/sdb1 rw\n" +
		"37 25 8:18 / /media/nocapos/Other rw - ext4 /dev/sdb2 rw\n37 25 8:18 / /bind rw - ext4 /dev/sdb2 rw\n"
	m := mountedDevices(mi)
	if m["/dev/sdb1"] != "/media/nocapos/My Stick" || m["/dev/sdb2"] != "/media/nocapos/Other" {
		t.Errorf("mountinfo: %v", m)
	}
	for in, want := range map[string]string{"HOLIDAY": "HOLIDAY", "a/b:c*": "a_b_c_", "..": "USB sdz1", "  Café 2024 ": "Café 2024"} {
		if got := mountName(Drive{Label: in, Device: "/dev/sdz1"}); got != want {
			t.Errorf("mountName(%q) = %q, want %q", in, got, want)
		}
	}
	none := func(string) bool { return false }
	if got := strings.Join(mountArgs("ntfs", "/dev/sdb1", "/m", none), " "); got != "-t ntfs -o nosuid,nodev,noexec,relatime,ro /dev/sdb1 /m" {
		t.Errorf("ntfs without drivers: %s", got)
	}
	if got := strings.Join(mountArgs("ext4", "/dev/sdb1", "/m", none), " "); got != "-t ext4 -o nosuid,nodev,noexec,relatime /dev/sdb1 /m" {
		t.Errorf("ext4: %s", got)
	}
	if !inFstab("LABEL=BACKUP /mnt/b vfat 0 0\n", Drive{Label: "BACKUP"}) || inFstab("# UUID=AB /x\n", Drive{UUID: "AB"}) {
		t.Error("fstab matching")
	}
}
