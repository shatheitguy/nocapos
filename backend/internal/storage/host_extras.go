package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Host side of Extras. Every command is an argument list built in
// extras_cmds.go; passphrases only ever travel on stdin.

func (h *Host) input(ctx context.Context, timeout time.Duration, stdin []byte, argv []string) ([]byte, error) {
	ir, ok := h.Run.(InputRunner)
	if !ok {
		return nil, errors.New("this runner can't pass input to commands")
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return ir.RunInput(cctx, stdin, argv[0], argv[1:]...)
}

func (h *Host) BootFacts(ctx context.Context) (*BootFacts, error) {
	out, err := h.cmd(ctx, readTimeout, []string{"findmnt", "-n", "-o", "SOURCE,FSTYPE", "--target", "/"})
	if err != nil {
		return nil, fmt.Errorf("couldn't read the system drive: %w", err)
	}
	f := strings.Fields(string(out))
	if len(f) < 2 {
		return nil, errors.New("couldn't read the system drive")
	}
	b := &BootFacts{Source: f[0], FSType: f[1], EFI: h.Exists("/sys/firmware/efi"), Arch: runtime.GOARCH}
	if out, err := h.cmd(ctx, readTimeout, []string{"df", "-B1", "--output=size,used,avail", "/"}); err == nil {
		b.Size, b.Used, b.Avail = parseDF(string(out))
	}
	if out, err := h.cmd(ctx, readTimeout, []string{"findmnt", "-n", "-o", "SOURCE", "--mountpoint", "/boot/efi"}); err == nil {
		b.ESP = strings.TrimSpace(string(out))
	}
	if m, err := h.ReadFile("/proc/device-tree/model"); err == nil {
		b.Model = strings.TrimSpace(strings.TrimRight(string(m), "\x00"))
	}
	return b, nil
}

// parseDF reads `df -B1 --output=size,used,avail` (a header, then numbers).
func parseDF(s string) (size, used, avail int64) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) < 2 {
		return
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) != 3 {
		return
	}
	size, _ = strconv.ParseInt(f[0], 10, 64)
	used, _ = strconv.ParseInt(f[1], 10, 64)
	avail, _ = strconv.ParseInt(f[2], 10, 64)
	return
}

func (h *Host) MirrorBoot(ctx context.Context, p BootMirrorPlan) error {
	for _, bin := range []string{"sgdisk", "grub-install"} {
		if !h.has(bin) {
			return bad("%s is missing; install the gdisk and GRUB packages first", bin)
		}
	}
	mounted := false
	defer func() {
		if mounted {
			_ = h.do(context.Background(), actionTimeout, []string{"umount", espMount})
		}
	}()
	for _, step := range p.Steps() {
		err := h.do(ctx, longTimeout, step)
		switch step[0] {
		case "udevadm":
			continue // best effort: wait for the new partitions to appear
		case "mount":
			mounted = err == nil
		case "umount":
			mounted = mounted && err != nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", step[0], err)
		}
	}
	return nil
}

func (h *Host) AddSupport(ctx context.Context, pool, kind string, devs []string) error {
	return h.do(ctx, longTimeout, supportArgs(pool, kind, devs))
}

func (h *Host) RemoveSupport(ctx context.Context, pool, dev string) error {
	return h.do(ctx, longTimeout, removeVdevArgs(pool, dev))
}

func (h *Host) Expand(ctx context.Context, pool string, leaves []string) error {
	for _, step := range expandSteps(pool, leaves) {
		if err := h.do(ctx, actionTimeout, step); err != nil {
			return err
		}
	}
	return nil
}

func (h *Host) ARC(ctx context.Context) (*ARCStats, error) {
	b, err := h.ReadFile("/proc/spl/kstat/zfs/arcstats")
	if err != nil {
		return nil, bad("ZFS's RAM cache numbers aren't available")
	}
	return ParseARC(string(b)), nil
}

func (h *Host) unitDir() string {
	if h.UnitDir != "" {
		return h.UnitDir
	}
	return "/etc/systemd/system"
}

