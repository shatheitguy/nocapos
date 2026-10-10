package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Demo is an in-memory machine (ALFA_STORAGE_DEMO=1) for trying the Storage
// app anywhere. Nothing here touches a real disk; actions change the fake
// state so the UI can be exercised end to end. Files locations point into a
// sandbox folder under the data dir.
type Demo struct {
	mu         sync.Mutex
	sandbox    string
	disks      []*demoDisk
	pools      map[string]*demoPool
	importable []*demoPool
	datasets   map[string]*Dataset
	snapshots  map[string][]Snapshot
	formatted  map[string]string // disk -> label
	delay      time.Duration
}

type demoDisk struct {
	name, model, serial, tran string
	size                      int64
	rota, rm                  bool
	parts                     []BlockDev
	smart                     *SmartReport
	testUntil                 time.Time
	testKind                  string
}

type demoGroup struct {
	layout string
	disks  []string
}

type demoPool struct {
	name, id, compression, mountpoint string
	groups                            []demoGroup
	alloc                             int64
	scanFn                            string
	scanStart                         time.Time
	scanDur                           time.Duration
	lastScan                          time.Time
	lastScanFn                        string
}

const tb = int64(4000787030016)

func sp(s string) *string { return &s }

func NewDemo(dataDir string) *Demo {
	d := &Demo{
		sandbox:   filepath.Join(dataDir, "storage-demo"),
		pools:     map[string]*demoPool{},
		datasets:  map[string]*Dataset{},
		snapshots: map[string][]Snapshot{},
		formatted: map[string]string{},
		delay:     3 * time.Second,
	}
	hdd := func(name, serial string, realloc int64) *demoDisk {
		return &demoDisk{name: name, model: "WDC WD40EFRX-68N32N0", serial: serial, tran: "sata", size: tb, rota: true,
			smart: demoATASmart(34, 28734, realloc)}
	}
	ssd := func(name, serial string) *demoDisk {
		return &demoDisk{name: name, model: "Samsung SSD 870 EVO 4TB", serial: serial, tran: "sata", size: tb,
			smart: demoATASmart(31, 6120, 0)}
	}
	nvme := &demoDisk{name: "nvme0n1", model: "Samsung SSD 980 PRO 1TB", serial: "S5GXNX0T512345A", tran: "nvme", size: 1000204886016,
		smart: demoNVMeSmart()}
	nvme.parts = []BlockDev{
		{Name: "nvme0n1p1", Size: 536870912, Type: "part", FSType: "vfat", UUID: "8C1A-2F3B", Mountpoints: []*string{sp("/boot/efi")}},
		{Name: "nvme0n1p2", Size: 982000000000, Type: "part", FSType: "ext4", UUID: "3f1c2a8e-7b1d-4c55-9a7e-0d6c1e2b4f10", Mountpoints: []*string{sp("/")}},
		{Name: "nvme0n1p3", Size: 17179869184, Type: "part", FSType: "swap", UUID: "b8e1f0aa-2c3d-4e5f-8a9b-0c1d2e3f4a5b", Mountpoints: []*string{sp("[SWAP]")}},
	}
	usb := &demoDisk{name: "sde", model: "Samsung PSSD T7", serial: "S6WBNS0R901234K", tran: "usb", size: 1000204886016, rm: false,
		smart: demoATASmart(29, 812, 0)}
	usb.parts = []BlockDev{{Name: "sde1", Size: 1000202273280, Type: "part", FSType: "ext4", Label: "photos-usb",
		UUID: "6a2f7d0c-11e2-4c8b-9d3f-5e7a1b2c3d4e", Mountpoints: []*string{sp("/media/usb")}}}
	d.disks = []*demoDisk{
		nvme,
		hdd("sda", "WD-WCC7K1A2B3C4", 0),
		hdd("sdb", "WD-WCC7K5D6E7F8", 8), // reallocated sectors: SMART warning
		ssd("sdc", "S6PNNM0T401122X"),
		ssd("sdd", "S6PNNM0T401133Y"),
		usb,
		hdd("sdf", "WD-WCC7K9A8B7C6", 0),
		hdd("sdg", "WD-WCC7K5F4E3D2", 0),
		{name: "sdh", model: "ST8000VN004-2M2101", serial: "WSD3A1B2", tran: "sata", size: 8001563222016, rota: true, smart: demoATASmart(36, 15011, 0)},
	}
	now := time.Now()
	d.pools["tank"] = &demoPool{name: "tank", id: "11624379120431855519", compression: "lz4", mountpoint: PoolBase + "/tank",
		groups: []demoGroup{{"mirror", []string{"sdf", "sdg"}}}, alloc: 1342177280000,
		scanFn: "scrub", scanStart: now.Add(-9 * time.Minute), scanDur: 40 * time.Minute,
		lastScan: now.Add(-33 * 24 * time.Hour), lastScanFn: "scrub"}
	d.importable = []*demoPool{{name: "archive", id: "4410227397452116412", compression: "zstd", mountpoint: PoolBase + "/archive",
		groups: []demoGroup{{"stripe", []string{"sdh"}}}, alloc: 2199023255552}}
	d.addDataset("tank", "/srv/nocapos/pools/tank", 1342177280000, "lz4", 1.12, 0)
	d.addDataset("tank/media", "/srv/nocapos/pools/tank/media", 1099511627776, "lz4", 1.01, 0)
	d.addDataset("tank/backups", "/srv/nocapos/pools/tank/backups", 214748364800, "zstd", 1.63, 536870912000)
	d.addDataset("tank/photos", "/srv/nocapos/pools/tank/photos", 27917287424, "lz4", 1.04, 0)
	d.snapshots["tank/media"] = []Snapshot{
		{Name: "tank/media@weekly-2026-09-27", Created: now.Add(-13 * 24 * time.Hour).UTC(), Used: 3221225472, Referenced: 1030792151040},
		{Name: "tank/media@weekly-2026-10-04", Created: now.Add(-6 * 24 * time.Hour).UTC(), Used: 1288490188, Referenced: 1073741824000},
	}
	d.snapshots["tank/backups"] = []Snapshot{
		{Name: "tank/backups@before-upgrade", Created: now.Add(-2 * 24 * time.Hour).UTC(), Used: 524288000, Referenced: 210453397504},
	}
	_ = os.MkdirAll(d.FilesPath(PoolBase+"/tank"), 0o755)
	return d
}

