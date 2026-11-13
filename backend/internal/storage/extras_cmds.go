package storage

import (
	"bufio"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Command builders and parsers for the Extras. Everything that reaches them
// was validated by the Manager; device arguments are absolute /dev paths.

// ---- support drives: cache (read speed), log (sync writes), special (metadata) ----

var supportKinds = map[string]bool{"cache": true, "log": true, "special": true}

func supportArgs(pool, kind string, devs []string) []string {
	args := []string{"zpool", "add", "-f", pool, kind}
	if kind != "cache" && len(devs) > 1 {
		args = append(args, "mirror")
	}
	return append(args, devs...)
}

func removeVdevArgs(pool, dev string) []string { return []string{"zpool", "remove", pool, dev} }

func expandSteps(pool string, leaves []string) [][]string {
	steps := [][]string{{"zpool", "set", "autoexpand=on", pool}}
	for _, l := range leaves {
		steps = append(steps, []string{"zpool", "online", "-e", pool, l})
	}
	return steps
}

// ---- RAM cache ----

// ParseARC reads /proc/spl/kstat/zfs/arcstats.
func ParseARC(text string) *ARCStats {
	v := map[string]int64{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			continue
		}
		if n, err := strconv.ParseInt(f[2], 10, 64); err == nil {
			v[f[0]] = n
		}
	}
	a := &ARCStats{Size: v["size"], Target: v["c"], Max: v["c_max"], Hits: v["hits"], Misses: v["misses"],
		L2Size: v["l2_size"], L2Hits: v["l2_hits"], L2Misses: v["l2_misses"]}
	if t := a.Hits + a.Misses; t > 0 {
		a.HitRate = float64(a.Hits*1000/t) / 10
	}
	return a
}

// ---- spin-down ----

var byIDRe = regexp.MustCompile(`^/dev/disk/by-id/[A-Za-z0-9][A-Za-z0-9._:+-]{0,190}$`)

// SpindownChoices are the idle times offered, in minutes (0 = never).
var SpindownChoices = []int{0, 5, 10, 15, 20, 30, 60, 120, 180, 240}

// hdparmValue turns minutes into hdparm's -S encoding: 1..240 are multiples
// of 5 seconds, 241..251 multiples of 30 minutes.
func hdparmValue(minutes int) (int, error) {
	ok := false
	for _, c := range SpindownChoices {
		ok = ok || c == minutes
	}
	if !ok {
		return 0, bad("pick one of the offered idle times")
	}
	switch {
	case minutes == 0:
		return 0, nil
	case minutes <= 20:
		return minutes * 12, nil
	}
	return 240 + minutes/30, nil
}

func spindownArgs(hdparm string, r SpindownRule) ([]string, error) {
	if !byIDRe.MatchString(r.ByID) {
		return nil, bad("unknown drive %q", r.ByID)
	}
	v, err := hdparmValue(r.Minutes)
	if err != nil {
		return nil, err
	}
	return []string{hdparm, "-S", strconv.Itoa(v), r.ByID}, nil
}

const spindownUnitName = "nocapos-spindown.service"

// spindownUnit is a oneshot unit that sets every drive's idle timer at boot.
// Every line comes from validated rules, never from free text.
func spindownUnit(hdparm string, rules []SpindownRule) (string, error) {
	var b strings.Builder
	b.WriteString("# Written by NoCapOS (Storage → Disks → Spin down). Changes here are replaced.\n")
	b.WriteString("[Unit]\nDescription=NoCapOS drive spin-down timers\nAfter=local-fs.target\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\n")
	for _, r := range sortedRules(rules) {
		if r.Minutes == 0 {
			continue
		}
		args, err := spindownArgs(hdparm, r)
		if err != nil {
			return "", err
		}
		b.WriteString("ExecStart=-" + strings.Join(args, " ") + "\n")
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")
	return b.String(), nil
}

func sortedRules(rules []SpindownRule) []SpindownRule {
	out := append([]SpindownRule(nil), rules...)
	sort.Slice(out, func(i, j int) bool { return out[i].ByID < out[j].ByID })
	return out
}

// ---- snapshots ----

func cloneArgs(snap, target string) []string { return []string{"zfs", "clone", snap, target} }

// ---- encryption ----

func encCreateArgs(name string, quota *int64) []string {
	args := []string{"zfs", "create", "-o", "encryption=aes-256-gcm", "-o", "keyformat=passphrase", "-o", "keylocation=prompt"}
	if quota != nil && *quota > 0 {
		args = append(args, "-o", "quota="+quotaValue(*quota))
	}
	return append(args, name)
}

func encStateArgs(pool string) []string {
	return []string{"zfs", "get", "-Hp", "-r", "-t", "filesystem", "-o", "name,property,value",
		"encryption,keystatus,encryptionroot,mounted", pool}
}

// ParseCrypt reads `zfs get -Hp -o name,property,value encryption,keystatus,encryptionroot,mounted`.
func ParseCrypt(text string) []CryptInfo {
	by := map[string]*CryptInfo{}
	var order []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		c, ok := by[f[0]]
		if !ok {
			c = &CryptInfo{Name: f[0]}
			by[f[0]] = c
			order = append(order, f[0])
		}
		switch f[1] {
		case "encryption":
			c.Encrypted = f[2] != "off" && f[2] != "-"
		case "keystatus":
			c.Unlocked = f[2] == "available"
		case "encryptionroot":
			if f[2] != "-" {
				c.Root = f[2]
			}
		case "mounted":
			c.Mounted = f[2] == "yes"
		}
	}
	out := make([]CryptInfo, 0, len(order))
	for _, n := range order {
		c := *by[n]
		if !c.Encrypted {
			c.Unlocked = true
		}
		out = append(out, c)
	}
	return out
}

