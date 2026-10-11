package vm

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Demo is an in-memory host (ALFA_VM_DEMO=1) for trying Virtual Desk
// anywhere. Nothing here runs a real machine: actions change the sample
// machines so the UI can be exercised end to end. The ISO folder holds two
// tiny placeholder files so picking an ISO works.
type Demo struct {
	mu    sync.Mutex
	dir   string
	vms   map[string]*demoVM // by uuid
	delay time.Duration
	start time.Time
}

type demoVM struct {
	vm    VM
	snaps []demoSnap
	cpu   uint64 // ns of CPU time used, grows while running
	at    time.Time
	load  float64 // typical CPU load, 0..1
	mem   float64 // typical share of memory in use
}

type demoSnap struct {
	Snapshot
	saved VM
}

const gb = int64(1) << 30

func NewDemo(dir string) *Demo {
	d := &Demo{dir: dir, vms: map[string]*demoVM{}, delay: 700 * time.Millisecond, start: time.Now()}
	isos := filepath.Join(dir, "isos")
	if err := os.MkdirAll(isos, 0o755); err == nil {
		for _, n := range []string{"ubuntu-24.04-desktop-amd64.iso", "virtio-win-drivers.iso"} {
			p := filepath.Join(isos, n)
			if _, err := os.Stat(p); err != nil {
				_ = os.WriteFile(p, []byte("Virtual Desk demo placeholder, not a real disc image.\n"), 0o644)
			}
		}
	}
	now := time.Now()
	ago := func(d time.Duration) *time.Time { t := now.Add(-d).UTC(); return &t }
	win := &demoVM{vm: VM{Name: "Windows 11", UUID: "5f0c7a1e-3b2d-4c8e-9a61-2d4e8b7f1c03", State: StateRunning, StateReason: "booted",
		OS: OSWindows11, CPUs: 4, MemoryMiB: 8192, Firmware: "uefi", TPM: true,
		Disks:   []Disk{{Target: "sda", Bus: "sata", Path: filepath.Join(dir, "Windows 11", "disk-1.qcow2"), Format: "qcow2", Size: 80 * gb, Used: 31 * gb}},
		Media:   []Media{{Target: "sdb", Bus: "sata"}, {Target: "sdc", Bus: "sata", Path: filepath.Join(isos, "virtio-win-drivers.iso")}},
		Network: Network{Mode: NetNAT, Model: "e1000e", MAC: "52:54:00:3a:7c:01"}, Autostart: true, VNCPort: 5900,
		Created: ago(41 * 24 * time.Hour), Managed: true}, load: 0.22, mem: 0.64}
	ubuntu := &demoVM{vm: VM{Name: "Ubuntu Server", UUID: "a9d34b62-71e0-4f1b-8c55-0e9b2f6d4a17", State: StateOff, StateReason: "shutdown",
		OS: OSLinux, CPUs: 2, MemoryMiB: 4096, Firmware: "bios",
		Disks:   []Disk{{Target: "vda", Bus: "virtio", Path: filepath.Join(dir, "Ubuntu Server", "disk-1.qcow2"), Format: "qcow2", Size: 32 * gb, Used: 7301444608}},
		Media:   []Media{{Target: "sda", Bus: "sata"}},
		Network: Network{Mode: NetBridge, Source: "br0", Model: "virtio", MAC: "52:54:00:9e:21:44"},
		Created: ago(12 * 24 * time.Hour), Managed: true}, load: 0.06, mem: 0.3}
	android := &demoVM{vm: VM{Name: "Android", UUID: "0c6e2f94-8d1a-4b37-a2f5-7e3c9d1b5a68", State: StatePaused, StateReason: "user",
		OS: OSAndroid, CPUs: 4, MemoryMiB: 4096, Firmware: "bios",
		Disks:   []Disk{{Target: "sda", Bus: "sata", Path: filepath.Join(dir, "Android", "disk-1.qcow2"), Format: "qcow2", Size: 16 * gb, Used: 3328599654}},
		Media:   []Media{{Target: "sdb", Bus: "sata"}},
		Network: Network{Mode: NetNAT, Model: "e1000e", MAC: "52:54:00:41:d2:9b"}, VNCPort: 5901,
		Created: ago(5 * 24 * time.Hour), Managed: true}, load: 0.15, mem: 0.71}
	fresh := win.vm
	fresh.State = StateOff
	win.snaps = []demoSnap{
		{Snapshot{Name: "fresh-install", Created: now.Add(-40 * 24 * time.Hour).UTC(), State: StateOff}, fresh},
		{Snapshot{Name: "before-updates", Created: now.Add(-3 * 24 * time.Hour).UTC(), State: StateRunning, Current: true}, win.vm},
	}
	ubuntu.snaps = []demoSnap{{Snapshot{Name: "clean-setup", Created: now.Add(-11 * 24 * time.Hour).UTC(), State: StateOff, Current: true}, ubuntu.vm}}
	for _, v := range []*demoVM{win, ubuntu, android} {
		v.at = now
		d.vms[v.vm.UUID] = v
	}
	return d
}

