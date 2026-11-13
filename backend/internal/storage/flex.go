package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Flexible array (preview, behind ALFA_STORAGE_FLEX_ARRAY=1).
//
// Design: every data disk keeps its own ext4 filesystem, so each file lives
// whole on one disk and a disk read on any Linux machine shows its files.
// A union mount joins the data disks into one folder (new files go to the
// disk with the most free space). One or two parity disks, each at least as
// big as the largest data disk, hold parity that is refreshed every night
// and can rebuild a failed data disk. Disks of any size can be added one at
// a time without rebuilding anything. If more disks fail than there are
// parity disks, only the files on the failed disks are lost.
//
// Packages used (Debian): mergerfs for the union mount, snapraid for parity.

const (
	FlexBase    = "/srv/nocapos/array"
	FlexMount   = FlexBase + "/all"
	flexEnv     = "ALFA_STORAGE_FLEX_ARRAY"
	flexSyncSvc = "nocapos-flex-sync.service"
	flexSyncTmr = "nocapos-flex-sync.timer"
	flexConf    = "flex-parity.conf"
)

// FlexEnabled reports whether the flexible array preview is switched on.
func FlexEnabled() bool { return os.Getenv(flexEnv) == "1" }

type FlexRequest struct {
	Data    []string `json:"data"`
	Parity  []string `json:"parity"`
	Confirm string   `json:"confirm,omitempty"`
}

type FlexPlan struct {
	Data      []string `json:"data"`   // kernel names
	Parity    []string `json:"parity"` // kernel names
	Usable    int64    `json:"usable"`
	Tolerance int      `json:"tolerance"`
	Warnings  []string `json:"warnings"`
	Enabled   bool     `json:"enabled"`
}

// planFlex checks a flexible array: 1-20 data disks, 1-2 parity disks, each
// parity disk at least as large as the biggest data disk.
func planFlex(data, parity []Disk) (*FlexPlan, error) {
	if len(data) < 1 || len(data) > 20 {
		return nil, bad("pick 1 to 20 data disks")
	}
	if len(parity) < 1 || len(parity) > 2 {
		return nil, bad("pick 1 or 2 parity disks")
	}
	p := &FlexPlan{Tolerance: len(parity), Warnings: []string{}}
	var biggest int64
	for _, d := range data {
		p.Data = append(p.Data, d.Name)
		p.Usable += d.Size
		biggest = max(biggest, d.Size)
	}
	for _, d := range parity {
		p.Parity = append(p.Parity, d.Name)
		if d.Size < biggest {
			return nil, bad("parity disk %s is smaller than the biggest data disk; parity disks must be at least as big", d.Name)
		}
	}
	if len(data) >= 6 && len(parity) == 1 {
		p.Warnings = append(p.Warnings, "with 6 or more data disks, two parity disks are safer")
	}
	return p, nil
}

func flexDataDir(i int) string   { return FlexBase + "/d" + strconv.Itoa(i+1) }
func flexParityDir(i int) string { return FlexBase + "/parity" + strconv.Itoa(i+1) }

// flexUnionLine is the fstab line for the union mount.
func flexUnionLine(n int) string {
	dirs := make([]string, n)
	for i := range dirs {
		dirs[i] = flexDataDir(i)
	}
	return strings.Join(dirs, ":") + " " + FlexMount +
		" fuse.mergerfs defaults,allow_other,use_ino,category.create=mfs,moveonenospc=true,minfreespace=10G,fsname=nocap-array,nofail 0 0 " + fstabMark
}

// flexParityConf is the parity tool's configuration.
func flexParityConf(nData, nParity int) string {
	var b strings.Builder
	b.WriteString("# Written by NoCapOS (Storage → Flexible array). Changes here are replaced.\n")
	for i := 0; i < nParity; i++ {
		key := "parity"
		if i > 0 {
			key = strconv.Itoa(i+1) + "-parity"
		}
		fmt.Fprintf(&b, "%s %s/array.parity\n", key, flexParityDir(i))
	}
	b.WriteString("content /var/lib/nocapos/array.content\n")
	for i := 0; i < nData; i++ {
		fmt.Fprintf(&b, "content %s/.array.content\n", flexDataDir(i))
	}
	for i := 0; i < nData; i++ {
		fmt.Fprintf(&b, "data d%d %s/\n", i+1, flexDataDir(i))
	}
	b.WriteString("exclude /lost+found/\nexclude *.tmp\n")
	return b.String()
}