func demoATASmart(temp, hours, realloc int64) *SmartReport {
	passed := true
	attrs := []SmartAttribute{
		{1, "Raw Read Error Rate", 200, 200, 51, "0", false},
		{3, "Spin Up Time", 178, 175, 21, "6075", false},
		{4, "Start Stop Count", 100, 100, 0, "212", false},
		{5, "Reallocated Sector Ct", 200 - realloc, 200 - realloc, 140, fmt.Sprint(realloc), false},
		{9, "Power On Hours", 61, 61, 0, fmt.Sprint(hours), false},
		{12, "Power Cycle Count", 100, 100, 0, "198", false},
		{194, "Temperature Celsius", 116, 103, 0, fmt.Sprint(temp), false},
		{197, "Current Pending Sector", 200, 200, 0, "0", false},
		{198, "Offline Uncorrectable", 100, 253, 0, "0", false},
		{199, "UDMA CRC Error Count", 200, 200, 0, "0", false},
	}
	return &SmartReport{
		Summary: SmartSummary{Available: true, Passed: &passed, TemperatureC: i64(temp), PowerOnHours: i64(hours),
			Reallocated: i64(realloc), Pending: i64(0)},
		Attributes: attrs,
		SelfTests:  []SelfTest{{Type: "Short offline", Status: "Completed without error", Hours: hours - 160}},
	}
}

func demoNVMeSmart() *SmartReport {
	passed := true
	return &SmartReport{
		Summary: SmartSummary{Available: true, Passed: &passed, TemperatureC: i64(41), PowerOnHours: i64(8123),
			PercentUsed: i64(3), MediaErrors: i64(0)},
		Attributes: []SmartAttribute{},
		NVMe: &NVMeHealth{AvailableSpare: 100, SpareThreshold: 10, PercentageUsed: 3, DataUnitsRead: 48219334,
			DataUnitsWritten: 39811207, PowerCycles: 312, UnsafeShutdowns: 21},
		SelfTests: []SelfTest{{Type: "Short", Status: "Completed without error", Hours: 8100}},
	}
}

