package storage

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// lsblk columns we ask for. Older util-linux (< 2.37) has no MOUNTPOINTS.
const (
	lsblkCols    = "NAME,KNAME,PATH,SIZE,TYPE,MODEL,SERIAL,TRAN,ROTA,RM,FSTYPE,LABEL,UUID,PARTUUID,MOUNTPOINTS"
	lsblkColsOld = "NAME,KNAME,PATH,SIZE,TYPE,MODEL,SERIAL,TRAN,ROTA,RM,FSTYPE,LABEL,UUID,PARTUUID,MOUNTPOINT"
)

// BlockDev is one lsblk node (a disk, partition, md array, LVM volume...).
type BlockDev struct {
	Name        string     `json:"name"`
	KName       string     `json:"kname"`
	Path        string     `json:"path"`
	Size        flexInt    `json:"size"`
	Type        string     `json:"type"`
	Model       string     `json:"model"`
	Serial      string     `json:"serial"`
	Tran        string     `json:"tran"`
	Rota        flexBool   `json:"rota"`
	RM          flexBool   `json:"rm"`
	FSType      string     `json:"fstype"`
	Label       string     `json:"label"`
	UUID        string     `json:"uuid"`
	PartUUID    string     `json:"partuuid"`
	Mountpoints []*string  `json:"mountpoints"`
	Mountpoint  *string    `json:"mountpoint"`
	Children    []BlockDev `json:"children"`
}

func (d *BlockDev) kname() string {
	if d.KName != "" {
		return d.KName
	}
	return d.Name
}

func (d *BlockDev) devPath() string {
	if d.Path != "" {
		return d.Path
	}
	return "/dev/" + d.kname()
}

func (d *BlockDev) mounts() []string {
	var out []string
	for _, m := range d.Mountpoints {
		if m != nil && *m != "" {
			out = append(out, *m)
		}
	}
	if d.Mountpoint != nil && *d.Mountpoint != "" && len(out) == 0 {
		out = append(out, *d.Mountpoint)
	}
	return out
}

// flexInt accepts a JSON number, a numeric string or null.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" || s == "-" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		v, perr := parseSize(s)
		if perr != nil {
			return fmt.Errorf("not a number: %s", s)
		}
		*f = flexInt(v)
		return nil
	}
	*f = flexInt(n)
	return nil
}

// flexBool accepts true/false, "1"/"0" (older lsblk) or null.
type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	*f = s == "true" || s == "1"
	return nil
}

// flexStr accepts a JSON string or number.
type flexStr string

func (f *flexStr) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexStr(s)
		return nil
	}
	if string(b) == "null" {
		*f = ""
		return nil
	}
	*f = flexStr(b)
	return nil
}

func (f flexStr) int() int64 {
	s := strings.TrimSuffix(strings.TrimSpace(string(f)), "%")
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if v, err := parseSize(s); err == nil {
		return v
	}
	return 0
}

