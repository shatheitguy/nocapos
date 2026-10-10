package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Fixtures in testdata are hand-written from the documented output formats
// of lsblk (util-linux 2.37+ and older), OpenZFS 2.1-2.3 text and 2.3 JSON,
// and smartctl 7.x JSON; they are not captures from a real machine.

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var testLinks = map[string]string{
	"/dev/disk/by-id/ata-ST4000VN008-2DR166_ZDH1AAAA":              "sdb",
	"/dev/disk/by-id/ata-ST4000VN008-2DR166_ZDH1AAAA-part1":        "sdb1",
	"/dev/disk/by-id/ata-ST4000VN008-2DR166_ZDH1BBBB":              "sdc",
	"/dev/disk/by-id/ata-ST4000VN008-2DR166_ZDH1BBBB-part1":        "sdc1",
	"/dev/disk/by-partuuid/6f1d5b0e-9a2c-4c1e-b3d4-7e8f9a0b1c2d":   "sdc1",
	"/dev/disk/by-id/wwn-0x50014ee2b5c1d2e3":                       "sdg",
	"/dev/disk/by-id/ata-WDC_WD40EFRX-68N32N0_WD-WCC7K1234567":     "sdg",
	"/dev/disk/by-id/nvme-eui.002538b521b3c4d5":                    "nvme0n1",
	"/dev/disk/by-id/nvme-Samsung_SSD_980_PRO_1TB_S5GXNX0T512345A": "nvme0n1",
	"/dev/disk/by-id/ata-ST8000VN004-2M2101_WSD3A1B2":              "sdl",
	"/dev/disk/by-id/ata-ST8000VN004-2M2101_WSD3A1B2-part1":        "sdl1",
}

func classifyFixture(t *testing.T) map[string]Disk {
	t.Helper()
	devs, err := ParseLsblk(fixture(t, "lsblk.json"))
	if err != nil {
		t.Fatal(err)
	}
	pools := ParseStatusText(string(fixture(t, "zpool_status_mirror.txt")))
	in := classifyInput{Facts: &Facts{Devices: devs, Links: testLinks}, PoolLeaves: map[string]string{},
		ActivePools: map[string]bool{"tank": true}, DataDir: "/var/lib/nocapos/data"}
	for _, l := range leaves(pools[0].Vdevs) {
		in.PoolLeaves[l.Path] = "tank"
	}
	out := map[string]Disk{}
	for _, d := range classify(in) {
		out[d.Name] = d
	}
	return out
}

func TestClassifyAvailability(t *testing.T) {
	disks := classifyFixture(t)
	cases := []struct {
		name, usage, reason string
		hidden              bool
	}{
		{"sda", UsageSystem, "System disk", false}, // / on LVM on sda3, /boot, /boot/efi
		{"sdb", UsagePool, "In pool tank", false},  // by-id path in zpool status
		{"sdc", UsagePool, "In pool tank", false},  // by-partuuid path, no zfs_member label
		{"sdd", UsageMD, "Part of a software RAID (md) array", false},
		{"sde", UsageMD, "Part of a software RAID (md) array", false},
		{"sdf", UsageLVM, "Used by LVM", false},
		{"sdg", UsageFree, "", false},
		{"sdh", UsageFree, "", false}, // NTFS but not mounted: can be erased
		{"sdi", UsageMounted, "Mounted at /media/usb", false},
		{"sdj", UsageStorage, "NoCapOS storage at /srv/nocapos/disks/media", false},
		{"sdk", UsageFree, "Too small (under 1 GiB)", false},
		{"sdl", UsagePool, "Has ZFS pool archive (not imported)", false},
		{"sr0", UsageSystem, "Optical drive", false},
		{"nvme0n1", UsageSystem, "Holds NoCapOS's own data", false},
		{"loop0", UsageSystem, "Virtual device", true},
		{"zram0", UsageSystem, "Virtual device", true},
	}
	for _, c := range cases {
		d, ok := disks[c.name]
		if !ok {
			t.Errorf("%s missing", c.name)
			continue
		}
		if d.Usage != c.usage || d.Reason != c.reason || d.hidden != c.hidden || d.Available != (c.reason == "") {
			t.Errorf("%s: got usage=%s reason=%q hidden=%v available=%v; want %s %q %v", c.name, d.Usage, d.Reason, d.hidden, d.Available, c.usage, c.reason, c.hidden)
		}
	}
	if got := disks["sdg"].ByID; got != "/dev/disk/by-id/ata-WDC_WD40EFRX-68N32N0_WD-WCC7K1234567" {
		t.Errorf("sdg by-id = %q, want the ata- name over wwn-", got)
	}
	if got := disks["nvme0n1"].ByID; got != "/dev/disk/by-id/nvme-Samsung_SSD_980_PRO_1TB_S5GXNX0T512345A" {
		t.Errorf("nvme by-id = %q, want the model/serial name over eui", got)
	}
	if got := disks["sda"]; len(got.Partitions) != 3 || got.Partitions[0].Mountpoints[0] != "/boot/efi" || got.Rotational {
		t.Errorf("sda partitions = %+v", got.Partitions)
	}
	if !disks["sdb"].Rotational || disks["sdb"].Size != 4000787030016 || disks["sdi"].Transport != "usb" {
		t.Errorf("disk fields not read: %+v", disks["sdb"])
	}
}

