package vm

import (
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"time"
)

// metaNS marks the <metadata> block Virtual Desk keeps in each domain it creates.
const metaNS = "urn:nocapos:virtualdesk:1"

// spec is a machine as Virtual Desk builds it; domainXML turns it into the
// libvirt domain definition.
type spec struct {
	Name      string
	UUID      string
	OS        string
	CPUs      int
	MemoryMiB int
	UEFI      bool
	TPM       bool
	Virtio    bool
	Video     string
	Disks     []specDisk
	CDROMs    []specDisk
	Network   Network
	Created   time.Time
}

type specDisk struct {
	Path   string
	Format string
	Target string
	Bus    string
	// For a new machine: make an empty disk of this size, or copy this image.
	NewSize  int64
	CopyFrom string
}

// ---- the parts of the libvirt domain XML Virtual Desk writes and reads ----

type xDomain struct {
	XMLName       xml.Name   `xml:"domain"`
	Type          string     `xml:"type,attr"`
	Name          string     `xml:"name"`
	UUID          string     `xml:"uuid,omitempty"`
	Metadata      *xMetadata `xml:"metadata,omitempty"`
	Memory        xMem       `xml:"memory"`
	CurrentMemory *xMem      `xml:"currentMemory,omitempty"`
	VCPU          xVCPU      `xml:"vcpu"`
	OS            xOS        `xml:"os"`
	Features      *xFeatures `xml:"features,omitempty"`
	CPU           *xCPU      `xml:"cpu,omitempty"`
	Clock         *xClock    `xml:"clock,omitempty"`
	OnPoweroff    string     `xml:"on_poweroff,omitempty"`
	OnReboot      string     `xml:"on_reboot,omitempty"`
	OnCrash       string     `xml:"on_crash,omitempty"`
	Devices       xDevices   `xml:"devices"`
}

type xMetadata struct {
	Inner string `xml:",innerxml"`
}

type xMem struct {
	Unit  string `xml:"unit,attr,omitempty"`
	Value int64  `xml:",chardata"`
}

type xVCPU struct {
	Placement string `xml:"placement,attr,omitempty"`
	Current   string `xml:"current,attr,omitempty"`
	Value     int    `xml:",chardata"`
}

type xOS struct {
	Firmware string   `xml:"firmware,attr,omitempty"`
	Type     xOSType  `xml:"type"`
	Loader   *xLoader `xml:"loader,omitempty"`
	NVRAM    string   `xml:"nvram,omitempty"`
}

