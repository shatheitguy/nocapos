package storage

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"alfaos/alfad/internal/store"
)

// fakeRunner answers commands from a table and records every call.
type fakeRunner struct {
	mu    sync.Mutex
	out   map[string]string
	errs  map[string]error
	calls [][]string
	stdin map[string]string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f.RunInput(ctx, nil, name, args...)
}

func (f *fakeRunner) RunInput(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	argv := append([]string{name}, args...)
	key := strings.Join(argv, " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, argv)
	if stdin != nil {
		f.stdin[key] = string(stdin)
	}
	return []byte(f.out[key]), f.errs[key]
}

func (f *fakeRunner) called(prefix string) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if strings.HasPrefix(strings.Join(c, " "), prefix) {
			out = append(out, c)
		}
	}
	return out
}

type memStore struct {
	mu   sync.Mutex
	locs map[string]store.StorageLocation
	set  map[string]string
}

func (s *memStore) StorageLocations(context.Context) ([]store.StorageLocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.StorageLocation
	for _, l := range s.locs {
		out = append(out, l)
	}
	return out, nil
}
func (s *memStore) SaveStorageLocation(_ context.Context, l store.StorageLocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locs[l.ID] = l
	return nil
}
func (s *memStore) DeleteStorageLocation(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.locs[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.locs, id)
	return nil
}
func (s *memStore) Setting(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set[k], nil
}
func (s *memStore) SetSetting(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.set[k] = v
	return nil
}

const (
	zpoolList   = "zpool list -Hp -o name,size,allocated,free,fragmentation,capacity,health"
	zfsRoots    = "zfs list -Hp -d 0 -o name,mountpoint"
	tankDS      = "zfs list -Hp -r -t filesystem -o name,used,avail,refer,mountpoint,compression,compressratio,quota tank"
	sdgByID     = "/dev/disk/by-id/ata-WDC_WD40EFRX-68N32N0_WD-WCC7K1234567"
	findDataSrc = "findmnt -n -o SOURCE --target /var/lib/nocapos/data"
)

func newTestManager(t *testing.T) (*Manager, *fakeRunner, *Host, *memStore) {
	t.Helper()
	fr := &fakeRunner{out: map[string]string{}, errs: map[string]error{}, stdin: map[string]string{}}
	fr.out["lsblk -J -b -o "+lsblkCols] = string(fixture(t, "lsblk.json"))
	fr.out["zpool status -P -p"] = string(fixture(t, "zpool_status_mirror.txt"))
	fr.out[zpoolList] = "tank\t3985729650688\t1342177280000\t2643552370688\t3\t33\tONLINE\n"
	fr.out[zfsRoots] = "tank\t/srv/nocapos/pools/tank\n"
	fr.out[tankDS] = "tank\t1342177280000\t2643552370688\t98304\t/srv/nocapos/pools/tank\tlz4\t1.12\t0\n" +
		"tank/media\t1099511627776\t2643552370688\t1099511627776\t/srv/nocapos/pools/tank/media\tlz4\t1.01\t0\n"
	fr.out[findDataSrc] = "/dev/nvme0n1p1\n"
	fr.errs["zpool import -d /dev/disk/by-id"] = errors.New("no pools available to import")

	fstab := filepath.Join(t.TempDir(), "fstab")
	if err := os.WriteFile(fstab, []byte("UUID=9d8c7b6a / ext4 errors=remount-ro 0 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"/sys/module/zfs/version": "2.2.2-0ubuntu9.1\n",
		"/proc/swaps":             "Filename\tType\tSize\tUsed\tPriority\n",
	}
	h := &Host{
		Run:      fr,
		LookPath: func(s string) (string, error) { return "/usr/sbin/" + s, nil },
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return os.ReadFile(p)
		},
		Exists:    func(string) bool { return true },
		Links:     func() map[string]string { return testLinks },
		MountedFn: func(string) bool { return false },
		Fstab:     fstab,
		MkdirAll:  func(string, os.FileMode) error { return nil },
	}
	st := &memStore{locs: map[string]store.StorageLocation{}, set: map[string]string{}}
	m := NewManager(Options{Backend: h, Store: st, DataDir: "/var/lib/nocapos/data", Native: func() bool { return true },
		CanInstall: func() bool { return true }, PkgManager: func() string { return "apt" }})
	return m, fr, h, st
}

