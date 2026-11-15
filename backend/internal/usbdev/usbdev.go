// Package usbdev lists the host's USB and serial devices that can be
// handed to a container (docker run --device): /dev/ttyUSB*, /dev/ttyACM*,
// their stable /dev/serial/by-id names, and raw USB devices under
// /dev/bus/usb with their vendor and product from sysfs.
//
// Everything is read from files under a root folder ("/" on a real host,
// a test folder in tests), so it builds and runs on every platform; where
// there is no /dev or /sys it simply finds nothing.
package usbdev

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Device is one device a container can be given.
type Device struct {
	Path      string `json:"path"`            // /dev/ttyUSB0, /dev/bus/usb/001/004
	Kind      string `json:"kind"`            // serial | usb
	ByID      string `json:"by_id,omitempty"` // /dev/serial/by-id/usb-… (stable across replugging)
	VendorID  string `json:"vendor_id,omitempty"`
	ProductID string `json:"product_id,omitempty"`
	Vendor    string `json:"vendor,omitempty"`
	Product   string `json:"product,omitempty"`
	Serial    string `json:"serial,omitempty"`
	// UsedBy is filled in by the API: containers that have this device.
	UsedBy []string `json:"used_by"`
}

// HostPath is the path to pass on the host side: the stable by-id name
// when there is one.
func (d Device) HostPath() string {
	if d.ByID != "" {
		return d.ByID
	}
	return d.Path
}

// DeviceFlag is the --device value that gives a container this device at
// its usual name.
func (d Device) DeviceFlag() string {
	if hp := d.HostPath(); hp != d.Path {
		return hp + ":" + d.Path
	}
	return d.Path
}

// safeDevPath is what a device path may look like (no ":" so it can't be
// confused with the host:container separator).
var safeDevPath = regexp.MustCompile(`^/dev/[A-Za-z0-9_.+/-]+$`)

// List finds the devices under root ("/" on the host).
func List(root string) []Device {
	out := []Device{}
	byID := serialByID(root)
	seen := map[string]bool{}
	for _, pattern := range []string{"ttyUSB*", "ttyACM*"} {
		matches, _ := filepath.Glob(filepath.Join(root, "dev", pattern))
		for _, m := range matches {
			name := filepath.Base(m)
			p := "/dev/" + name
			if seen[p] || !safeDevPath.MatchString(p) {
				continue
			}
			seen[p] = true
			d := Device{Path: p, Kind: "serial", ByID: byID[name], UsedBy: []string{}}
			if usb := ttyUSBParent(root, name); usb != "" {
				fillUSB(&d, usb)
			}
			out = append(out, d)
		}
	}
	out = append(out, usbDevices(root)...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "serial"
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// serialByID maps tty names (ttyUSB0) to their /dev/serial/by-id link.
func serialByID(root string) map[string]string {
	out := map[string]string{}
	dir := filepath.Join(root, "dev", "serial", "by-id")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		p := "/dev/serial/by-id/" + e.Name()
		if !safeDevPath.MatchString(p) {
			continue
		}
		tty := path.Base(filepath.ToSlash(target))
		if _, dup := out[tty]; !dup {
			out[tty] = p
		}
	}
	return out
}

// ttyUSBParent finds the sysfs folder of the USB device a tty belongs to.
func ttyUSBParent(root, tty string) string {
	dev, err := filepath.EvalSymlinks(filepath.Join(root, "sys", "class", "tty", tty, "device"))
	if err != nil {
		return ""
	}
	for i := 0; i < 6 && dev != "" && dev != filepath.Dir(dev); i++ {
		if _, err := os.Stat(filepath.Join(dev, "idVendor")); err == nil {
			return dev
		}
		dev = filepath.Dir(dev)
	}
	return ""
}

func readAttr(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 128 {
		s = s[:128]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func fillUSB(d *Device, dir string) {
	d.VendorID, d.ProductID = readAttr(dir, "idVendor"), readAttr(dir, "idProduct")
	d.Vendor, d.Product, d.Serial = readAttr(dir, "manufacturer"), readAttr(dir, "product"), readAttr(dir, "serial")
}

// usbDevices lists /dev/bus/usb/BBB/DDD for every USB device in sysfs,
// leaving out hubs (including the root hubs).
func usbDevices(root string) []Device {
	dirs, _ := filepath.Glob(filepath.Join(root, "sys", "bus", "usb", "devices", "*"))
	out := []Device{}
	for _, dir := range dirs {
		if strings.Contains(filepath.Base(dir), ":") { // interfaces, not devices
			continue
		}
		bus, err1 := strconv.Atoi(readAttr(dir, "busnum"))
		num, err2 := strconv.Atoi(readAttr(dir, "devnum"))
		if err1 != nil || err2 != nil || bus <= 0 || num <= 0 || bus > 999 || num > 999 {
			continue
		}
		if readAttr(dir, "bDeviceClass") == "09" { // a hub
			continue
		}
		p := fmt.Sprintf("/dev/bus/usb/%03d/%03d", bus, num)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(p, "/")))); err != nil {
			continue
		}
		d := Device{Path: p, Kind: "usb", UsedBy: []string{}}
		fillUSB(&d, dir)
		out = append(out, d)
	}
	return out
}