type xOSType struct {
	Arch    string `xml:"arch,attr,omitempty"`
	Machine string `xml:"machine,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type xLoader struct {
	Readonly string `xml:"readonly,attr,omitempty"`
	Type     string `xml:"type,attr,omitempty"`
	Secure   string `xml:"secure,attr,omitempty"`
	Path     string `xml:",chardata"`
}

type xState struct {
	State   string `xml:"state,attr"`
	Retries string `xml:"retries,attr,omitempty"`
}

type xHyperV struct {
	Mode      string  `xml:"mode,attr,omitempty"`
	Relaxed   *xState `xml:"relaxed,omitempty"`
	VAPIC     *xState `xml:"vapic,omitempty"`
	Spinlocks *xState `xml:"spinlocks,omitempty"`
}

type xFeatures struct {
	ACPI   *struct{} `xml:"acpi,omitempty"`
	APIC   *struct{} `xml:"apic,omitempty"`
	HyperV *xHyperV  `xml:"hyperv,omitempty"`
	SMM    *xState   `xml:"smm,omitempty"`
}

type xCPU struct {
	Mode  string `xml:"mode,attr,omitempty"`
	Check string `xml:"check,attr,omitempty"`
}

type xTimer struct {
	Name       string `xml:"name,attr"`
	TickPolicy string `xml:"tickpolicy,attr,omitempty"`
	Present    string `xml:"present,attr,omitempty"`
}

type xClock struct {
	Offset string   `xml:"offset,attr"`
	Timers []xTimer `xml:"timer"`
}

type xDevices struct {
	Disks      []xDisk      `xml:"disk"`
	Interfaces []xIface     `xml:"interface"`
	Graphics   []xGraphics  `xml:"graphics"`
	Video      []xVideo     `xml:"video"`
	Inputs     []xInput     `xml:"input"`
	TPM        *xTPM        `xml:"tpm,omitempty"`
	Channels   []xChannel   `xml:"channel"`
	MemBalloon *xMemBalloon `xml:"memballoon,omitempty"`
	RNG        *xRNG        `xml:"rng,omitempty"`
}

type xDisk struct {
	Type     string      `xml:"type,attr"`
	Device   string      `xml:"device,attr"`
	Driver   *xDriver    `xml:"driver,omitempty"`
	Source   *xSource    `xml:"source,omitempty"`
	Target   xTarget     `xml:"target"`
	ReadOnly *struct{}   `xml:"readonly,omitempty"`
	Boot     *xBootOrder `xml:"boot,omitempty"`
}

type xDriver struct {
	Name    string `xml:"name,attr,omitempty"`
	Type    string `xml:"type,attr,omitempty"`
	Discard string `xml:"discard,attr,omitempty"`
}

type xSource struct {
	File    string `xml:"file,attr,omitempty"`
	Dev     string `xml:"dev,attr,omitempty"`
	Network string `xml:"network,attr,omitempty"`
	Bridge  string `xml:"bridge,attr,omitempty"`
	Mode    string `xml:"mode,attr,omitempty"`
}

type xTarget struct {
	Dev string `xml:"dev,attr"`
	Bus string `xml:"bus,attr,omitempty"`
}

type xBootOrder struct {
	Order int `xml:"order,attr"`
}

type xIface struct {
	Type   string   `xml:"type,attr"`
	MAC    *xAddr   `xml:"mac,omitempty"`
	Source *xSource `xml:"source,omitempty"`
	Model  *xModel  `xml:"model,omitempty"`
}

type xAddr struct {
	Address string `xml:"address,attr"`
}

type xModel struct {
	Type  string `xml:"type,attr"`
	VRAM  int    `xml:"vram,attr,omitempty"`
	Heads int    `xml:"heads,attr,omitempty"`
}

type xListen struct {
	Type    string `xml:"type,attr"`
	Address string `xml:"address,attr,omitempty"`
}

type xGraphics struct {
	Type     string    `xml:"type,attr"`
	Port     string    `xml:"port,attr,omitempty"`
	Autoport string    `xml:"autoport,attr,omitempty"`
	Listen   string    `xml:"listen,attr,omitempty"`
	Listens  []xListen `xml:"listen"`
}

type xVideo struct {
	Model xModel `xml:"model"`
}

type xInput struct {
	Type string `xml:"type,attr"`
	Bus  string `xml:"bus,attr,omitempty"`
}

type xTPM struct {
	Model   string      `xml:"model,attr,omitempty"`
	Backend xTPMBackend `xml:"backend"`
}

type xTPMBackend struct {
	Type    string `xml:"type,attr"`
	Version string `xml:"version,attr,omitempty"`
}

type xChannel struct {
	Type   string         `xml:"type,attr"`
	Target xChannelTarget `xml:"target"`
}

type xChannelTarget struct {
	Type string `xml:"type,attr"`
	Name string `xml:"name,attr,omitempty"`
}

type xMemBalloon struct {
	Model string `xml:"model,attr"`
}

type xRNG struct {
	Model   string      `xml:"model,attr"`
	Backend xRNGBackend `xml:"backend"`
}

type xRNGBackend struct {
	Model string `xml:"model,attr"`
	Path  string `xml:",chardata"`
}

// nocapMeta is Virtual Desk's own record inside <metadata>.
type nocapMeta struct {
	OS      string `xml:"os"`
	Created string `xml:"created"`
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// domainXML renders the libvirt definition of a new machine.
func domainXML(s spec) ([]byte, error) {
	win := isWindows(s.OS)
	d := xDomain{
		Type: "kvm",
		Name: s.Name,
		UUID: s.UUID,
		Metadata: &xMetadata{Inner: `<nocap:vm xmlns:nocap="` + metaNS + `"><nocap:os>` + esc(s.OS) +
			`</nocap:os><nocap:created>` + s.Created.UTC().Format(time.RFC3339) + `</nocap:created></nocap:vm>`},
		Memory:        xMem{Unit: "MiB", Value: int64(s.MemoryMiB)},
		CurrentMemory: &xMem{Unit: "MiB", Value: int64(s.MemoryMiB)},
		VCPU:          xVCPU{Placement: "static", Value: s.CPUs},
		OS:            xOS{Type: xOSType{Arch: "x86_64", Machine: "q35", Value: "hvm"}},
		Features:      &xFeatures{ACPI: &struct{}{}, APIC: &struct{}{}},
		CPU:           &xCPU{Mode: "host-passthrough", Check: "none"},
		Clock: &xClock{Offset: "utc", Timers: []xTimer{
			{Name: "rtc", TickPolicy: "catchup"}, {Name: "pit", TickPolicy: "delay"}, {Name: "hpet", Present: "no"}}},
		OnPoweroff: "destroy",
		OnReboot:   "restart",
		OnCrash:    "destroy",
	}
	if s.UEFI {
		d.OS.Firmware = "efi"
		d.Features.SMM = &xState{State: "on"}
	}
	if win {
		d.Features.HyperV = &xHyperV{Mode: "custom", Relaxed: &xState{State: "on"}, VAPIC: &xState{State: "on"},
			Spinlocks: &xState{State: "on", Retries: "8191"}}
		d.Clock.Offset = "localtime"
		d.Clock.Timers = append(d.Clock.Timers, xTimer{Name: "hypervclock", Present: "yes"})
	}
	order := 1
	for _, dk := range s.Disks {
		format := dk.Format
		if format == "" {
			format = "qcow2"
		}
		d.Devices.Disks = append(d.Devices.Disks, xDisk{Type: "file", Device: "disk",
			Driver: &xDriver{Name: "qemu", Type: format, Discard: "unmap"}, Source: &xSource{File: dk.Path},
			Target: xTarget{Dev: dk.Target, Bus: dk.Bus}, Boot: &xBootOrder{Order: order}})
		order++
	}
	for _, cd := range s.CDROMs {
		x := xDisk{Type: "file", Device: "cdrom", Driver: &xDriver{Name: "qemu", Type: "raw"},
			Target: xTarget{Dev: cd.Target, Bus: cd.Bus}, ReadOnly: &struct{}{}, Boot: &xBootOrder{Order: order}}
		if cd.Path != "" {
			x.Source = &xSource{File: cd.Path}
		}
		d.Devices.Disks = append(d.Devices.Disks, x)
		order++
	}
	if ifc, ok := ifaceXML(s.Network); ok {
		d.Devices.Interfaces = []xIface{ifc}
	}
	// The screen: VNC on loopback only, no password. It is reachable only
	// through the authenticated guacd tunnel in alfad.
	d.Devices.Graphics = []xGraphics{{Type: "vnc", Port: "-1", Autoport: "yes", Listen: "127.0.0.1",
		Listens: []xListen{{Type: "address", Address: "127.0.0.1"}}}}
	video := xModel{Type: s.Video, Heads: 1}
	if video.Type == "" {
		video.Type = "vga"
	}
	if video.Type == "vga" {
		video.VRAM = 16384
	}
	d.Devices.Video = []xVideo{{Model: video}}
	d.Devices.Inputs = []xInput{{Type: "tablet", Bus: "usb"}} // absolute pointer: the mouse lines up over VNC
	if s.TPM {
		d.Devices.TPM = &xTPM{Model: "tpm-crb", Backend: xTPMBackend{Type: "emulator", Version: "2.0"}}
	}
	d.Devices.Channels = []xChannel{{Type: "unix", Target: xChannelTarget{Type: "virtio", Name: "org.qemu.guest_agent.0"}}}
	d.Devices.MemBalloon = &xMemBalloon{Model: "virtio"}
	if s.Virtio {
		d.Devices.RNG = &xRNG{Model: "virtio", Backend: xRNGBackend{Model: "random", Path: "/dev/urandom"}}
	}
	out, err := xml.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// ifaceXML is the <interface> for a network choice (none: no card at all).
func ifaceXML(n Network) (xIface, bool) {
	model := n.Model
	if model == "" {
		model = "virtio"
	}
	x := xIface{Model: &xModel{Type: model}}
	if n.MAC != "" {
		x.MAC = &xAddr{Address: n.MAC}
	}
	switch n.Mode {
	case NetNAT:
		x.Type, x.Source = "network", &xSource{Network: "default"}
	case NetBridge:
		x.Type, x.Source = "bridge", &xSource{Bridge: n.Source}
	case NetDirect:
		x.Type, x.Source = "direct", &xSource{Dev: n.Source, Mode: "bridge"}
	default:
		return x, false
	}
	return x, true
}

// xmlBytes renders one device element (for attach-device).
func xmlBytes(v any, element string) ([]byte, error) {
	var b bytes.Buffer
	enc := xml.NewEncoder(&b)
	if err := enc.EncodeElement(v, xml.StartElement{Name: xml.Name{Local: element}}); err != nil {
		return nil, err
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// parseDomain reads `virsh dumpxml` into a VM (config only: no state or sizes).
func parseDomain(b []byte) (*VM, error) {
	var d xDomain
	if err := xml.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	v := &VM{Name: d.Name, UUID: d.UUID, CPUs: d.VCPU.Value, MemoryMiB: int(toMiB(d.Memory)), Firmware: "bios",
		Disks: []Disk{}, Media: []Media{}, Network: Network{Mode: NetNone}}
	if d.CurrentMemory != nil && d.CurrentMemory.Value > 0 {
		v.MemoryMiB = int(toMiB(*d.CurrentMemory))
	}
	if d.OS.Firmware == "efi" || (d.OS.Loader != nil && d.OS.Loader.Type == "pflash") {
		v.Firmware = "uefi"
	}
	v.TPM = d.Devices.TPM != nil
	for _, dk := range d.Devices.Disks {
		path := ""
		if dk.Source != nil {
			path = dk.Source.File
			if path == "" {
				path = dk.Source.Dev
			}
		}
		switch dk.Device {
		case "cdrom":
			v.Media = append(v.Media, Media{Target: dk.Target.Dev, Bus: dk.Target.Bus, Path: path})
		case "disk", "":
			format := ""
			if dk.Driver != nil {
				format = dk.Driver.Type
			}
			v.Disks = append(v.Disks, Disk{Target: dk.Target.Dev, Bus: dk.Target.Bus, Path: path, Format: format})
		}
	}
	if len(d.Devices.Interfaces) > 0 {
		v.Network = parseIface(d.Devices.Interfaces[0])
	}
	for _, g := range d.Devices.Graphics {
		if g.Type == "vnc" {
			if p, err := strconv.Atoi(g.Port); err == nil && p > 0 {
				v.VNCPort = p
			}
		}
	}
	if m, ok := readMeta(d.Metadata); ok {
		v.Managed = true
		v.OS = m.OS
		if t, err := time.Parse(time.RFC3339, m.Created); err == nil {
			v.Created = &t
		}
	}
	if v.OS == "" {
		v.OS = guessOS(b, d.Features != nil && d.Features.HyperV != nil)
	}
	return v, nil
}

func parseIface(i xIface) Network {
	n := Network{Mode: NetNAT}
	if i.Model != nil {
		n.Model = i.Model.Type
	}
	if i.MAC != nil {
		n.MAC = i.MAC.Address
	}
	src := i.Source
	if src == nil {
		src = &xSource{}
	}
	switch i.Type {
	case "bridge":
		n.Mode, n.Source = NetBridge, src.Bridge
	case "direct":
		n.Mode, n.Source = NetDirect, src.Dev
	case "network":
		if src.Network != "default" {
			n.Source = src.Network
		}
	default:
		n.Source = i.Type
	}
	return n
}

func toMiB(m xMem) int64 {
	switch strings.ToLower(m.Unit) {
	case "b", "bytes":
		return m.Value >> 20
	case "k", "kib", "":
		return m.Value >> 10
	case "kb":
		return m.Value * 1000 >> 20
	case "m", "mib":
		return m.Value
	case "mb":
		return m.Value * 1000 * 1000 >> 20
	case "g", "gib":
		return m.Value << 10
	case "gb":
		return m.Value * 1000 * 1000 * 1000 >> 20
	}
	return m.Value >> 10
}

// readMeta finds Virtual Desk's block among any other metadata.
func readMeta(md *xMetadata) (nocapMeta, bool) {
	var m nocapMeta
	if md == nil || !strings.Contains(md.Inner, metaNS) {
		return m, false
	}
	dec := xml.NewDecoder(strings.NewReader(md.Inner))
	for {
		tok, err := dec.Token()
		if err == io.EOF || err != nil {
			return m, false
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Space == metaNS && se.Name.Local == "vm" {
			if err := dec.DecodeElement(&m, &se); err != nil {
				return m, false
			}
			return m, true
		}
	}
}

// guessOS gives machines created elsewhere an icon: from the libosinfo id
// other tools record, or Hyper-V enlightenments meaning Windows.
func guessOS(raw []byte, hyperv bool) string {
	s := strings.ToLower(string(raw))
	i := strings.Index(s, "libosinfo.org/xmlns/libvirt/domain")
	if i >= 0 {
		rest := s[i:]
		if j := strings.Index(rest, `os id="`); j >= 0 {
			id := rest[j+7:]
			if k := strings.IndexByte(id, '"'); k >= 0 {
				id = id[:k]
			}
			switch {
			case strings.Contains(id, "microsoft.com/win/11"):
				return OSWindows11
			case strings.Contains(id, "microsoft.com/win"):
				return OSWindows10
			case strings.Contains(id, "android"):
				return OSAndroid
			case strings.Contains(id, "bsd"):
				return OSOther
			case id != "" && !strings.Contains(id, "unknown"):
				return OSLinux
			}
		}
	}
	if hyperv {
		return OSWindows10
	}
	return OSOther
}
