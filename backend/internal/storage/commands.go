package storage

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Command builders. Every name reaching these was validated first; disk
// arguments are always absolute /dev paths, so none can look like a flag.

// layoutWord is the zpool keyword for a layout ("" for a plain stripe).
func layoutWord(layout string) string {
	if layout == "stripe" {
		return ""
	}
	return layout
}

func createPoolArgs(name, layout string, devs []string, compression, mountpoint string) []string {
	args := []string{"zpool", "create", "-f", "-o", "ashift=12",
		"-O", "compression=" + compression, "-O", "atime=off", "-O", "xattr=sa", "-O", "acltype=posixacl",
		"-m", mountpoint, name}
	if w := layoutWord(layout); w != "" {
		args = append(args, w)
	}
	return append(args, devs...)
}

func addVdevArgs(pool, layout string, devs []string) []string {
	args := []string{"zpool", "add", "-f", pool}
	if w := layoutWord(layout); w != "" {
		args = append(args, w)
	}
	return append(args, devs...)
}

func replaceArgs(pool, old, dev string) []string {
	return []string{"zpool", "replace", "-f", pool, old, dev}
}

func destroyArgs(pool string) []string { return []string{"zpool", "destroy", pool} }
func exportArgs(pool string) []string  { return []string{"zpool", "export", pool} }
func importArgs(nameOrID string) []string {
	return []string{"zpool", "import", "-d", "/dev/disk/by-id", nameOrID}
}

func scrubArgs(pool string, stop bool) []string {
	if stop {
		return []string{"zpool", "scrub", "-s", pool}
	}
	return []string{"zpool", "scrub", pool}
}

func quotaValue(q int64) string {
	if q <= 0 {
		return "none"
	}
	return strconv.FormatInt(q, 10)
}

func datasetCreateArgs(name string, quota *int64, compression string) []string {
	args := []string{"zfs", "create"}
	if quota != nil && *quota > 0 {
		args = append(args, "-o", "quota="+quotaValue(*quota))
	}
	if compression != "" && compression != "inherit" {
		args = append(args, "-o", "compression="+compression)
	}
	return append(args, name)
}

func datasetUpdateArgs(name string, quota *int64, compression string) [][]string {
	var steps [][]string
	if quota != nil {
		steps = append(steps, []string{"zfs", "set", "quota=" + quotaValue(*quota), name})
	}
	switch compression {
	case "":
	case "inherit":
		steps = append(steps, []string{"zfs", "inherit", "compression", name})
	default:
		steps = append(steps, []string{"zfs", "set", "compression=" + compression, name})
	}
	return steps
}

func datasetDestroyArgs(name string) []string  { return []string{"zfs", "destroy", "-r", name} }
func snapshotArgs(full string) []string        { return []string{"zfs", "snapshot", full} }
func rollbackArgs(full string) []string        { return []string{"zfs", "rollback", "-r", full} }
func snapshotDestroyArgs(full string) []string { return []string{"zfs", "destroy", full} }

// partitionPath is the first partition of a disk: sdb -> sdb1, nvme0n1 -> nvme0n1p1.
func partitionPath(dev string) string {
	if last := dev[len(dev)-1]; last >= '0' && last <= '9' {
		return dev + "p1"
	}
	return dev + "1"
}

// sfdiskScript makes one GPT partition spanning the disk (Linux filesystem type).
const sfdiskScript = "label: gpt\ntype=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=nocapos\n"

func formatSteps(dev, label string) [][]string {
	part := partitionPath(dev)
	return [][]string{
		{"wipefs", "-a", dev},
		{"sfdisk", "--wipe", "always", "--wipe-partitions", "always", dev},
		{"udevadm", "settle"},
		{"wipefs", "-a", part},
		{"mkfs.ext4", "-F", "-m", "0", "-L", label, part},
		{"blkid", "-s", "UUID", "-o", "value", part},
	}
}

// ---- /etc/fstab ----

const fstabMark = "# nocapos"

func fstabLine(uuid, mountpoint string) string {
	return "UUID=" + uuid + " " + mountpoint + " ext4 defaults,nofail,x-systemd.device-timeout=10s 0 2 " + fstabMark
}

// fstabMountpoint returns the mountpoint field of a line ("" for comments).
func fstabMountpoint(line string) string {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return ""
	}
	if f := strings.Fields(t); len(f) >= 2 {
		return f[1]
	}
	return ""
}

func isOurs(line string) bool { return strings.HasSuffix(strings.TrimSpace(line), fstabMark) }

// fstabSet adds (or replaces) our line for a mountpoint and keeps every other
// line as it was. A mountpoint used by someone else's line is an error.
func fstabSet(content, uuid, mountpoint string) (string, error) {
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
	out = append(out, fstabLine(uuid, mountpoint))
	return strings.Join(out, "\n") + "\n", nil
}

// fstabRemove drops our line for a mountpoint (only lines we wrote).
func fstabRemove(content, mountpoint string) string {
	lines := splitLines(content)
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if fstabMountpoint(l) == mountpoint && isOurs(l) {
			continue
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

func splitLines(content string) []string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

// writeFileAtomic replaces a file via a temp file + rename in the same folder.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".nocapos-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, path)
}