// validPassphrase: ZFS takes 8 to 512 bytes; a newline would end it early.
func validPassphrase(p string) error {
	if len(p) < 8 || len(p) > 512 {
		return bad("the passphrase needs 8 to 512 characters")
	}
	if strings.ContainsAny(p, "\r\n\x00") {
		return bad("the passphrase can't contain line breaks")
	}
	return nil
}

// ---- boot drive mirror ----

// partPath is partition n of a disk: sdb,2 -> sdb2; nvme0n1,2 -> nvme0n1p2.
func partPath(dev string, n int) string {
	if last := dev[len(dev)-1]; last >= '0' && last <= '9' {
		return dev + "p" + strconv.Itoa(n)
	}
	return dev + strconv.Itoa(n)
}

var partSuffixRe = regexp.MustCompile(`(?:-part|p)?([0-9]+)$`)

// partNumber is the partition number of part on disk ("sda3","sda" -> 3).
func partNumber(part, disk string) int {
	rest := strings.TrimPrefix(part, disk)
	if rest == part || rest == "" {
		return 0
	}
	m := partSuffixRe.FindStringSubmatch(rest)
	if m == nil || m[0] != rest {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// BootPoolMember is one pool with a partition on the boot disk.
type BootPoolMember struct {
	Pool string `json:"pool"`
	Part int    `json:"part"`
	Old  string `json:"old"` // the surviving leaf (attach) or the failed one (replace)
}

// BootMirrorPlan copies the boot disk's layout to a new disk, joins every
// boot pool and installs the bootloader there too.
type BootMirrorPlan struct {
	Mode       string           `json:"mode"`   // attach | replace
	Source     string           `json:"source"` // /dev path of the healthy boot disk
	Target     string           `json:"target"` // /dev path of the new disk
	TargetByID string           `json:"target_by_id,omitempty"`
	Members    []BootPoolMember `json:"members"`
	ESPPart    int              `json:"esp_part,omitempty"`
	EFI        bool             `json:"efi"`
	Arch       string           `json:"arch"`
}

const espMount = "/boot/efi2"

func (p BootMirrorPlan) targetPart(n int) string {
	if p.TargetByID != "" {
		return p.TargetByID + "-part" + strconv.Itoa(n)
	}
	return partPath(p.Target, n)
}

func grubTarget(arch string) string {
	if arch == "arm64" {
		return "arm64-efi"
	}
	return "x86_64-efi"
}

// Steps are the exact commands, in order (also shown in the dry run).
func (p BootMirrorPlan) Steps() [][]string {
	steps := [][]string{
		{"sgdisk", "--replicate=" + p.Target, p.Source},
		{"sgdisk", "--randomize-guids", p.Target},
		{"udevadm", "settle"},
	}
	for _, m := range p.Members {
		if p.Mode == "replace" {
			steps = append(steps, []string{"zpool", "replace", "-f", m.Pool, m.Old, p.targetPart(m.Part)})
		} else {
			steps = append(steps, []string{"zpool", "attach", "-f", m.Pool, m.Old, p.targetPart(m.Part)})
		}
	}
	if p.EFI && p.ESPPart > 0 {
		esp := partPath(p.Target, p.ESPPart)
		t := grubTarget(p.Arch)
		steps = append(steps,
			[]string{"mkfs.vfat", "-F", "32", "-n", "NOCAPEFI2", esp},
			[]string{"mkdir", "-p", espMount},
			[]string{"mount", esp, espMount},
			[]string{"grub-install", "--target=" + t, "--efi-directory=" + espMount, "--bootloader-id=nocapos-mirror", "--recheck"},
			[]string{"grub-install", "--target=" + t, "--efi-directory=" + espMount, "--removable", "--no-nvram", "--recheck"},
			[]string{"umount", espMount},
		)
	} else if !p.EFI {
		steps = append(steps, []string{"grub-install", "--target=i386-pc", "--recheck", p.Target})
	}
	return steps
}