func flexUnits(bin, conf string) (svc, timer string) {
	svc = "# Written by NoCapOS.\n[Unit]\nDescription=NoCapOS flexible array parity update\nRequiresMountsFor=" + FlexBase + "\n\n" +
		"[Service]\nType=oneshot\nNice=10\nIOSchedulingClass=idle\nExecStart=" + bin + " -c " + conf + " sync\n"
	timer = "# Written by NoCapOS.\n[Unit]\nDescription=Nightly NoCapOS flexible array parity update\n\n" +
		"[Timer]\nOnCalendar=*-*-* 03:30:00\nPersistent=true\nRandomizedDelaySec=15m\n\n[Install]\nWantedBy=timers.target\n"
	return
}

// fstabSetLine adds (or replaces) our line for a mountpoint.
func fstabSetLine(content, line, mountpoint string) (string, error) {
	lines := splitLines(content)
	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		if fstabMountpoint(l) == mountpoint {
			if !isOurs(l) {
				return "", bad("%s is already in /etc/fstab", mountpoint)
			}
			continue
		}
		out = append(out, l)
	}
	out = append(out, line)
	return strings.Join(out, "\n") + "\n", nil
}

func (h *Host) etcDir() string {
	if h.EtcDir != "" {
		return h.EtcDir
	}
	return "/etc/nocapos"
}

// CreateFlexArray formats each disk, joins the data disks and sets up parity.
// devs are the data disks' /dev paths followed by the parity disks'.
func (h *Host) CreateFlexArray(ctx context.Context, p FlexPlan, devs []string) error {
	if _, err := h.LookPath("mergerfs"); err != nil {
		return bad("install the flexible array tools first")
	}
	parity, err := h.LookPath("snapraid")
	if err != nil {
		return bad("install the flexible array tools first")
	}
	nd := len(p.Data)
	if len(devs) != nd+len(p.Parity) {
		return bad("the disk list changed; start again")
	}
	for i, dev := range devs {
		label, mp := "array-d"+strconv.Itoa(i+1), flexDataDir(i)
		if i >= nd {
			label, mp = "array-p"+strconv.Itoa(i-nd+1), flexParityDir(i-nd)
		}
		if err := h.Format(ctx, dev, label, mp); err != nil {
			return fmt.Errorf("%s: %w", dev, err)
		}
	}
	old, err := h.ReadFile(h.Fstab)
	if err != nil {
		return err
	}
	content, err := fstabSetLine(string(old), flexUnionLine(nd), FlexMount)
	if err != nil {
		return err
	}
	if err := h.MkdirAll(FlexMount, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(h.Fstab, []byte(content), 0o644); err != nil {
		return err
	}
	if err := h.MkdirAll(h.etcDir(), 0o755); err != nil {
		return err
	}
	conf := filepath.ToSlash(filepath.Join(h.etcDir(), flexConf))
	if err := writeFileAtomic(conf, []byte(flexParityConf(nd, len(p.Parity))), 0o644); err != nil {
		return err
	}
	svc, timer := flexUnits(parity, conf)
	if err := writeFileAtomic(filepath.Join(h.unitDir(), flexSyncSvc), []byte(svc), 0o644); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(h.unitDir(), flexSyncTmr), []byte(timer), 0o644); err != nil {
		return err
	}
	_ = h.do(ctx, readTimeout, []string{"systemctl", "daemon-reload"})
	if err := h.do(ctx, actionTimeout, []string{"mount", FlexMount}); err != nil {
		return err
	}
	if err := h.do(ctx, readTimeout, []string{"systemctl", "enable", "--now", flexSyncTmr}); err != nil {
		return err
	}
	return h.do(ctx, readTimeout, []string{"systemctl", "start", "--no-block", flexSyncSvc})
}
