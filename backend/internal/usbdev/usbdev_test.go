package usbdev

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func write(t *testing.T, p, v string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeHost builds a small /dev and /sys: one FTDI serial adapter, one
// Zigbee stick on ACM, a USB device, a hub and an interface entry.
func fakeHost(t *testing.T) (root string, links bool) {
	root = t.TempDir()
	write(t, filepath.Join(root, "dev", "ttyUSB0"), "")
	write(t, filepath.Join(root, "dev", "ttyACM0"), "")
	write(t, filepath.Join(root, "dev", "tty0"), "") // not a USB serial port
	write(t, filepath.Join(root, "dev", "bus", "usb", "001", "004"), "")
	write(t, filepath.Join(root, "dev", "bus", "usb", "001", "001"), "")

	usb := filepath.Join(root, "sys", "bus", "usb", "devices")
	dev := filepath.Join(usb, "1-1")
	write(t, filepath.Join(dev, "busnum"), "1\n")
	write(t, filepath.Join(dev, "devnum"), "4\n")
	write(t, filepath.Join(dev, "idVendor"), "0403\n")
	write(t, filepath.Join(dev, "idProduct"), "6001\n")
	write(t, filepath.Join(dev, "manufacturer"), "FTDI\n")
	write(t, filepath.Join(dev, "product"), "FT232R USB UART\x07\n")
	write(t, filepath.Join(dev, "bDeviceClass"), "00\n")
	hub := filepath.Join(usb, "usb1")
	write(t, filepath.Join(hub, "busnum"), "1")
	write(t, filepath.Join(hub, "devnum"), "1")
	write(t, filepath.Join(hub, "bDeviceClass"), "09")
	ifName := "1-1:1.0"
	if runtime.GOOS == "windows" {
		ifName = "1-1.if0" // no ":" in Windows file names
	} else {
		write(t, filepath.Join(usb, ifName, "busnum"), "1") // an interface
	}

	// ttyUSB0 belongs to 1-1 (sysfs: device → …/1-1/1-1:1.0/ttyUSB0).
	iface := filepath.Join(dev, ifName, "ttyUSB0")
	if err := os.MkdirAll(iface, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sys", "class", "tty", "ttyUSB0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dev", "serial", "by-id"), 0o755); err != nil {
		t.Fatal(err)
	}
	links = os.Symlink(iface, filepath.Join(root, "sys", "class", "tty", "ttyUSB0", "device")) == nil &&
		os.Symlink("../../ttyUSB0", filepath.Join(root, "dev", "serial", "by-id", "usb-FTDI_FT232R_USB_UART_A50285BI-if00-port0")) == nil
	if links && runtime.GOOS != "windows" {
		// A name with ":" can't be told apart from host:container; it's skipped.
		_ = os.Symlink("../../ttyUSB0", filepath.Join(root, "dev", "serial", "by-id", "usb-bad:name"))
	}
	return root, links
}

func TestList(t *testing.T) {
	root, links := fakeHost(t)
	got := List(root)
	byPath := map[string]Device{}
	for _, d := range got {
		byPath[d.Path] = d
	}
	if len(got) != 3 {
		t.Fatalf("want ttyUSB0, ttyACM0 and one USB device, got %+v", got)
	}
	if got[0].Kind != "serial" || got[2].Kind != "usb" {
		t.Errorf("serial ports come first: %+v", got)
	}
	if _, ok := byPath["/dev/tty0"]; ok {
		t.Error("a console tty is not a USB serial port")
	}
	if _, ok := byPath["/dev/bus/usb/001/001"]; ok {
		t.Error("hubs are left out")
	}
	u := byPath["/dev/bus/usb/001/004"]
	if u.VendorID != "0403" || u.ProductID != "6001" || u.Vendor != "FTDI" || u.Product != "FT232R USB UART" {
		t.Errorf("usb device details (control characters dropped): %+v", u)
	}
	if u.UsedBy == nil {
		t.Error("UsedBy must be an empty list, not null")
	}
	if !links {
		t.Skip("symlinks aren't available here; by-id and tty parents not checked")
	}
	s := byPath["/dev/ttyUSB0"]
	if s.ByID != "/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A50285BI-if00-port0" {
		t.Errorf("by-id name: %q", s.ByID)
	}
	if s.Vendor != "FTDI" || s.VendorID != "0403" {
		t.Errorf("the serial port's USB details: %+v", s)
	}
	if s.DeviceFlag() != s.ByID+":/dev/ttyUSB0" {
		t.Errorf("--device line: %q", s.DeviceFlag())
	}
	if a := byPath["/dev/ttyACM0"]; a.ByID != "" || a.DeviceFlag() != "/dev/ttyACM0" {
		t.Errorf("no by-id name → plain path: %+v", a)
	}
}

func TestListNothing(t *testing.T) {
	if got := List(t.TempDir()); len(got) != 0 {
		t.Fatalf("an empty host has no devices: %+v", got)
	}
}