func (d *Demo) addDataset(name, mp string, used int64, comp string, ratio float64, quota int64) {
	d.datasets[name] = &Dataset{Name: name, Used: used, Referenced: used, Mountpoint: mp, Compression: comp, CompressRatio: ratio, Quota: quota}
}

func (d *Demo) wait(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(d.delay):
	}
}

func (d *Demo) disk(name string) *demoDisk {
	for _, x := range d.disks {
		if x.name == name {
			return x
		}
	}
	return nil
}

func byIDOf(x *demoDisk) string {
	switch x.tran {
	case "nvme":
		return "/dev/disk/by-id/nvme-" + strings.ReplaceAll(x.model, " ", "_") + "_" + x.serial
	case "usb":
		return "/dev/disk/by-id/usb-" + strings.ReplaceAll(x.model, " ", "_") + "_" + x.serial + "-0:0"
	}
	return "/dev/disk/by-id/ata-" + strings.ReplaceAll(x.model, " ", "_") + "_" + x.serial
}

// devName maps a /dev path (or by-id path) back to a demo disk name.
func (d *Demo) devName(p string) string {
	for _, x := range d.disks {
		if p == "/dev/"+x.name || p == byIDOf(x) {
			return x.name
		}
	}
	return ""
}

func (d *Demo) Tools(context.Context) Tools {
	return Tools{ZFSInstalled: true, ZFSModule: true, ZFSVersion: "2.2.2", SmartInstalled: true, Lsblk: true}
}

func (d *Demo) Install(ctx context.Context, tool string) error { d.wait(ctx); return nil }

func (d *Demo) poolOfDisk(name string) (*demoPool, bool) {
	for _, p := range d.pools {
		for _, g := range p.groups {
			for _, n := range g.disks {
				if n == name {
					return p, true
				}
			}
		}
	}
	for _, p := range d.importable {
		for _, g := range p.groups {
			for _, n := range g.disks {
				if n == name {
					return p, false
				}
			}
		}
	}
	return nil, false
}

func (d *Demo) Facts(context.Context) (*Facts, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f := &Facts{Links: map[string]string{}}
	for _, x := range d.disks {
		dev := BlockDev{Name: x.name, Path: "/dev/" + x.name, Size: flexInt(x.size), Type: "disk", Model: x.model, Serial: x.serial,
			Tran: x.tran, Rota: flexBool(x.rota), RM: flexBool(x.rm)}
		f.Links[byIDOf(x)] = x.name
		part := partitionPath(x.name)
		switch {
		case func() bool { p, _ := d.poolOfDisk(x.name); return p != nil }():
			p, _ := d.poolOfDisk(x.name)
			dev.Children = []BlockDev{
				{Name: part, Size: flexInt(x.size - 8<<20), Type: "part", FSType: "zfs_member", Label: p.name},
				{Name: x.name + "9", Size: 8 << 20, Type: "part"},
			}
			f.Links[byIDOf(x)+"-part1"] = part
		case d.formatted[x.name] != "":
			l := d.formatted[x.name]
			dev.Children = []BlockDev{{Name: part, Size: flexInt(x.size - 1<<20), Type: "part", FSType: "ext4", Label: l,
				UUID:        "0d9c" + fmt.Sprintf("%04x", len(l)) + "-5e1f-4a2b-8c3d-" + fmt.Sprintf("%012x", x.size%0xffffffffffff),
				Mountpoints: []*string{sp(DiskBase + "/" + l)}}}
		default:
			for _, c := range x.parts {
				c.Path = "/dev/" + c.Name
				dev.Children = append(dev.Children, c)
			}
		}
		f.Devices = append(f.Devices, dev)
	}
	f.Swaps = []string{"/dev/nvme0n1p3"}
	return f, nil
}

func (d *Demo) DataSource(context.Context, string) string { return "/dev/nvme0n1p2" }