func (d *Demo) wait(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(d.delay):
	}
}

func (d *Demo) get(v *VM) (*demoVM, error) {
	x, ok := d.vms[v.UUID]
	if !ok {
		return nil, notFound("there's no virtual machine called %q", v.Name)
	}
	return x, nil
}

// tick advances a machine's CPU counter to now.
func (d *Demo) tick(x *demoVM, now time.Time) {
	if x.vm.State == StateRunning {
		el := now.Sub(x.at).Seconds()
		wave := x.load * (1 + 0.6*math.Sin(now.Sub(d.start).Seconds()/7+float64(len(x.vm.Name))))
		x.cpu += uint64(el * wave * float64(x.vm.CPUs) * 1e9)
	}
	x.at = now
}

func (d *Demo) Tools(context.Context) Tools {
	return Tools{Virsh: true, QemuImg: true, KVM: true, Libvirtd: true, Swtpm: true, OVMF: true, Version: "QEMU 8.2.2 (demo)"}
}

func (d *Demo) Install(ctx context.Context) error { d.wait(ctx); return nil }

func (d *Demo) Host(context.Context) HostInfo {
	isos := filepath.Join(d.dir, "isos")
	return HostInfo{CPUs: 16, MemoryMiB: 32768, ISODir: isos, ISOs: listISOs(isos), Interfaces: []HostIface{
		{Name: "enp3s0", Kind: "ethernet"}, {Name: "br0", Kind: "bridge"}, {Name: "wlp4s0", Kind: "wireless"}}}
}

func copyVM(v VM) VM {
	v.Disks = append([]Disk(nil), v.Disks...)
	v.Media = append([]Media(nil), v.Media...)
	return v
}

func (d *Demo) List(context.Context) ([]VM, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []VM{}
	for _, x := range d.vms {
		v := copyVM(x.vm)
		if !v.Running() {
			v.VNCPort = 0
		}
		out = append(out, v)
	}
	return out, nil
}

func (d *Demo) Stats(context.Context) (map[string]rawStats, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	out := map[string]rawStats{}
	for _, x := range d.vms {
		if !x.vm.Running() {
			continue
		}
		d.tick(x, now)
		total := int64(x.vm.MemoryMiB) << 20
		used := x.mem * (1 + 0.05*math.Sin(now.Sub(d.start).Seconds()/11))
		out[x.vm.Name] = rawStats{CPUTime: x.cpu, VCPUs: x.vm.CPUs, MemTotal: total, MemUsed: int64(float64(total) * min(used, 0.97))}
	}
	return out, nil
}

