package vm

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// demoManager is a Manager on the demo host with no artificial delays.
func demoManager(t *testing.T) (*Manager, *Demo) {
	t.Helper()
	dir := t.TempDir()
	d := NewDemo(dir)
	d.delay = 0
	m := NewManager(Options{Backend: d, Dir: dir, Demo: true, Log: quiet(), Native: func() bool { return false }, CanInstall: func() bool { return false }})
	return m, d
}

func status(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

func mustGet(t *testing.T, m *Manager, name string) *VM {
	t.Helper()
	v, err := m.Get(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestUnavailableOffLinux(t *testing.T) {
	m := NewManager(Options{Backend: NewDemo(t.TempDir()), Dir: t.TempDir(), Native: func() bool { return false }})
	st := m.Status(context.Background())
	if st.Available || st.Reason == "" || st.CanInstall {
		t.Errorf("status %+v", st)
	}
	if _, err := m.List(context.Background()); status(err) != http.StatusServiceUnavailable {
		t.Errorf("List without a host: %v", err)
	}
}

func TestDemoStatusAndList(t *testing.T) {
	m, _ := demoManager(t)
	ctx := context.Background()
	st := m.Status(ctx)
	if !st.Available || !st.Demo || len(st.Presets) != 5 || len(st.Host.ISOs) != 2 || st.Host.CPUs == 0 {
		t.Fatalf("status %+v", st)
	}
	vms, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vms {
		got[v.Name] = v.State
	}
	if got["Windows 11"] != StateRunning || got["Ubuntu Server"] != StateOff || got["Android"] != StatePaused || len(vms) != 3 {
		t.Errorf("sample machines: %v", got)
	}
	if vms[0].Name != "Android" {
		t.Errorf("list is sorted by name: %s first", vms[0].Name)
	}
}

func TestDemoPower(t *testing.T) {
	m, _ := demoManager(t)
	ctx := context.Background()
	if err := m.Action(ctx, "Ubuntu Server", "shutdown"); status(err) != http.StatusBadRequest {
		t.Errorf("shutting down a stopped machine: %v", err)
	}
	if err := m.Action(ctx, "Ubuntu Server", "start"); err != nil {
		t.Fatal(err)
	}
	v := mustGet(t, m, "Ubuntu Server")
	if v.State != StateRunning || v.VNCPort < 5900 {
		t.Errorf("after start: %s port %d", v.State, v.VNCPort)
	}
	if err := m.Action(ctx, "Ubuntu Server", "start"); err == nil {
		t.Error("starting twice should fail")
	}
	for _, a := range []string{"pause", "resume", "reboot", "shutdown"} {
		if err := m.Action(ctx, "Ubuntu Server", a); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	if v := mustGet(t, m, "Ubuntu Server"); v.State != StateOff || v.VNCPort != 0 {
		t.Errorf("after shutdown: %s port %d", v.State, v.VNCPort)
	}
	if err := m.Action(ctx, "Android", "resume"); err != nil {
		t.Fatal(err)
	}
	if err := m.Action(ctx, "Android", "poweroff"); err != nil {
		t.Fatal(err)
	}
	if err := m.Action(ctx, "Android", "explode"); err == nil {
		t.Error("unknown actions are refused")
	}
	if err := m.Action(ctx, "Nope", "start"); status(err) != http.StatusNotFound {
		t.Errorf("missing machine: %v", err)
	}
}

func TestDemoCreate(t *testing.T) {
	m, _ := demoManager(t)
	ctx := context.Background()
	iso := m.Status(ctx).Host.ISOs[0].Path
	v, err := m.Create(ctx, CreateRequest{Name: "Dev Box", OS: OSLinux, ISO: iso, Network: Network{Mode: NetBridge, Source: "br0"}, Start: true, Autostart: true})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StateRunning || !v.Autostart || v.CPUs != 2 || v.MemoryMiB != 4096 || v.Firmware != "bios" {
		t.Errorf("preset defaults: %+v", v)
	}
	if len(v.Disks) != 1 || v.Disks[0].Bus != "virtio" || v.Disks[0].Target != "vda" || v.Disks[0].Size != 32<<30 ||
		v.Disks[0].Path != filepath.Join(m.dir, "Dev Box", "disk-1.qcow2") {
		t.Errorf("disk: %+v", v.Disks)
	}
	if len(v.Media) != 1 || v.Media[0].Path != iso || v.Network.Model != "virtio" || v.Network.Source != "br0" {
		t.Errorf("media %+v network %+v", v.Media, v.Network)
	}

	w, err := m.Create(ctx, CreateRequest{Name: "Win", OS: OSWindows11, CPUs: 6, MemoryMiB: 12288, DiskGiB: 100, ISO: iso,
		ISO2: m.Status(ctx).Host.ISOs[1].Path})
	if err != nil {
		t.Fatal(err)
	}
	if w.Firmware != "uefi" || !w.TPM || w.Disks[0].Bus != "sata" || w.Network.Model != "e1000e" || w.Network.Mode != NetNAT || len(w.Media) != 2 {
		t.Errorf("Windows 11 preset: %+v", w)
	}

	outside := filepath.Join(t.TempDir(), "x.iso")
	_ = os.WriteFile(outside, []byte("x"), 0o644)
	for name, req := range map[string]CreateRequest{
		"taken name":       {Name: "dev box", OS: OSLinux},
		"bad name":         {Name: "a/b", OS: OSLinux},
		"unknown os":       {Name: "x", OS: "plan9"},
		"too many cpus":    {Name: "x", OS: OSLinux, CPUs: 64},
		"too much memory":  {Name: "x", OS: OSLinux, MemoryMiB: 1 << 19},
		"too little":       {Name: "x", OS: OSLinux, MemoryMiB: 16},
		"huge disk":        {Name: "x", OS: OSLinux, DiskGiB: 1 << 20},
		"iso outside":      {Name: "x", OS: OSLinux, ISO: outside},
		"no such bridge":   {Name: "x", OS: OSLinux, Network: Network{Mode: NetBridge, Source: "br9"}},
		"not a bridge":     {Name: "x", OS: OSLinux, Network: Network{Mode: NetBridge, Source: "enp3s0"}},
		"bad card":         {Name: "x", OS: OSLinux, Network: Network{Mode: NetNAT, Model: "ne2k"}},
		"disk not allowed": {Name: "x", OS: OSLinux, DiskImage: outside},
	} {
		if _, err := m.Create(ctx, req); status(err) != http.StatusBadRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDemoSettings(t *testing.T) {
	m, _ := demoManager(t)
	ctx := context.Background()
	four, big := 4, 6144
	if err := m.Update(ctx, "Windows 11", UpdateRequest{CPUs: &four}); status(err) != http.StatusBadRequest {
		t.Errorf("changing a running machine's CPUs: %v", err)
	}
	if err := m.Update(ctx, "Ubuntu Server", UpdateRequest{CPUs: &four, MemoryMiB: &big, AddDiskGiB: 50,
		Resize: []DiskResize{{Target: "vda", SizeGiB: 64}}, Network: &Network{Mode: NetNAT}}); err != nil {
		t.Fatal(err)
	}
	v := mustGet(t, m, "Ubuntu Server")
	if v.CPUs != 4 || v.MemoryMiB != 6144 || len(v.Disks) != 2 || v.Disks[0].Size != 64<<30 || v.Disks[1].Target != "vdb" ||
		v.Disks[1].Size != 50<<30 || v.Network.Mode != NetNAT || v.Network.MAC != "52:54:00:9e:21:44" {
		t.Errorf("after update: %+v", v)
	}
	if err := m.Update(ctx, "Ubuntu Server", UpdateRequest{Resize: []DiskResize{{Target: "vda", SizeGiB: 10}}}); err == nil {
		t.Error("disks only grow")
	}
	if err := m.Update(ctx, "Ubuntu Server", UpdateRequest{Resize: []DiskResize{{Target: "vdz", SizeGiB: 100}}}); err == nil {
		t.Error("unknown disk")
	}
	// Media and autostart work while running.
	iso := m.Status(ctx).Host.ISOs[0].Path
	on := false
	if err := m.Update(ctx, "Windows 11", UpdateRequest{Media: []MediaChange{{Target: "sdb", Path: iso}, {Target: "sdc"}}, Autostart: &on}); err != nil {
		t.Fatal(err)
	}
	w := mustGet(t, m, "Windows 11")
	if w.Media[0].Path != iso || w.Media[1].Path != "" || w.Autostart {
		t.Errorf("media %+v autostart %v", w.Media, w.Autostart)
	}
	if err := m.Update(ctx, "Windows 11", UpdateRequest{Media: []MediaChange{{Target: "sda", Path: iso}}}); err == nil {
		t.Error("sda is a disk, not a CD drive")
	}
}

func TestDemoCloneRenameDelete(t *testing.T) {
	m, _ := demoManager(t)
	ctx := context.Background()
	if err := m.Clone(ctx, "Windows 11", "Win copy"); status(err) != http.StatusBadRequest {
		t.Errorf("cloning a running machine: %v", err)
	}
	if err := m.Clone(ctx, "Ubuntu Server", "Ubuntu Copy"); err != nil {
		t.Fatal(err)
	}
	src, cp := mustGet(t, m, "Ubuntu Server"), mustGet(t, m, "Ubuntu Copy")
	if cp.UUID == src.UUID || cp.Network.MAC == src.Network.MAC || cp.CPUs != src.CPUs || cp.Disks[0].Size != src.Disks[0].Size ||
		cp.Disks[0].Path == src.Disks[0].Path || !strings.Contains(cp.Disks[0].Path, "Ubuntu Copy") {
		t.Errorf("clone: %+v", cp)
	}
	if err := m.Clone(ctx, "Ubuntu Server", "ubuntu copy"); err == nil {
		t.Error("clone names must be unique")
	}
	if err := m.Rename(ctx, "Ubuntu Copy", "Lab"); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename(ctx, "Windows 11", "Win"); err == nil {
		t.Error("renaming a running machine should fail")
	}
	if err := m.Delete(ctx, "Lab", "lab", true); status(err) != http.StatusBadRequest {
		t.Errorf("wrong confirmation: %v", err)
	}
	if err := m.Delete(ctx, "Lab", "Lab", true); err != nil {
		t.Fatal(err)
	}
	// Deleting a running machine turns it off first.
	if err := m.Delete(ctx, "Windows 11", "Windows 11", false); err != nil {
		t.Fatal(err)
	}
	vms, _ := m.List(ctx)
	if len(vms) != 2 {
		t.Errorf("left: %d machines", len(vms))
	}
}

// recBackend records Undefine's files to check that only Virtual Desk's own disks go.
type recBackend struct {
	*Demo
	files []string
}

func (r *recBackend) Undefine(ctx context.Context, v *VM, files []string) error {
	r.files = files
	return r.Demo.Undefine(ctx, v, files)
}

func TestDeleteKeepsOutsideImages(t *testing.T) {
	dir := t.TempDir()
	d := NewDemo(dir)
	d.delay = 0
	rb := &recBackend{Demo: d}
	m := NewManager(Options{Backend: rb, Dir: dir, Demo: true, Log: quiet()})
	ctx := context.Background()
	v := mustGet(t, m, "Ubuntu Server")
	d.vms[v.UUID].vm.Disks = append(d.vms[v.UUID].vm.Disks, Disk{Target: "vdb", Bus: "virtio", Path: filepath.Join(filepath.Dir(dir), "shared.qcow2")})
	if err := m.Delete(ctx, "Ubuntu Server", "Ubuntu Server", true); err != nil {
		t.Fatal(err)
	}
	if len(rb.files) != 1 || !inside(rb.files[0], dir) {
		t.Errorf("deleted files %v", rb.files)
	}
}

func TestDemoSnapshots(t *testing.T) {
	m, _ := demoManager(t)
	ctx := context.Background()
	snaps, err := m.Snapshots(ctx, "Windows 11")
	if err != nil || len(snaps) != 2 || snaps[0].Name != "before-updates" || !snaps[0].Current {
		t.Fatalf("snapshots %+v %v", snaps, err)
	}
	if err := m.CreateSnapshot(ctx, "Windows 11", "bad name"); err == nil {
		t.Error("snapshot names are validated")
	}
	if err := m.CreateSnapshot(ctx, "Windows 11", "fresh-install"); err == nil {
		t.Error("duplicate snapshot")
	}
	if err := m.CreateSnapshot(ctx, "Windows 11", "today"); err != nil {
		t.Fatal(err)
	}
	if err := m.RevertSnapshot(ctx, "Windows 11", "fresh-install"); err != nil {
		t.Fatal(err)
	}
	if v := mustGet(t, m, "Windows 11"); v.State != StateOff {
		t.Errorf("reverting to an offline snapshot turns it off: %s", v.State)
	}
	if err := m.RevertSnapshot(ctx, "Windows 11", "nope"); status(err) != http.StatusNotFound {
		t.Errorf("missing snapshot: %v", err)
	}
	if err := m.DeleteSnapshot(ctx, "Windows 11", "today"); err != nil {
		t.Fatal(err)
	}
	if snaps, _ := m.Snapshots(ctx, "Windows 11"); len(snaps) != 2 {
		t.Errorf("after delete: %d", len(snaps))
	}
}

func TestDemoConsoleAndStats(t *testing.T) {
	m, _ := demoManager(t)
	ctx := context.Background()
	c, err := m.Console(ctx, "Windows 11")
	if err != nil || !c.Demo || c.Message == "" || c.Port != 0 {
		t.Errorf("demo console %+v %v", c, err)
	}
	if _, err := m.Console(ctx, "Ubuntu Server"); err == nil {
		t.Error("a stopped machine has no screen")
	}
	if _, err := m.Stats(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	st, err := m.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w, ok := st["Windows 11"]
	if !ok || w.CPU <= 0 || w.CPU > 100 || w.MemUsed <= 0 || w.MemUsed > w.MemTotal {
		t.Errorf("Windows 11 usage %+v", w)
	}
	if _, ok := st["Ubuntu Server"]; ok {
		t.Error("stopped machines have no usage")
	}
}

func TestBusyLock(t *testing.T) {
	m, _ := demoManager(t)
	v := mustGet(t, m, "Ubuntu Server")
	unlock, err := m.lock(v, "cloning")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Action(context.Background(), "Ubuntu Server", "start"); status(err) != http.StatusConflict {
		t.Errorf("a busy machine: %v", err)
	}
	if got := mustGet(t, m, "Ubuntu Server"); got.Busy != "cloning" {
		t.Errorf("busy %q", got.Busy)
	}
	unlock()
	if err := m.Action(context.Background(), "Ubuntu Server", "start"); err != nil {
		t.Error(err)
	}
}

func TestUnexpectedStops(t *testing.T) {
	m, _ := demoManager(t)
	v := VM{UUID: "u", State: StateCrashed}
	if _, hit := m.unexpected(v); !hit {
		t.Error("a crash is unexpected")
	}
	v.State, v.StateReason = StateOff, "shutdown"
	if _, hit := m.unexpected(v); hit {
		t.Error("the guest shutting itself down is fine")
	}
	v.StateReason = "destroyed"
	if _, hit := m.unexpected(v); !hit {
		t.Error("forced off elsewhere is unexpected")
	}
	m.expect(&v)
	if _, hit := m.unexpected(v); hit {
		t.Error("NoCapOS turned it off on purpose")
	}
}

// ---- the real host backend, against recorded commands ----

type fakeRunner struct {
	mu    sync.Mutex
	out   map[string]string
	errs  map[string]error
	calls []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, key)
	for k, err := range f.errs {
		if strings.HasPrefix(key, k) {
			return nil, err
		}
	}
	return []byte(f.out[key]), nil
}

func (f *fakeRunner) called(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func fakeHost(t *testing.T) (*Host, *fakeRunner) {
	f := &fakeRunner{out: map[string]string{}, errs: map[string]error{}}
	h := &Host{Run: f, Dir: filepath.Join(t.TempDir(), "vms"),
		LookPath: func(string) (string, error) { return "/usr/bin/x", nil },
		Exists:   func(string) bool { return true },
		ReadFile: func(string) ([]byte, error) { return []byte("MemTotal: 16777216 kB\n"), nil }}
	return h, f
}

const virsh = "virsh --connect qemu:///system "

func TestHostDefine(t *testing.T) {
	h, f := fakeHost(t)
	s := winSpec()
	s.Disks[0].Path = filepath.Join(h.Dir, "Windows 11", "disk-1.qcow2")
	if err := h.Define(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := f.called("qemu-img create"); len(got) != 1 || !strings.HasSuffix(got[0], "disk-1.qcow2 85899345920") {
		t.Errorf("qemu-img: %v", got)
	}
	if got := f.called(virsh + "define "); len(got) != 1 {
		t.Errorf("define: %v", f.calls)
	}
	// A refused definition leaves no disk behind.
	h2, f2 := fakeHost(t)
	f2.errs[virsh+"define"] = errors.New("XML error")
	s.Disks[0].Path = filepath.Join(h2.Dir, "Windows 11", "disk-1.qcow2")
	if err := h2.Define(context.Background(), s); err == nil {
		t.Fatal("define should fail")
	}
	if _, err := os.Stat(filepath.Dir(s.Disks[0].Path)); err == nil {
		t.Error("the machine folder should be removed again")
	}
}

func TestHostCommands(t *testing.T) {
	h, f := fakeHost(t)
	ctx := context.Background()
	v := &VM{Name: "web", UUID: "uuid-1", CPUs: 2, MemoryMiB: 2048, State: StateOff, Network: Network{Mode: NetNAT, MAC: "52:54:00:00:00:01"},
		Media: []Media{{Target: "sda", Bus: "sata"}}}
	_ = h.Power(ctx, v, "start")
	_ = h.Power(ctx, v, "poweroff")
	_ = h.SetCPUs(ctx, v, 4)
	_ = h.SetCPUs(ctx, v, 1)
	_ = h.SetMemory(ctx, v, 4096)
	_ = h.ChangeMedia(ctx, v, v.Media[0], "/srv/a.iso")
	_ = h.ChangeMedia(ctx, v, Media{Target: "sda", Path: "/srv/a.iso"}, "")
	_ = h.SetAutostart(ctx, v, false)
	_ = h.Snapshot(ctx, v, "s1")
	_ = h.Revert(ctx, v, "s1")
	_ = h.ResizeDisk(ctx, v, Disk{Path: "/d/web/disk-1.qcow2", Format: "qcow2"}, 64<<30)
	want := []string{
		virsh + "net-start default",
		virsh + "start uuid-1",
		virsh + "destroy uuid-1",
		virsh + "setvcpus uuid-1 4 --config --maximum", // grow: maximum first
		virsh + "setvcpus uuid-1 4 --config",
		virsh + "setvcpus uuid-1 1 --config", // shrink: current first
		virsh + "setvcpus uuid-1 1 --config --maximum",
		virsh + "setmaxmem uuid-1 4194304 --config",
		virsh + "setmem uuid-1 4194304 --config",
		virsh + "change-media uuid-1 sda /srv/a.iso --insert --config",
		virsh + "change-media uuid-1 sda --eject --config",
		virsh + "autostart uuid-1 --disable",
		virsh + "snapshot-create-as uuid-1 --name s1 --atomic",
		virsh + "snapshot-revert uuid-1 --snapshotname s1",
		"qemu-img resize -f qcow2 /d/web/disk-1.qcow2 68719476736",
	}
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestHostListAndTools(t *testing.T) {
	h, f := fakeHost(t)
	ctx := context.Background()
	id := "0f1e2d3c-4b5a-4968-8776-655443322110"
	f.out[virsh+"list --all --uuid"] = id + "\n\n"
	f.out[virsh+"dumpxml "+id] = foreignXML
	f.out[virsh+"domstate "+id+" --reason"] = "running (booted)\n"
	f.out[virsh+"dominfo "+id] = "Name: debian12\nAutostart:      enable\n"
	f.out["qemu-img info --output=json -U /var/lib/libvirt/images/debian12.qcow2"] = `{"virtual-size": 21474836480, "actual-size": 4294967296, "format": "qcow2"}`
	f.out[virsh+"version"] = "Compiled against library: libvirt 10.0.0\nRunning hypervisor: QEMU 8.2.2\n"
	vms, err := h.List(ctx)
	if err != nil || len(vms) != 1 {
		t.Fatalf("List = %v, %v", vms, err)
	}
	v := vms[0]
	if v.State != StateRunning || !v.Autostart || v.VNCPort != 5902 || v.Disks[0].Size != 20<<30 || v.Disks[0].Used != 4<<30 {
		t.Errorf("listed %+v", v)
	}
	if tl := h.Tools(ctx); !tl.Libvirtd || tl.Version != "QEMU 8.2.2" || !tl.KVM {
		t.Errorf("tools %+v", tl)
	}
	f.out[virsh+"vncdisplay uuid-9"] = "127.0.0.1:4\n"
	if p, err := h.VNCPort(ctx, &VM{UUID: "uuid-9"}); err != nil || p != 5904 {
		t.Errorf("VNCPort = %d, %v", p, err)
	}
}