func (d *Demo) Smart(_ context.Context, disk Disk) (*SmartReport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	x := d.disk(disk.Name)
	if x == nil {
		return nil, fmt.Errorf("no such disk")
	}
	r := *x.smart
	r.Summary.TestRunning = false
	if !x.testUntil.IsZero() {
		if time.Now().Before(x.testUntil) {
			r.Summary.TestRunning = true
			total := 30 * time.Second
			if x.testKind == "long" {
				total = 3 * time.Minute
			}
			left := int64(time.Until(x.testUntil) * 100 / total)
			r.Summary.TestRemaining = &left
		} else {
			kind := "Short offline"
			if x.tran == "nvme" {
				kind = "Short"
			}
			if x.testKind == "long" {
				kind = "Extended offline"
			}
			hours := int64(0)
			if r.Summary.PowerOnHours != nil {
				hours = *r.Summary.PowerOnHours
			}
			x.smart.SelfTests = append([]SelfTest{{Type: kind, Status: "Completed without error", Hours: hours}}, x.smart.SelfTests...)
			x.testUntil = time.Time{}
			r.SelfTests = x.smart.SelfTests
		}
	}
	return &r, nil
}

func (d *Demo) SmartTest(_ context.Context, disk Disk, kind string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	x := d.disk(disk.Name)
	if x == nil {
		return fmt.Errorf("no such disk")
	}
	dur := 30 * time.Second
	if kind == "long" {
		dur = 3 * time.Minute
	}
	x.testUntil, x.testKind = time.Now().Add(dur), kind
	return nil
}

func usable(layout string, sizes []int64) int64 {
	if len(sizes) == 0 {
		return 0
	}
	min := sizes[0]
	var sum int64
	for _, s := range sizes {
		sum += s
		if s < min {
			min = s
		}
	}
	n := int64(len(sizes))
	switch layout {
	case "mirror":
		return min
	case "raidz1":
		return (n - 1) * min
	case "raidz2":
		return (n - 2) * min
	case "raidz3":
		return (n - 3) * min
	}
	return sum
}

func (d *Demo) poolView(p *demoPool) Pool {
	now := time.Now()
	out := Pool{Name: p.name, Health: "ONLINE", Mountpoint: p.mountpoint, Vdevs: []Vdev{}, Scan: Scan{State: "none"}}
	for i, g := range p.groups {
		var sizes []int64
		var kids []Vdev
		for _, n := range g.disks {
			x := d.disk(n)
			sizes = append(sizes, x.size)
			id := byIDOf(x) + "-part1"
			kids = append(kids, Vdev{Name: id, Type: "disk", Health: "ONLINE", Path: id, Children: []Vdev{}})
		}
		out.Size += usable(g.layout, sizes)
		if g.layout == "stripe" {
			out.Vdevs = append(out.Vdevs, kids...)
		} else {
			out.Vdevs = append(out.Vdevs, Vdev{Name: fmt.Sprintf("%s-%d", g.layout, i), Type: g.layout, Health: "ONLINE", Children: kids})
		}
	}
	out.Layout = layoutOf(out.Vdevs)
	out.Allocated = min(p.alloc, out.Size)
	out.Free = out.Size - out.Allocated
	if out.Size > 0 {
		out.Capacity = out.Allocated * 100 / out.Size
	}
	out.Fragmentation = 4
	if p.scanFn != "" {
		elapsed := now.Sub(p.scanStart)
		if elapsed >= p.scanDur {
			p.lastScan, p.lastScanFn, p.scanFn = p.scanStart.Add(p.scanDur), p.scanFn, ""
		} else {
			pct := float64(elapsed*10000/p.scanDur) / 100
			eta := int64((p.scanDur - elapsed).Seconds())
			out.Scan = Scan{Function: p.scanFn, State: "scanning", Percent: &pct, EtaSeconds: &eta}
		}
	}
	if p.scanFn == "" && !p.lastScan.IsZero() {
		zero := int64(0)
		out.Scan = Scan{Function: p.lastScanFn, State: "finished", Errors: &zero, Finished: p.lastScan.Format(time.RFC3339)}
	}
	return out
}

func (d *Demo) Pools(context.Context) ([]Pool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	names := make([]string, 0, len(d.pools))
	for n := range d.pools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []Pool{}
	for _, n := range names {
		out = append(out, d.poolView(d.pools[n]))
	}
	return out, nil
}

func (d *Demo) Importable(context.Context) ([]ImportablePool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []ImportablePool{}
	for _, p := range d.importable {
		ip := ImportablePool{Name: p.name, ID: p.id, Health: "ONLINE", Disks: []string{}}
		for _, g := range p.groups {
			for _, n := range g.disks {
				ip.Disks = append(ip.Disks, strings.TrimPrefix(byIDOf(d.disk(n)), "/dev/disk/by-id/")+"-part1")
			}
		}
		out = append(out, ip)
	}
	return out, nil
}

