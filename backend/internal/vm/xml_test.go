package vm

import (
	"strings"
	"testing"
	"time"
)

func winSpec() spec {
	return spec{Name: "Windows 11", UUID: "11111111-2222-4333-8444-555555555555", OS: OSWindows11, CPUs: 4, MemoryMiB: 8192,
		UEFI: true, TPM: true, Video: "vga", Created: time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC),
		Disks:   []specDisk{{Path: "/data/vms/Windows 11/disk-1.qcow2", Format: "qcow2", Target: "sda", Bus: "sata", NewSize: 80 << 30}},
		CDROMs:  []specDisk{{Path: "/srv/isos/Win11 & more.iso", Target: "sdb", Bus: "sata"}, {Target: "sdc", Bus: "sata"}},
		Network: Network{Mode: NetNAT, Model: "e1000e"}}
}

func TestDomainXMLWindows(t *testing.T) {
	x, err := domainXML(winSpec())
	if err != nil {
		t.Fatal(err)
	}
	s := string(x)
	for _, want := range []string{
		`<domain type="kvm">`,
		`<name>Windows 11</name>`,
		`<memory unit="MiB">8192</memory>`,
		`<vcpu placement="static">4</vcpu>`,
		`<os firmware="efi">`,
		`<type arch="x86_64" machine="q35">hvm</type>`,
		`<smm state="on"></smm>`,
		`<spinlocks state="on" retries="8191"></spinlocks>`,
		`<clock offset="localtime">`,
		`<timer name="hypervclock" present="yes"></timer>`,
		`<source file="/data/vms/Windows 11/disk-1.qcow2"></source>`,
		`<target dev="sda" bus="sata"></target>`,
		`<boot order="1"></boot>`,
		`<source file="/srv/isos/Win11 &amp; more.iso"></source>`,
		`<target dev="sdc" bus="sata"></target>`,
		`<interface type="network">`,
		`<source network="default"></source>`,
		`<model type="e1000e"></model>`,
		`<graphics type="vnc" port="-1" autoport="yes" listen="127.0.0.1">`,
		`<listen type="address" address="127.0.0.1"></listen>`,
		`<input type="tablet" bus="usb"></input>`,
		`<tpm model="tpm-crb">`,
		`<backend type="emulator" version="2.0"></backend>`,
		`<nocap:os>windows11</nocap:os>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("domain XML is missing %s\n%s", want, s)
		}
	}
	// The screen must never listen beyond loopback, and there's no password to leak.
	if strings.Contains(s, "0.0.0.0") || strings.Contains(s, "passwd") {
		t.Errorf("screen must be loopback only:\n%s", s)
	}
	// The empty CD drive has no source.
	if strings.Count(s, `device="cdrom"`) != 2 || strings.Count(s, "<source file=") != 2 {
		t.Errorf("want two CD drives, one empty:\n%s", s)
	}
}

func TestDomainXMLLinux(t *testing.T) {
	s := spec{Name: "web", UUID: "u", OS: OSLinux, CPUs: 2, MemoryMiB: 2048, Virtio: true, Video: "virtio", Created: time.Now(),
		Disks:   []specDisk{{Path: "/d/web/disk-1.qcow2", Target: "vda", Bus: "virtio"}},
		Network: Network{Mode: NetDirect, Source: "enp3s0", Model: "virtio"}}
	x, err := domainXML(s)
	if err != nil {
		t.Fatal(err)
	}
	out := string(x)
	for _, want := range []string{`<target dev="vda" bus="virtio">`, `<interface type="direct">`, `<source dev="enp3s0" mode="bridge">`,
		`<model type="virtio" heads="1">`, `<rng model="virtio">`, `<clock offset="utc">`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\n%s", want, out)
		}
	}
	for _, not := range []string{"firmware=", "<tpm", "hyperv", "<smm"} {
		if strings.Contains(out, not) {
			t.Errorf("a BIOS Linux machine shouldn't have %s", not)
		}
	}
	s.Network = Network{Mode: NetNone}
	x, _ = domainXML(s)
	if strings.Contains(string(x), "<interface") {
		t.Error("no network means no network card")
	}
}

func TestDomainRoundTrip(t *testing.T) {
	x, err := domainXML(winSpec())
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseDomain(x)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "Windows 11" || v.UUID != "11111111-2222-4333-8444-555555555555" || v.CPUs != 4 || v.MemoryMiB != 8192 {
		t.Errorf("basics: %+v", v)
	}
	if !v.Managed || v.OS != OSWindows11 || v.Created == nil || !v.Created.Equal(time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("metadata: managed=%v os=%q created=%v", v.Managed, v.OS, v.Created)
	}
	if v.Firmware != "uefi" || !v.TPM {
		t.Errorf("firmware %q tpm %v", v.Firmware, v.TPM)
	}
	if len(v.Disks) != 1 || v.Disks[0].Target != "sda" || v.Disks[0].Format != "qcow2" {
		t.Errorf("disks: %+v", v.Disks)
	}
	if len(v.Media) != 2 || v.Media[0].Path != "/srv/isos/Win11 & more.iso" || v.Media[1].Path != "" {
		t.Errorf("media: %+v", v.Media)
	}
	if v.Network.Mode != NetNAT || v.Network.Model != "e1000e" {
		t.Errorf("network: %+v", v.Network)
	}
}

// A machine defined by another tool, as `virsh dumpxml` prints it while running.
const foreignXML = `<domain type='kvm' id='3'>
  <name>debian12</name>
  <uuid>0f1e2d3c-4b5a-4968-8776-655443322110</uuid>
  <metadata>
    <libosinfo:libosinfo xmlns:libosinfo="http://libosinfo.org/xmlns/libvirt/domain/1.0">
      <libosinfo:os id="http://debian.org/debian/12"/>
    </libosinfo:libosinfo>
  </metadata>
  <memory unit='KiB'>4194304</memory>
  <currentMemory unit='KiB'>2097152</currentMemory>
  <vcpu placement='static'>2</vcpu>
  <os>
    <type arch='x86_64' machine='pc-q35-8.2'>hvm</type>
    <loader readonly='yes' type='pflash'>/usr/share/OVMF/OVMF_CODE_4M.fd</loader>
    <nvram>/var/lib/libvirt/qemu/nvram/debian12_VARS.fd</nvram>
  </os>
  <devices>
    <emulator>/usr/bin/qemu-system-x86_64</emulator>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='/var/lib/libvirt/images/debian12.qcow2' index='2'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <target dev='sda' bus='sata'/>
      <readonly/>
    </disk>
    <interface type='bridge'>
      <mac address='52:54:00:12:34:56'/>
      <source bridge='br0'/>
      <target dev='vnet2'/>
      <model type='virtio'/>
    </interface>
    <graphics type='vnc' port='5902' autoport='yes' listen='127.0.0.1'>
      <listen type='address' address='127.0.0.1'/>
    </graphics>
  </devices>
</domain>`

func TestParseForeignDomain(t *testing.T) {
	v, err := parseDomain([]byte(foreignXML))
	if err != nil {
		t.Fatal(err)
	}
	if v.Managed || v.OS != OSLinux {
		t.Errorf("managed=%v os=%q", v.Managed, v.OS)
	}
	if v.MemoryMiB != 2048 || v.CPUs != 2 || v.Firmware != "uefi" || v.VNCPort != 5902 {
		t.Errorf("parsed %+v", v)
	}
	if len(v.Disks) != 1 || v.Disks[0].Path != "/var/lib/libvirt/images/debian12.qcow2" || len(v.Media) != 1 || v.Media[0].Path != "" {
		t.Errorf("disks %+v media %+v", v.Disks, v.Media)
	}
	if v.Network != (Network{Mode: NetBridge, Source: "br0", Model: "virtio", MAC: "52:54:00:12:34:56"}) {
		t.Errorf("network %+v", v.Network)
	}
	win := strings.Replace(foreignXML, "http://debian.org/debian/12", "http://microsoft.com/win/11", 1)
	if v, _ := parseDomain([]byte(win)); v.OS != OSWindows11 {
		t.Errorf("libosinfo Windows 11 → %q", v.OS)
	}
}

func TestIfaceXML(t *testing.T) {
	x, ok := ifaceXML(Network{Mode: NetBridge, Source: "br0", Model: "virtio", MAC: "52:54:00:aa:bb:cc"})
	if !ok {
		t.Fatal("bridge should give a card")
	}
	b, err := xmlBytes(x, "interface")
	if err != nil {
		t.Fatal(err)
	}
	want := `<interface type="bridge"><mac address="52:54:00:aa:bb:cc"></mac><source bridge="br0"></source><model type="virtio"></model></interface>`
	if string(b) != want {
		t.Errorf("got %s", b)
	}
}
