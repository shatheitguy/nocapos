package vm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"alfaos/alfad/internal/syspkg"
)

// Backend does the machine work. Host drives libvirt; Demo keeps sample
// machines in memory for trying the UI. The Manager checks every rule first.
// Machines are addressed by their *VM (the host uses its UUID).
type Backend interface {
	Tools(ctx context.Context) Tools
	Install(ctx context.Context) error
	Host(ctx context.Context) HostInfo
	List(ctx context.Context) ([]VM, error)
	Stats(ctx context.Context) (map[string]rawStats, error)
	// Define makes the disks a spec asks for (new or copied) and defines it.
	Define(ctx context.Context, s spec) error
	Power(ctx context.Context, v *VM, action string) error
	Undefine(ctx context.Context, v *VM, files []string) error
	Rename(ctx context.Context, v *VM, name string) error
	SetCPUs(ctx context.Context, v *VM, n int) error
	SetMemory(ctx context.Context, v *VM, mib int) error
	AddDisk(ctx context.Context, v *VM, d specDisk) error
	ResizeDisk(ctx context.Context, v *VM, d Disk, size int64) error
	ChangeMedia(ctx context.Context, v *VM, m Media, path string) error
	SetNetwork(ctx context.Context, v *VM, n Network) error
	SetAutostart(ctx context.Context, v *VM, on bool) error
	Snapshots(ctx context.Context, v *VM) ([]Snapshot, error)
	Snapshot(ctx context.Context, v *VM, name string) error
	Revert(ctx context.Context, v *VM, name string) error
	DeleteSnapshot(ctx context.Context, v *VM, name string) error
	VNCPort(ctx context.Context, v *VM) (int, error)
}

// Timeouts for host commands.
const (
	readTimeout   = 30 * time.Second
	actionTimeout = 3 * time.Minute
	copyTimeout   = 2 * time.Hour
)

// URI is the libvirt connection every virsh call uses.
const URI = "qemu:///system"

// Host is the real backend. Fields are replaceable in tests.
type Host struct {
	Run      Runner
	LookPath func(string) (string, error)
	Exists   func(string) bool
	ReadFile func(string) ([]byte, error)
	// Dir holds Virtual Desk's disks (DataDir/vms) and isos/.
	Dir string
}

func NewHost(dir string) *Host {
	return &Host{Run: ExecRunner{}, LookPath: exec.LookPath, ReadFile: os.ReadFile, Dir: dir,
		Exists: func(p string) bool { _, err := os.Stat(p); return err == nil }}
}

func (h *Host) cmd(ctx context.Context, timeout time.Duration, argv ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return h.Run.Run(cctx, argv[0], argv[1:]...)
}

func (h *Host) virsh(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	return h.cmd(ctx, timeout, append([]string{"virsh", "--connect", URI}, args...)...)
}

func (h *Host) has(bin string) bool { _, err := h.LookPath(bin); return err == nil }

// Where distributions put the UEFI firmware (OVMF).
var ovmfPaths = []string{"/usr/share/OVMF", "/usr/share/edk2/ovmf", "/usr/share/edk2-ovmf", "/usr/share/edk2/x64",
	"/usr/share/qemu/ovmf-x86_64-code.bin"}

var hvVersionRe = regexp.MustCompile(`(?m)^Running hypervisor:\s*(.+)$`)

func (h *Host) Tools(ctx context.Context) Tools {
	t := Tools{Virsh: h.has("virsh"), QemuImg: h.has("qemu-img"), KVM: h.Exists("/dev/kvm"), Swtpm: h.has("swtpm")}
	for _, p := range ovmfPaths {
		if h.Exists(p) {
			t.OVMF = true
		}
	}
	if t.Virsh {
		if out, err := h.virsh(ctx, 10*time.Second, "version"); err == nil {
			t.Libvirtd = true
			if m := hvVersionRe.FindStringSubmatch(string(out)); m != nil {
				t.Version = strings.TrimSpace(m[1])
			}
		}
	}
	return t
}

var packages = syspkg.Names{
	"apt":    {"qemu-system-x86", "qemu-utils", "libvirt-daemon-system", "libvirt-clients", "ovmf", "swtpm", "swtpm-tools"},
	"dnf":    {"qemu-kvm", "qemu-img", "libvirt", "edk2-ovmf", "swtpm", "swtpm-tools"},
	"pacman": {"qemu-base", "libvirt", "edk2-ovmf", "swtpm", "dnsmasq"},
	"zypper": {"qemu-kvm", "qemu-tools", "libvirt", "qemu-ovmf-x86_64", "swtpm"},
	"apk":    {"qemu-system-x86_64", "qemu-img", "libvirt-daemon", "libvirt-client", "ovmf", "swtpm"},
}

