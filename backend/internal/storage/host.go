package storage

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/syspkg"
)

// Backend does the actual disk work. Host runs real commands; Demo keeps an
// in-memory machine for trying the UI. The Manager enforces every safety
// rule before calling either.
type Backend interface {
	Tools(ctx context.Context) Tools
	Install(ctx context.Context, tool string) error
	Facts(ctx context.Context) (*Facts, error)
	DataSource(ctx context.Context, dir string) string
	Smart(ctx context.Context, d Disk) (*SmartReport, error)
	SmartTest(ctx context.Context, d Disk, kind string) error
	Pools(ctx context.Context) ([]Pool, error)
	Importable(ctx context.Context) ([]ImportablePool, error)
	CreatePool(ctx context.Context, name, layout string, devs []string, compression, mountpoint string) error
	AddVdev(ctx context.Context, pool, layout string, devs []string) error
	Replace(ctx context.Context, pool, old, dev string) error
	Destroy(ctx context.Context, pool string) error
	Export(ctx context.Context, pool string) error
	Import(ctx context.Context, nameOrID string) error
	Scrub(ctx context.Context, pool string, stop bool) error
	Datasets(ctx context.Context, pool string) ([]Dataset, error)
	CreateDataset(ctx context.Context, name string, quota *int64, compression string) error
	UpdateDataset(ctx context.Context, name string, quota *int64, compression string) error
	DestroyDataset(ctx context.Context, name string) error
	Snapshots(ctx context.Context, dataset string) ([]Snapshot, error)
	Snapshot(ctx context.Context, full string) error
	Rollback(ctx context.Context, full string) error
	DestroySnapshot(ctx context.Context, full string) error
	Format(ctx context.Context, dev, label, mountpoint string) error
	// FilesPath is where Files should look for a mountpoint (the demo maps it
	// into a sandbox folder); Mounted reports whether it's ready to serve.
	FilesPath(mountpoint string) string
	Mounted(path string) bool
}

// Timeouts for host commands.
const (
	readTimeout   = 30 * time.Second
	actionTimeout = 3 * time.Minute
	longTimeout   = 30 * time.Minute
)

// Host is the real backend. Fields are replaceable in tests.
type Host struct {
	Run       Runner
	LookPath  func(string) (string, error)
	ReadFile  func(string) ([]byte, error)
	Exists    func(string) bool
	Links     func() map[string]string
	MountedFn func(string) bool
	Fstab     string
	MkdirAll  func(string, os.FileMode) error

	mu     sync.Mutex
	jsonOK *bool // OpenZFS >= 2.3 (zpool -j)
}

func NewHost() *Host {
	return &Host{
		Run: ExecRunner{}, LookPath: exec.LookPath, ReadFile: os.ReadFile,
		Exists:    func(p string) bool { _, err := os.Stat(p); return err == nil },
		Links:     readDevLinks,
		MountedFn: isMountPoint,
		Fstab:     "/etc/fstab",
		MkdirAll:  os.MkdirAll,
	}
}

func (h *Host) cmd(ctx context.Context, timeout time.Duration, argv []string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return h.Run.Run(cctx, argv[0], argv[1:]...)
}

func (h *Host) do(ctx context.Context, timeout time.Duration, argv []string) error {
	_, err := h.cmd(ctx, timeout, argv)
	return err
}

func (h *Host) has(bin string) bool { _, err := h.LookPath(bin); return err == nil }

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func (h *Host) Tools(ctx context.Context) Tools {
	t := Tools{ZFSInstalled: h.has("zpool") && h.has("zfs"), ZFSModule: h.Exists("/sys/module/zfs"),
		SmartInstalled: h.has("smartctl"), Lsblk: h.has("lsblk")}
	if t.ZFSModule {
		if b, err := h.ReadFile("/sys/module/zfs/version"); err == nil {
			t.ZFSVersion = versionRe.FindString(string(b))
		}
	}
	if t.ZFSVersion == "" && t.ZFSInstalled {
		if out, err := h.cmd(ctx, readTimeout, []string{"zfs", "version"}); err == nil {
			t.ZFSVersion = versionRe.FindString(string(out))
		}
	}
	return t
}

