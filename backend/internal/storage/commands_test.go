package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCommandArgs(t *testing.T) {
	cases := []struct {
		got, want []string
	}{
		{createPoolArgs("media", "mirror", []string{"/dev/disk/by-id/ata-A", "/dev/disk/by-id/ata-B"}, "lz4", "/srv/nocapos/pools/media"),
			[]string{"zpool", "create", "-f", "-o", "ashift=12", "-O", "compression=lz4", "-O", "atime=off", "-O", "xattr=sa", "-O", "acltype=posixacl",
				"-m", "/srv/nocapos/pools/media", "media", "mirror", "/dev/disk/by-id/ata-A", "/dev/disk/by-id/ata-B"}},
		{createPoolArgs("scratch", "stripe", []string{"/dev/sdg"}, "off", "/srv/nocapos/pools/scratch"),
			[]string{"zpool", "create", "-f", "-o", "ashift=12", "-O", "compression=off", "-O", "atime=off", "-O", "xattr=sa", "-O", "acltype=posixacl",
				"-m", "/srv/nocapos/pools/scratch", "scratch", "/dev/sdg"}},
		{createPoolArgs("big", "raidz2", []string{"/dev/a", "/dev/b", "/dev/c", "/dev/d"}, "zstd", "/srv/nocapos/pools/big")[15:],
			[]string{"big", "raidz2", "/dev/a", "/dev/b", "/dev/c", "/dev/d"}},
		{addVdevArgs("tank", "mirror", []string{"/dev/disk/by-id/ata-C", "/dev/disk/by-id/ata-D"}),
			[]string{"zpool", "add", "-f", "tank", "mirror", "/dev/disk/by-id/ata-C", "/dev/disk/by-id/ata-D"}},
		{replaceArgs("tank", "7965954862312465432", "/dev/disk/by-id/ata-E"),
			[]string{"zpool", "replace", "-f", "tank", "7965954862312465432", "/dev/disk/by-id/ata-E"}},
		{destroyArgs("tank"), []string{"zpool", "destroy", "tank"}},
		{exportArgs("tank"), []string{"zpool", "export", "tank"}},
		{importArgs("archive"), []string{"zpool", "import", "-d", "/dev/disk/by-id", "archive"}},
		{scrubArgs("tank", false), []string{"zpool", "scrub", "tank"}},
		{scrubArgs("tank", true), []string{"zpool", "scrub", "-s", "tank"}},
		{datasetCreateArgs("tank/media", i64(1<<40), "zstd"), []string{"zfs", "create", "-o", "quota=1099511627776", "-o", "compression=zstd", "tank/media"}},
		{datasetCreateArgs("tank/media", nil, ""), []string{"zfs", "create", "tank/media"}},
		{datasetDestroyArgs("tank/media"), []string{"zfs", "destroy", "-r", "tank/media"}},
		{snapshotArgs("tank/media@x"), []string{"zfs", "snapshot", "tank/media@x"}},
		{rollbackArgs("tank/media@x"), []string{"zfs", "rollback", "-r", "tank/media@x"}},
		{snapshotDestroyArgs("tank/media@x"), []string{"zfs", "destroy", "tank/media@x"}},
	}
	for i, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("case %d:\n got %q\nwant %q", i, c.got, c.want)
		}
	}
	upd := datasetUpdateArgs("tank/media", i64(0), "inherit")
	if !reflect.DeepEqual(upd, [][]string{{"zfs", "set", "quota=none", "tank/media"}, {"zfs", "inherit", "compression", "tank/media"}}) {
		t.Errorf("update: %q", upd)
	}
	steps := formatSteps("/dev/nvme1n1", "media")
	if steps[4][len(steps[4])-1] != "/dev/nvme1n1p1" || steps[0][2] != "/dev/nvme1n1" {
		t.Errorf("format steps: %q", steps)
	}
	if partitionPath("/dev/sdb") != "/dev/sdb1" || partitionPath("/dev/mmcblk0") != "/dev/mmcblk0p1" {
		t.Error("partition paths")
	}
}

