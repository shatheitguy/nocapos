package syspkg

import (
	"reflect"
	"testing"
)

func TestGrubInstallDevices(t *testing.T) {
	empty := "  grub-pc/install_devices:\n  grub-pc/install_devices_failed_upgrade: true\n"
	if got := grubInstallDevices(empty); got != "" {
		t.Errorf("empty answer: %q", got)
	}
	set := "* grub-pc/install_devices: /dev/disk/by-id/ata-WDC_1\n  grub-pc/install_devices_empty: false\n"
	if got := grubInstallDevices(set); got != "/dev/disk/by-id/ata-WDC_1" {
		t.Errorf("set answer: %q", got)
	}
	if got := grubInstallDevices(""); got != "" {
		t.Errorf("no output: %q", got)
	}
}

func TestDiskNames(t *testing.T) {
	// Plain partition.
	if got := diskNames("sda2 part\nsda  disk\n"); !reflect.DeepEqual(got, []string{"sda"}) {
		t.Errorf("partition: %v", got)
	}
	// Encrypted LVM on a partition.
	lvm := "vg-root lvm\nsda3_crypt crypt\nsda3 part\nsda disk\n"
	if got := diskNames(lvm); !reflect.DeepEqual(got, []string{"sda"}) {
		t.Errorf("lvm: %v", got)
	}
	// RAID 1 over two disks: both get the boot loader.
	raid := "md0 raid1\nsda1 part\nsda disk\nsdb1 part\nsdb disk\n"
	if got := diskNames(raid); !reflect.DeepEqual(got, []string{"sda", "sdb"}) {
		t.Errorf("raid: %v", got)
	}
	if got := diskNames(""); len(got) != 0 {
		t.Errorf("nothing: %v", got)
	}
}

func TestStableName(t *testing.T) {
	links := map[string]string{
		"ata-WDC_WDS120G1G0A-00SS50_171405A01997":       "../../sda",
		"ata-WDC_WDS120G1G0A-00SS50_171405A01997-part1": "../../sda1",
		"wwn-0x5001b448b4ef2c76":                        "../../sda",
		"usb-Generic-_Multi-Card":                       "../../sdb",
	}
	if got := stableName("sda", links); got != "/dev/disk/by-id/ata-WDC_WDS120G1G0A-00SS50_171405A01997" {
		t.Errorf("sda: %s", got)
	}
	if got := stableName("sdb", links); got != "/dev/disk/by-id/usb-Generic-_Multi-Card" {
		t.Errorf("sdb: %s", got)
	}
	if got := stableName("vda", links); got != "/dev/vda" {
		t.Errorf("no link: %s", got)
	}
}
