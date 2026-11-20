// Package installer is the NoCapOS installer's engine (cmd/nocap-installer):
// it lists the disks NoCapOS can go on, then writes NoCapOS onto one with
// the fixed A/B layout (os/README.md), from the USB stick it booted from.
package installer

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MinDiskBytes: the smallest disk NoCapOS installs on (startup, two system
// slots of 6 GiB and room for your stuff).
const MinDiskBytes = 24 * 1000 * 1000 * 1000

// Layout sizes (match Rugix's PC layout and os/packaging/.../bootstrapping.toml).
const (
	ConfigBytes = 256 << 20
	BootBytes   = 256 << 20
	SystemBytes = 6 << 30
)

// Disk kinds, for the drive icon and badge.
const (
	KindNVMe = "nvme"
	KindSSD  = "ssd"
	KindHDD  = "hdd"
	KindUSB  = "usb"
	KindSD   = "sd"
)

// Segment kinds on a disk's usage bar.
const (
	SegStartup = "startup" // small boot partitions
	SegSystem  = "system"  // another operating system
	SegData    = "data"    // files
	SegFree    = "free"    // not used
)

// Segment is one coloured part of a disk's usage bar.
type Segment struct {
	Kind  string `json:"kind"`
	Bytes int64  `json:"bytes"`
	Label string `json:"label,omitempty"` // the partition's own name, when it has one
}

// Disk is a drive NoCapOS could be installed on.
type Disk struct {
	Path     string    `json:"path"` // /dev/nvme0n1
	Name     string    `json:"name"` // friendly: the model
	Model    string    `json:"model"`
	Vendor   string    `json:"vendor,omitempty"`
	Bytes    int64     `json:"bytes"`
	Kind     string    `json:"kind"`
	Segments []Segment `json:"segments"`
	// Boot: the installer runs from this disk (hidden in the list).
	Boot bool `json:"boot"`
	// TooSmall: under MinDiskBytes (shown greyed out).
	TooSmall bool `json:"too_small"`
}

// lsblk -J -b -o NAME,PATH,SIZE,MODEL,VENDOR,TRAN,ROTA,RM,TYPE,FSTYPE,LABEL,PARTLABEL,PARTTYPE,MOUNTPOINTS
type lsblkDev struct {
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	Size        jsonInt    `json:"size"`
	Model       *string    `json:"model"`
	Vendor      *string    `json:"vendor"`
	Tran        *string    `json:"tran"`
	Rota        jsonBool   `json:"rota"`
	RM          jsonBool   `json:"rm"`
	Type        string     `json:"type"`
	FSType      *string    `json:"fstype"`
	Label       *string    `json:"label"`
	PartLabel   *string    `json:"partlabel"`
	PartType    *string    `json:"parttype"`
	Mountpoints []*string  `json:"mountpoints"`
	Children    []lsblkDev `json:"children"`
}

// LsblkArgs is how ListDisks calls lsblk.
var LsblkArgs = []string{"-J", "-b", "-o", "NAME,PATH,SIZE,MODEL,VENDOR,TRAN,ROTA,RM,TYPE,FSTYPE,LABEL,PARTLABEL,PARTTYPE,MOUNTPOINTS"}

// ParseDisks turns lsblk's JSON into the disk list. bootMounts are the
// installer's own mount points: the disk holding one is the boot disk.
func ParseDisks(b []byte, bootMounts []string) ([]Disk, error) {
	var out struct {
		Devices []lsblkDev `json:"blockdevices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	var disks []Disk
	for _, d := range out.Devices {
		if d.Type != "disk" || int64(d.Size) == 0 {
			continue
		}
		if strings.HasPrefix(d.Name, "zram") || strings.HasPrefix(d.Name, "loop") || strings.HasPrefix(d.Name, "sr") || strings.HasPrefix(d.Name, "fd") {
			continue
		}
		if strings.HasPrefix(d.Name, "mmcblk") && strings.Contains(d.Name, "boot") {
			continue // eMMC boot areas
		}
		disk := Disk{Path: d.Path, Model: str(d.Model), Vendor: str(d.Vendor), Bytes: int64(d.Size)}
		if disk.Path == "" {
			disk.Path = "/dev/" + d.Name
		}
		disk.Kind = kindOf(d)
		disk.Name = friendlyName(disk)
		disk.TooSmall = disk.Bytes < MinDiskBytes
		disk.Boot = holdsMount(d, bootMounts)
		disk.Segments = segments(d)
		disks = append(disks, disk)
	}
	sort.SliceStable(disks, func(i, j int) bool { return disks[i].Path < disks[j].Path })
	return disks, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func kindOf(d lsblkDev) string {
	tran := str(d.Tran)
	switch {
	case strings.HasPrefix(d.Name, "nvme") || tran == "nvme":
		return KindNVMe
	case strings.HasPrefix(d.Name, "mmcblk"):
		return KindSD
	case tran == "usb":
		return KindUSB
	case bool(d.RM) && tran != "sata" && tran != "ata":
		return KindUSB
	case bool(d.Rota):
		return KindHDD
	default:
		return KindSSD
	}
}

func friendlyName(d Disk) string {
	name := strings.Join(strings.Fields(strings.ReplaceAll(d.Model, "_", " ")), " ")
	if v := d.Vendor; v != "" && name != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(v)) && !looksGenericVendor(v) {
		name = v + " " + name
	}
	if name == "" {
		switch d.Kind {
		case KindNVMe:
			name = "NVMe drive"
		case KindSD:
			name = "Memory card"
		case KindUSB:
			name = "USB drive"
		case KindHDD:
			name = "Hard drive"
		default:
			name = "Drive"
		}
	}
	return name
}

func looksGenericVendor(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "ATA", "SCSI", "USB", "GENERIC", "QEMU", "VIRTIO":
		return true
	}
	return false
}

func holdsMount(d lsblkDev, mounts []string) bool {
	for _, m := range d.Mountpoints {
		if m == nil {
			continue
		}
		for _, want := range mounts {
			if *m == want {
				return true
			}
		}
	}
	for _, c := range d.Children {
		if holdsMount(c, mounts) {
			return true
		}
	}
	return false
}

// GPT partition type GUIDs (lowercase) worth knowing.
const (
	guidEFI       = "c12a7328-f81f-11d2-ba4b-00a0c93ec93b"
	guidBIOS      = "21686148-6449-6e6f-744e-656564454649"
	guidMSReserv  = "e3c9e316-0b5c-4db8-817d-f92df00215ae"
	guidMSBasic   = "ebd0a0a2-b9e5-4433-87c0-68b6b72699c7"
	guidMSRecov   = "de94bba4-06d1-4d40-a16a-bfd50179d6ac"
	guidLinuxRoot = "4f68bce3-e8cd-4db1-96e7-fbcaf984b709"
	guidSwap      = "0657fd6d-a4ab-43c4-84e5-0933c84b4f4f"
	guidAppleAPFS = "7c3457ef-0000-11aa-aa11-00306543ecac"
)

// segments describes what's on a disk in plain words.
func segments(d lsblkDev) []Segment {
	total := int64(d.Size)
	var used int64
	windows := false
	for _, c := range d.Children {
		pt := strings.ToLower(str(c.PartType))
		if pt == guidMSReserv || pt == guidMSRecov || strings.EqualFold(str(c.FSType), "ntfs") {
			windows = true
		}
	}
	var segs []Segment
	add := func(kind string, n int64, label string) {
		if n <= 0 {
			return
		}
		if k := len(segs) - 1; k >= 0 && segs[k].Kind == kind && segs[k].Label == label {
			segs[k].Bytes += n
			return
		}
		segs = append(segs, Segment{Kind: kind, Bytes: n, Label: label})
	}
	for _, c := range d.Children {
		if c.Type != "part" {
			continue
		}
		n := int64(c.Size)
		used += n
		pt := strings.ToLower(str(c.PartType))
		fs := strings.ToLower(str(c.FSType))
		label := str(c.Label)
		if label == "" {
			label = str(c.PartLabel)
		}
		switch {
		case pt == guidEFI || pt == guidBIOS || pt == guidMSReserv || pt == guidMSRecov || n < 1<<30:
			add(SegStartup, n, "")
		case pt == guidLinuxRoot || pt == guidSwap || pt == guidAppleAPFS || fs == "swap" || fs == "squashfs" || fs == "apfs":
			add(SegSystem, n, "")
		case fs == "ntfs" && windows && segmentIsFirstBig(d, c):
			add(SegSystem, n, "")
		case pt == guidMSBasic || fs == "ntfs" || fs == "exfat" || fs == "vfat" || fs == "ext4" || fs == "xfs" || fs == "btrfs" || fs == "zfs_member" || fs == "crypto_luks":
			if (fs == "ext4" || fs == "btrfs" || fs == "xfs") && looksLikeSystem(label) {
				add(SegSystem, n, "")
			} else {
				add(SegData, n, label)
			}
		default:
			add(SegData, n, label)
		}
	}
	if free := total - used; free > total/100 && free > 64<<20 {
		add(SegFree, free, "")
	}
	if len(segs) == 0 {
		segs = []Segment{{Kind: SegFree, Bytes: total}}
	}
	return segs
}

// segmentIsFirstBig: the first large NTFS partition is where Windows lives.
func segmentIsFirstBig(d lsblkDev, c lsblkDev) bool {
	for _, x := range d.Children {
		if strings.EqualFold(str(x.FSType), "ntfs") && int64(x.Size) >= 8<<30 {
			return x.Path == c.Path && x.Name == c.Name
		}
	}
	return false
}

func looksLikeSystem(label string) bool {
	l := strings.ToLower(label)
	for _, w := range []string{"root", "system", "ubuntu", "debian", "fedora", "linux", "mint", "arch", "rootfs", "writable"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// jsonInt accepts lsblk's sizes as numbers or strings (older versions).
type jsonInt int64

func (j *jsonInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		*j = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	*j = jsonInt(n)
	return err
}

// jsonBool accepts true/false, "1"/"0" and 1/0.
type jsonBool bool

func (j *jsonBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	*j = jsonBool(s == "true" || s == "1")
	return nil
}

// PartPath is partition n of disk: /dev/sda3, /dev/nvme0n1p3, /dev/mmcblk0p3.
func PartPath(disk string, n int) string {
	if disk != "" && disk[len(disk)-1] >= '0' && disk[len(disk)-1] <= '9' {
		return disk + "p" + strconv.Itoa(n)
	}
	return disk + strconv.Itoa(n)
}

// SourceIDs are the stick's partition table and partition IDs. The boot
// files name the system partition by its ID (root=PARTUUID=...), so the new
// drive's partitions 1-4 keep the stick's.
type SourceIDs struct {
	LabelID string
	UUIDs   map[int]string // partition number -> PARTUUID
}

var (
	labelIDRe = regexp.MustCompile(`(?m)^label-id:\s*([0-9A-Fa-f-]{36})\s*$`)
	partRe    = regexp.MustCompile(`(?m)^\S*?(\d+)\s*:.*\buuid=([0-9A-Fa-f-]{36})`)
)

// ParseSfdiskDump reads `sfdisk -d` output.
func ParseSfdiskDump(dump string) SourceIDs {
	ids := SourceIDs{UUIDs: map[int]string{}}
	if m := labelIDRe.FindStringSubmatch(dump); m != nil {
		ids.LabelID = m[1]
	}
	for _, m := range partRe.FindAllStringSubmatch(dump, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			ids.UUIDs[n] = m[2]
		}
	}
	return ids
}

// SfdiskScript is the new partition table: Rugix's PC layout with the data
// partition filling the rest of the disk, keeping the stick's IDs.
func SfdiskScript(src SourceIDs) string {
	uuid := func(n int) string {
		if u := src.UUIDs[n]; u != "" {
			return ", uuid=" + u
		}
		return ""
	}
	lines := []string{"label: gpt"}
	if src.LabelID != "" {
		lines = append(lines, "label-id: "+src.LabelID)
	}
	return strings.Join(append(lines,
		"size=256MiB, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B, name=\"NoCapOS startup\""+uuid(1),
		"size=256MiB, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=\"NoCapOS boot A\""+uuid(2),
		"size=256MiB, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=\"NoCapOS boot B\""+uuid(3),
		"size=6GiB, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=\"NoCapOS system A\""+uuid(4),
		"size=6GiB, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=\"NoCapOS system B\"",
		"type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=\"NoCapOS data\"",
		"",
	), "\n")
}

// NewLayout is the "after" bar for a disk: startup, system, your stuff.
func NewLayout(bytes int64) []Segment {
	startup := int64(ConfigBytes + 2*BootBytes)
	system := int64(2 * SystemBytes)
	return []Segment{
		{Kind: SegStartup, Bytes: startup},
		{Kind: SegSystem, Bytes: system},
		{Kind: SegData, Bytes: bytes - startup - system - 1<<20},
	}
}
