// Package vm runs Virtual Desk's virtual machines on a Linux host with
// QEMU/KVM through libvirt. It talks to libvirt with the virsh command line
// (always against qemu:///system), makes disks with qemu-img and writes the
// domain XML itself. Every rule lives here, not in the UI: names and paths are
// validated, disk images must come from an allowed folder, commands run as
// argument lists (never a shell) and screens only listen on 127.0.0.1.
package vm

import (
	"fmt"
	"net/http"
	"time"
)

// States, simplified from libvirt's.
const (
	StateRunning   = "running"
	StatePaused    = "paused"
	StateOff       = "off"
	StateCrashed   = "crashed"
	StateStopping  = "stopping"
	StateSuspended = "suspended"
)

// OS presets.
const (
	OSWindows11 = "windows11"
	OSWindows10 = "windows10"
	OSLinux     = "linux"
	OSAndroid   = "android"
	OSOther     = "other"
)

// Network modes: the libvirt "default" NAT network, a host bridge, or a
// direct (macvtap) link on a host interface.
const (
	NetNAT    = "nat"
	NetBridge = "bridge"
	NetDirect = "direct"
	NetNone   = "none"
)

// Disk is a virtual hard disk.
type Disk struct {
	Target string `json:"target"` // vda, sda…
	Bus    string `json:"bus"`    // virtio | sata | …
	Path   string `json:"path"`
	Format string `json:"format,omitempty"`
	Size   int64  `json:"size"` // bytes the guest sees
	Used   int64  `json:"used"` // bytes on the host
}

// Media is a CD/DVD drive; Path is empty when nothing is inserted.
type Media struct {
	Target string `json:"target"`
	Bus    string `json:"bus"`
	Path   string `json:"path,omitempty"`
}

type Network struct {
	Mode   string `json:"mode"`
	Source string `json:"source,omitempty"` // bridge or host interface
	Model  string `json:"model,omitempty"`  // virtio | e1000e
	MAC    string `json:"mac,omitempty"`
}

type VM struct {
	Name        string     `json:"name"`
	UUID        string     `json:"uuid"`
	State       string     `json:"state"`
	StateReason string     `json:"state_reason,omitempty"`
	OS          string     `json:"os"`
	CPUs        int        `json:"cpus"`
	MemoryMiB   int        `json:"memory_mib"`
	Firmware    string     `json:"firmware"` // bios | uefi
	TPM         bool       `json:"tpm"`
	Disks       []Disk     `json:"disks"`
	Media       []Media    `json:"media"`
	Network     Network    `json:"network"`
	Autostart   bool       `json:"autostart"`
	VNCPort     int        `json:"vnc_port,omitempty"`
	Created     *time.Time `json:"created,omitempty"`
	// Managed is true for machines Virtual Desk created.
	Managed bool `json:"managed"`
	// Busy says what NoCapOS is doing with it right now.
	Busy string `json:"busy,omitempty"`
}

// Running reports whether the machine has a live process (and a screen).
func (v *VM) Running() bool {
	return v.State == StateRunning || v.State == StatePaused || v.State == StateStopping
}

// Usage is the live load of a running machine.
type Usage struct {
	CPU      float64 `json:"cpu"` // percent of its own virtual CPUs
	MemUsed  int64   `json:"mem_used"`
	MemTotal int64   `json:"mem_total"`
}

type Snapshot struct {
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	State   string    `json:"state"` // the machine's state when it was taken
	Current bool      `json:"current"`
}

// Tools are the host pieces Virtual Desk needs.
type Tools struct {
	Virsh    bool   `json:"virsh"`
	QemuImg  bool   `json:"qemu_img"`
	KVM      bool   `json:"kvm"`
	Libvirtd bool   `json:"libvirtd"`
	Swtpm    bool   `json:"swtpm"`
	OVMF     bool   `json:"ovmf"`
	Version  string `json:"version,omitempty"`
}

type HostIface struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // bridge | ethernet | wireless
}

type ISO struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// HostInfo is what the wizard needs to suggest sensible sizes.
type HostInfo struct {
	CPUs       int         `json:"cpus"`
	MemoryMiB  int         `json:"memory_mib"`
	Interfaces []HostIface `json:"interfaces"`
	ISODir     string      `json:"iso_dir"`
	ISOs       []ISO       `json:"isos"`
}

type Status struct {
	Available  bool     `json:"available"`
	Demo       bool     `json:"demo"`
	Native     bool     `json:"native"`
	Reason     string   `json:"reason,omitempty"`
	Missing    []string `json:"missing,omitempty"`
	Hint       string   `json:"hint,omitempty"`
	CanInstall bool     `json:"can_install"`
	Tools      Tools    `json:"tools"`
	Host       HostInfo `json:"host"`
	Presets    []Preset `json:"presets"`
}

// Console is what the screen window needs: a VNC port on 127.0.0.1 that the
// API hands to guacd, or (demo) a message instead of a screen.
type Console struct {
	Port    int    `json:"-"`
	Demo    bool   `json:"demo"`
	Message string `json:"message,omitempty"`
}

// ---- requests ----

type CreateRequest struct {
	Name      string  `json:"name"`
	OS        string  `json:"os"`
	CPUs      int     `json:"cpus"`
	MemoryMiB int     `json:"memory_mib"`
	DiskGiB   int     `json:"disk_gib"`
	DiskImage string  `json:"disk_image,omitempty"` // use this image instead of a new disk
	ISO       string  `json:"iso,omitempty"`
	ISO2      string  `json:"iso2,omitempty"` // e.g. drivers
	Virtio    *bool   `json:"virtio,omitempty"`
	Network   Network `json:"network"`
	Autostart bool    `json:"autostart"`
	Start     bool    `json:"start"`
}

type DiskResize struct {
	Target  string `json:"target"`
	SizeGiB int    `json:"size_gib"`
}

type MediaChange struct {
	Target string `json:"target"`
	Path   string `json:"path"` // "" ejects
}

// UpdateRequest changes settings. CPUs, memory, disks and network need the
// machine off; media and autostart can change any time.
type UpdateRequest struct {
	CPUs       *int          `json:"cpus,omitempty"`
	MemoryMiB  *int          `json:"memory_mib,omitempty"`
	AddDiskGiB int           `json:"add_disk_gib,omitempty"`
	Resize     []DiskResize  `json:"resize,omitempty"`
	Media      []MediaChange `json:"media,omitempty"`
	Network    *Network      `json:"network,omitempty"`
	Autostart  *bool         `json:"autostart,omitempty"`
}

// ---- errors ----

// Error carries the HTTP status the API should answer with.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func bad(format string, a ...any) error {
	return &Error{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}
func notFound(format string, a ...any) error {
	return &Error{http.StatusNotFound, fmt.Sprintf(format, a...)}
}
func busy(format string, a ...any) error {
	return &Error{http.StatusConflict, fmt.Sprintf(format, a...)}
}
func unavailable(msg string) error { return &Error{http.StatusServiceUnavailable, msg} }