// atLeast23 reports OpenZFS 2.3+ (JSON output).
func atLeast23(v string) bool {
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	maj, _ := strconv.Atoi(m[1])
	mnr, _ := strconv.Atoi(m[2])
	return maj > 2 || (maj == 2 && mnr >= 3)
}

var packages = map[string]syspkg.Names{
	"smart": {"apt": {"smartmontools"}, "dnf": {"smartmontools"}, "pacman": {"smartmontools"}, "zypper": {"smartmontools"}, "apk": {"smartmontools"}},
	"zfs":   {"apt": {"zfsutils-linux"}},
}

func isDebian(read func(string) ([]byte, error)) bool {
	b, err := read("/etc/os-release")
	return err == nil && regexp.MustCompile(`(?m)^ID=("?)debian("?)$`).Match(b)
}

const debianZFSHelp = "On Debian, ZFS comes from the contrib repository and is built for your kernel: add \"contrib\" to your apt sources, then run: apt install linux-headers-amd64 zfs-dkms zfsutils-linux"

func (h *Host) Install(ctx context.Context, tool string) error {
	names, ok := packages[tool]
	if !ok {
		return bad("unknown tool %q", tool)
	}
	if tool == "zfs" && syspkg.Manager() != "apt" {
		return bad("NoCapOS can install ZFS on Ubuntu. On this system, install OpenZFS with your distribution's instructions (see openzfs.github.io/openzfs-docs/Getting Started), then come back")
	}
	if err := syspkg.Install(ctx, names); err != nil {
		if tool == "zfs" && isDebian(h.ReadFile) {
			return bad("%s. %s", err.Error(), debianZFSHelp)
		}
		return err
	}
	if tool == "zfs" {
		if err := h.do(ctx, actionTimeout, []string{"modprobe", "zfs"}); err != nil {
			if isDebian(h.ReadFile) {
				return bad("the ZFS tools are installed but the kernel module didn't load (%s). %s", err.Error(), debianZFSHelp)
			}
			return bad("the ZFS tools are installed but the kernel module didn't load: %s", err.Error())
		}
		h.mu.Lock()
		h.jsonOK = nil
		h.mu.Unlock()
	}
	return nil
}

func (h *Host) Facts(ctx context.Context) (*Facts, error) {
	out, err := h.cmd(ctx, readTimeout, []string{"lsblk", "-J", "-b", "-o", lsblkCols})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unknown column") {
		out, err = h.cmd(ctx, readTimeout, []string{"lsblk", "-J", "-b", "-o", lsblkColsOld})
	}
	if err != nil {
		return nil, fmt.Errorf("couldn't list disks: %w", err)
	}
	devs, err := ParseLsblk(out)
	if err != nil {
		return nil, err
	}
	f := &Facts{Devices: devs, Links: h.Links()}
	if b, err := h.ReadFile("/proc/swaps"); err == nil {
		f.Swaps = parseSwaps(string(b))
	}
	return f, nil
}

func parseSwaps(s string) []string {
	var out []string
	for i, line := range strings.Split(s, "\n") {
		if f := strings.Fields(line); i > 0 && len(f) > 0 && strings.HasPrefix(f[0], "/dev/") {
			out = append(out, f[0])
		}
	}
	return out
}

