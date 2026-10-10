package vm

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/syspkg"
)

// Dir is where Virtual Desk keeps machines' disks (and isos/) under the data
// dir. Demo mode uses its own sandbox so it never mixes with real machines.
func Dir(dataDir string, demo bool) string {
	if demo {
		return filepath.Join(dataDir, "vm-demo")
	}
	return filepath.Join(dataDir, "vms")
}

type Options struct {
	Backend Backend
	// Dir holds new machines' disks (see Dir).
	Dir string
	// Roots lists the folders disk images and ISOs may be picked from (the
	// Files locations); Dir is always allowed too.
	Roots func() []string
	Log   *slog.Logger
	Demo  bool
	// Host checks, replaceable in tests.
	Native     func() bool
	CanInstall func() bool
}

type Manager struct {
	be      Backend
	dir     string
	roots   func() []string
	log     *slog.Logger
	demo    bool
	native  func() bool
	canInst func() bool

	mu       sync.Mutex
	busy     map[string]string    // uuid -> what's running
	samples  map[string]cpuSample // name -> last CPU reading
	expected map[string]time.Time // uuid -> when NoCapOS stopped it on purpose
	ready    *readyCache
}

type cpuSample struct {
	cpu uint64
	at  time.Time
}

type readyCache struct {
	err error
	at  time.Time
}

const readyMaxAge = 15 * time.Second

func NewManager(o Options) *Manager {
	if o.Native == nil {
		o.Native = syspkg.Native
	}
	if o.CanInstall == nil {
		o.CanInstall = syspkg.CanInstall
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Roots == nil {
		o.Roots = func() []string { return nil }
	}
	return &Manager{be: o.Backend, dir: o.Dir, roots: o.Roots, log: o.Log, demo: o.Demo, native: o.Native, canInst: o.CanInstall,
		busy: map[string]string{}, samples: map[string]cpuSample{}, expected: map[string]time.Time{}}
}

// ---- status ----

func unsupportedReason() string {
	switch {
	case runtime.GOOS != "linux":
		return "Virtual machines need NoCapOS installed directly on a Linux server with hardware virtualization. This copy runs on " + osName() + ", so Virtual Desk can only explain what it does."
	case os.Geteuid() != 0:
		return "NoCapOS needs to run as root to manage virtual machines. Start the NoCapOS service as root to use Virtual Desk."
	}
	return "NoCapOS is running inside a container, so it can't run virtual machines on the server. Install NoCapOS directly on a Linux server to use Virtual Desk."
}

func osName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	}
	return runtime.GOOS
}

const (
	hintPackages = "Install the virtualization packages (QEMU/KVM and libvirt)."
	hintKVM      = "This server's processor has hardware virtualization turned off, or doesn't have it. Turn on Intel VT-x or AMD-V (sometimes called SVM) in the firmware settings, then restart the server."
	hintLibvirt  = "The libvirt service isn't running. Install the virtualization packages again to start it, or run: systemctl enable --now libvirtd"
)

func (m *Manager) Status(ctx context.Context) Status {
	st := Status{Demo: m.demo, Native: m.native(), Presets: Presets()}
	if !m.demo && !st.Native {
		st.Reason = unsupportedReason()
		return st
	}
	t := m.be.Tools(ctx)
	st.Tools = t
	pkgs := false
	if !t.Virsh {
		st.Missing, pkgs = append(st.Missing, "virsh"), true
	}
	if !t.QemuImg {
		st.Missing, pkgs = append(st.Missing, "qemu-img"), true
	}
	if t.Virsh && !t.Libvirtd {
		st.Missing, pkgs = append(st.Missing, "libvirtd"), true
	}
	if !t.KVM {
		st.Missing = append(st.Missing, "kvm")
	}
	st.Available = len(st.Missing) == 0
	switch {
	case pkgs && t.Virsh && !t.Libvirtd && t.QemuImg:
		st.Hint = hintLibvirt
	case pkgs:
		st.Hint = hintPackages
	case !t.KVM:
		st.Hint = hintKVM
	}
	// Install also adds the optional UEFI firmware and TPM emulator.
	st.CanInstall = (m.demo || m.canInst()) && (pkgs || !t.OVMF || !t.Swtpm)
	st.Host = m.be.Host(ctx)
	if !st.Available {
		st.Reason = "Virtual Desk isn't ready on this server yet."
	}
	return st
}

