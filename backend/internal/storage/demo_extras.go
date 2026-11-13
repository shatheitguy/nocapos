package storage

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"os"
	"strings"
	"time"
)

// Demo side of Extras: the same in-memory machine, nothing real is touched.

var demoSupportGroup = map[string]string{"cache": "cache", "log": "logs", "special": "special"}

type demoExtras struct {
	spindown map[string]int      // by-id -> minutes
	keys     map[string][32]byte // encrypted dataset -> passphrase hash (demo only)
	locked   map[string]bool
	mirrored bool
	flex     *FlexPlan
}

func (d *Demo) exInit() {
	if d.ex.keys == nil {
		d.ex = demoExtras{spindown: map[string]int{}, keys: map[string][32]byte{}, locked: map[string]bool{}}
	}
}

func (d *Demo) BootFacts(context.Context) (*BootFacts, error) {
	return &BootFacts{Source: "/dev/nvme0n1p2", FSType: "ext4", Size: 966367641600, Used: 41875931136, Avail: 875305824256,
		EFI: true, ESP: "/dev/nvme0n1p1", Arch: "amd64"}, nil
}

func (d *Demo) MirrorBoot(ctx context.Context, p BootMirrorPlan) error {
	d.wait(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exInit()
	d.ex.mirrored = true
	return nil
}

func (d *Demo) AddSupport(ctx context.Context, pool, kind string, devs []string) error {
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
	p.groups = append(p.groups, demoGroup{kind, ns})
	return nil
}

func (d *Demo) RemoveSupport(ctx context.Context, pool, dev string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pools[pool]
	if !ok {
		return fmt.Errorf("no such pool")
	}
	name := d.devName(strings.TrimSuffix(dev, "-part1"))
	for gi, g := range p.groups {
		if _, support := demoSupportGroup[g.layout]; !support {
			continue
		}
		for i, n := range g.disks {
			if n == name {
				g.disks = append(g.disks[:i:i], g.disks[i+1:]...)
				p.groups[gi].disks = g.disks
				if len(g.disks) == 0 {
					p.groups = append(p.groups[:gi:gi], p.groups[gi+1:]...)
				}
				return nil
			}
		}
	}
	return fmt.Errorf("cannot remove %s: no such device in pool", dev)
}

func (d *Demo) Expand(ctx context.Context, pool string, leaves []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.pools[pool]; !ok {
		return fmt.Errorf("no such pool")
	}
	return nil
}

func (d *Demo) ARC(context.Context) (*ARCStats, error) {
	a := &ARCStats{Size: 6012954214, Target: 6442450944, Max: 8321499136, Hits: 48211934, Misses: 2209113}
	d.mu.Lock()
	for _, p := range d.pools {
		for _, g := range p.groups {
			if g.layout == "cache" {
				a.L2Size, a.L2Hits, a.L2Misses = 120259084288, 1822010, 387114
			}
		}
	}
	d.mu.Unlock()
	a.HitRate = float64(a.Hits*1000/(a.Hits+a.Misses)) / 10
	return a, nil
}

func (d *Demo) ApplySpindown(_ context.Context, rules []SpindownRule) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exInit()
	d.ex.spindown = map[string]int{}
	for _, r := range rules {
		d.ex.spindown[r.ByID] = r.Minutes
	}
	return nil
}

func (d *Demo) Clone(_ context.Context, snap, target string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	ds, _, _ := strings.Cut(snap, "@")
	src, ok := d.datasets[ds]
	if !ok {
		return fmt.Errorf("cannot open '%s': dataset does not exist", snap)
	}
	if _, ok := d.datasets[target]; ok {
		return fmt.Errorf("cannot create '%s': dataset already exists", target)
	}
	pool := poolOfDataset(target)
	root, ok := d.datasets[pool]
	if !ok {
		return fmt.Errorf("no such pool")
	}
	d.addDataset(target, root.Mountpoint+target[len(pool):], 98304, src.Compression, src.CompressRatio, 0)
	return nil
}

func (d *Demo) BrowseSnapshot(_ context.Context, dir string) ([]SnapEntry, error) {
	_, rel, _ := strings.Cut(dir, "/.zfs/snapshot/")
	_, rel, _ = strings.Cut(rel, "/")
	t := time.Now().Add(-48 * time.Hour).UTC()
	if rel != "" {
		return []SnapEntry{{Name: "notes.txt", Size: 2048, Modified: t}, {Name: "photo-0412.jpg", Size: 4718592, Modified: t}}, nil
	}
	return []SnapEntry{{Name: "Albums", Dir: true, Modified: t}, {Name: "Projects", Dir: true, Modified: t},
		{Name: "readme.md", Size: 912, Modified: t}}, nil
}

func (d *Demo) Encryption(_ context.Context, pool string) ([]CryptInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exInit()
	out := []CryptInfo{}
	for n := range d.datasets {
		if n != pool && !strings.HasPrefix(n, pool+"/") {
			continue
		}
		c := CryptInfo{Name: n, Unlocked: true, Mounted: true}
		if _, enc := d.ex.keys[n]; enc {
			c.Encrypted, c.Root = true, n
			c.Unlocked, c.Mounted = !d.ex.locked[n], !d.ex.locked[n]
		}
		out = append(out, c)
	}
	return out, nil
}

func (d *Demo) CreateEncrypted(_ context.Context, name, passphrase string, quota *int64) error {
	if err := d.CreateDataset(context.Background(), name, quota, ""); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exInit()
	d.ex.keys[name] = sha256.Sum256([]byte(passphrase))
	_ = os.MkdirAll(d.FilesPath(d.datasets[name].Mountpoint), 0o755)
	return nil
}

func (d *Demo) Unlock(ctx context.Context, name, passphrase string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exInit()
	want, ok := d.ex.keys[name]
	if !ok {
		return fmt.Errorf("Key load error: encryption is not enabled for %s", name)
	}
	got := sha256.Sum256([]byte(passphrase))
	if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
		return bad("that passphrase isn't right")
	}
	d.ex.locked[name] = false
	return nil
}

func (d *Demo) Lock(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exInit()
	if _, ok := d.ex.keys[name]; !ok {
		return fmt.Errorf("Key unload error: '%s' is not encrypted", name)
	}
	d.ex.locked[name] = true
	return nil
}

func (d *Demo) CreateFlexArray(ctx context.Context, p FlexPlan, devs []string) error {
	nd := len(p.Data)
	for i, dev := range devs {
		label := fmt.Sprintf("array-d%d", i+1)
		if i >= nd {
			label = fmt.Sprintf("array-p%d", i-nd+1)
		}
		if err := d.Format(ctx, dev, label, DiskBase+"/"+label); err != nil {
			return err
		}
	}
	d.mu.Lock()
	d.exInit()
	d.ex.flex = &p
	d.mu.Unlock()
	return nil
}