// Install installs QEMU/KVM and libvirt, starts the libvirt service and its
// default NAT network.
func (h *Host) Install(ctx context.Context) error {
	if err := syspkg.Install(ctx, packages); err != nil {
		return err
	}
	if _, err := h.cmd(ctx, actionTimeout, "systemctl", "enable", "--now", "libvirtd"); err != nil {
		// Newer distributions split libvirtd into per-driver daemons.
		if _, err2 := h.cmd(ctx, actionTimeout, "systemctl", "enable", "--now", "virtqemud.socket", "virtnetworkd.socket", "virtstoraged.socket"); err2 != nil {
			return fmt.Errorf("the packages are installed but the libvirt service didn't start: %w", err)
		}
	}
	_, _ = h.virsh(ctx, readTimeout, "net-autostart", "default")
	_, _ = h.virsh(ctx, readTimeout, "net-start", "default")
	return nil
}

func (h *Host) Host(context.Context) HostInfo {
	info := HostInfo{CPUs: runtime.NumCPU(), Interfaces: []HostIface{}, ISODir: filepath.Join(h.Dir, "isos")}
	if b, err := h.ReadFile("/proc/meminfo"); err == nil {
		info.MemoryMiB = parseMemTotal(b)
	}
	if ifs, err := net.Interfaces(); err == nil {
		for _, i := range ifs {
			if i.Flags&net.FlagLoopback != 0 || skipIface(i.Name) {
				continue
			}
			kind := "ethernet"
			if h.Exists("/sys/class/net/" + i.Name + "/bridge") {
				kind = "bridge"
			} else if h.Exists("/sys/class/net/" + i.Name + "/wireless") {
				kind = "wireless"
			}
			info.Interfaces = append(info.Interfaces, HostIface{Name: i.Name, Kind: kind})
		}
	}
	info.ISOs = listISOs(info.ISODir)
	return info
}

