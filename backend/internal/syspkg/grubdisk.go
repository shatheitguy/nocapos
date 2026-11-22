package syspkg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// The boot loader on BIOS PCs (grub-pc) must know which disk(s) to write
// itself to every time the package is updated. Installers that don't record
// that answer leave it empty, and then the first grub-pc update stops half
// way (updates can't ask questions), which blocks every later install or
// update ("Not fully installed: grub-pc"). Before repairing or updating,
// NoCapOS fills in the answer with the disk the system runs from.

// grubInstallDevices is the recorded answer from "debconf-show grub-pc"
// ("" when it is empty or missing).
func grubInstallDevices(debconfShow string) string {
	for _, l := range strings.Split(debconfShow, "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "*"))
		if strings.HasPrefix(l, "grub-pc/install_devices:") {
			return strings.TrimSpace(strings.TrimPrefix(l, "grub-pc/install_devices:"))
		}
	}
	return ""
}

// diskNames are the whole disks below a block device, from
// "lsblk -s -n -l -o NAME,TYPE <dev>" (the device and what it sits on:
// partition, LVM, encryption, RAID, ... down to the disks).
func diskNames(lsblkInverse string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range strings.Split(lsblkInverse, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || f[1] != "disk" || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		out = append(out, f[0])
	}
	sort.Strings(out)
	return out
}

// stableName picks the best /dev/disk/by-id name for a disk from the links
// in that folder (name -> what it points to, e.g. "../../sda"): a bus name
// (ata-, nvme-, scsi-, virtio-, usb-) over a wwn-/eui- name, and never a
// partition link. Falls back to /dev/<disk>.
func stableName(disk string, links map[string]string) string {
	rank := func(n string) int {
		switch {
		case strings.HasPrefix(n, "ata-"), strings.HasPrefix(n, "nvme-"), strings.HasPrefix(n, "scsi-"), strings.HasPrefix(n, "virtio-"):
			return 0
		case strings.HasPrefix(n, "usb-"):
			return 1
		}
		return 2
	}
	best := ""
	for name, target := range links {
		if strings.Contains(name, "-part") || filepath.Base(target) != disk {
			continue
		}
		if best == "" || rank(name) < rank(best) || (rank(name) == rank(best) && name < best) {
			best = name
		}
	}
	if best == "" {
		return "/dev/" + disk
	}
	return "/dev/disk/by-id/" + best
}

// GrubBootDiskStep fills in grub-pc's boot disk when it's missing. It does
// nothing on UEFI PCs, when grub-pc isn't installed, or when the answer is
// already there. Optional in every plan: it never blocks a repair.
func GrubBootDiskStep() Step {
	return Step{
		Label:    "Check the boot loader knows which disk to use",
		Optional: true,
		Fn:       ensureGrubBootDisk,
	}
}

func ensureGrubBootDisk(ctx context.Context, out io.Writer) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if _, err := os.Stat("/sys/firmware/efi"); err == nil {
		return nil // UEFI: grub-pc isn't the boot loader
	}
	status, err := exec.CommandContext(ctx, "dpkg-query", "-W", "-f=${Status}", "grub-pc").Output()
	if err != nil || !strings.Contains(string(status), "install") {
		return nil // not installed
	}
	show, _ := exec.CommandContext(ctx, "debconf-show", "grub-pc").Output()
	if grubInstallDevices(string(show)) != "" {
		return nil
	}
	src := ""
	for _, mp := range []string{"/boot", "/"} {
		b, err := exec.CommandContext(ctx, "findmnt", "-n", "-o", "SOURCE", "--target", mp).Output()
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(b)), "/dev/") {
			src = strings.TrimSpace(string(b))
			// btrfs subvolumes: /dev/sda2[/@]
			if i := strings.IndexByte(src, '['); i > 0 {
				src = src[:i]
			}
			break
		}
	}
	if src == "" {
		return errors.New("couldn't tell which disk the system starts from")
	}
	inv, err := exec.CommandContext(ctx, "lsblk", "-s", "-n", "-l", "-o", "NAME,TYPE", src).Output()
	if err != nil {
		return fmt.Errorf("lsblk %s: %w", src, err)
	}
	disks := diskNames(string(inv))
	if len(disks) == 0 {
		return fmt.Errorf("no disk found under %s", src)
	}
	links := map[string]string{}
	if ents, err := os.ReadDir("/dev/disk/by-id"); err == nil {
		for _, e := range ents {
			if t, err := os.Readlink(filepath.Join("/dev/disk/by-id", e.Name())); err == nil {
				links[e.Name()] = t
			}
		}
	}
	var devs []string
	for _, d := range disks {
		devs = append(devs, stableName(d, links))
	}
	answer := "grub-pc grub-pc/install_devices multiselect " + strings.Join(devs, ", ") + "\n"
	cmd := exec.CommandContext(ctx, "debconf-set-selections")
	cmd.Stdin = strings.NewReader(answer)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("debconf-set-selections: %w", err)
	}
	fmt.Fprintf(out, "Boot loader disk set to %s\n", strings.Join(devs, ", "))
	return nil
}
