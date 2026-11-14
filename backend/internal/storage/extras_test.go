package storage

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/store"
)

func TestSupportArgs(t *testing.T) {
	cases := []struct {
		kind string
		devs []string
		want string
	}{
		{"cache", []string{"/dev/a", "/dev/b"}, "zpool add -f tank cache /dev/a /dev/b"},
		{"log", []string{"/dev/a"}, "zpool add -f tank log /dev/a"},
		{"log", []string{"/dev/a", "/dev/b"}, "zpool add -f tank log mirror /dev/a /dev/b"},
		{"special", []string{"/dev/a", "/dev/b"}, "zpool add -f tank special mirror /dev/a /dev/b"},
	}
	for _, c := range cases {
		if got := strings.Join(supportArgs("tank", c.kind, c.devs), " "); got != c.want {
			t.Errorf("%s: got %q want %q", c.kind, got, c.want)
		}
	}
	steps := expandSteps("tank", []string{"/dev/a", "/dev/b"})
	if len(steps) != 3 || strings.Join(steps[0], " ") != "zpool set autoexpand=on tank" || strings.Join(steps[2], " ") != "zpool online -e tank /dev/b" {
		t.Fatalf("expand: %v", steps)
	}
}

func TestHdparmValue(t *testing.T) {
	for min, want := range map[int]int{0: 0, 5: 60, 10: 120, 20: 240, 30: 241, 60: 242, 120: 244, 240: 248} {
		got, err := hdparmValue(min)
		if err != nil || got != want {
			t.Errorf("%d min: got %d, %v; want %d", min, got, err, want)
		}
	}
	for _, bad := range []int{-1, 1, 7, 25, 999} {
		if _, err := hdparmValue(bad); err == nil {
			t.Errorf("%d min accepted", bad)
		}
	}
}