func TestClassifySwapFromProc(t *testing.T) {
	devs, err := ParseLsblk(fixture(t, "lsblk.json"))
	if err != nil {
		t.Fatal(err)
	}
	in := classifyInput{Facts: &Facts{Devices: devs, Links: testLinks, Swaps: parseSwaps("Filename\tType\tSize\tUsed\tPriority\n/dev/sdh1 partition 2929287 0 -2\n")}}
	for _, d := range classify(in) {
		if d.Name == "sdh" && (d.Available || d.Reason != "System disk") {
			t.Errorf("swap partition from /proc/swaps: %+v", d)
		}
	}
}

func TestLsblkOldFormat(t *testing.T) {
	devs, err := ParseLsblk(fixture(t, "lsblk_old.json"))
	if err != nil {
		t.Fatal(err)
	}
	disks := classify(classifyInput{Facts: &Facts{Devices: devs}})
	if len(disks) != 2 {
		t.Fatalf("got %d disks", len(disks))
	}
	if disks[0].Reason != "System disk" || disks[0].Rotational {
		t.Errorf("sda: %+v", disks[0])
	}
	if !disks[1].Available || !disks[1].Rotational || disks[1].Size != 4000787030016 {
		t.Errorf("sdb: %+v", disks[1])
	}
}

func TestStatusTextMirrorScrub(t *testing.T) {
	pools := ParseStatusText(string(fixture(t, "zpool_status_mirror.txt")))
	if len(pools) != 1 {
		t.Fatalf("got %d pools", len(pools))
	}
	p := pools[0]
	if p.Name != "tank" || p.Health != "ONLINE" || p.Layout != "mirror" || p.Errors != "" {
		t.Errorf("pool: %+v", p)
	}
	if len(p.Vdevs) != 1 || p.Vdevs[0].Type != "mirror" || len(p.Vdevs[0].Children) != 2 {
		t.Fatalf("vdevs: %+v", p.Vdevs)
	}
	if c := p.Vdevs[0].Children[1]; c.Path != "/dev/disk/by-partuuid/6f1d5b0e-9a2c-4c1e-b3d4-7e8f9a0b1c2d" || c.Type != "disk" || c.Health != "ONLINE" {
		t.Errorf("leaf: %+v", c)
	}
	s := p.Scan
	if s.Function != "scrub" || s.State != "scanning" || s.Percent == nil || *s.Percent != 22.61 || s.EtaSeconds == nil || *s.EtaSeconds != 6730 {
		t.Errorf("scan: %+v", s)
	}
}

func TestStatusTextDegradedRaidz2(t *testing.T) {
	pools := ParseStatusText(string(fixture(t, "zpool_status_degraded.txt")))
	if len(pools) != 2 {
		t.Fatalf("got %d pools", len(pools))
	}
	v := pools[0]
	if v.Name != "vault" || v.Health != "DEGRADED" || v.Layout != "raidz2" || v.Status == "" {
		t.Errorf("vault: %+v", v)
	}
	if len(v.Vdevs) != 4 || v.Vdevs[1].Type != "logs" || v.Vdevs[2].Type != "cache" || v.Vdevs[3].Type != "spares" {
		t.Fatalf("groups: %+v", v.Vdevs)
	}
	rz := v.Vdevs[0]
	if rz.Type != "raidz2" || rz.Health != "DEGRADED" || len(rz.Children) != 6 {
		t.Fatalf("raidz2: %+v", rz)
	}
	bad := rz.Children[2]
	if bad.GUID != "7965954862312465432" || bad.Health != "FAULTED" || bad.Was != "/dev/disk/by-id/ata-ST4000VN008-2DR166_ZDH1EEEE-part1" {
		t.Errorf("faulted: %+v", bad)
	}
	if rz.Children[1].ChecksumErrors != 2 || rz.Children[3].ReadErrors != 3 {
		t.Errorf("counters: %+v %+v", rz.Children[1], rz.Children[3])
	}
	if sp := v.Vdevs[3].Children[0]; sp.Health != "AVAIL" || sp.Path == "" {
		t.Errorf("spare: %+v", sp)
	}
	if v.Scan.State != "finished" || v.Scan.Errors == nil || *v.Scan.Errors != 0 || v.Scan.Finished == "" {
		t.Errorf("scan: %+v", v.Scan)
	}
	tiny := pools[1]
	if tiny.Layout != "stripe" || tiny.Scan.State != "none" || len(tiny.Vdevs) != 1 || tiny.Vdevs[0].Path != "/dev/sdx1" {
		t.Errorf("tiny: %+v", tiny)
	}
}