// checkReady fails with 503 until the host can run machines (cached briefly).
func (m *Manager) checkReady(ctx context.Context) error {
	m.mu.Lock()
	c := m.ready
	m.mu.Unlock()
	if c != nil && time.Since(c.at) < readyMaxAge {
		return c.err
	}
	var err error
	st := m.Status(ctx)
	switch {
	case st.Reason != "" && !st.Available && st.Hint == "":
		err = unavailable(st.Reason)
	case !st.Available:
		err = unavailable(st.Hint)
	}
	m.mu.Lock()
	m.ready = &readyCache{err: err, at: time.Now()}
	m.mu.Unlock()
	return err
}

func (m *Manager) forgetReady() {
	m.mu.Lock()
	m.ready = nil
	m.mu.Unlock()
}

// Install installs the virtualization packages.
func (m *Manager) Install(ctx context.Context) error {
	if !m.demo && !m.native() {
		return unavailable(unsupportedReason())
	}
	defer m.forgetReady()
	return m.be.Install(ctx)
}

// ---- locks ----

func (m *Manager) lock(v *VM, what string) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.busy[v.UUID]; ok {
		return nil, busy("%s is busy (%s); try again when that finishes", v.Name, cur)
	}
	m.busy[v.UUID] = what
	return func() {
		m.mu.Lock()
		delete(m.busy, v.UUID)
		m.mu.Unlock()
	}, nil
}

func (m *Manager) expect(v *VM) {
	m.mu.Lock()
	m.expected[v.UUID] = time.Now()
	m.mu.Unlock()
}

// ---- listing ----