func (h *Host) DataSource(ctx context.Context, dir string) string {
	out, err := h.cmd(ctx, readTimeout, []string{"findmnt", "-n", "-o", "SOURCE", "--target", dir})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (h *Host) smartArgs(d Disk, extra ...string) []string {
	args := []string{"smartctl"}
	args = append(args, extra...)
	if d.Transport == "usb" {
		args = append(args, "-d", "sat")
	}
	return append(args, d.Path)
}

func (h *Host) Smart(ctx context.Context, d Disk) (*SmartReport, error) {
	out, err := h.cmd(ctx, readTimeout, h.smartArgs(d, "-j", "-a", "-n", "standby"))
	if smartAsleep(out) {
		return &SmartReport{Summary: SmartSummary{Available: true, Asleep: true}, Attributes: []SmartAttribute{}, SelfTests: []SelfTest{}}, nil
	}
	if len(bytes.TrimSpace(out)) == 0 && err != nil {
		return nil, err
	}
	return ParseSmart(out)
}

func (h *Host) SmartTest(ctx context.Context, d Disk, kind string) error {
	out, err := h.cmd(ctx, readTimeout, h.smartArgs(d, "-t", kind))
	if err != nil && !bytes.Contains(out, []byte("Testing has begun")) {
		return err
	}
	return nil
}

func (h *Host) useJSON(ctx context.Context) bool {
	h.mu.Lock()
	known := h.jsonOK
	h.mu.Unlock()
	if known != nil {
		return *known
	}
	v := atLeast23(h.Tools(ctx).ZFSVersion)
	h.mu.Lock()
	h.jsonOK = &v
	h.mu.Unlock()
	return v
}

func (h *Host) Pools(ctx context.Context) ([]Pool, error) {
	var pools []Pool
	var sizes map[string]poolSizes
	if h.useJSON(ctx) {
		if out, err := h.cmd(ctx, readTimeout, []string{"zpool", "status", "-j", "--json-int", "-P"}); err == nil {
			pools, _ = ParseStatusJSON(out, time.Now())
		}
		if out, err := h.cmd(ctx, readTimeout, []string{"zpool", "list", "-j", "--json-int", "-o", "name,size,allocated,free,fragmentation,capacity,health"}); err == nil {
			sizes, _ = ParseListJSON(out)
		}
	}
	if pools == nil {
		out, err := h.cmd(ctx, readTimeout, []string{"zpool", "status", "-P", "-p"})
		if err != nil {
			if strings.Contains(err.Error(), "no pools available") {
				return []Pool{}, nil
			}
			return nil, fmt.Errorf("couldn't read pool status: %w", err)
		}
		pools = ParseStatusText(string(out))
	}
	if sizes == nil {
		out, err := h.cmd(ctx, readTimeout, []string{"zpool", "list", "-Hp", "-o", "name,size,allocated,free,fragmentation,capacity,health"})
		if err == nil {
			sizes = ParseListText(string(out))
		}
	}
	mounts := map[string]string{}
	if out, err := h.cmd(ctx, readTimeout, []string{"zfs", "list", "-Hp", "-d", "0", "-o", "name,mountpoint"}); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if n, m, ok := strings.Cut(line, "\t"); ok {
				mounts[n] = m
			}
		}
	}
	for i := range pools {
		p := &pools[i]
		if s, ok := sizes[p.Name]; ok {
			p.Size, p.Allocated, p.Free, p.Fragmentation, p.Capacity = s.Size, s.Alloc, s.Free, s.Frag, s.Cap
		}
		p.Mountpoint = mounts[p.Name]
	}
	if pools == nil {
		pools = []Pool{}
	}
	return pools, nil
}

func (h *Host) Importable(ctx context.Context) ([]ImportablePool, error) {
	out, err := h.cmd(ctx, time.Minute, []string{"zpool", "import", "-d", "/dev/disk/by-id"})
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		if strings.Contains(err.Error(), "no pools available") {
			return []ImportablePool{}, nil
		}
		return nil, err
	}
	return ParseImport(string(out)), nil
}

func (h *Host) CreatePool(ctx context.Context, name, layout string, devs []string, compression, mountpoint string) error {
	return h.do(ctx, longTimeout, createPoolArgs(name, layout, devs, compression, mountpoint))
}
func (h *Host) AddVdev(ctx context.Context, pool, layout string, devs []string) error {
	return h.do(ctx, longTimeout, addVdevArgs(pool, layout, devs))
}
func (h *Host) Replace(ctx context.Context, pool, old, dev string) error {
	return h.do(ctx, longTimeout, replaceArgs(pool, old, dev))
}
func (h *Host) Destroy(ctx context.Context, pool string) error {
	return h.do(ctx, actionTimeout, destroyArgs(pool))
}
func (h *Host) Export(ctx context.Context, pool string) error {
	return h.do(ctx, actionTimeout, exportArgs(pool))
}
func (h *Host) Import(ctx context.Context, nameOrID string) error {
	return h.do(ctx, 5*time.Minute, importArgs(nameOrID))
}
func (h *Host) Scrub(ctx context.Context, pool string, stop bool) error {
	return h.do(ctx, actionTimeout, scrubArgs(pool, stop))
}

