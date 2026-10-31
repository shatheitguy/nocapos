// Package removable notices USB drives and other removable disks being
// plugged in and out. On native Linux it polls /sys/block every few seconds
// (cheap: a directory and a few small files), opens new drives read-write
// under /media/nocapos/<label> with safe options (nosuid, nodev, noexec) and
// ejects them on request: unmount, then power the disk off when it can.
package removable

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Drive is a filesystem on a removable disk.
type Drive struct {
	Device string `json:"device"` // /dev/sdb1
	Disk   string `json:"disk"`   // sdb
	Name   string `json:"name"`   // label, or a description
	Label  string `json:"label,omitempty"`
	UUID   string `json:"uuid,omitempty"`
	FSType string `json:"fs_type"`
	Size   int64  `json:"size"`
	Model  string `json:"model,omitempty"`
	Mount  string `json:"mount,omitempty"` // where it's open ("" = not mounted)
	Error  string `json:"error,omitempty"` // why it couldn't be opened
}

// Change is what one scan found new or gone.
type Change struct {
	Added   []Drive
	Removed []Drive
}

// Filesystems NoCapOS opens by itself.
var autoFS = map[string]bool{"vfat": true, "exfat": true, "ntfs": true, "ext2": true, "ext3": true, "ext4": true}

// Watcher follows removable drives.
type Watcher struct {
	// Host access, replaceable in tests.
	Sys       string // /sys
	MediaDir  string // /media/nocapos
	Fstab     string // /etc/fstab
	Exec      func(ctx context.Context, name string, args ...string) (string, error)
	ReadFile  func(string) ([]byte, error)
	ReadDir   func(string) ([]os.DirEntry, error)
	Readlink  func(string) (string, error)
	WriteFile func(string, []byte, os.FileMode) error
	MkdirAll  func(string, os.FileMode) error
	Remove    func(string) error
	LookPath  func(string) (string, error)
	Interval  time.Duration
	Log       *slog.Logger

	// OnChange hears about drives plugged in or out (not the ones present at start).
	OnChange func(Change)

	mu      sync.Mutex
	sig     string
	drives  map[string]Drive // by device
	ejected map[string]bool  // devices ejected but still attached: don't reopen
	failed  map[string]string
	scanned bool
}

// New returns a watcher for the real machine.
func New(log *slog.Logger) *Watcher {
	if log == nil {
		log = slog.Default()
	}
	return &Watcher{
		Sys: "/sys", MediaDir: "/media/nocapos", Fstab: "/etc/fstab",
		Exec: runCmd, ReadFile: os.ReadFile, ReadDir: os.ReadDir, Readlink: os.Readlink, WriteFile: os.WriteFile,
		MkdirAll: os.MkdirAll, Remove: os.Remove, LookPath: exec.LookPath,
		Interval: 3 * time.Second, Log: log,
		drives: map[string]Drive{}, ejected: map[string]bool{}, failed: map[string]string{},
	}
}

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s == "" {
			s = err.Error()
		}
		return s, errors.New(s)
	}
	return s, nil
}

// Supported reports whether drives can be followed here: Linux, as root,
// not inside a container.
func Supported() bool {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return false
	}
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return false
		}
	}
	return true
}

// Run scans until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		w.Scan(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SetOnChange sets OnChange safely while the watcher runs.
func (w *Watcher) SetOnChange(fn func(Change)) {
	w.mu.Lock()
	w.OnChange = fn
	w.mu.Unlock()
}

// Drives lists the removable drives present now.
func (w *Watcher) Drives() []Drive {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Drive, 0, len(w.drives))
	for _, d := range w.drives {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}

// skipDisk leaves out block devices that are never removable drives.
var skipDisk = regexp.MustCompile(`^(loop|ram|zram|dm-|md|nbd|sr|fd|zd|nvme)`)