func (d *Demo) names(devs []string) ([]string, error) {
	out := make([]string, 0, len(devs))
	for _, p := range devs {
		n := d.devName(p)
		if n == "" {
			return nil, fmt.Errorf("no such device %s", p)
		}
		out = append(out, n)
	}
	return out, nil
}

func (d *Demo) CreatePool(ctx context.Context, name, layout string, devs []string, compression, mountpoint string) error {
	d.wait(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.pools[name]; ok {
		return fmt.Errorf("cannot create '%s': pool already exists", name)
	}
	ns, err := d.names(devs)
	if err != nil {
		return err
	}
	d.pools[name] = &demoPool{name: name, id: fmt.Sprint(time.Now().UnixNano()), compression: compression, mountpoint: mountpoint,
		groups: []demoGroup{{layout, ns}}, alloc: 1 << 20}
	d.addDataset(name, mountpoint, 1<<20, compression, 1, 0)
	_ = os.MkdirAll(d.FilesPath(mountpoint), 0o755)
	return nil
}

func (d *Demo) AddVdev(ctx context.Context, pool, layout string, devs []string) error {
	d.wait(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pools[pool]
	if !ok {
		return fmt.Errorf("no such pool")
	}
	ns, err := d.names(devs)
	if err != nil {
		return err
	}
	p.groups = append(p.groups, demoGroup{layout, ns})
	return nil
}

func (d *Demo) Replace(ctx context.Context, pool, old, dev string) error {
	d.wait(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pools[pool]
	if !ok {
		return fmt.Errorf("no such pool")
	}
	oldName := d.devName(strings.TrimSuffix(old, "-part1"))
	newName := d.devName(dev)
	for gi := range p.groups {
		for i, n := range p.groups[gi].disks {
			if n == oldName {
				p.groups[gi].disks[i] = newName
				p.scanFn, p.scanStart, p.scanDur = "resilver", time.Now(), 45*time.Second
				return nil
			}
		}
	}
	return fmt.Errorf("cannot replace %s: no such device in pool", old)
}

func (d *Demo) dropDatasets(pool string) {
	for n := range d.datasets {
		if n == pool || strings.HasPrefix(n, pool+"/") {
			delete(d.datasets, n)
			delete(d.snapshots, n)
		}
	}
}

func (d *Demo) Destroy(ctx context.Context, pool string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.pools[pool]; !ok {
		return fmt.Errorf("no such pool")
	}
	delete(d.pools, pool)
	d.dropDatasets(pool)
	return nil
}

func (d *Demo) Export(ctx context.Context, pool string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pools[pool]
	if !ok {
		return fmt.Errorf("no such pool")
	}
	delete(d.pools, pool)
	p.scanFn = ""
	d.importable = append(d.importable, p)
	d.dropDatasets(pool)
	return nil
}

func (d *Demo) Import(ctx context.Context, nameOrID string) error {
	d.wait(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, p := range d.importable {
		if p.name == nameOrID || p.id == nameOrID {
			d.importable = append(d.importable[:i], d.importable[i+1:]...)
			d.pools[p.name] = p
			d.addDataset(p.name, p.mountpoint, p.alloc, p.compression, 1.21, 0)
			_ = os.MkdirAll(d.FilesPath(p.mountpoint), 0o755)
			return nil
		}
	}
	return fmt.Errorf("cannot import '%s': no such pool available", nameOrID)
}

func (d *Demo) Scrub(ctx context.Context, pool string, stop bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pools[pool]
	if !ok {
		return fmt.Errorf("no such pool")
	}
	if stop {
		if p.scanFn == "" {
			return fmt.Errorf("cannot cancel scrubbing %s: there is no active scrub", pool)
		}
		p.scanFn = ""
		return nil
	}
	if p.scanFn != "" {
		return fmt.Errorf("cannot scrub %s: currently scrubbing; use 'zpool scrub -s' to cancel the current scrub", pool)
	}
	p.scanFn, p.scanStart, p.scanDur = "scrub", time.Now(), 2*time.Minute
	return nil
}

func (d *Demo) Datasets(_ context.Context, pool string) ([]Dataset, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pools[pool]
	if !ok {
		return nil, fmt.Errorf("cannot open '%s': dataset does not exist", pool)
	}
	view := d.poolView(p)
	out := []Dataset{}
	for n, ds := range d.datasets {
		if n == pool || strings.HasPrefix(n, pool+"/") {
			c := *ds
			c.Available = view.Free
			if c.Quota > 0 && c.Quota-c.Used < c.Available {
				c.Available = max(0, c.Quota-c.Used)
			}
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (d *Demo) CreateDataset(_ context.Context, name string, quota *int64, compression string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	parent := name[:strings.LastIndex(name, "/")]
	pds, ok := d.datasets[parent]
	if !ok {
		return fmt.Errorf("cannot create '%s': parent does not exist", name)
	}
	if _, ok := d.datasets[name]; ok {
		return fmt.Errorf("cannot create '%s': dataset already exists", name)
	}
	if compression == "" || compression == "inherit" {
		compression = pds.Compression
	}
	var q int64
	if quota != nil {
		q = *quota
	}
	d.addDataset(name, pds.Mountpoint+name[len(parent):], 98304, compression, 1, q)
	return nil
}

func (d *Demo) UpdateDataset(_ context.Context, name string, quota *int64, compression string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	ds, ok := d.datasets[name]
	if !ok {
		return fmt.Errorf("cannot open '%s': dataset does not exist", name)
	}
	if quota != nil {
		ds.Quota = *quota
	}
	switch compression {
	case "":
	case "inherit":
		if pds, ok := d.datasets[name[:strings.LastIndex(name, "/")]]; ok {
			ds.Compression = pds.Compression
		}
	default:
		ds.Compression = compression
	}
	return nil
}

func (d *Demo) DestroyDataset(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.datasets[name]; !ok {
		return fmt.Errorf("cannot open '%s': dataset does not exist", name)
	}
	d.dropDatasets(name)
	return nil
}

func (d *Demo) Snapshots(_ context.Context, dataset string) ([]Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.datasets[dataset]; !ok {
		return nil, fmt.Errorf("cannot open '%s': dataset does not exist", dataset)
	}
	return append([]Snapshot{}, d.snapshots[dataset]...), nil
}

func (d *Demo) Snapshot(_ context.Context, full string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	ds, _, _ := strings.Cut(full, "@")
	x, ok := d.datasets[ds]
	if !ok {
		return fmt.Errorf("cannot open '%s': dataset does not exist", ds)
	}
	for _, s := range d.snapshots[ds] {
		if s.Name == full {
			return fmt.Errorf("cannot create snapshot '%s': dataset already exists", full)
		}
	}
	d.snapshots[ds] = append(d.snapshots[ds], Snapshot{Name: full, Created: time.Now().UTC(), Used: 0, Referenced: x.Referenced})
	return nil
}

func (d *Demo) Rollback(_ context.Context, full string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	ds, _, _ := strings.Cut(full, "@")
	list := d.snapshots[ds]
	for i, s := range list {
		if s.Name == full {
			d.snapshots[ds] = list[:i+1] // later snapshots are destroyed (-r)
			return nil
		}
	}
	return fmt.Errorf("cannot open '%s': dataset does not exist", full)
}

func (d *Demo) DestroySnapshot(_ context.Context, full string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	ds, _, _ := strings.Cut(full, "@")
	list := d.snapshots[ds]
	for i, s := range list {
		if s.Name == full {
			d.snapshots[ds] = append(list[:i:i], list[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("could not find any snapshots to destroy; check snapshot names")
}

func (d *Demo) Format(ctx context.Context, dev, label, mountpoint string) error {
	d.wait(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.devName(dev)
	if n == "" {
		return fmt.Errorf("no such device %s", dev)
	}
	d.disk(n).parts = nil
	d.formatted[n] = label
	return os.MkdirAll(d.FilesPath(mountpoint), 0o755)
}

func (d *Demo) FilesPath(mountpoint string) string {
	rel := strings.TrimPrefix(mountpoint, "/srv/nocapos/")
	return filepath.Join(d.sandbox, filepath.FromSlash(rel))
}

func (d *Demo) Mounted(path string) bool {
	if !strings.HasPrefix(path, d.sandbox) {
		return false
	}
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		return false
	}
	// A pool's folder only counts while the pool is imported.
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.importable {
		if d.FilesPath(p.mountpoint) == path {
			return false
		}
	}
	return true
}