// ParseLsblk reads `lsblk -J -b -o ...` output.
func ParseLsblk(out []byte) ([]BlockDev, error) {
	var v struct {
		Blockdevices []BlockDev `json:"blockdevices"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("couldn't read the disk list: %w", err)
	}
	return v.Blockdevices, nil
}

// classifyInput is everything availability depends on.
type classifyInput struct {
	Facts       *Facts
	PoolLeaves  map[string]string // leaf path or name from zpool status -> pool name
	ActivePools map[string]bool   // imported pools
	DataDir     string            // NoCapOS data dir (slash path)
}

// systemMounts make a disk the system disk.
var systemMounts = map[string]bool{"/": true, "/boot": true, "/boot/efi": true, "/efi": true, "[SWAP]": true, "/usr": true, "/var": true}

// node is a flattened lsblk device with its top-level disk.
type node struct {
	dev  *BlockDev
	disk string
}

func flatten(devs []BlockDev) map[string]node {
	out := map[string]node{}
	var walk func(d *BlockDev, top string)
	walk = func(d *BlockDev, top string) {
		out[d.kname()] = node{d, top}
		for i := range d.Children {
			walk(&d.Children[i], top)
		}
	}
	for i := range devs {
		walk(&devs[i], devs[i].kname())
	}
	return out
}

// resolveDev turns a path or name from zpool output into a kernel name.
func resolveDev(p string, links map[string]string) string {
	if k, ok := links[p]; ok {
		return k
	}
	if !strings.HasPrefix(p, "/") {
		for _, dir := range []string{"/dev/disk/by-id/", "/dev/disk/by-partuuid/", "/dev/disk/by-uuid/", "/dev/disk/by-path/", "/dev/"} {
			if k, ok := links[dir+p]; ok {
				return k
			}
		}
		return p
	}
	if strings.HasPrefix(p, "/dev/") && !strings.HasPrefix(p, "/dev/disk/") && !strings.HasPrefix(p, "/dev/mapper/") {
		return path.Base(p)
	}
	return ""
}

// byIDPreference ranks /dev/disk/by-id names: model+serial names first.
func byIDRank(name string) int {
	for i, p := range []string{"nvme-", "ata-", "scsi-", "usb-", "mmc-", "wwn-"} {
		if strings.HasPrefix(name, p) && !strings.HasPrefix(name, "nvme-eui.") && !strings.HasPrefix(name, "nvme-nvme.") {
			return i
		}
	}
	return 99
}

// stableID picks the best /dev/disk/by-id path for a whole disk.
func stableID(kname string, links map[string]string) string {
	best, rank := "", 100
	keys := make([]string, 0, len(links))
	for k := range links {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, l := range keys {
		if links[l] != kname || !strings.HasPrefix(l, "/dev/disk/by-id/") {
			continue
		}
		base := strings.TrimPrefix(l, "/dev/disk/by-id/")
		if strings.Contains(base, "-part") {
			continue
		}
		if r := byIDRank(base); r < rank {
			best, rank = l, r
		}
	}
	return best
}

// classify turns the scan into the disk list with availability.
func classify(in classifyInput) []Disk {
	f := in.Facts
	flat := flatten(f.Devices)

	// Which disks hold pool members.
	poolOf := map[string]string{}
	for leaf, pool := range in.PoolLeaves {
		if k := resolveDev(leaf, f.Links); k != "" {
			if n, ok := flat[k]; ok {
				poolOf[n.disk] = pool
			}
		}
	}
	swaps := map[string]bool{}
	for _, s := range f.Swaps {
		swaps[s] = true
	}
	// The disk holding the data dir: the deepest mountpoint containing it.
	dataDisk, bestLen := "", -1
	if in.DataDir != "" {
		for _, n := range flat {
			for _, m := range n.dev.mounts() {
				if strings.HasPrefix(m, "/") && within(in.DataDir, m) && len(m) > bestLen {
					dataDisk, bestLen = n.disk, len(m)
				}
			}
		}
	}

	out := make([]Disk, 0, len(f.Devices))
	for i := range f.Devices {
		d := &f.Devices[i]
		disk := Disk{
			Name: d.kname(), Path: d.devPath(), ByID: stableID(d.kname(), f.Links), Size: int64(d.Size),
			Model: strings.TrimSpace(d.Model), Serial: strings.TrimSpace(d.Serial), Transport: d.Tran,
			Rotational: bool(d.Rota), Removable: bool(d.RM), Partitions: []Partition{},
		}
		for _, c := range d.Children {
			if c.Type != "part" {
				continue
			}
			mp := c.mounts()
			if mp == nil {
				mp = []string{}
			}
			// A partition's own mounts plus anything stacked on it (LVM, crypt).
			disk.Partitions = append(disk.Partitions, Partition{Name: c.kname(), Path: c.devPath(), Size: int64(c.Size),
				FSType: c.FSType, Label: c.Label, UUID: c.UUID, Mountpoints: mp})
		}
		disk.Usage, disk.Reason = usageOf(d, in, poolOf[d.kname()], swaps, dataDisk == d.kname())
		disk.Available = disk.Reason == ""
		disk.hidden = isVirtual(d) && d.Type != "rom"
		out = append(out, disk)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

func isVirtual(d *BlockDev) bool {
	n := d.kname()
	return d.Type == "loop" || d.Type == "rom" || strings.HasPrefix(n, "loop") || strings.HasPrefix(n, "zram") ||
		strings.HasPrefix(n, "ram") || strings.HasPrefix(n, "sr") || strings.HasPrefix(n, "nbd")
}

// usageOf applies the safety rules in order; reason "" means available.
func usageOf(d *BlockDev, in classifyInput, pool string, swaps map[string]bool, holdsData bool) (string, string) {
	if isVirtual(d) {
		if d.Type == "rom" || strings.HasPrefix(d.kname(), "sr") {
			return UsageSystem, "Optical drive"
		}
		return UsageSystem, "Virtual device"
	}
	var mounts []string
	var types []*BlockDev
	var walk func(x *BlockDev)
	walk = func(x *BlockDev) {
		mounts = append(mounts, x.mounts()...)
		types = append(types, x)
		if swaps[x.devPath()] || swaps["/dev/"+x.kname()] {
			mounts = append(mounts, "[SWAP]")
		}
		for i := range x.Children {
			walk(&x.Children[i])
		}
	}
	walk(d)

	for _, m := range mounts {
		if systemMounts[m] {
			return UsageSystem, "System disk"
		}
	}
	if holdsData {
		return UsageSystem, "Holds NoCapOS's own data"
	}
	if pool != "" {
		return UsagePool, "In pool " + pool
	}
	for _, x := range types {
		if x.FSType == "zfs_member" {
			if x.Label != "" && in.ActivePools[x.Label] {
				return UsagePool, "In pool " + x.Label
			}
			if x.Label != "" {
				return UsagePool, "Has ZFS pool " + x.Label + " (not imported)"
			}
			return UsagePool, "Has ZFS data from an old pool"
		}
	}
	for _, x := range types {
		if strings.HasPrefix(x.Type, "raid") || x.Type == "md" || x.FSType == "linux_raid_member" {
			return UsageMD, "Part of a software RAID (md) array"
		}
	}
	for _, x := range types {
		if x.FSType == "LVM2_member" || x.Type == "lvm" {
			return UsageLVM, "Used by LVM"
		}
	}
	for _, m := range mounts {
		if within(m, DiskBase) {
			return UsageStorage, "NoCapOS storage at " + m
		}
	}
	if len(mounts) > 0 {
		return UsageMounted, "Mounted at " + mounts[0]
	}
	if int64(d.Size) < minDiskSize {
		return UsageFree, "Too small (under 1 GiB)"
	}
	return UsageFree, ""
}

// diskOfPartition finds the top-level disk for any kernel name.
func diskOf(f *Facts, kname string) string {
	if n, ok := flatten(f.Devices)[kname]; ok {
		return n.disk
	}
	return ""
}