// skipIface hides virtual links nobody should attach a machine to.
func skipIface(n string) bool {
	for _, p := range []string{"virbr", "vnet", "veth", "docker", "br-", "tap", "macvtap", "tun", "lo", "podman", "cni", "flannel", "kube"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return !ifRe.MatchString(n)
}

// listISOs lists the .iso files in Virtual Desk's ISO folder.
func listISOs(dir string) []ISO {
	out := []ISO{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".iso" {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
			out = append(out, ISO{Name: e.Name(), Path: filepath.Join(dir, e.Name()), Size: fi.Size()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (h *Host) List(ctx context.Context) ([]VM, error) {
	out, err := h.virsh(ctx, readTimeout, "list", "--all", "--uuid")
	if err != nil {
		return nil, fmt.Errorf("couldn't list virtual machines: %w", err)
	}
	vms := []VM{}
	for _, id := range parseUUIDs(string(out)) {
		v, err := h.load(ctx, id)
		if err != nil {
			continue // it vanished between the two calls
		}
		vms = append(vms, *v)
	}
	return vms, nil
}

func (h *Host) load(ctx context.Context, id string) (*VM, error) {
	raw, err := h.virsh(ctx, readTimeout, "dumpxml", id)
	if err != nil {
		return nil, err
	}
	v, err := parseDomain(raw)
	if err != nil {
		return nil, err
	}
	if out, err := h.virsh(ctx, readTimeout, "domstate", id, "--reason"); err == nil {
		v.State, v.StateReason = parseState(string(out))
	}
	if out, err := h.virsh(ctx, readTimeout, "dominfo", id); err == nil {
		v.Autostart = parseInfo(string(out))["Autostart"] == "enable"
	}
	if !v.Running() {
		v.VNCPort = 0
	}
	for i := range v.Disks {
		d := &v.Disks[i]
		if d.Path == "" || !h.has("qemu-img") {
			continue
		}
		if b, err := h.cmd(ctx, readTimeout, "qemu-img", "info", "--output=json", "-U", d.Path); err == nil {
			if size, used, format, err := parseImgInfo(b); err == nil {
				d.Size, d.Used = size, used
				if d.Format == "" {
					d.Format = format
				}
			}
		}
	}
	return v, nil
}

func (h *Host) Stats(ctx context.Context) (map[string]rawStats, error) {
	out, err := h.virsh(ctx, readTimeout, "domstats", "--cpu-total", "--balloon", "--vcpu", "--list-active")
	if err != nil {
		return nil, err
	}
	return parseDomStats(string(out)), nil
}

// prepareDir makes a machine's folder reachable by the QEMU process, which
// libvirt runs as its own user: the folders on the way get "others may pass
// through" (o+x, no listing) and the machine folder is 0711.
func (h *Host) prepareDir(dir string) error {
	if err := os.MkdirAll(dir, 0o711); err != nil {
		return err
	}
	for _, p := range []string{filepath.Dir(h.Dir), h.Dir, dir} {
		if !inside(dir, h.Dir) && p == dir {
			continue
		}
		if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o001 == 0 {
			_ = os.Chmod(p, fi.Mode().Perm()|0o001)
		}
	}
	return nil
}

func (h *Host) makeDisk(ctx context.Context, d specDisk) error {
	if d.CopyFrom != "" {
		_, err := h.cmd(ctx, copyTimeout, "qemu-img", "convert", "-O", "qcow2", d.CopyFrom, d.Path)
		return err
	}
	_, err := h.cmd(ctx, actionTimeout, "qemu-img", "create", "-f", "qcow2", d.Path, strconv.FormatInt(d.NewSize, 10))
	return err
}

func (h *Host) Define(ctx context.Context, s spec) (err error) {
	var made []string
	var dirs []string
	defer func() {
		if err != nil {
			for _, f := range made {
				_ = os.Remove(f)
			}
			for _, d := range dirs {
				_ = os.Remove(d) // only if empty
			}
		}
	}()
	for _, d := range s.Disks {
		if d.NewSize == 0 && d.CopyFrom == "" {
			continue
		}
		dir := filepath.Dir(d.Path)
		if _, statErr := os.Stat(dir); statErr != nil {
			dirs = append(dirs, dir)
		}
		if err = h.prepareDir(dir); err != nil {
			return err
		}
		if _, statErr := os.Stat(d.Path); statErr == nil {
			return bad("a disk already exists at %s", d.Path)
		}
		if err = h.makeDisk(ctx, d); err != nil {
			_ = os.Remove(d.Path)
			return fmt.Errorf("couldn't make the disk: %w", err)
		}
		made = append(made, d.Path)
	}
	x, err := domainXML(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "nocap-vm-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(x); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if _, err = h.virsh(ctx, actionTimeout, "define", f.Name()); err != nil {
		return fmt.Errorf("libvirt refused the machine: %w", err)
	}
	return nil
}

var powerVerb = map[string]string{"start": "start", "shutdown": "shutdown", "poweroff": "destroy", "reboot": "reboot",
	"pause": "suspend", "resume": "resume"}

func (h *Host) Power(ctx context.Context, v *VM, action string) error {
	verb, ok := powerVerb[action]
	if !ok {
		return bad("unknown action %q", action)
	}
	if action == "start" && v.Network.Mode == NetNAT {
		_, _ = h.virsh(ctx, readTimeout, "net-start", "default") // fine if it already runs
	}
	_, err := h.virsh(ctx, actionTimeout, verb, v.UUID)
	return err
}

func (h *Host) Undefine(ctx context.Context, v *VM, files []string) error {
	if _, err := h.virsh(ctx, actionTimeout, "undefine", v.UUID, "--nvram", "--snapshots-metadata", "--managed-save"); err != nil {
		// Machines without UEFI variables may refuse --nvram on old libvirt.
		if _, err2 := h.virsh(ctx, actionTimeout, "undefine", v.UUID, "--snapshots-metadata", "--managed-save"); err2 != nil {
			return err
		}
	}
	var errs []error
	for _, f := range files {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
		_ = os.Remove(filepath.Dir(f)) // the machine folder, once empty
	}
	if len(errs) > 0 {
		return fmt.Errorf("the machine is deleted but some disks couldn't be removed: %w", errors.Join(errs...))
	}
	return nil
}

func (h *Host) Rename(ctx context.Context, v *VM, name string) error {
	_, err := h.virsh(ctx, actionTimeout, "domrename", v.UUID, name)
	return err
}

// setPair sets a value and its maximum in the order libvirt accepts
// (the current value can never exceed the maximum).
func (h *Host) setPair(ctx context.Context, grow bool, maxArgs, curArgs []string) error {
	first, second := curArgs, maxArgs
	if grow {
		first, second = maxArgs, curArgs
	}
	if _, err := h.virsh(ctx, actionTimeout, first...); err != nil {
		return err
	}
	_, err := h.virsh(ctx, actionTimeout, second...)
	return err
}

func (h *Host) SetCPUs(ctx context.Context, v *VM, n int) error {
	c := strconv.Itoa(n)
	return h.setPair(ctx, n > v.CPUs, []string{"setvcpus", v.UUID, c, "--config", "--maximum"}, []string{"setvcpus", v.UUID, c, "--config"})
}

func (h *Host) SetMemory(ctx context.Context, v *VM, mib int) error {
	kib := strconv.Itoa(mib << 10)
	return h.setPair(ctx, mib > v.MemoryMiB, []string{"setmaxmem", v.UUID, kib, "--config"}, []string{"setmem", v.UUID, kib, "--config"})
}

func (h *Host) AddDisk(ctx context.Context, v *VM, d specDisk) error {
	if err := h.prepareDir(filepath.Dir(d.Path)); err != nil {
		return err
	}
	if _, err := os.Stat(d.Path); err == nil {
		return bad("a disk already exists at %s", d.Path)
	}
	if err := h.makeDisk(ctx, d); err != nil {
		_ = os.Remove(d.Path)
		return fmt.Errorf("couldn't make the disk: %w", err)
	}
	if _, err := h.virsh(ctx, actionTimeout, "attach-disk", v.UUID, d.Path, d.Target, "--driver", "qemu", "--subdriver", "qcow2",
		"--targetbus", d.Bus, "--config"); err != nil {
		_ = os.Remove(d.Path)
		return err
	}
	return nil
}

func (h *Host) ResizeDisk(ctx context.Context, v *VM, d Disk, size int64) error {
	args := []string{"qemu-img", "resize"}
	if d.Format != "" {
		args = append(args, "-f", d.Format)
	}
	_, err := h.cmd(ctx, actionTimeout, append(args, d.Path, strconv.FormatInt(size, 10))...)
	return err
}

func (h *Host) ChangeMedia(ctx context.Context, v *VM, m Media, path string) error {
	args := []string{"change-media", v.UUID, m.Target}
	switch {
	case path == "":
		args = append(args, "--eject")
	case m.Path == "":
		args = append(args, path, "--insert")
	default:
		args = append(args, path, "--update")
	}
	args = append(args, "--config")
	if v.Running() {
		args = append(args, "--live")
	}
	_, err := h.virsh(ctx, actionTimeout, args...)
	return err
}

var ifaceType = map[string]string{NetNAT: "network", NetBridge: "bridge", NetDirect: "direct"}

func (h *Host) SetNetwork(ctx context.Context, v *VM, n Network) error {
	if old := v.Network; old.Mode != NetNone && old.MAC != "" {
		t := ifaceType[old.Mode]
		if t == "" {
			t = "network"
		}
		if _, err := h.virsh(ctx, actionTimeout, "detach-interface", v.UUID, t, "--mac", old.MAC, "--config"); err != nil {
			return err
		}
		n.MAC = old.MAC // keep the address the guest (and DHCP leases) know
	}
	x, ok := ifaceXML(n)
	if !ok {
		return nil
	}
	b, err := xmlBytes(x, "interface")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "nocap-nic-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(b)
	if cerr := f.Close(); werr != nil || cerr != nil {
		return errors.Join(werr, cerr)
	}
	_, err = h.virsh(ctx, actionTimeout, "attach-device", v.UUID, f.Name(), "--config")
	return err
}

func (h *Host) SetAutostart(ctx context.Context, v *VM, on bool) error {
	args := []string{"autostart", v.UUID}
	if !on {
		args = append(args, "--disable")
	}
	_, err := h.virsh(ctx, readTimeout, args...)
	return err
}

func (h *Host) Snapshots(ctx context.Context, v *VM) ([]Snapshot, error) {
	out, err := h.virsh(ctx, readTimeout, "snapshot-list", v.UUID)
	if err != nil {
		return nil, err
	}
	snaps := parseSnapshots(string(out))
	if cur, err := h.virsh(ctx, readTimeout, "snapshot-current", v.UUID, "--name"); err == nil {
		name := strings.TrimSpace(string(cur))
		for i := range snaps {
			snaps[i].Current = snaps[i].Name == name
		}
	}
	return snaps, nil
}

func (h *Host) Snapshot(ctx context.Context, v *VM, name string) error {
	_, err := h.virsh(ctx, copyTimeout, "snapshot-create-as", v.UUID, "--name", name, "--atomic")
	return err
}

func (h *Host) Revert(ctx context.Context, v *VM, name string) error {
	_, err := h.virsh(ctx, copyTimeout, "snapshot-revert", v.UUID, "--snapshotname", name)
	return err
}

func (h *Host) DeleteSnapshot(ctx context.Context, v *VM, name string) error {
	_, err := h.virsh(ctx, copyTimeout, "snapshot-delete", v.UUID, "--snapshotname", name)
	return err
}

func (h *Host) VNCPort(ctx context.Context, v *VM) (int, error) {
	out, err := h.virsh(ctx, readTimeout, "vncdisplay", v.UUID)
	if err != nil {
		return 0, err
	}
	port, ok := parseVNCDisplay(string(out))
	if !ok {
		return 0, bad("this machine has no screen")
	}
	return port, nil
}