func TestStatusTextResilver(t *testing.T) {
	p := ParseStatusText(string(fixture(t, "zpool_status_resilver.txt")))[0]
	if p.Scan.Function != "resilver" || p.Scan.State != "scanning" || *p.Scan.Percent != 42.75 || *p.Scan.EtaSeconds != 47*60+3 {
		t.Errorf("scan: %+v", p.Scan)
	}
	if p.Layout != "mirror" {
		t.Errorf("layout %s", p.Layout)
	}
	rep := p.Vdevs[0].Children[1]
	if rep.Type != "replacing" || len(rep.Children) != 2 {
		t.Fatalf("replacing: %+v", rep)
	}
	if rep.Children[0].Health != "UNAVAIL" || rep.Children[0].Was == "" || rep.Children[1].Note != "resilvering" {
		t.Errorf("children: %+v", rep.Children)
	}
	if n := len(leaves(p.Vdevs)); n != 3 {
		t.Errorf("leaves = %d", n)
	}
}

func TestStatusJSON(t *testing.T) {
	now := time.Unix(1791419041+1800, 0)
	pools, err := ParseStatusJSON(fixture(t, "zpool_status.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 2 || pools[0].Name != "tank" || pools[1].Name != "vault" {
		t.Fatalf("pools: %+v", pools)
	}
	tank := pools[0]
	if tank.Layout != "mirror" || tank.Health != "ONLINE" || len(tank.Vdevs[0].Children) != 2 {
		t.Errorf("tank: %+v", tank)
	}
	if tank.Scan.State != "scanning" || tank.Scan.Function != "scrub" || tank.Scan.Percent == nil || *tank.Scan.Percent != 22.64 || tank.Scan.EtaSeconds == nil {
		t.Errorf("tank scan: %+v", tank.Scan)
	}
	vault := pools[1]
	if vault.Health != "DEGRADED" || vault.Layout != "raidz2" || vault.Status == "" {
		t.Errorf("vault: %+v", vault)
	}
	rz := vault.Vdevs[0]
	if rz.Type != "raidz2" || len(rz.Children) != 4 {
		t.Fatalf("raidz: %+v", rz)
	}
	var faulted *Vdev
	for i := range rz.Children {
		if rz.Children[i].Health == "FAULTED" {
			faulted = &rz.Children[i]
		}
	}
	if faulted == nil || faulted.GUID != "7965954862312465432" || faulted.Path == "" {
		t.Errorf("faulted: %+v", faulted)
	}
	if vault.Vdevs[1].Type != "logs" || vault.Vdevs[2].Type != "spares" {
		t.Errorf("groups: %+v", vault.Vdevs[1:])
	}
	if vault.Scan.State != "finished" || vault.Scan.Finished == "" {
		t.Errorf("vault scan: %+v", vault.Scan)
	}
}

func TestListParsing(t *testing.T) {
	txt := ParseListText("tank\t3985729650688\t1342177280000\t2643552370688\t3\t33\tONLINE\nvault\t23991687168000\t9895604649984\t14096082518016\t-\t41\tDEGRADED\n")
	if s := txt["tank"]; s.Size != 3985729650688 || s.Frag != 3 || s.Cap != 33 || s.Health != "ONLINE" {
		t.Errorf("text: %+v", s)
	}
	if s := txt["vault"]; s.Frag != 0 || s.Health != "DEGRADED" {
		t.Errorf("text vault: %+v", s)
	}
	js, err := ParseListJSON(fixture(t, "zpool_list.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s := js["tank"]; s.Size != 3985729650688 || s.Alloc != 1342177280000 || s.Cap != 33 || s.Health != "ONLINE" {
		t.Errorf("json: %+v", s)
	}
}

func TestImportParsing(t *testing.T) {
	imp := ParseImport(string(fixture(t, "zpool_import.txt")))
	if len(imp) != 2 {
		t.Fatalf("got %+v", imp)
	}
	if imp[0].Name != "archive" || imp[0].ID != "4410227397452116412" || imp[0].Health != "ONLINE" ||
		len(imp[0].Disks) != 1 || imp[0].Disks[0] != "ata-ST8000VN004-2M2101_WSD3A1B2-part1" {
		t.Errorf("archive: %+v", imp[0])
	}
	if imp[1].Health != "DEGRADED" || len(imp[1].Disks) != 1 {
		t.Errorf("oldmirror: %+v", imp[1])
	}
	if k := resolveDev(imp[0].Disks[0], testLinks); k != "sdl1" {
		t.Errorf("resolve import disk = %q", k)
	}
}

func TestDatasetAndSnapshotParsing(t *testing.T) {
	ds := ParseDatasets("tank\t1342177280000\t2643552370688\t98304\t/srv/nocapos/pools/tank\tlz4\t1.12\t0\ntank/backups\t214748364800\t321922011136\t214748364800\t/srv/nocapos/pools/tank/backups\tzstd\t1.63\t536870912000\n")
	if len(ds) != 2 || ds[1].Quota != 536870912000 || ds[1].CompressRatio != 1.63 || ds[0].Mountpoint != "/srv/nocapos/pools/tank" {
		t.Errorf("datasets: %+v", ds)
	}
	sn := ParseSnapshots("tank/media@weekly\t1790000000\t3221225472\t1030792151040\n")
	if len(sn) != 1 || sn[0].Created.Unix() != 1790000000 || sn[0].Used != 3221225472 {
		t.Errorf("snapshots: %+v", sn)
	}
}

func TestSmartSATA(t *testing.T) {
	r, err := ParseSmart(fixture(t, "smart_sata.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := r.Summary
	if !s.Available || s.Passed == nil || !*s.Passed || *s.TemperatureC != 36 || *s.PowerOnHours != 28734 ||
		*s.Reallocated != 16 || *s.Pending != 0 || !s.TestRunning || *s.TestRemaining != 90 {
		t.Errorf("summary: %+v", s)
	}
	if len(r.Attributes) != 7 || r.Attributes[2].Name != "Reallocated Sector Ct" || r.Attributes[3].Raw != "28734 (63 112 0)" {
		t.Errorf("attributes: %+v", r.Attributes)
	}
	if len(r.SelfTests) != 2 || r.SelfTests[0].Type != "Short offline" || r.SelfTests[0].Hours != 28700 {
		t.Errorf("self tests: %+v", r.SelfTests)
	}
	if lvl, _ := smartProblem(&s); lvl != "warning" {
		t.Errorf("reallocated sectors should warn, got %q", lvl)
	}
}

func TestSmartNVMe(t *testing.T) {
	r, err := ParseSmart(fixture(t, "smart_nvme.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := r.Summary
	if !*s.Passed || *s.TemperatureC != 41 || *s.PercentUsed != 3 || *s.MediaErrors != 0 || *s.PowerOnHours != 8123 || s.TestRunning {
		t.Errorf("summary: %+v", s)
	}
	if r.NVMe == nil || r.NVMe.AvailableSpare != 100 || r.NVMe.UnsafeShutdowns != 21 || len(r.SelfTests) != 1 {
		t.Errorf("nvme: %+v %+v", r.NVMe, r.SelfTests)
	}
	if lvl, _ := smartProblem(&s); lvl != "" {
		t.Errorf("healthy NVMe flagged %q", lvl)
	}
}

func TestSmartFailing(t *testing.T) {
	r, err := ParseSmart(fixture(t, "smart_failing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if lvl, _ := smartProblem(&r.Summary); lvl != "error" {
		t.Errorf("failed SMART should be an error, got %q", lvl)
	}
	if !r.Attributes[0].Failing || r.Attributes[1].Failing {
		t.Errorf("failing flags: %+v", r.Attributes)
	}
	alerts := computeAlerts([]Pool{{Name: "vault", Health: "DEGRADED"}, {Name: "gone", Health: "FAULTED"}},
		[]Disk{{Name: "sdc", Model: "WDC WD30EFRX-68EUZN0", Smart: &r.Summary}})
	if len(alerts) != 3 || alerts[0].Level != "warning" || alerts[1].Level != "error" || alerts[2].Level != "error" {
		t.Errorf("alerts: %+v", alerts)
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"0B": 0, "512": 512, "1.5K": 1536, "800G": 800 << 30, "3.45T": 3793315115827, "-": 0} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
}