func waitJob(t *testing.T, m *Manager, j *Job) Job {
	t.Helper()
	for i := 0; i < 200; i++ {
		got, err := m.Job(j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != "running" {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job never finished")
	return Job{}
}

func wantStatus(t *testing.T, err error, status int, contains string) {
	t.Helper()
	var se *Error
	if !errors.As(err, &se) || se.Status != status || !strings.Contains(se.Msg, contains) {
		t.Fatalf("got %v, want %d containing %q", err, status, contains)
	}
}

func TestDisksFromHost(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	disks, err := m.Disks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, d := range disks {
		names = append(names, d.Name)
	}
	if strings.Contains(strings.Join(names, " "), "loop0") || strings.Contains(strings.Join(names, " "), "zram0") {
		t.Errorf("virtual devices listed: %v", names)
	}
	res, err := m.Pools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kids := res.Pools[0].Vdevs[0].Children
	if kids[0].Disk != "sdb" || kids[1].Disk != "sdc" || res.Pools[0].Mountpoint != "/srv/nocapos/pools/tank" || res.Pools[0].Size != 3985729650688 {
		t.Errorf("pool: %+v", res.Pools[0])
	}
}

func TestCreatePool(t *testing.T) {
	m, fr, _, st := newTestManager(t)
	ctx := context.Background()
	req := CreatePoolRequest{Name: "media", Layout: "mirror", Disks: []string{"sdg", "sdh"}, Compression: "zstd", AddToFiles: true}

	for _, confirm := range []string{"", "erase", "ERASE ", "media"} {
		req.Confirm = confirm
		_, err := m.CreatePool(ctx, req, nil)
		wantStatus(t, err, http.StatusBadRequest, "type ERASE")
	}
	if len(fr.called("zpool create")) != 0 {
		t.Fatal("zpool create ran without confirmation")
	}

	req.Confirm = "ERASE"
	j, err := m.CreatePool(ctx, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitJob(t, m, j); got.State != "done" {
		t.Fatalf("job: %+v", got)
	}
	calls := fr.called("zpool create")
	want := []string{"zpool", "create", "-f", "-o", "ashift=12", "-O", "compression=zstd", "-O", "atime=off", "-O", "xattr=sa",
		"-O", "acltype=posixacl", "-m", "/srv/nocapos/pools/media", "media", "mirror", sdgByID, "/dev/sdh"}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("zpool create calls:\n%q\nwant\n%q", calls, want)
	}
	if l, ok := st.locs["pool:media"]; !ok || l.Path != "/srv/nocapos/pools/media" {
		t.Errorf("Files location not saved: %+v", st.locs)
	}
}

func TestCreatePoolRefusesUnavailableDisks(t *testing.T) {
	m, fr, _, _ := newTestManager(t)
	ctx := context.Background()
	for disk, reason := range map[string]string{
		"sda": "System disk", "sdb": "In pool tank", "sdc": "In pool tank", "sdd": "software RAID", "sdf": "LVM",
		"sdi": "Mounted at /media/usb", "sdj": "NoCapOS storage", "sdk": "Too small", "sdl": "not imported",
		"nvme0n1": "NoCapOS's own data", "sr0": "Optical", "loop0": "isn't connected", "sdz": "isn't connected", "../sda": "unknown disk",
	} {
		_, err := m.CreatePool(ctx, CreatePoolRequest{Name: "media", Layout: "mirror", Disks: []string{"sdg", disk}, Confirm: "ERASE"}, nil)
		wantStatus(t, err, http.StatusBadRequest, reason)
	}
	_, err := m.CreatePool(ctx, CreatePoolRequest{Name: "media", Layout: "mirror", Disks: []string{"sdg", "sdg"}, Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "twice")
	_, err = m.CreatePool(ctx, CreatePoolRequest{Name: "media", Layout: "raidz2", Disks: []string{"sdg", "sdh"}, Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "at least 4")
	_, err = m.CreatePool(ctx, CreatePoolRequest{Name: "tank", Layout: "stripe", Disks: []string{"sdg"}, Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "already exists")
	_, err = m.CreatePool(ctx, CreatePoolRequest{Name: "raidz", Layout: "stripe", Disks: []string{"sdg"}, Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "reserved")
	_, err = m.CreatePool(ctx, CreatePoolRequest{Name: "x", Layout: "stripe", Disks: []string{"sdg"}, Confirm: "ERASE", Mountpoint: "/etc"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "mountpoint")
	if len(fr.called("zpool create")) != 0 {
		t.Fatal("zpool create ran for a refused request")
	}
}

func TestDiskLocks(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	unlock, err := m.lock("test", "disk:sdg")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Format(context.Background(), "sdg", FormatRequest{Label: "media2", Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusConflict, "busy")
	_, err = m.CreatePool(context.Background(), CreatePoolRequest{Name: "p", Layout: "stripe", Disks: []string{"sdg"}, Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusConflict, "busy")
	unlock()
	if _, err := m.lock("test", "disk:sdg"); err != nil {
		t.Fatalf("lock not released: %v", err)
	}
}

func TestAddAndReplace(t *testing.T) {
	m, fr, _, _ := newTestManager(t)
	ctx := context.Background()
	_, err := m.AddVdev(ctx, "tank", AddVdevRequest{Layout: "mirror", Disks: []string{"sdg", "sdh"}}, nil)
	wantStatus(t, err, http.StatusBadRequest, "ERASE")
	_, err = m.AddVdev(ctx, "tank", AddVdevRequest{Layout: "stripe", Disks: []string{"sdg"}, Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "mismatch")
	_, err = m.AddVdev(ctx, "tank", AddVdevRequest{Layout: "mirror", Disks: []string{"sdg", "sdb"}, Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "In pool tank")
	j, err := m.AddVdev(ctx, "tank", AddVdevRequest{Layout: "mirror", Disks: []string{"sdg", "sdh"}, Confirm: "ERASE"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, j)
	if c := fr.called("zpool add"); len(c) != 1 || !reflect.DeepEqual(c[0], []string{"zpool", "add", "-f", "tank", "mirror", sdgByID, "/dev/sdh"}) {
		t.Errorf("zpool add: %q", c)
	}

	old := "/dev/disk/by-partuuid/6f1d5b0e-9a2c-4c1e-b3d4-7e8f9a0b1c2d"
	_, err = m.Replace(ctx, "tank", ReplaceRequest{Old: old, Disk: "sdg", Confirm: "no"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "ERASE")
	_, err = m.Replace(ctx, "tank", ReplaceRequest{Old: "/dev/sda1", Disk: "sdg", Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "isn't a disk in pool")
	_, err = m.Replace(ctx, "tank", ReplaceRequest{Old: old, Disk: "sdi", Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "Mounted")
	j, err = m.Replace(ctx, "tank", ReplaceRequest{Old: old, Disk: "sdg", Confirm: "ERASE"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, j)
	if c := fr.called("zpool replace"); len(c) != 1 || !reflect.DeepEqual(c[0], []string{"zpool", "replace", "-f", "tank", old, sdgByID}) {
		t.Errorf("zpool replace: %q", c)
	}
}

func TestDestroyAndExport(t *testing.T) {
	m, fr, _, st := newTestManager(t)
	ctx := context.Background()
	st.locs["pool:tank"] = store.StorageLocation{ID: "pool:tank", Kind: "pool", Name: "tank", Path: "/srv/nocapos/pools/tank"}

	for _, c := range []string{"", "Tank", "ERASE", "tank "} {
		wantStatus(t, m.Destroy(ctx, "tank", c), http.StatusBadRequest, "type tank")
	}
	wantStatus(t, m.Destroy(ctx, "nope", "nope"), http.StatusNotFound, "no pool")

	// NoCapOS's data on the pool (findmnt source is a dataset of it).
	fr.out[findDataSrc] = "tank/nocapos\n"
	wantStatus(t, m.Destroy(ctx, "tank", "tank"), http.StatusBadRequest, "NoCapOS keeps its own data")
	wantStatus(t, m.Export(ctx, "tank"), http.StatusBadRequest, "NoCapOS keeps its own data")
	fr.out[findDataSrc] = "/dev/nvme0n1p1\n"

	// An app keeps data on the pool.
	m.InUse = func(context.Context) []PathUse {
		return []PathUse{{Path: "/srv/nocapos/pools/tank/media/jellyfin", App: "jellyfin"}}
	}
	wantStatus(t, m.Destroy(ctx, "tank", "tank"), http.StatusBadRequest, "jellyfin")
	m.InUse = nil

	if len(fr.called("zpool destroy")) != 0 || len(fr.called("zpool export")) != 0 {
		t.Fatal("ran a refused destroy/export")
	}
	if err := m.Export(ctx, "tank"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.locs["pool:tank"]; !ok {
		t.Error("export forgot the Files location (it should come back on import)")
	}
	if err := m.Destroy(ctx, "tank", "tank"); err != nil {
		t.Fatal(err)
	}
	if c := fr.called("zpool destroy"); len(c) != 1 || !reflect.DeepEqual(c[0], []string{"zpool", "destroy", "tank"}) {
		t.Errorf("destroy: %q", c)
	}
	if _, ok := st.locs["pool:tank"]; ok {
		t.Error("destroy kept the Files location")
	}
}

func TestDatasetsAndSnapshots(t *testing.T) {
	m, fr, _, _ := newTestManager(t)
	ctx := context.Background()
	wantStatus(t, m.DeleteDataset(ctx, "tank", "tank"), http.StatusBadRequest, "pool itself")
	wantStatus(t, m.DeleteDataset(ctx, "tank/media", "tank/Media"), http.StatusBadRequest, "type tank/media")
	wantStatus(t, m.DeleteDataset(ctx, "tank/nope", "tank/nope"), http.StatusNotFound, "no dataset")
	if err := m.DeleteDataset(ctx, "tank/media", "tank/media"); err != nil {
		t.Fatal(err)
	}
	if c := fr.called("zfs destroy"); len(c) != 1 || !reflect.DeepEqual(c[0], []string{"zfs", "destroy", "-r", "tank/media"}) {
		t.Errorf("dataset destroy: %q", c)
	}

	// Snapshot deletes take no confirmation, so they must never reach a dataset.
	for _, bad := range []string{"tank/media", "tank", "tank/media@", "tank/media@a@b", "-r tank"} {
		if err := m.DeleteSnapshot(ctx, bad); err == nil {
			t.Errorf("DeleteSnapshot(%q) accepted", bad)
		}
	}
	if n := len(fr.called("zfs destroy")); n != 1 {
		t.Fatalf("snapshot delete ran zfs destroy on a non-snapshot (%d calls)", n)
	}
	if err := m.DeleteSnapshot(ctx, "tank/media@weekly"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, m.Rollback(ctx, "tank/media@weekly", "tank/media"), http.StatusBadRequest, "type tank/media@weekly")
	if err := m.Rollback(ctx, "tank/media@weekly", "tank/media@weekly"); err != nil {
		t.Fatal(err)
	}
	if c := fr.called("zfs rollback"); len(c) != 1 || !reflect.DeepEqual(c[0], []string{"zfs", "rollback", "-r", "tank/media@weekly"}) {
		t.Errorf("rollback: %q", c)
	}
	if err := m.CreateDataset(ctx, DatasetRequest{Name: "tank/new", Quota: i64(1 << 30), Compression: "zstd"}); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, m.CreateDataset(ctx, DatasetRequest{Name: "tank/x", Compression: "gzip-9"}), http.StatusBadRequest, "compression")
	wantStatus(t, m.CreateDataset(ctx, DatasetRequest{Name: "tank/x", Quota: i64(-1)}), http.StatusBadRequest, "negative")
	wantStatus(t, m.CreateSnapshot(ctx, "tank/media", "a b"), http.StatusBadRequest, "snapshot names")
}

func TestFormatDisk(t *testing.T) {
	m, fr, h, st := newTestManager(t)
	ctx := context.Background()
	fr.out["blkid -s UUID -o value /dev/sdg1"] = "2b3c4d5e-6f70-4182-93a4-b5c6d7e8f901\n"

	_, err := m.Format(ctx, "sdg", FormatRequest{Label: "media2"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "ERASE")
	_, err = m.Format(ctx, "sdg", FormatRequest{Label: "bad label", Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "labels")
	_, err = m.Format(ctx, "sdb", FormatRequest{Label: "x", Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusBadRequest, "In pool tank")
	if len(fr.called("wipefs")) != 0 {
		t.Fatal("wiped a disk for a refused request")
	}

	j, err := m.Format(ctx, "sdg", FormatRequest{Label: "media2", AddToFiles: true, Confirm: "ERASE"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitJob(t, m, j); got.State != "done" {
		t.Fatalf("job: %+v", got)
	}
	var seq []string
	for _, c := range fr.calls {
		if c[0] != "lsblk" && c[0] != "zpool" && c[0] != "zfs" && c[0] != "smartctl" {
			seq = append(seq, strings.Join(c, " "))
		}
	}
	want := []string{
		"wipefs -a /dev/sdg",
		"sfdisk --wipe always --wipe-partitions always /dev/sdg",
		"udevadm settle",
		"wipefs -a /dev/sdg1",
		"mkfs.ext4 -F -m 0 -L media2 /dev/sdg1",
		"blkid -s UUID -o value /dev/sdg1",
		"systemctl daemon-reload",
		"mount /srv/nocapos/disks/media2",
	}
	if !reflect.DeepEqual(seq, want) {
		t.Errorf("format sequence:\n%s\nwant\n%s", strings.Join(seq, "\n"), strings.Join(want, "\n"))
	}
	if fr.stdin["sfdisk --wipe always --wipe-partitions always /dev/sdg"] != sfdiskScript {
		t.Error("sfdisk didn't get the partition script")
	}
	b, _ := os.ReadFile(h.Fstab)
	if !strings.HasPrefix(string(b), "UUID=9d8c7b6a / ext4") || !strings.Contains(string(b), "UUID=2b3c4d5e-6f70-4182-93a4-b5c6d7e8f901 /srv/nocapos/disks/media2 ext4 defaults,nofail,x-systemd.device-timeout=10s 0 2 # nocapos") {
		t.Errorf("fstab:\n%s", b)
	}
	if _, ok := st.locs["disk:media2"]; !ok {
		t.Error("Files location not saved")
	}
}

func TestSmartAndUnsupported(t *testing.T) {
	m, fr, _, _ := newTestManager(t)
	ctx := context.Background()
	fr.out["smartctl -j -a -n standby /dev/sdb"] = string(fixture(t, "smart_sata.json"))
	fr.errs["smartctl -j -a -n standby /dev/sdb"] = errors.New("exit status 64")
	r, err := m.SmartDetail(ctx, "sdb")
	if err != nil {
		t.Fatal(err)
	}
	if *r.Summary.Reallocated != 16 {
		t.Errorf("smart: %+v", r.Summary)
	}
	_, err = m.SmartTest(ctx, "sdb", "conveyance", nil)
	wantStatus(t, err, http.StatusBadRequest, "short or long")
	j, err := m.SmartTest(ctx, "sdb", "short", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, j)
	if c := fr.called("smartctl -t"); len(c) != 1 || !reflect.DeepEqual(c[0], []string{"smartctl", "-t", "short", "/dev/sdb"}) {
		t.Errorf("smart test: %q", c)
	}

	m.native = func() bool { return false }
	if s := m.Status(ctx); s.Supported || s.Reason == "" {
		t.Errorf("status off Linux: %+v", s)
	}
	_, err = m.Disks(ctx)
	wantStatus(t, err, http.StatusServiceUnavailable, "")
	_, err = m.CreatePool(ctx, CreatePoolRequest{Name: "p", Layout: "stripe", Disks: []string{"sdg"}, Confirm: "ERASE"}, nil)
	wantStatus(t, err, http.StatusServiceUnavailable, "")
}

func TestAutoScrub(t *testing.T) {
	m, fr, _, st := newTestManager(t)
	ctx := context.Background()
	now := time.Now()
	recent := Pool{Name: "fresh", Health: "ONLINE", Scan: Scan{State: "finished", Finished: now.Add(-24 * time.Hour).Format(time.RFC3339)}}
	old := Pool{Name: "old", Health: "ONLINE", Scan: Scan{State: "finished", Finished: now.Add(-40 * 24 * time.Hour).Format(time.RFC3339)}}
	sick := Pool{Name: "sick", Health: "DEGRADED"}
	m.autoScrub(ctx, []Pool{recent, old, sick}, now)
	if c := fr.called("zpool scrub"); len(c) != 1 || c[0][2] != "old" {
		t.Fatalf("scrubs: %q", c)
	}
	m.autoScrub(ctx, []Pool{old}, now.Add(time.Hour)) // already started this month
	if n := len(fr.called("zpool scrub")); n != 1 {
		t.Fatalf("scrubbed twice: %d", n)
	}
	st.set[keyAutoScrub] = "0"
	m.autoScrub(ctx, []Pool{old}, now.Add(40*24*time.Hour))
	if n := len(fr.called("zpool scrub")); n != 1 {
		t.Fatal("scrubbed with auto scrub off")
	}
}

func TestDemoEndToEnd(t *testing.T) {
	dir := t.TempDir()
	d := NewDemo(dir)
	d.delay = 0
	st := &memStore{locs: map[string]store.StorageLocation{}, set: map[string]string{}}
	m := NewManager(Options{Backend: d, Store: st, DataDir: dir, Demo: true, Native: func() bool { return false }})
	ctx := context.Background()
	disks, err := m.Disks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	free := 0
	for _, x := range disks {
		if x.Available {
			free++
		}
	}
	if free != 4 {
		t.Errorf("demo free disks = %d, want 4", free)
	}
	j, err := m.CreatePool(ctx, CreatePoolRequest{Name: "media", Layout: "raidz1", Disks: []string{"sda", "sdb", "sdc"}, Confirm: "ERASE", AddToFiles: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitJob(t, m, j); got.State != "done" {
		t.Fatalf("job %+v", got)
	}
	res, _ := m.Pools(ctx)
	if len(res.Pools) != 2 || res.Pools[0].Name != "media" || res.Pools[0].Layout != "raidz1" || !res.Pools[0].InFiles {
		t.Fatalf("pools: %+v", res.Pools)
	}
	if err := m.Import(ctx, "archive"); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(ctx, "media", "media"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.locs["pool:media"]; ok {
		t.Error("location kept after destroy")
	}
}