func TestSpindownArgsAndUnit(t *testing.T) {
	ok := SpindownRule{ByID: "/dev/disk/by-id/ata-WDC_WD40EFRX_WD-1", Minutes: 20}
	args, err := spindownArgs("/usr/sbin/hdparm", ok)
	if err != nil || strings.Join(args, " ") != "/usr/sbin/hdparm -S 240 /dev/disk/by-id/ata-WDC_WD40EFRX_WD-1" {
		t.Fatalf("got %v %v", args, err)
	}
	for _, id := range []string{"/dev/sda", "/dev/disk/by-id/../sda", "/dev/disk/by-id/a b", "/dev/disk/by-id/x\nExecStart=/bin/sh", "/dev/disk/by-id/-x"} {
		if _, err := spindownArgs("hdparm", SpindownRule{ByID: id, Minutes: 5}); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
	unit, err := spindownUnit("/usr/sbin/hdparm", []SpindownRule{ok, {ByID: "/dev/disk/by-id/ata-B", Minutes: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unit, "ExecStart=-/usr/sbin/hdparm -S 240 /dev/disk/by-id/ata-WDC_WD40EFRX_WD-1\n") || strings.Contains(unit, "ata-B") {
		t.Fatalf("unit:\n%s", unit)
	}
	if _, err := spindownUnit("hdparm", []SpindownRule{{ByID: "/dev/disk/by-id/x;rm", Minutes: 5}}); err == nil {
		t.Fatal("bad id in unit accepted")
	}
}

func TestParseARC(t *testing.T) {
	a := ParseARC("13 1 0x01 123 33456 1\nname type data\nhits 4 900\nmisses 4 100\nsize 4 2048\nc 4 4096\nc_max 4 8192\nl2_size 4 77\n")
	if a.Hits != 900 || a.Misses != 100 || a.Size != 2048 || a.Target != 4096 || a.Max != 8192 || a.L2Size != 77 || a.HitRate != 90 {
		t.Fatalf("%+v", a)
	}
}

func TestParseCrypt(t *testing.T) {
	out := "tank\tencryption\toff\ntank\tkeystatus\t-\ntank\tencryptionroot\t-\ntank\tmounted\tyes\n" +
		"tank/vault\tencryption\taes-256-gcm\ntank/vault\tkeystatus\tunavailable\ntank/vault\tencryptionroot\ttank/vault\ntank/vault\tmounted\tno\n" +
		"tank/vault/in\tencryption\taes-256-gcm\ntank/vault/in\tkeystatus\tavailable\ntank/vault/in\tencryptionroot\ttank/vault\ntank/vault/in\tmounted\tyes\n"
	got := ParseCrypt(out)
	want := []CryptInfo{
		{Name: "tank", Unlocked: true, Mounted: true},
		{Name: "tank/vault", Encrypted: true, Root: "tank/vault"},
		{Name: "tank/vault/in", Encrypted: true, Root: "tank/vault", Unlocked: true, Mounted: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestValidPassphrase(t *testing.T) {
	for _, p := range []string{"short", "", strings.Repeat("x", 513), "good pass\nword", "nul\x00passphrase"} {
		if validPassphrase(p) == nil {
			t.Errorf("accepted %q", p)
		}
	}
	if err := validPassphrase("correct horse battery"); err != nil {
		t.Fatal(err)
	}
}

func TestPartitions(t *testing.T) {
	if partPath("/dev/sdb", 2) != "/dev/sdb2" || partPath("/dev/nvme0n1", 3) != "/dev/nvme0n1p3" {
		t.Fatal("partPath")
	}
	for _, c := range []struct {
		part, disk string
		want       int
	}{{"sda3", "sda", 3}, {"nvme0n1p2", "nvme0n1", 2}, {"sda", "sda", 0}, {"sdb1", "sda", 0}, {"sda1x", "sda", 0}} {
		if got := partNumber(c.part, c.disk); got != c.want {
			t.Errorf("%s/%s: %d", c.part, c.disk, got)
		}
	}
}

func TestBootMirrorSteps(t *testing.T) {
	p := BootMirrorPlan{Mode: "attach", Source: "/dev/sda", Target: "/dev/sdb", TargetByID: "/dev/disk/by-id/ata-NEW",
		Members: []BootPoolMember{{Pool: "bpool", Part: 3, Old: "/dev/disk/by-id/ata-OLD-part3"}, {Pool: "rpool", Part: 4, Old: "/dev/disk/by-id/ata-OLD-part4"}},
		ESPPart: 2, EFI: true, Arch: "amd64"}
	var got []string
	for _, s := range p.Steps() {
		got = append(got, strings.Join(s, " "))
	}
	want := []string{
		"sgdisk --replicate=/dev/sdb /dev/sda",
		"sgdisk --randomize-guids /dev/sdb",
		"udevadm settle",
		"zpool attach -f bpool /dev/disk/by-id/ata-OLD-part3 /dev/disk/by-id/ata-NEW-part3",
		"zpool attach -f rpool /dev/disk/by-id/ata-OLD-part4 /dev/disk/by-id/ata-NEW-part4",
		"mkfs.vfat -F 32 -n NOCAPEFI2 /dev/sdb2",
		"mkdir -p /boot/efi2",
		"mount /dev/sdb2 /boot/efi2",
		"grub-install --target=x86_64-efi --efi-directory=/boot/efi2 --bootloader-id=nocapos-mirror --recheck",
		"grub-install --target=x86_64-efi --efi-directory=/boot/efi2 --removable --no-nvram --recheck",
		"umount /boot/efi2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
	p.Mode, p.EFI, p.TargetByID, p.Members = "replace", false, "", []BootPoolMember{{Pool: "rpool", Part: 1, Old: "1234567890"}}
	steps := p.Steps()
	if strings.Join(steps[3], " ") != "zpool replace -f rpool 1234567890 /dev/sdb1" || strings.Join(steps[len(steps)-1], " ") != "grub-install --target=i386-pc --recheck /dev/sdb" {
		t.Fatalf("replace/bios: %v", steps)
	}
	p.EFI, p.Arch, p.ESPPart = true, "arm64", 1
	if !strings.Contains(strings.Join(p.Steps()[7], " "), "arm64-efi") {
		t.Fatal("arm64 target")
	}
}

func bootScan(rootDisk string, pools []Pool) *scan {
	f := &Facts{Links: map[string]string{
		"/dev/disk/by-id/ata-OLD":       "sda",
		"/dev/disk/by-id/ata-OLD-part2": "sda2",
		"/dev/disk/by-id/ata-OLD-part3": "sda3",
	}, Devices: []BlockDev{
		{Name: rootDisk, Type: "disk", Children: []BlockDev{{Name: partPath(rootDisk, 2), Type: "part"}, {Name: partPath(rootDisk, 3), Type: "part"}}},
		{Name: "sdb", Type: "disk"},
		{Name: "sdc", Type: "disk"},
	}}
	return &scan{facts: f, pools: pools, disks: []Disk{
		{Name: rootDisk, Path: "/dev/" + rootDisk, Size: 500 << 30, Usage: UsageSystem},
		{Name: "sdb", Path: "/dev/sdb", ByID: "/dev/disk/by-id/ata-NEW", Size: 1000 << 30, Available: true},
		{Name: "sdc", Path: "/dev/sdc", Size: 250 << 30, Available: true},
	}}
}

func rootPool(health string, kids ...Vdev) Pool {
	layout := "stripe"
	vdevs := kids
	if len(kids) > 1 {
		layout = "mirror"
		vdevs = []Vdev{{Name: "mirror-0", Type: "mirror", Health: health, Children: kids}}
	}
	return Pool{Name: "rpool", Health: health, Layout: layout, Vdevs: vdevs}
}

func TestBootFrom(t *testing.T) {
	ext4 := bootFrom(&BootFacts{Source: "/dev/sda2", FSType: "ext4", EFI: true, Arch: "amd64"}, bootScan("sda", nil))
	if ext4.CanMirror || ext4.Why != "not-zfs" || len(ext4.Disks) != 1 || ext4.Disks[0].Name != "sda" {
		t.Fatalf("ext4: %+v", ext4)
	}
	pi := bootFrom(&BootFacts{Source: "/dev/mmcblk0p2", FSType: "ext4", Arch: "arm64", Model: "Raspberry Pi 5 Model B Rev 1.0"}, bootScan("mmcblk0", nil))
	if pi.CanMirror || pi.Why != "pi" || pi.Platform != "pi" {
		t.Fatalf("pi: %+v", pi)
	}
	sd := bootFrom(&BootFacts{Source: "/dev/mmcblk0p2", FSType: "ext4", Arch: "arm64", EFI: true}, bootScan("mmcblk0", nil))
	if sd.Why != "sd" {
		t.Fatalf("sd: %+v", sd)
	}

	leaf := Vdev{Name: "/dev/disk/by-id/ata-OLD-part3", Path: "/dev/disk/by-id/ata-OLD-part3", Type: "disk", Health: "ONLINE", Disk: "sda"}
	single := bootFrom(&BootFacts{Source: "rpool/ROOT/debian", FSType: "zfs", EFI: true, ESP: "/dev/sda2", Arch: "amd64"},
		bootScan("sda", []Pool{rootPool("ONLINE", leaf)}))
	if !single.CanMirror || single.Mode != "attach" || single.espPart != 2 || !reflect.DeepEqual(single.Candidates, []string{"sdb"}) {
		t.Fatalf("single: %+v", single)
	}
	if !reflect.DeepEqual(single.Members, []BootPoolMember{{Pool: "rpool", Part: 3, Old: "/dev/disk/by-id/ata-OLD-part3"}}) {
		t.Fatalf("members: %+v", single.Members)
	}

	other := Vdev{Name: "/dev/disk/by-id/ata-OLD2-part3", Path: "/dev/disk/by-id/ata-OLD2-part3", Type: "disk", Health: "ONLINE", Disk: "sdz"}
	mirrored := bootFrom(&BootFacts{Source: "rpool/ROOT/debian", FSType: "zfs", EFI: true, Arch: "amd64"}, bootScan("sda", []Pool{rootPool("ONLINE", leaf, other)}))
	if mirrored.CanMirror || mirrored.Why != "already" || !mirrored.Mirrored {
		t.Fatalf("mirrored: %+v", mirrored)
	}

	gone := Vdev{Name: "9876543210", GUID: "9876543210", Type: "disk", Health: "UNAVAIL", Was: "/dev/disk/by-id/ata-DEAD-part3"}
	deg := bootFrom(&BootFacts{Source: "rpool/ROOT/debian", FSType: "zfs", Arch: "amd64"}, bootScan("sda", []Pool{rootPool("DEGRADED", leaf, gone)}))
	if !deg.CanMirror || deg.Mode != "replace" || !deg.Degraded || deg.Members[0].Old != "9876543210" || deg.Members[0].Part != 3 {
		t.Fatalf("degraded: %+v", deg)
	}

	armBIOS := bootFrom(&BootFacts{Source: "rpool/ROOT/debian", FSType: "zfs", Arch: "arm64"}, bootScan("sda", []Pool{rootPool("ONLINE", leaf)}))
	if armBIOS.Why != "arch" {
		t.Fatalf("arm without UEFI: %+v", armBIOS)
	}

	// A boot pool striped over two disks: only one would get a twin, so
	// it isn't offered (it would then say "mirrored" while half isn't).
	second := Vdev{Name: "/dev/sdc3", Path: "/dev/sdc3", Type: "disk", Health: "ONLINE", Disk: "sdc"}
	wide := Pool{Name: "rpool", Health: "ONLINE", Layout: "stripe", Vdevs: []Vdev{leaf, second}}
	striped := bootFrom(&BootFacts{Source: "rpool/ROOT/debian", FSType: "zfs", EFI: true, Arch: "amd64"}, bootScan("sda", []Pool{wide}))
	if striped.CanMirror || striped.Why != "unknown" {
		t.Fatalf("two-disk stripe: %+v", striped)
	}
}

func TestSnapLabelDots(t *testing.T) {
	for _, s := range []string{".", ".."} {
		if validSnapLabel(s) == nil {
			t.Errorf("snapshot label %q accepted", s)
		}
	}
	if validSnapLabel("before.upgrade") != nil {
		t.Error("a dot inside a name is fine")
	}
}

func TestSnapPlan(t *testing.T) {
	now := time.Date(2026, 11, 13, 14, 25, 0, 0, time.UTC) // a Friday
	if got := periodStart("weekly", now); !got.Equal(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("week start %v", got)
	}
	if got := periodStart("monthly", now); !got.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("month start %v", got)
	}
	snap := func(label string, at time.Time) Snapshot { return Snapshot{Name: "tank/media@" + label, Created: at} }
	existing := []Snapshot{
		snap("auto-hourly-20261113-1100", now.Add(-3*time.Hour)),
		snap("auto-hourly-20261113-1200", now.Add(-2*time.Hour)),
		snap("auto-hourly-20261113-1300", now.Add(-time.Hour)),
		snap("auto-daily-20261113-0000", now.Add(-14*time.Hour)),
		snap("before-upgrade", now.Add(-100*time.Hour)),
		snap("auto-hourly-handmade", now.Add(-200*time.Hour)),
	}
	take, prune := snapPlan(SnapSchedule{Dataset: "tank/media", Hourly: 2, Daily: 7}, existing, now)
	if !reflect.DeepEqual(take, []string{"tank/media@auto-hourly-20261113-1425"}) {
		t.Fatalf("take %v", take)
	}
	if !reflect.DeepEqual(prune, []string{"tank/media@auto-hourly-20261113-1100", "tank/media@auto-hourly-20261113-1200"}) {
		t.Fatalf("prune %v", prune)
	}
	// Already taken this hour: nothing new; monthly off: nothing pruned there.
	existing = append(existing, snap("auto-hourly-20261113-1400", now.Add(-20*time.Minute)), snap("auto-monthly-20260101-0000", now.Add(-5000*time.Hour)))
	take, _ = snapPlan(SnapSchedule{Dataset: "tank/media", Hourly: 5}, existing, now)
	if len(take) != 0 {
		t.Fatalf("take %v", take)
	}
	for _, n := range append(take, prune...) {
		if ValidSnapshotName(n) != nil {
			t.Fatalf("invalid name %s", n)
		}
	}
}

func TestSnapDir(t *testing.T) {
	d, err := snapDir("/srv/nocapos/pools/tank/media", "s1", "Albums/2026")
	if err != nil || d != "/srv/nocapos/pools/tank/media/.zfs/snapshot/s1/Albums/2026" {
		t.Fatalf("%q %v", d, err)
	}
	if d, _ := snapDir("/m", "s1", ""); d != "/m/.zfs/snapshot/s1" {
		t.Fatal(d)
	}
	for _, rel := range []string{"../../etc", "a/../../..", "x\x00y"} {
		if _, err := snapDir("/m", "s1", rel); err == nil {
			t.Errorf("accepted %q", rel)
		}
	}
	if _, err := snapDir("legacy", "s1", ""); err == nil {
		t.Fatal("unmounted accepted")
	}
}

func TestPlanFlex(t *testing.T) {
	d := func(n string, gb int64) Disk { return Disk{Name: n, Size: gb << 30} }
	p, err := planFlex([]Disk{d("sda", 4000), d("sdb", 2000), d("sdc", 8000)}, []Disk{d("sdd", 8000)})
	if err != nil || p.Usable != 14000<<30 || p.Tolerance != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := planFlex([]Disk{d("sda", 8000)}, []Disk{d("sdb", 4000)}); err == nil {
		t.Fatal("small parity accepted")
	}
	if _, err := planFlex([]Disk{d("sda", 1)}, nil); err == nil {
		t.Fatal("no parity accepted")
	}
	line := flexUnionLine(2)
	if !strings.HasPrefix(line, "/srv/nocapos/array/d1:/srv/nocapos/array/d2 /srv/nocapos/array/all fuse.mergerfs ") || !isOurs(line) {
		t.Fatal(line)
	}
	conf := flexParityConf(2, 2)
	for _, want := range []string{"parity /srv/nocapos/array/parity1/array.parity\n", "2-parity /srv/nocapos/array/parity2/array.parity\n",
		"data d2 /srv/nocapos/array/d2/\n", "content /srv/nocapos/array/d1/.array.content\n"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("missing %q in\n%s", want, conf)
		}
	}
	if _, err := fstabSetLine("/dev/x /srv/nocapos/array/all ext4 defaults 0 0\n", line, FlexMount); err == nil {
		t.Fatal("foreign fstab line replaced")
	}
}

func TestParseDFAndLayouts(t *testing.T) {
	s, u, a := parseDF("     1B-blocks        Used       Avail\n  100 40 60\n")
	if s != 100 || u != 40 || a != 60 {
		t.Fatal(s, u, a)
	}
	if l, err := normLayout("single", 1); err != nil || l != "stripe" {
		t.Fatal(l, err)
	}
	if _, err := normLayout("single", 2); err == nil {
		t.Fatal("single with 2 disks")
	}
	if layoutTolerance("mirror", 3) != 2 || layoutTolerance("raidz2", 6) != 2 || layoutTolerance("stripe", 4) != 0 {
		t.Fatal("tolerance")
	}
	p := &Pool{Layout: "mirror"}
	if _, err := checkSupport(p, "special", []Disk{{Name: "a"}}); err == nil {
		t.Fatal("single special on mirror accepted")
	}
	if _, err := checkSupport(p, "dedup", []Disk{{Name: "a"}}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	w, err := checkSupport(p, "cache", []Disk{{Name: "a", Rotational: true}})
	if err != nil || !reflect.DeepEqual(w, []string{"not-ssd"}) {
		t.Fatal(w, err)
	}
}

// ---- Manager over the demo machine ----

func newDemoManager(t *testing.T) (*Manager, *Demo) {
	t.Helper()
	d := NewDemo(t.TempDir())
	d.delay = 0
	st := &memStore{locs: map[string]store.StorageLocation{}, set: map[string]string{}}
	m := NewManager(Options{Backend: d, Store: st, DataDir: "/var/lib/nocapos/data", Demo: true,
		Native: func() bool { return false }, CanInstall: func() bool { return true }, PkgManager: func() string { return "apt" }})
	return m, d
}

func TestDemoEncryptedFolders(t *testing.T) {
	m, _ := newDemoManager(t)
	ctx := context.Background()
	req := EncryptedRequest{Name: "tank/vault", Passphrase: "correct horse", Repeat: "correct horse", AutoLock: 30}
	if err := m.CreateEncrypted(ctx, EncryptedRequest{Name: "tank/vault", Passphrase: "correct horse", Repeat: "wrong horse!"}); err == nil {
		t.Fatal("mismatch accepted")
	}
	if err := m.CreateEncrypted(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := m.Lock(ctx, "tank/vault"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, m.Unlock(ctx, "tank/vault", "not the passphrase"), http.StatusBadRequest, "isn't right")
	if err := m.Unlock(ctx, "tank/vault", "correct horse"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, m.Lock(ctx, "tank/media"), http.StatusBadRequest, "isn't encrypted")
	list, err := m.Encryption(ctx, "tank")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range list {
		if c.Name == "tank/vault" {
			found = c.Encrypted && c.Unlocked && c.AutoLock == 30 && c.UnlockedAt != ""
		}
	}
	if !found {
		t.Fatalf("%+v", list)
	}
	// Auto-lock after the limit.
	m.autoLock(ctx, time.Now().Add(31*time.Minute))
	list, _ = m.Encryption(ctx, "tank")
	for _, c := range list {
		if c.Name == "tank/vault" && c.Unlocked {
			t.Fatal("not auto-locked")
		}
	}
	// The passphrase is never kept in settings.
	for k, v := range m.st.(*memStore).set {
		if strings.Contains(v, "correct horse") {
			t.Fatalf("passphrase stored under %s", k)
		}
	}
}

func TestDemoSnapshotsAndClone(t *testing.T) {
	m, _ := newDemoManager(t)
	ctx := context.Background()
	wantStatus(t, m.Clone(ctx, "tank/media@weekly-2026-10-04", "other/copy"), http.StatusBadRequest, "same pool")
	if err := m.Clone(ctx, "tank/media@weekly-2026-10-04", "tank/media-copy"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetSnapSchedule(ctx, SnapSchedule{Dataset: "tank/media", Hourly: 24, Daily: 7}); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, m.SetSnapSchedule(ctx, SnapSchedule{Dataset: "tank/nope", Daily: 1}), http.StatusNotFound, "no dataset")
	m.runSchedules(ctx, time.Now())
	snaps, _ := m.Snapshots(ctx, "tank/media")
	auto := 0
	for _, s := range snaps {
		if strings.Contains(s.Name, "@auto-") {
			auto++
		}
	}
	if auto != 2 {
		t.Fatalf("want an hourly and a daily snapshot, got %d in %+v", auto, snaps)
	}
	ov, err := m.SnapshotOverview(ctx)
	if err != nil || len(ov) == 0 {
		t.Fatal(ov, err)
	}
	entries, err := m.BrowseSnapshot(ctx, "tank/media@weekly-2026-10-04", "")
	if err != nil || len(entries) == 0 {
		t.Fatal(entries, err)
	}
	if _, err := m.BrowseSnapshot(ctx, "tank/media@weekly-2026-10-04", "../.."); err == nil {
		t.Fatal("escape accepted")
	}
}

func TestDemoPlansNeverTouchSystemDisk(t *testing.T) {
	m, _ := newDemoManager(t)
	ctx := context.Background()
	if _, err := m.PlanPool(ctx, PlanRequest{Action: "create", Name: "fast", Layout: "mirror", Disks: []string{"nvme0n1", "sdc"}}); err == nil {
		t.Fatal("system disk accepted")
	}
	p, err := m.PlanPool(ctx, PlanRequest{Action: "create", Name: "fast", Layout: "mirror", Disks: []string{"sdc", "sdd"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Tolerance != 1 || p.Usable != tb || p.Confirm != "ERASE" || p.Commands[0][0] != "zpool" {
		t.Fatalf("%+v", p)
	}
	sp, err := m.PlanPool(ctx, PlanRequest{Action: "support", Pool: "tank", Kind: "cache", Disks: []string{"sdc"}})
	if err != nil || strings.Join(sp.Commands[0][:5], " ") != "zpool add -f tank cache" {
		t.Fatal(sp, err)
	}
	j, err := m.AddSupport(ctx, "tank", SupportRequest{Kind: "cache", Disks: []string{"sdc"}, Confirm: "ERASE"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitJob(t, m, j); got.State != "done" {
		t.Fatal(got)
	}
	pools, _ := m.Pools(ctx)
	var cache *Vdev
	for i, v := range pools.Pools[0].Vdevs {
		if v.Type == "cache" {
			cache = &pools.Pools[0].Vdevs[i]
		}
	}
	if cache == nil || len(cache.Children) != 1 {
		t.Fatalf("%+v", pools.Pools[0].Vdevs)
	}
	if err := m.RemoveSupport(ctx, "tank", cache.Children[0].Path); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, m.RemoveSupport(ctx, "tank", "/dev/disk/by-id/whatever"), http.StatusBadRequest, "isn't a cache")
	if a, err := m.ARC(ctx); err != nil || a.HitRate <= 0 {
		t.Fatal(a, err)
	}
	b, err := m.Boot(ctx)
	if err != nil || b.Why != "not-zfs" || b.Disks[0].Name != "nvme0n1" {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err := m.PlanBootMirror(ctx, BootMirrorRequest{Disk: "sdc"}); err == nil {
		t.Fatal("ext4 boot mirror accepted")
	}
}

func TestDemoSpindownAndFlex(t *testing.T) {
	m, d := newDemoManager(t)
	ctx := context.Background()
	if err := m.SetSpindown(ctx, "sdc", 20); err == nil {
		t.Fatal("SSD spin-down accepted")
	}
	if err := m.SetSpindown(ctx, "sda", 7); err == nil {
		t.Fatal("odd minutes accepted")
	}
	if err := m.SetSpindown(ctx, "sda", 20); err != nil {
		t.Fatal(err)
	}
	if d.ex.spindown[byIDOf(d.disk("sda"))] != 20 {
		t.Fatalf("%v", d.ex.spindown)
	}
	plan, err := m.PlanFlex(ctx, FlexRequest{Data: []string{"sda", "sdb"}, Parity: []string{"sdc"}})
	if err != nil || !plan.Enabled || plan.Tolerance != 1 {
		t.Fatal(plan, err)
	}
	wantStatus(t, func() error {
		_, err := m.CreateFlex(ctx, FlexRequest{Data: []string{"sda"}, Parity: []string{"sdc"}}, nil)
		return err
	}(), http.StatusBadRequest, "ERASE")
}

func TestFlexOffOnRealServers(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	t.Setenv(flexEnv, "")
	_, err := m.CreateFlex(context.Background(), FlexRequest{Data: []string{"sdb"}, Parity: []string{"sdc"}, Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "preview")
}

// ---- Host commands through the fake runner ----

func TestHostEncryptionPassesPassphraseOnStdin(t *testing.T) {
	_, fr, h, _ := newTestManager(t)
	ctx := context.Background()
	q := int64(1 << 30)
	if err := h.CreateEncrypted(ctx, "tank/vault", "correct horse", &q); err != nil {
		t.Fatal(err)
	}
	key := "zfs create -o encryption=aes-256-gcm -o keyformat=passphrase -o keylocation=prompt -o quota=1073741824 tank/vault"
	if fr.stdin[key] != "correct horse\n" {
		t.Fatalf("stdin %q", fr.stdin)
	}
	for _, c := range fr.calls {
		if strings.Contains(strings.Join(c, " "), "correct horse") {
			t.Fatal("passphrase in argv")
		}
	}
	fr.errs["zfs load-key tank/vault"] = errors.New("Key load error: Incorrect key provided for 'tank/vault'.")
	wantStatus(t, h.Unlock(ctx, "tank/vault", "nope nope nope"), http.StatusBadRequest, "isn't right")
	if err := h.Lock(ctx, "tank/vault"); err != nil {
		t.Fatal(err)
	}
	if len(fr.called("zfs unmount tank/vault")) != 1 || len(fr.called("zfs unload-key tank/vault")) != 1 {
		t.Fatal(fr.calls)
	}
}

func TestHostSpindownWritesUnit(t *testing.T) {
	_, fr, h, _ := newTestManager(t)
	h.UnitDir = t.TempDir()
	rule := SpindownRule{ByID: "/dev/disk/by-id/ata-HDD_1", Minutes: 30}
	if err := h.ApplySpindown(context.Background(), []SpindownRule{rule}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(h.UnitDir, spindownUnitName))
	if err != nil || !strings.Contains(string(b), "ExecStart=-/usr/sbin/hdparm -S 241 /dev/disk/by-id/ata-HDD_1") {
		t.Fatalf("%s %v", b, err)
	}
	if len(fr.called("/usr/sbin/hdparm -S 241 /dev/disk/by-id/ata-HDD_1")) != 1 || len(fr.called("systemctl enable "+spindownUnitName)) != 1 {
		t.Fatal(fr.calls)
	}
	rule.Minutes = 0
	if err := h.ApplySpindown(context.Background(), []SpindownRule{rule}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.UnitDir, spindownUnitName)); !os.IsNotExist(err) {
		t.Fatal("unit kept with no rules")
	}
}

func TestHostMirrorBootUnmountsOnFailure(t *testing.T) {
	_, fr, h, _ := newTestManager(t)
	p := BootMirrorPlan{Mode: "attach", Source: "/dev/sda", Target: "/dev/sdb", Members: []BootPoolMember{{Pool: "rpool", Part: 3, Old: "/dev/sda3"}},
		ESPPart: 2, EFI: true, Arch: "amd64"}
	fr.errs["grub-install --target=x86_64-efi --efi-directory=/boot/efi2 --bootloader-id=nocapos-mirror --recheck"] = errors.New("boom")
	if err := h.MirrorBoot(context.Background(), p); err == nil || !strings.Contains(err.Error(), "grub-install") {
		t.Fatal(err)
	}
	if len(fr.called("umount /boot/efi2")) != 1 || len(fr.called("zpool attach -f rpool /dev/sda3 /dev/sdb3")) != 1 {
		t.Fatal(fr.calls)
	}
}

func TestHostBrowseStaysInsideSnapshot(t *testing.T) {
	_, _, h, _ := newTestManager(t)
	mp := t.TempDir()
	snap := filepath.Join(mp, ".zfs", "snapshot", "s1")
	if err := os.MkdirAll(filepath.Join(snap, "Albums"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snap, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.ToSlash(mp) + "/.zfs/snapshot/s1"
	list, err := h.BrowseSnapshot(context.Background(), dir)
	if err != nil || len(list) != 2 || !list[0].Dir || list[1].Size != 2 {
		t.Fatalf("%+v %v", list, err)
	}
	if _, err := h.BrowseSnapshot(context.Background(), dir+"/missing"); err == nil {
		t.Fatal("missing folder")
	}
}