func (h *Host) ApplySpindown(ctx context.Context, rules []SpindownRule) error {
	hdparm, err := h.LookPath("hdparm")
	if err != nil {
		return bad("install hdparm first")
	}
	unit := filepath.Join(h.unitDir(), spindownUnitName)
	active := false
	for _, r := range rules {
		active = active || r.Minutes > 0
	}
	// Apply now (drives set to "never" get 0, which turns the timer off).
	var failed []string
	for _, r := range sortedRules(rules) {
		args, err := spindownArgs(hdparm, r)
		if err != nil {
			return err
		}
		if err := h.do(ctx, readTimeout, args); err != nil {
			failed = append(failed, filepath.Base(r.ByID))
		}
	}
	if !active {
		_ = h.do(ctx, readTimeout, []string{"systemctl", "disable", spindownUnitName})
		if err := os.Remove(unit); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		text, err := spindownUnit(hdparm, rules)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(unit, []byte(text), 0o644); err != nil {
			return err
		}
		_ = h.do(ctx, readTimeout, []string{"systemctl", "daemon-reload"})
		if err := h.do(ctx, readTimeout, []string{"systemctl", "enable", spindownUnitName}); err != nil {
			return err
		}
	}
	if len(failed) > 0 {
		return bad("saved, but %s didn't accept a spin-down timer (some USB enclosures ignore it)", strings.Join(failed, ", "))
	}
	return nil
}

func (h *Host) Clone(ctx context.Context, snap, target string) error {
	return h.do(ctx, actionTimeout, cloneArgs(snap, target))
}

func (h *Host) BrowseSnapshot(_ context.Context, dir string) ([]SnapEntry, error) {
	// A link inside the snapshot must not lead out of it.
	base, rest, ok := strings.Cut(dir, "/.zfs/snapshot/")
	if !ok {
		return nil, bad("bad folder")
	}
	label, _, _ := strings.Cut(rest, "/")
	root, err := filepath.EvalSymlinks(base + "/.zfs/snapshot/" + label)
	if err != nil {
		return nil, notFound("that snapshot can't be opened")
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, notFound("that folder isn't in the snapshot")
	}
	if real != root && !strings.HasPrefix(real, root+string(filepath.Separator)) {
		return nil, bad("that folder leads outside the snapshot")
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, notFound("that folder isn't in the snapshot")
		}
		return nil, err
	}
	return snapEntries(entries), nil
}

func snapEntries(entries []os.DirEntry) []SnapEntry {
	out := make([]SnapEntry, 0, len(entries))
	for _, e := range entries {
		se := SnapEntry{Name: e.Name(), Dir: e.IsDir()}
		if info, err := e.Info(); err == nil {
			se.Modified = info.ModTime().UTC()
			if !se.Dir {
				se.Size = info.Size()
			}
		}
		out = append(out, se)
		if len(out) >= 2000 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (h *Host) Encryption(ctx context.Context, pool string) ([]CryptInfo, error) {
	out, err := h.cmd(ctx, readTimeout, encStateArgs(pool))
	if err != nil {
		return nil, err
	}
	return ParseCrypt(string(out)), nil
}

func (h *Host) CreateEncrypted(ctx context.Context, name, passphrase string, quota *int64) error {
	_, err := h.input(ctx, actionTimeout, []byte(passphrase+"\n"), encCreateArgs(name, quota))
	return err
}

func (h *Host) Unlock(ctx context.Context, name, passphrase string) error {
	if _, err := h.input(ctx, actionTimeout, []byte(passphrase+"\n"), []string{"zfs", "load-key", name}); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "incorrect key") {
			return bad("that passphrase isn't right")
		}
		if !strings.Contains(err.Error(), "already loaded") {
			return err
		}
	}
	return h.do(ctx, actionTimeout, []string{"zfs", "mount", name})
}

func (h *Host) Lock(ctx context.Context, name string) error {
	if err := h.do(ctx, actionTimeout, []string{"zfs", "unmount", name}); err != nil && !strings.Contains(err.Error(), "not currently mounted") {
		if strings.Contains(err.Error(), "busy") {
			return busy("something still has files open in %s; close them and try again", name)
		}
		return err
	}
	return h.do(ctx, actionTimeout, []string{"zfs", "unload-key", name})
}