func (d *Demo) Define(ctx context.Context, s spec) error {
	d.wait(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, x := range d.vms {
		if strings.EqualFold(x.vm.Name, s.Name) {
			return bad("there's already a virtual machine called %s", s.Name)
		}
	}
	created := s.Created.UTC()
	v := VM{Name: s.Name, UUID: s.UUID, State: StateOff, StateReason: "unknown", OS: s.OS, CPUs: s.CPUs, MemoryMiB: s.MemoryMiB,
		Firmware: "bios", TPM: s.TPM, Network: s.Network, Created: &created, Managed: true, Disks: []Disk{}, Media: []Media{}}
	if s.UEFI {
		v.Firmware = "uefi"
	}
	if v.Network.Mode != NetNone && v.Network.MAC == "" {
		v.Network.MAC = "52:54:00:" + s.UUID[0:2] + ":" + s.UUID[2:4] + ":" + s.UUID[4:6]
	}
	if v.Network.Mode == NetNone {
		v.Network = Network{Mode: NetNone}
	}
	for _, sd := range s.Disks {
		dk := Disk{Target: sd.Target, Bus: sd.Bus, Path: sd.Path, Format: sd.Format, Size: sd.NewSize, Used: 196608}
		if sd.CopyFrom != "" {
			for _, x := range d.vms {
				for _, src := range x.vm.Disks {
					if src.Path == sd.CopyFrom {
						dk.Size, dk.Used = src.Size, src.Used
					}
				}
			}
		} else if sd.NewSize == 0 { // an existing image
			dk.Size, dk.Used = 20*gb, 6*gb
			if fi, err := os.Stat(sd.Path); err == nil && fi.Size() > dk.Used {
				dk.Used = fi.Size()
			}
		}
		v.Disks = append(v.Disks, dk)
	}
	for _, cd := range s.CDROMs {
		v.Media = append(v.Media, Media{Target: cd.Target, Bus: cd.Bus, Path: cd.Path})
	}
	d.vms[s.UUID] = &demoVM{vm: v, at: time.Now(), load: 0.12, mem: 0.45}
	return nil
}

func (d *Demo) Power(ctx context.Context, v *VM, action string) error {
	d.wait(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	x, err := d.get(v)
	if err != nil {
		return err
	}
	now := time.Now()
	d.tick(x, now)
	switch action {
	case "start":
		x.vm.State, x.vm.StateReason, x.vm.VNCPort = StateRunning, "booted", d.freePort()
		if len(x.vm.Disks) > 0 && x.vm.Disks[0].Used < gb {
			x.vm.Disks[0].Used += 512 << 20 // the installer has started writing
		}
	case "shutdown":
		x.vm.State, x.vm.StateReason = StateOff, "shutdown"
	case "poweroff":
		x.vm.State, x.vm.StateReason = StateOff, "destroyed"
	case "reboot":
		x.vm.StateReason = "booted"
	case "pause":
		x.vm.State, x.vm.StateReason = StatePaused, "user"
	case "resume":
		x.vm.State, x.vm.StateReason = StateRunning, "unpaused"
	default:
		return bad("unknown action %q", action)
	}
	if !x.vm.Running() {
		x.vm.VNCPort = 0
	}
	return nil
}

func (d *Demo) freePort() int {
	used := map[int]bool{}
	for _, x := range d.vms {
		used[x.vm.VNCPort] = true
	}
	for p := 5900; ; p++ {
		if !used[p] {
			return p
		}
	}
}

func (d *Demo) Undefine(ctx context.Context, v *VM, _ []string) error {
	d.wait(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.get(v); err != nil {
		return err
	}
	delete(d.vms, v.UUID)
	return nil
}

func (d *Demo) edit(v *VM, fn func(x *demoVM) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	x, err := d.get(v)
	if err != nil {
		return err
	}
	return fn(x)
}

func (d *Demo) Rename(ctx context.Context, v *VM, name string) error {
	d.wait(ctx)
	return d.edit(v, func(x *demoVM) error { x.vm.Name = name; return nil })
}

func (d *Demo) SetCPUs(_ context.Context, v *VM, n int) error {
	return d.edit(v, func(x *demoVM) error { x.vm.CPUs = n; return nil })
}

func (d *Demo) SetMemory(_ context.Context, v *VM, mib int) error {
	return d.edit(v, func(x *demoVM) error { x.vm.MemoryMiB = mib; return nil })
}

func (d *Demo) AddDisk(ctx context.Context, v *VM, sd specDisk) error {
	d.wait(ctx)
	return d.edit(v, func(x *demoVM) error {
		x.vm.Disks = append(x.vm.Disks, Disk{Target: sd.Target, Bus: sd.Bus, Path: sd.Path, Format: "qcow2", Size: sd.NewSize, Used: 196608})
		return nil
	})
}

func (d *Demo) ResizeDisk(_ context.Context, v *VM, dk Disk, size int64) error {
	return d.edit(v, func(x *demoVM) error {
		for i := range x.vm.Disks {
			if x.vm.Disks[i].Target == dk.Target {
				x.vm.Disks[i].Size = size
			}
		}
		return nil
	})
}

func (d *Demo) ChangeMedia(_ context.Context, v *VM, m Media, path string) error {
	return d.edit(v, func(x *demoVM) error {
		for i := range x.vm.Media {
			if x.vm.Media[i].Target == m.Target {
				x.vm.Media[i].Path = path
			}
		}
		return nil
	})
}

func (d *Demo) SetNetwork(_ context.Context, v *VM, n Network) error {
	return d.edit(v, func(x *demoVM) error {
		mac := x.vm.Network.MAC
		x.vm.Network = n
		if n.Mode == NetNone {
			x.vm.Network = Network{Mode: NetNone}
		} else if mac != "" {
			x.vm.Network.MAC = mac
		}
		return nil
	})
}

func (d *Demo) SetAutostart(_ context.Context, v *VM, on bool) error {
	return d.edit(v, func(x *demoVM) error { x.vm.Autostart = on; return nil })
}

func (d *Demo) Snapshots(_ context.Context, v *VM) ([]Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	x, err := d.get(v)
	if err != nil {
		return nil, err
	}
	out := []Snapshot{}
	for _, s := range x.snaps {
		out = append(out, s.Snapshot)
	}
	return out, nil
}

func (d *Demo) Snapshot(ctx context.Context, v *VM, name string) error {
	d.wait(ctx)
	return d.edit(v, func(x *demoVM) error {
		for i := range x.snaps {
			x.snaps[i].Current = false
		}
		x.snaps = append(x.snaps, demoSnap{Snapshot{Name: name, Created: time.Now().UTC(), State: x.vm.State, Current: true}, copyVM(x.vm)})
		return nil
	})
}

func (d *Demo) Revert(ctx context.Context, v *VM, name string) error {
	d.wait(ctx)
	return d.edit(v, func(x *demoVM) error {
		for i := range x.snaps {
			x.snaps[i].Current = x.snaps[i].Name == name
			if x.snaps[i].Name == name {
				s := copyVM(x.snaps[i].saved)
				s.Name, s.Autostart = x.vm.Name, x.vm.Autostart // like libvirt: the name and autostart aren't snapshotted
				if s.State == StateRunning || s.State == StatePaused {
					s.VNCPort = d.freePort()
				}
				x.vm = s
			}
		}
		return nil
	})
}

func (d *Demo) DeleteSnapshot(_ context.Context, v *VM, name string) error {
	return d.edit(v, func(x *demoVM) error {
		out := x.snaps[:0]
		for _, s := range x.snaps {
			if s.Name != name {
				out = append(out, s)
			}
		}
		x.snaps = out
		return nil
	})
}

func (d *Demo) VNCPort(_ context.Context, v *VM) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	x, err := d.get(v)
	if err != nil {
		return 0, err
	}
	return x.vm.VNCPort, nil
}