// removableDisks lists kernel disk names that are removable or on USB, with
// their size (a card reader without a card has size 0 and is left out).
func (w *Watcher) removableDisks() map[string]int64 {
	out := map[string]int64{}
	entries, err := w.ReadDir(path.Join(w.Sys, "block"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		n := e.Name()
		if skipDisk.MatchString(n) || !w.isRemovable(n) {
			continue
		}
		size := w.readInt(path.Join(w.Sys, "block", n, "size")) * 512
		if size > 0 {
			out[n] = size
		}
	}
	return out
}

func (w *Watcher) isRemovable(disk string) bool {
	if b, err := w.ReadFile(path.Join(w.Sys, "block", disk, "removable")); err == nil && strings.TrimSpace(string(b)) == "1" {
		return true
	}
	target, err := w.Readlink(path.Join(w.Sys, "block", disk))
	return err == nil && strings.Contains(target, "/usb")
}

func (w *Watcher) readInt(p string) int64 {
	b, err := w.ReadFile(p)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

// lsblkEntry is one line of `lsblk -P`.
type lsblkEntry map[string]string

var pairRe = regexp.MustCompile(`([A-Z:-]+)="((?:[^"\\]|\\.)*)"`)

// parseLsblkPairs reads `lsblk -P` output (KEY="value" pairs, \xNN escapes).
func parseLsblkPairs(text string) []lsblkEntry {
	var out []lsblkEntry
	for _, line := range strings.Split(text, "\n") {
		m := pairRe.FindAllStringSubmatch(line, -1)
		if len(m) == 0 {
			continue
		}
		e := lsblkEntry{}
		for _, kv := range m {
			e[kv[1]] = unescapeHex(kv[2])
		}
		out = append(out, e)
	}
	return out
}

func unescapeHex(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if c, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(c))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// mountedDevices maps device → first mount point from /proc/self/mountinfo.
func mountedDevices(mountinfo string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(mountinfo, "\n") {
		pre, post, ok := strings.Cut(line, " - ")
		f, g := strings.Fields(pre), strings.Fields(post)
		if !ok || len(f) < 5 || len(g) < 2 {
			continue
		}
		dev := unescapeOctal(g[1])
		if !strings.HasPrefix(dev, "/dev/") {
			continue
		}
		if _, seen := out[dev]; !seen {
			out[dev] = unescapeOctal(f[4])
		}
	}
	return out
}

func unescapeOctal(s string) string {
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

// inFstab reports whether the system already has plans for a filesystem.
func inFstab(fstab string, d Drive) bool {
	for _, line := range strings.Split(fstab, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		src := f[0]
		if src == d.Device || (d.UUID != "" && strings.EqualFold(src, "UUID="+d.UUID)) ||
			(d.Label != "" && src == "LABEL="+d.Label) || (d.UUID != "" && strings.HasSuffix(src, "/by-uuid/"+d.UUID)) {
			return true
		}
	}
	return false
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N} ._()+-]+`)

// mountName is the folder a drive opens in: its label, made safe.
func mountName(d Drive) string {
	n := strings.TrimSpace(unsafeName.ReplaceAllString(d.Label, "_"))
	n = strings.Trim(n, ". ")
	if n == "" {
		n = "USB " + strings.TrimPrefix(d.Device, "/dev/")
	}
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

// mountArgs builds the mount command for a filesystem. has says whether a
// kernel filesystem or mount helper is available.
func mountArgs(fstype, dev, mp string, has func(string) bool) []string {
	opts := "nosuid,nodev,noexec,relatime"
	t := fstype
	switch fstype {
	case "vfat":
		opts += ",utf8,flush"
	case "ntfs":
		switch {
		case has("ntfs3"):
			t = "ntfs3"
		case has("ntfs-3g"):
			t = "ntfs-3g"
		default:
			opts += ",ro" // the old kernel driver can only read
		}
	}
	return []string{"-t", t, "-o", opts, dev, mp}
}

// has reports a kernel filesystem (/proc/filesystems) or mount helper.
func (w *Watcher) has(fs string) bool {
	if b, err := w.ReadFile("/proc/filesystems"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[len(f)-1] == fs {
				return true
			}
		}
	}
	_, err := w.LookPath("mount." + fs)
	return err == nil
}

// Scan looks once: new drives are opened, gone ones cleaned up and the
// change reported.
func (w *Watcher) Scan(ctx context.Context) {
	disks := w.removableDisks()
	mi, _ := w.ReadFile("/proc/self/mountinfo")
	mounts := mountedDevices(string(mi))
	sig := signature(disks, mounts)
	w.mu.Lock()
	same := sig == w.sig && w.scanned
	w.mu.Unlock()
	if same {
		return
	}
	found, err := w.describe(ctx, disks)
	if err != nil {
		w.Log.Warn("removable: couldn't read the drives", "err", err)
		return // try again next time; don't report drives as gone
	}
	w.cleanStale(ctx, mounts, found)
	fstab, _ := w.ReadFile(w.Fstab)
	used := map[string]bool{}
	for _, mp := range mounts {
		used[mp] = true
	}
	devs := make([]string, 0, len(found))
	for dev := range found {
		devs = append(devs, dev)
	}
	sort.Strings(devs)
	for _, dev := range devs {
		d := found[dev]
		if mp, ok := mounts[dev]; ok {
			d.Mount = mp
		} else if w.shouldOpen(d, string(fstab)) {
			if mp, err := w.open(ctx, d, used); err != nil {
				d.Error = err.Error()
				w.Log.Warn("removable: couldn't open", "device", dev, "err", err)
			} else {
				d.Mount = mp
				used[mp] = true
				w.Log.Info("removable: opened", "device", dev, "path", mp)
			}
		}
		found[dev] = d
	}
	// Re-read mounts for the signature, so what we just mounted isn't a change.
	mi, _ = w.ReadFile("/proc/self/mountinfo")
	sig = signature(disks, mountedDevices(string(mi)))

	w.mu.Lock()
	var ch Change
	for dev, d := range found {
		if _, had := w.drives[dev]; !had {
			ch.Added = append(ch.Added, d)
		}
	}
	for dev, d := range w.drives {
		if _, still := found[dev]; !still {
			ch.Removed = append(ch.Removed, d)
			delete(w.ejected, dev)
			delete(w.failed, dev)
		}
	}
	first := !w.scanned
	w.drives, w.sig, w.scanned = found, sig, true
	cb := w.OnChange
	w.mu.Unlock()
	if !first && cb != nil && (len(ch.Added) > 0 || len(ch.Removed) > 0) {
		sort.Slice(ch.Added, func(i, j int) bool { return ch.Added[i].Device < ch.Added[j].Device })
		sort.Slice(ch.Removed, func(i, j int) bool { return ch.Removed[i].Device < ch.Removed[j].Device })
		cb(ch)
	}
}

func signature(disks map[string]int64, mounts map[string]string) string {
	var parts []string
	for n, size := range disks {
		parts = append(parts, n+"="+strconv.FormatInt(size, 10))
	}
	for dev, mp := range mounts {
		parts = append(parts, dev+">"+mp)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// describe asks lsblk about the filesystems on the removable disks.
func (w *Watcher) describe(ctx context.Context, disks map[string]int64) (map[string]Drive, error) {
	out := map[string]Drive{}
	if len(disks) == 0 {
		return out, nil
	}
	args := []string{"-P", "-b", "-o", "NAME,FSTYPE,LABEL,UUID,SIZE,TYPE,MODEL"}
	names := make([]string, 0, len(disks))
	for n := range disks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		args = append(args, "/dev/"+n)
	}
	text, err := w.Exec(ctx, "lsblk", args...)
	if err != nil && !strings.Contains(text, `NAME="`) {
		return nil, err
	}
	model := map[string]string{}
	var cur string
	for _, e := range parseLsblkPairs(text) {
		name := e["NAME"]
		if e["TYPE"] == "disk" {
			cur = name
			model[name] = strings.TrimSpace(e["MODEL"])
		}
		if e["FSTYPE"] == "" || (e["TYPE"] != "part" && e["TYPE"] != "disk") {
			continue
		}
		disk := cur
		if e["TYPE"] == "disk" {
			disk = name
		}
		size, _ := strconv.ParseInt(e["SIZE"], 10, 64)
		d := Drive{Device: "/dev/" + name, Disk: disk, Label: e["LABEL"], UUID: e["UUID"], FSType: e["FSTYPE"], Size: size, Model: model[disk]}
		d.Name = d.Label
		if d.Name == "" {
			d.Name = strings.TrimSpace(firstNonEmpty(d.Model, "USB drive") + " " + humanSize(size))
		}
		out[d.Device] = d
	}
	return out, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func humanSize(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	if v >= 10 || i == 0 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func (w *Watcher) shouldOpen(d Drive, fstab string) bool {
	w.mu.Lock()
	skip := w.ejected[d.Device] || w.failed[d.Device] != ""
	w.mu.Unlock()
	return !skip && autoFS[d.FSType] && !inFstab(fstab, d)
}

// open mounts a drive under MediaDir.
func (w *Watcher) open(ctx context.Context, d Drive, used map[string]bool) (string, error) {
	base := path.Join(w.MediaDir, mountName(d))
	mp := base
	for i := 2; i < 100; i++ {
		if entries, err := w.ReadDir(mp); !used[mp] && (err != nil || len(entries) == 0) {
			break // free: missing, or an empty folder nothing is mounted on
		}
		mp = fmt.Sprintf("%s %d", base, i)
	}
	if err := w.MkdirAll(mp, 0o755); err != nil {
		return "", err
	}
	if out, err := w.Exec(ctx, "mount", mountArgs(d.FSType, d.Device, mp, w.has)...); err != nil {
		_ = w.Remove(mp)
		w.mu.Lock()
		w.failed[d.Device] = out
		w.mu.Unlock()
		return "", friendlyMountError(d, out)
	}
	return mp, nil
}

func friendlyMountError(d Drive, out string) error {
	low := strings.ToLower(out)
	switch {
	case strings.Contains(low, "unknown filesystem type") || strings.Contains(low, "no such device"):
		return fmt.Errorf("this machine can't read %s drives yet (install support for %s)", strings.ToUpper(d.FSType), d.FSType)
	case strings.Contains(low, "dirty") || strings.Contains(low, "unclean") || strings.Contains(low, "hibernat"):
		return errors.New("the drive wasn't ejected properly last time — check it on the computer it came from")
	case strings.Contains(low, "wrong fs type") || strings.Contains(low, "bad superblock"):
		return errors.New("the drive's filesystem looks damaged")
	}
	if i := strings.LastIndex(out, "\n"); i >= 0 {
		out = out[i+1:]
	}
	return errors.New(strings.TrimSpace(out))
}

// cleanStale detaches our mounts whose drive was pulled out without
// ejecting, and removes our empty folders.
func (w *Watcher) cleanStale(ctx context.Context, mounts map[string]string, present map[string]Drive) {
	prefix := strings.TrimSuffix(w.MediaDir, "/") + "/"
	for dev, mp := range mounts {
		if !strings.HasPrefix(mp, prefix) {
			continue
		}
		if _, ok := present[dev]; ok {
			continue
		}
		if _, err := w.ReadFile(path.Join(w.Sys, "class", "block", path.Base(dev), "dev")); err == nil {
			continue // still attached (just not a removable disk we follow)
		}
		_, _ = w.Exec(ctx, "umount", "-l", mp)
		_ = w.Remove(mp)
		w.Log.Info("removable: cleaned up after a drive that was pulled out", "device", dev, "path", mp)
	}
	entries, err := w.ReadDir(w.MediaDir)
	if err != nil {
		return
	}
	inUse := map[string]bool{}
	for _, mp := range mounts {
		inUse[mp] = true
	}
	for _, e := range entries {
		p := path.Join(w.MediaDir, e.Name())
		if e.IsDir() && !inUse[p] {
			_ = w.Remove(p) // only removes empty folders
		}
	}
}

// ErrBusy means something still has files open on the drive.
var ErrBusy = errors.New("something is still using the drive; close open files and wait for copies to finish, then try again")

// Eject unmounts a mount point and, when nothing else on that disk is still
// mounted, powers the disk off so it can be pulled out safely. poweredOff
// says whether that last step happened.
func (w *Watcher) Eject(ctx context.Context, mountpoint, device string) (poweredOff bool, err error) {
	if !strings.HasPrefix(device, "/dev/") {
		return false, errors.New("not a drive")
	}
	_, _ = w.Exec(ctx, "sync")
	if out, err := w.Exec(ctx, "umount", mountpoint); err != nil {
		if strings.Contains(strings.ToLower(out), "busy") {
			return false, ErrBusy
		}
		return false, fmt.Errorf("couldn't eject: %s", out)
	}
	if strings.HasPrefix(mountpoint, strings.TrimSuffix(w.MediaDir, "/")+"/") {
		_ = w.Remove(mountpoint)
	}
	w.mu.Lock()
	w.ejected[device] = true
	if d, ok := w.drives[device]; ok {
		d.Mount = ""
		w.drives[device] = d
	}
	w.mu.Unlock()

	disk := w.diskOf(device)
	if disk == "" || !w.isRemovable(disk) {
		return false, nil
	}
	mi, _ := w.ReadFile("/proc/self/mountinfo")
	for dev := range mountedDevices(string(mi)) {
		if w.diskOf(dev) == disk {
			return false, nil // another part of the disk is still open
		}
	}
	if _, err := w.LookPath("udisksctl"); err == nil {
		if _, err := w.Exec(ctx, "udisksctl", "power-off", "--no-user-interaction", "-b", "/dev/"+disk); err == nil {
			return true, nil
		}
	}
	// Without udisks: detach the disk from the kernel (USB disks only), the
	// same as the "safely remove" of desktop systems.
	target, err := w.Readlink(path.Join(w.Sys, "block", disk))
	if err != nil || !strings.Contains(target, "/usb") {
		return false, nil
	}
	if err := w.WriteFile(path.Join(w.Sys, "block", disk, "device", "delete"), []byte("1"), 0o200); err != nil {
		return false, nil
	}
	return true, nil
}

// diskOf maps a partition device to its disk's kernel name (sdb1 → sdb).
func (w *Watcher) diskOf(device string) string {
	name := path.Base(device)
	target, err := w.Readlink(path.Join(w.Sys, "class", "block", name))
	if err != nil {
		return ""
	}
	if _, err := w.ReadFile(path.Join(w.Sys, "class", "block", name, "partition")); err == nil {
		return path.Base(path.Dir(target))
	}
	return name
}