func (h *Host) Datasets(ctx context.Context, pool string) ([]Dataset, error) {
	out, err := h.cmd(ctx, readTimeout, []string{"zfs", "list", "-Hp", "-r", "-t", "filesystem",
		"-o", "name,used,avail,refer,mountpoint,compression,compressratio,quota", pool})
	if err != nil {
		return nil, err
	}
	return ParseDatasets(string(out)), nil
}

func (h *Host) CreateDataset(ctx context.Context, name string, quota *int64, compression string) error {
	return h.do(ctx, actionTimeout, datasetCreateArgs(name, quota, compression))
}

func (h *Host) UpdateDataset(ctx context.Context, name string, quota *int64, compression string) error {
	for _, step := range datasetUpdateArgs(name, quota, compression) {
		if err := h.do(ctx, actionTimeout, step); err != nil {
			return err
		}
	}
	return nil
}

func (h *Host) DestroyDataset(ctx context.Context, name string) error {
	return h.do(ctx, actionTimeout, datasetDestroyArgs(name))
}

func (h *Host) Snapshots(ctx context.Context, dataset string) ([]Snapshot, error) {
	out, err := h.cmd(ctx, readTimeout, []string{"zfs", "list", "-Hp", "-t", "snapshot", "-d", "1", "-s", "creation",
		"-o", "name,creation,used,refer", dataset})
	if err != nil {
		return nil, err
	}
	return ParseSnapshots(string(out)), nil
}

func (h *Host) Snapshot(ctx context.Context, full string) error {
	return h.do(ctx, actionTimeout, snapshotArgs(full))
}
func (h *Host) Rollback(ctx context.Context, full string) error {
	return h.do(ctx, actionTimeout, rollbackArgs(full))
}
func (h *Host) DestroySnapshot(ctx context.Context, full string) error {
	return h.do(ctx, actionTimeout, snapshotDestroyArgs(full))
}

// Format makes one GPT partition, ext4 on it, an fstab line and mounts it.
func (h *Host) Format(ctx context.Context, dev, label, mountpoint string) error {
	var uuid string
	for _, step := range formatSteps(dev, label) {
		var out []byte
		var err error
		if step[0] == "sfdisk" {
			ir, ok := h.Run.(InputRunner)
			if !ok {
				return errors.New("this runner can't partition disks")
			}
			cctx, cancel := context.WithTimeout(ctx, actionTimeout)
			out, err = ir.RunInput(cctx, []byte(sfdiskScript), step[0], step[1:]...)
			cancel()
		} else {
			out, err = h.cmd(ctx, longTimeout, step)
		}
		if step[0] == "udevadm" {
			continue // best effort: wait for the new partition to appear
		}
		if err != nil {
			return fmt.Errorf("%s: %w", step[0], err)
		}
		if step[0] == "blkid" {
			uuid = strings.TrimSpace(string(out))
		}
	}
	if !regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`).MatchString(uuid) {
		return fmt.Errorf("couldn't read the new filesystem's ID")
	}
	old, err := h.ReadFile(h.Fstab)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content, err := fstabSet(string(old), uuid, mountpoint)
	if err != nil {
		return err
	}
	if err := h.MkdirAll(mountpoint, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(h.Fstab, []byte(content), 0o644); err != nil {
		return err
	}
	_ = h.do(ctx, readTimeout, []string{"systemctl", "daemon-reload"})
	return h.do(ctx, actionTimeout, []string{"mount", mountpoint})
}

func (h *Host) FilesPath(mountpoint string) string { return mountpoint }
func (h *Host) Mounted(path string) bool           { return h.MountedFn(path) }

// readDevLinks maps /dev/disk/by-* symlinks to kernel names.
func readDevLinks() map[string]string {
	out := map[string]string{}
	for _, dir := range []string{"/dev/disk/by-id", "/dev/disk/by-partuuid", "/dev/disk/by-uuid", "/dev/disk/by-path"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := dir + "/" + e.Name()
			if target, err := filepath.EvalSymlinks(p); err == nil {
				out[p] = filepath.Base(target)
			}
		}
	}
	return out
}

// isMountPoint reports whether path is a mount point (from /proc/self/mountinfo).
func isMountPoint(path string) bool {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 4 && unescapeMount(fields[4]) == path {
			return true
		}
	}
	return false
}

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if c, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(c))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