func TestNameValidation(t *testing.T) {
	for _, ok := range []string{"tank", "Media_2", "a", "pool.one", "my-pool:1"} {
		if err := ValidPoolName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "1tank", "-f", "mirror", "raidz", "RAIDZ2", "raidz9", "draid1", "spare", "log", "cache", "special",
		"c0t0d0", "c1", "tank/x", "tank pool", "tank;rm", strings.Repeat("a", 51), "/dev/sda", "tänk"} {
		if err := ValidPoolName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"tank", "tank/media", "tank/a/b/c/d/e", "tank/x_1.2:3-4"} {
		if err := ValidDatasetName(ok); err != nil {
			t.Errorf("dataset %q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"tank/a/b/c/d/e/f", "tank/", "tank//x", "tank/../etc", "tank/a b", "-r", "tank/x@snap", "/tank"} {
		if err := ValidDatasetName(bad); err == nil {
			t.Errorf("dataset %q accepted", bad)
		}
	}
	if ValidChildDataset("tank") == nil {
		t.Error("pool root accepted as a child dataset")
	}
	for _, bad := range []string{"tank/media", "tank/media@", "tank/media@a@b", "@x", "tank/media@a b", "tank@x/y"} {
		if err := ValidSnapshotName(bad); err == nil {
			t.Errorf("snapshot %q accepted", bad)
		}
	}
	if ValidSnapshotName("tank/media@weekly-2026.10.04") != nil {
		t.Error("good snapshot rejected")
	}
	for _, bad := range []string{"", "-media", "my disk", "a/b", strings.Repeat("x", 17), "../etc"} {
		if ValidLabel(bad) == nil {
			t.Errorf("label %q accepted", bad)
		}
	}
	for _, c := range []struct {
		layout string
		n      int
		ok     bool
	}{{"stripe", 1, true}, {"mirror", 1, false}, {"mirror", 2, true}, {"raidz1", 2, false}, {"raidz1", 3, true},
		{"raidz2", 3, false}, {"raidz2", 4, true}, {"raidz3", 4, false}, {"raidz3", 5, true}, {"raid5", 9, false}} {
		if err := validLayout(c.layout, c.n); (err == nil) != c.ok {
			t.Errorf("layout %s with %d disks: %v", c.layout, c.n, err)
		}
	}
	if mp, err := poolMountpoint("tank", ""); err != nil || mp != "/srv/nocapos/pools/tank" {
		t.Errorf("default mountpoint %q %v", mp, err)
	}
	for _, bad := range []string{"/srv/nocapos/pools", "/mnt/tank", "/srv/nocapos/pools/../../etc", "/srv/nocapos/poolsX/a", "/srv/nocapos/pools/a b"} {
		if _, err := poolMountpoint("tank", bad); err == nil {
			t.Errorf("mountpoint %q accepted", bad)
		}
	}
}

func TestFstab(t *testing.T) {
	orig := "# /etc/fstab: static file system information.\nUUID=9d8c7b6a / ext4 errors=remount-ro 0 1\n/swapfile none swap sw 0 0\n"
	one, err := fstabSet(orig, "2b3c4d5e-6f70", "/srv/nocapos/disks/media")
	if err != nil {
		t.Fatal(err)
	}
	want := orig + "UUID=2b3c4d5e-6f70 /srv/nocapos/disks/media ext4 defaults,nofail,x-systemd.device-timeout=10s 0 2 # nocapos\n"
	if one != want {
		t.Errorf("add:\n%s\nwant:\n%s", one, want)
	}
	again, _ := fstabSet(one, "2b3c4d5e-6f70", "/srv/nocapos/disks/media")
	if again != one {
		t.Errorf("not idempotent:\n%s", again)
	}
	replaced, _ := fstabSet(one, "ffff-0000", "/srv/nocapos/disks/media")
	if strings.Count(replaced, "/srv/nocapos/disks/media") != 1 || !strings.Contains(replaced, "UUID=ffff-0000") || !strings.HasPrefix(replaced, orig) {
		t.Errorf("replace:\n%s", replaced)
	}
	if _, err := fstabSet(orig+"/dev/sdz1 /srv/nocapos/disks/media ext4 defaults 0 2\n", "x", "/srv/nocapos/disks/media"); err == nil {
		t.Error("overwrote someone else's fstab line")
	}
	if got := fstabRemove(one, "/srv/nocapos/disks/media"); got != orig {
		t.Errorf("remove:\n%s", got)
	}
	if got := fstabRemove(orig, "/"); got != orig {
		t.Errorf("removed a line we didn't write:\n%s", got)
	}
	noNL, _ := fstabSet("UUID=a / ext4 defaults 0 1", "b", "/srv/nocapos/disks/x")
	if !strings.HasPrefix(noNL, "UUID=a / ext4 defaults 0 1\nUUID=b ") {
		t.Errorf("missing newline handling:\n%s", noNL)
	}

	p := filepath.Join(t.TempDir(), "fstab")
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte(one), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != one {
		t.Errorf("atomic write: %s", b)
	}
	if entries, _ := os.ReadDir(filepath.Dir(p)); len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}