func (m *Manager) List(ctx context.Context) ([]VM, error) {
	if err := m.checkReady(ctx); err != nil {
		return nil, err
	}
	vms, err := m.be.List(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	for i := range vms {
		vms[i].Busy = m.busy[vms[i].UUID]
	}
	m.mu.Unlock()
	sort.Slice(vms, func(i, j int) bool { return strings.ToLower(vms[i].Name) < strings.ToLower(vms[j].Name) })
	return vms, nil
}

// Get finds a machine by name.
func (m *Manager) Get(ctx context.Context, name string) (*VM, error) {
	vms, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range vms {
		if vms[i].Name == name {
			return &vms[i], nil
		}
	}
	return nil, notFound("there's no virtual machine called %q", name)
}

func (m *Manager) nameTaken(ctx context.Context, name string) (bool, error) {
	vms, err := m.List(ctx)
	if err != nil {
		return false, err
	}
	for _, v := range vms {
		if strings.EqualFold(v.Name, name) {
			return true, nil
		}
	}
	return false, nil
}

// Stats returns live CPU and memory use of running machines, by name. CPU is
// measured between two calls, so the first call after a start reports 0.
func (m *Manager) Stats(ctx context.Context) (map[string]Usage, error) {
	if err := m.checkReady(ctx); err != nil {
		return nil, err
	}
	raw, err := m.be.Stats(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := map[string]Usage{}
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, r := range raw {
		u := Usage{MemUsed: r.MemUsed, MemTotal: r.MemTotal}
		if prev, ok := m.samples[name]; ok && r.CPUTime >= prev.cpu && r.VCPUs > 0 {
			if el := now.Sub(prev.at); el > 0 {
				u.CPU = float64(r.CPUTime-prev.cpu) / float64(el.Nanoseconds()*int64(r.VCPUs)) * 100
				u.CPU = max(0, min(100, u.CPU))
			}
		}
		m.samples[name] = cpuSample{cpu: r.CPUTime, at: now}
		out[name] = u
	}
	for name := range m.samples {
		if _, ok := raw[name]; !ok {
			delete(m.samples, name)
		}
	}
	return out, nil
}

// ---- create ----

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// allowedRoots: the Files locations plus Virtual Desk's own folder.
func (m *Manager) allowedRoots() []string {
	return append(m.roots(), m.dir)
}

var diskFormats = map[string]string{".qcow2": "qcow2", ".img": "raw", ".raw": "raw", ".vmdk": "vmdk", ".vdi": "vdi", ".vhd": "vpc", ".vhdx": "vhdx"}

func (m *Manager) checkISO(p string) (string, error) {
	return checkImage(p, m.allowedRoots(), isoExts, "ISO")
}

func (m *Manager) Create(ctx context.Context, req CreateRequest) (*VM, error) {
	if err := m.checkReady(ctx); err != nil {
		return nil, err
	}
	req.Name = strings.TrimSpace(req.Name)
	if err := ValidName(req.Name); err != nil {
		return nil, err
	}
	if taken, err := m.nameTaken(ctx, req.Name); err != nil {
		return nil, err
	} else if taken {
		return nil, bad("there's already a virtual machine called %s", req.Name)
	}
	p, ok := presetFor(req.OS)
	if !ok {
		return nil, bad("pick what you'll install: Windows 11, Windows 10, Linux, Android or Other")
	}
	host := m.be.Host(ctx)
	tools := m.be.Tools(ctx)
	if err := m.checkSize(&req.CPUs, &req.MemoryMiB, p, host); err != nil {
		return nil, err
	}
	virtio := p.Virtio
	if req.Virtio != nil {
		virtio = *req.Virtio
	}
	if p.UEFI && !tools.OVMF && req.OS == OSWindows11 {
		return nil, bad("Windows 11 needs UEFI firmware (OVMF) on the server. Use Install in Virtual Desk to add it, then try again")
	}
	n := req.Network
	if n.Mode == "" {
		n.Mode = NetNAT
	}
	n.MAC = ""
	if n.Model == "" {
		n.Model = "e1000e"
		if virtio {
			n.Model = "virtio"
		}
	}
	if err := validNetwork(n, host.Interfaces); err != nil {
		return nil, err
	}
	if err := validModel(n.Model); err != nil {
		return nil, err
	}
	s := spec{Name: req.Name, UUID: newUUID(), OS: req.OS, CPUs: req.CPUs, MemoryMiB: req.MemoryMiB,
		UEFI: p.UEFI && tools.OVMF, TPM: p.TPM && tools.Swtpm, Virtio: virtio, Video: p.Video, Network: n, Created: time.Now()}
	bus := "sata"
	if virtio {
		bus = "virtio"
	}
	used := map[string]bool{}
	if req.DiskImage != "" {
		img, err := checkImage(req.DiskImage, m.allowedRoots(), diskExts, "disk image")
		if err != nil {
			return nil, err
		}
		if owner, err := m.diskOwner(ctx, img); err != nil {
			return nil, err
		} else if owner != "" {
			return nil, bad("that disk image already belongs to %s", owner)
		}
		s.Disks = []specDisk{{Path: img, Format: diskFormats[strings.ToLower(filepath.Ext(img))], Bus: bus, Target: diskTarget(bus, used)}}
	} else {
		if req.DiskGiB == 0 {
			req.DiskGiB = p.DiskGiB
		}
		if req.DiskGiB < minDiskGiB || req.DiskGiB > maxDiskGiB {
			return nil, bad("the disk must be between %d GB and %d TB", minDiskGiB, maxDiskGiB>>10)
		}
		s.Disks = []specDisk{{Path: filepath.Join(m.dir, req.Name, "disk-1.qcow2"), Format: "qcow2", Bus: bus,
			Target: diskTarget(bus, used), NewSize: gib(req.DiskGiB)}}
	}
	// One CD drive always, so an ISO can be inserted later; a second for drivers.
	cd := specDisk{Bus: "sata"}
	if req.ISO != "" {
		iso, err := m.checkISO(req.ISO)
		if err != nil {
			return nil, err
		}
		cd.Path = iso
	}
	cd.Target = diskTarget("sata", used)
	s.CDROMs = []specDisk{cd}
	if req.ISO2 != "" {
		iso, err := m.checkISO(req.ISO2)
		if err != nil {
			return nil, err
		}
		s.CDROMs = append(s.CDROMs, specDisk{Path: iso, Bus: "sata", Target: diskTarget("sata", used)})
	}
	if err := m.be.Define(ctx, s); err != nil {
		return nil, err
	}
	v, err := m.Get(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if req.Autostart {
		if err := m.be.SetAutostart(ctx, v, true); err != nil {
			return v, fmt.Errorf("%s was created, but autostart couldn't be turned on: %w", v.Name, err)
		}
	}
	if req.Start {
		if err := m.be.Power(ctx, v, "start"); err != nil {
			return v, fmt.Errorf("%s was created, but didn't start: %w", v.Name, err)
		}
	}
	return m.Get(ctx, req.Name)
}

// checkSize fills in preset sizes and keeps them within the host's limits.
func (m *Manager) checkSize(cpus, mem *int, p Preset, host HostInfo) error {
	if *cpus == 0 {
		*cpus = min(p.CPUs, max(1, host.CPUs))
	}
	if *mem == 0 {
		*mem = p.MemoryMiB
		if host.MemoryMiB > 0 {
			*mem = min(*mem, max(minMemoryMiB, host.MemoryMiB/2))
		}
	}
	maxC := maxCPUs
	if host.CPUs > 0 {
		maxC = min(maxC, host.CPUs)
	}
	if *cpus < 1 || *cpus > maxC {
		return bad("processors must be between 1 and %d", maxC)
	}
	maxM := maxMemoryMiB
	if host.MemoryMiB > 0 {
		maxM = min(maxM, host.MemoryMiB)
	}
	if *mem < minMemoryMiB || *mem > maxM {
		return bad("memory must be between %d MB and %d MB", minMemoryMiB, maxM)
	}
	return nil
}

// diskOwner names the machine already using an image, if any.
func (m *Manager) diskOwner(ctx context.Context, path string) (string, error) {
	vms, err := m.List(ctx)
	if err != nil {
		return "", err
	}
	for _, v := range vms {
		for _, d := range v.Disks {
			if d.Path == path {
				return v.Name, nil
			}
		}
	}
	return "", nil
}

// ---- power ----

// Actions: start, shutdown (ACPI, the guest decides), poweroff (pull the
// plug), reboot, pause, resume.
func (m *Manager) Action(ctx context.Context, name, action string) error {
	v, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	switch action {
	case "start":
		if v.State != StateOff && v.State != StateCrashed {
			return bad("%s is already running", v.Name)
		}
	case "shutdown", "reboot", "pause":
		if v.State != StateRunning {
			return bad("%s isn't running", v.Name)
		}
	case "resume":
		if v.State != StatePaused {
			return bad("%s isn't paused", v.Name)
		}
	case "poweroff":
		if !v.Running() && v.State != StateCrashed {
			return bad("%s is already off", v.Name)
		}
	default:
		return bad("unknown action %q", action)
	}
	unlock, err := m.lock(v, action)
	if err != nil {
		return err
	}
	defer unlock()
	if action == "shutdown" || action == "poweroff" {
		m.expect(v)
	}
	return m.be.Power(ctx, v, action)
}

// ---- delete, clone, rename ----

// Delete removes a machine; confirm must be its name. With disks, the disk
// files Virtual Desk made (inside its folder) go too; images picked from
// elsewhere are always kept.
func (m *Manager) Delete(ctx context.Context, name, confirm string, disks bool) error {
	v, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if confirm != v.Name {
		return bad("type %s to confirm", v.Name)
	}
	unlock, err := m.lock(v, "deleting")
	if err != nil {
		return err
	}
	defer unlock()
	if v.Running() || v.State == StateCrashed {
		m.expect(v)
		if err := m.be.Power(ctx, v, "poweroff"); err != nil && v.Running() {
			return fmt.Errorf("couldn't turn %s off first: %w", v.Name, err)
		}
	}
	var files []string
	if disks {
		for _, d := range v.Disks {
			if d.Path != "" && inside(filepath.Clean(d.Path), m.dir) && filepath.Clean(d.Path) != m.dir {
				files = append(files, d.Path)
			}
		}
	}
	return m.be.Undefine(ctx, v, files)
}

func (m *Manager) needOff(v *VM, what string) error {
	if v.Running() {
		return bad("shut %s down to %s", v.Name, what)
	}
	return nil
}

// Clone copies a stopped machine: its disks are copied into a new folder and
// it gets new identifiers (UUID, network address).
func (m *Manager) Clone(ctx context.Context, name, newName string) error {
	newName = strings.TrimSpace(newName)
	if err := ValidName(newName); err != nil {
		return err
	}
	v, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if err := m.needOff(v, "clone it"); err != nil {
		return err
	}
	if taken, err := m.nameTaken(ctx, newName); err != nil {
		return err
	} else if taken {
		return bad("there's already a virtual machine called %s", newName)
	}
	unlock, err := m.lock(v, "cloning")
	if err != nil {
		return err
	}
	defer unlock()
	p, _ := presetFor(v.OS)
	if p.ID == "" {
		p, _ = presetFor(OSOther)
	}
	s := spec{Name: newName, UUID: newUUID(), OS: v.OS, CPUs: v.CPUs, MemoryMiB: v.MemoryMiB, UEFI: v.Firmware == "uefi",
		TPM: v.TPM, Video: p.Video, Created: time.Now(), Network: v.Network}
	s.Network.MAC = ""
	if s.Network.Model == "" {
		s.Network.Model = "virtio"
	}
	for i, d := range v.Disks {
		if d.Path == "" {
			return bad("%s has a disk that isn't a file, so it can't be cloned", v.Name)
		}
		if d.Bus == "virtio" {
			s.Virtio = true
		}
		s.Disks = append(s.Disks, specDisk{Path: filepath.Join(m.dir, newName, fmt.Sprintf("disk-%d.qcow2", i+1)), Format: "qcow2",
			Target: d.Target, Bus: d.Bus, CopyFrom: d.Path})
	}
	for _, md := range v.Media {
		s.CDROMs = append(s.CDROMs, specDisk{Path: md.Path, Target: md.Target, Bus: md.Bus})
	}
	return m.be.Define(ctx, s)
}

// Rename renames a stopped machine (its disk files stay where they are).
func (m *Manager) Rename(ctx context.Context, name, newName string) error {
	newName = strings.TrimSpace(newName)
	if err := ValidName(newName); err != nil {
		return err
	}
	v, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if newName == v.Name {
		return nil
	}
	if err := m.needOff(v, "rename it"); err != nil {
		return err
	}
	if !strings.EqualFold(newName, v.Name) {
		if taken, err := m.nameTaken(ctx, newName); err != nil {
			return err
		} else if taken {
			return bad("there's already a virtual machine called %s", newName)
		}
	}
	unlock, err := m.lock(v, "renaming")
	if err != nil {
		return err
	}
	defer unlock()
	return m.be.Rename(ctx, v, newName)
}

// ---- settings ----

// Update applies setting changes after checking all of them first.
func (m *Manager) Update(ctx context.Context, name string, req UpdateRequest) error {
	v, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if (req.CPUs != nil || req.MemoryMiB != nil || req.AddDiskGiB > 0 || len(req.Resize) > 0 || req.Network != nil) && v.Running() {
		return bad("shut %s down to change its processors, memory, disks or network", v.Name)
	}
	host := m.be.Host(ctx)
	p, _ := presetFor(v.OS)
	if req.CPUs != nil || req.MemoryMiB != nil {
		cpus, mem := v.CPUs, v.MemoryMiB
		if req.CPUs != nil {
			cpus = *req.CPUs
		}
		if req.MemoryMiB != nil {
			mem = *req.MemoryMiB
		}
		if cpus == 0 || mem == 0 {
			return bad("processors and memory can't be zero")
		}
		if err := m.checkSize(&cpus, &mem, p, host); err != nil {
			return err
		}
	}
	diskBy := map[string]Disk{}
	used := map[string]bool{}
	for _, d := range v.Disks {
		diskBy[d.Target] = d
		used[d.Target] = true
	}
	mediaBy := map[string]Media{}
	for _, md := range v.Media {
		mediaBy[md.Target] = md
		used[md.Target] = true
	}
	for _, r := range req.Resize {
		d, ok := diskBy[r.Target]
		if !ok {
			return bad("%s has no disk %s", v.Name, r.Target)
		}
		if r.SizeGiB > maxDiskGiB {
			return bad("disks can be at most %d TB", maxDiskGiB>>10)
		}
		if gib(r.SizeGiB) <= d.Size {
			return bad("disks can only grow; %s is already %d GB", r.Target, d.Size>>30)
		}
	}
	var add *specDisk
	if req.AddDiskGiB != 0 {
		if req.AddDiskGiB < minDiskGiB || req.AddDiskGiB > maxDiskGiB {
			return bad("the new disk must be between %d GB and %d TB", minDiskGiB, maxDiskGiB>>10)
		}
		if len(v.Disks) >= maxDisks {
			return bad("a machine can have at most %d disks", maxDisks)
		}
		bus := "sata"
		if len(v.Disks) > 0 && v.Disks[0].Bus == "virtio" || len(v.Disks) == 0 && p.Virtio {
			bus = "virtio"
		}
		add = &specDisk{Path: m.newDiskPath(v), Format: "qcow2", Bus: bus, Target: diskTarget(bus, used), NewSize: gib(req.AddDiskGiB)}
	}
	media := map[string]string{}
	for _, mc := range req.Media {
		if _, ok := mediaBy[mc.Target]; !ok {
			return bad("%s has no CD drive %s", v.Name, mc.Target)
		}
		if mc.Path == "" {
			media[mc.Target] = ""
			continue
		}
		iso, err := m.checkISO(mc.Path)
		if err != nil {
			return err
		}
		media[mc.Target] = iso
	}
	if req.Network != nil {
		n := *req.Network
		if n.Model == "" {
			n.Model = v.Network.Model
		}
		if err := validNetwork(n, host.Interfaces); err != nil {
			return err
		}
		if err := validModel(n.Model); err != nil {
			return err
		}
		req.Network = &n
	}

	unlock, err := m.lock(v, "changing settings")
	if err != nil {
		return err
	}
	defer unlock()
	if req.CPUs != nil && *req.CPUs != v.CPUs {
		if err := m.be.SetCPUs(ctx, v, *req.CPUs); err != nil {
			return fmt.Errorf("couldn't change processors: %w", err)
		}
	}
	if req.MemoryMiB != nil && *req.MemoryMiB != v.MemoryMiB {
		if err := m.be.SetMemory(ctx, v, *req.MemoryMiB); err != nil {
			return fmt.Errorf("couldn't change memory: %w", err)
		}
	}
	for _, r := range req.Resize {
		if err := m.be.ResizeDisk(ctx, v, diskBy[r.Target], gib(r.SizeGiB)); err != nil {
			return fmt.Errorf("couldn't grow %s: %w", r.Target, err)
		}
	}
	if add != nil {
		if err := m.be.AddDisk(ctx, v, *add); err != nil {
			return fmt.Errorf("couldn't add the disk: %w", err)
		}
	}
	if req.Network != nil {
		if err := m.be.SetNetwork(ctx, v, *req.Network); err != nil {
			return fmt.Errorf("couldn't change the network: %w", err)
		}
	}
	for _, md := range v.Media {
		path, ok := media[md.Target]
		if !ok || path == md.Path {
			continue
		}
		if err := m.be.ChangeMedia(ctx, v, md, path); err != nil {
			return fmt.Errorf("couldn't change the disc in %s: %w", md.Target, err)
		}
	}
	if req.Autostart != nil && *req.Autostart != v.Autostart {
		if err := m.be.SetAutostart(ctx, v, *req.Autostart); err != nil {
			return fmt.Errorf("couldn't change autostart: %w", err)
		}
	}
	return nil
}

// newDiskPath picks disk-N.qcow2 in the machine's folder that is free.
func (m *Manager) newDiskPath(v *VM) string {
	taken := map[string]bool{}
	for _, d := range v.Disks {
		taken[filepath.Clean(d.Path)] = true
	}
	for i := 1; ; i++ {
		p := filepath.Join(m.dir, v.Name, fmt.Sprintf("disk-%d.qcow2", i))
		if taken[p] {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			continue
		}
		return p
	}
}

// ---- snapshots ----

func (m *Manager) Snapshots(ctx context.Context, name string) ([]Snapshot, error) {
	v, err := m.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	snaps, err := m.be.Snapshots(ctx, v)
	if err != nil {
		return nil, err
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Created.After(snaps[j].Created) })
	return snaps, nil
}

func (m *Manager) findSnapshot(ctx context.Context, v *VM, snap string) error {
	if err := ValidSnapshotName(snap); err != nil {
		return err
	}
	snaps, err := m.be.Snapshots(ctx, v)
	if err != nil {
		return err
	}
	for _, s := range snaps {
		if s.Name == snap {
			return nil
		}
	}
	return notFound("%s has no snapshot called %s", v.Name, snap)
}

// snapshotError explains libvirt's refusal to snapshot UEFI machines.
func snapshotError(err error) error {
	if err != nil && strings.Contains(err.Error(), "pflash") {
		return bad("this server's libvirt can't take snapshots of machines that use UEFI firmware (%s)", err.Error())
	}
	return err
}

func (m *Manager) CreateSnapshot(ctx context.Context, name, snap string) error {
	if err := ValidSnapshotName(snap); err != nil {
		return err
	}
	v, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if err := m.findSnapshot(ctx, v, snap); err == nil {
		return bad("%s already has a snapshot called %s", v.Name, snap)
	}
	unlock, err := m.lock(v, "taking a snapshot")
	if err != nil {
		return err
	}
	defer unlock()
	return snapshotError(m.be.Snapshot(ctx, v, snap))
}

func (m *Manager) RevertSnapshot(ctx context.Context, name, snap string) error {
	v, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if err := m.findSnapshot(ctx, v, snap); err != nil {
		return err
	}
	unlock, err := m.lock(v, "restoring a snapshot")
	if err != nil {
		return err
	}
	defer unlock()
	m.expect(v)
	return snapshotError(m.be.Revert(ctx, v, snap))
}

func (m *Manager) DeleteSnapshot(ctx context.Context, name, snap string) error {
	v, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if err := m.findSnapshot(ctx, v, snap); err != nil {
		return err
	}
	unlock, err := m.lock(v, "deleting a snapshot")
	if err != nil {
		return err
	}
	defer unlock()
	return m.be.DeleteSnapshot(ctx, v, snap)
}

// ---- screen ----

// Console returns the machine's VNC port on 127.0.0.1 (or, in demo mode, a
// message: there's no real screen to show).
func (m *Manager) Console(ctx context.Context, name string) (Console, error) {
	v, err := m.Get(ctx, name)
	if err != nil {
		return Console{}, err
	}
	if !v.Running() {
		return Console{}, bad("start %s to see its screen", v.Name)
	}
	if m.demo {
		return Console{Demo: true, Message: "This is a demo machine, so there's no screen to show. On a Linux server with Virtual Desk, the machine's display appears here."}, nil
	}
	port, err := m.be.VNCPort(ctx, v)
	if err != nil {
		return Console{}, err
	}
	if port < 5900 || port > 65535 {
		return Console{}, bad("this machine has no screen")
	}
	return Console{Port: port}, nil
}

// ---- unexpected stops ----

const (
	watchEvery   = 20 * time.Second
	expectWindow = 5 * time.Minute
)

// Watch calls onStop when a running machine crashes or is killed without
// NoCapOS asking for it (a guest shutting itself down is not reported).
func (m *Manager) Watch(ctx context.Context, onStop func(v VM, why string)) {
	prev := map[string]string{}
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	for {
		if vms, err := m.List(ctx); err == nil {
			next := map[string]string{}
			for _, v := range vms {
				next[v.UUID] = v.State
				was, seen := prev[v.UUID]
				if !seen || (was != StateRunning && was != StatePaused && was != StateStopping) {
					continue
				}
				if why, hit := m.unexpected(v); hit {
					onStop(v, why)
				}
			}
			prev = next
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) unexpected(v VM) (string, bool) {
	m.mu.Lock()
	at, ok := m.expected[v.UUID]
	if ok && time.Since(at) > expectWindow {
		delete(m.expected, v.UUID)
		ok = false
	}
	m.mu.Unlock()
	if ok {
		return "", false
	}
	switch {
	case v.State == StateCrashed:
		return "it crashed", true
	case v.State == StateOff && (v.StateReason == "crashed" || v.StateReason == "failed"):
		return "its process ended unexpectedly", true
	case v.State == StateOff && v.StateReason == "destroyed":
		return "it was forced off outside NoCapOS", true
	}
	return "", false
}
