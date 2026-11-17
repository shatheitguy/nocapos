package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"alfaos/alfad/internal/osimage"
)

const lsblkJSON = `{"blockdevices":[
 {"name":"sda","path":"/dev/sda","size":16008609792,"model":"Ultra Fit","vendor":"SanDisk ","tran":"usb","rota":false,"rm":true,"type":"disk","fstype":null,"label":null,"partlabel":null,"parttype":null,"mountpoints":[null],
  "children":[
   {"name":"sda1","path":"/dev/sda1","size":268435456,"type":"part","fstype":"vfat","label":"CONFIG","parttype":"c12a7328-f81f-11d2-ba4b-00a0c93ec93b","mountpoints":["/run/rugix/mounts/config"]},
   {"name":"sda4","path":"/dev/sda4","size":1900000000,"type":"part","fstype":"squashfs","parttype":"0fc63daf-8483-4772-8e79-3d69d8477de4","mountpoints":[null]}
  ]},
 {"name":"nvme0n1","path":"/dev/nvme0n1","size":512110190592,"model":"Samsung SSD 980 PRO 500GB","vendor":null,"tran":"nvme","rota":false,"rm":false,"type":"disk","mountpoints":[null],
  "children":[
   {"name":"nvme0n1p1","path":"/dev/nvme0n1p1","size":104857600,"type":"part","fstype":"vfat","parttype":"c12a7328-f81f-11d2-ba4b-00a0c93ec93b","mountpoints":[null]},
   {"name":"nvme0n1p2","path":"/dev/nvme0n1p2","size":16777216,"type":"part","fstype":null,"parttype":"e3c9e316-0b5c-4db8-817d-f92df00215ae","mountpoints":[null]},
   {"name":"nvme0n1p3","path":"/dev/nvme0n1p3","size":128849018880,"type":"part","fstype":"ntfs","label":"Windows","parttype":"ebd0a0a2-b9e5-4433-87c0-68b6b72699c7","mountpoints":[null]},
   {"name":"nvme0n1p4","path":"/dev/nvme0n1p4","size":429496729600,"type":"part","fstype":"ntfs","label":"Games","parttype":"ebd0a0a2-b9e5-4433-87c0-68b6b72699c7","mountpoints":[null]}
  ]},
 {"name":"sdb","path":"/dev/sdb","size":"2000398934016","model":"WDC_WD20EZRZ-00Z5HB0","vendor":"ATA     ","tran":"sata","rota":"1","rm":"0","type":"disk","mountpoints":[null]},
 {"name":"mmcblk0","path":"/dev/mmcblk0","size":8053063680,"model":null,"tran":null,"rota":false,"rm":false,"type":"disk","mountpoints":[null]},
 {"name":"zram0","path":"/dev/zram0","size":4294967296,"type":"disk","mountpoints":["[SWAP]"]},
 {"name":"sr0","path":"/dev/sr0","size":1073741312,"type":"rom","mountpoints":[null]}
]}`

func TestParseDisks(t *testing.T) {
	disks, err := ParseDisks([]byte(lsblkJSON), []string{"/run/rugix/mounts/config", "/run/rugix/mounts/data"})
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 4 {
		t.Fatalf("want 4 disks, got %d: %+v", len(disks), disks)
	}
	by := map[string]Disk{}
	for _, d := range disks {
		by[d.Path] = d
	}
	usb := by["/dev/sda"]
	if !usb.Boot || usb.Kind != KindUSB || usb.Name != "SanDisk Ultra Fit" {
		t.Errorf("boot stick: %+v", usb)
	}
	nv := by["/dev/nvme0n1"]
	if nv.Kind != KindNVMe || nv.Boot || nv.TooSmall || nv.Name != "Samsung SSD 980 PRO 500GB" {
		t.Errorf("nvme: %+v", nv)
	}
	kinds := []string{}
	for _, s := range nv.Segments {
		kinds = append(kinds, s.Kind)
	}
	if strings.Join(kinds, ",") != "startup,system,data" {
		t.Errorf("nvme segments %v", nv.Segments)
	}
	if nv.Segments[2].Label != "Games" {
		t.Errorf("data label %+v", nv.Segments[2])
	}
	hdd := by["/dev/sdb"]
	if hdd.Kind != KindHDD || hdd.Name != "WDC WD20EZRZ-00Z5HB0" || len(hdd.Segments) != 1 || hdd.Segments[0].Kind != SegFree {
		t.Errorf("hdd: %+v", hdd)
	}
	sd := by["/dev/mmcblk0"]
	if sd.Kind != KindSD || !sd.TooSmall || sd.Name != "Memory card" {
		t.Errorf("sd: %+v", sd)
	}
}

func TestPartPathAndLayout(t *testing.T) {
	for in, want := range map[string]string{"/dev/sda": "/dev/sda3", "/dev/nvme0n1": "/dev/nvme0n1p3", "/dev/mmcblk0": "/dev/mmcblk0p3", "/dev/vdb": "/dev/vdb3"} {
		if got := PartPath(in, 3); got != want {
			t.Errorf("PartPath(%s) = %s, want %s", in, got, want)
		}
	}
	s := SfdiskScript()
	if strings.Count(s, "type=") != 6 || !strings.Contains(s, "label: gpt") || !strings.Contains(s, "size=6GiB") {
		t.Errorf("layout:\n%s", s)
	}
	l := NewLayout(500 << 30)
	if len(l) != 3 || l[0].Kind != SegStartup || l[1].Kind != SegSystem || l[2].Kind != SegData || l[2].Bytes <= 0 {
		t.Errorf("new layout %+v", l)
	}
}

func goodRequest() Request {
	var r Request
	r.Disk = "/dev/nvme0n1"
	r.Account.Username = "sha"
	r.Account.Password = "correct horse"
	r.Hostname, r.TimeZone, r.Keyboard, r.Usage = "nocap", "Asia/Dubai", "us", osimage.UsageDesktopWeb
	return r
}

func TestValidate(t *testing.T) {
	disks, _ := ParseDisks([]byte(lsblkJSON), []string{"/run/rugix/mounts/config"})
	r := goodRequest()
	if err := Validate(&r, disks); err != nil {
		t.Fatal(err)
	}
	bad := []func(*Request){
		func(r *Request) { r.Disk = "/dev/sda" },     // the boot stick
		func(r *Request) { r.Disk = "/dev/mmcblk0" }, // too small
		func(r *Request) { r.Disk = "/dev/nope" },
		func(r *Request) { r.Account.Password = "" },
		func(r *Request) { r.Encrypt, r.Passcode = true, "short" },
		func(r *Request) { r.Usage = "tv" },
		func(r *Request) { r.Keyboard = "us; rm -rf" },
	}
	for i, mod := range bad {
		x := goodRequest()
		mod(&x)
		if err := Validate(&x, disks); err == nil {
			t.Errorf("bad request %d accepted", i)
		}
	}
	x := goodRequest()
	x.Encrypt, x.UseTPM = true, true
	if err := Validate(&x, disks); err != nil {
		t.Errorf("TPM without a passphrase refused: %v", err)
	}
}

func TestInstallSteps(t *testing.T) {
	work := t.TempDir()
	var mu sync.Mutex
	var cmds []string
	run := func(ctx context.Context, stdin, name string, args ...string) (string, error) {
		mu.Lock()
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		mu.Unlock()
		if name == "mount" { // the "partition" is a folder here
			return "", os.MkdirAll(args[len(args)-1], 0o700)
		}
		return "", nil
	}
	var copies []string
	cp := func(ctx context.Context, src, dst string, progress func(done, total int64)) error {
		copies = append(copies, src+">"+dst)
		if progress != nil {
			progress(5, 10)
		}
		return nil
	}
	in := &Installer{Src: Source{Disk: "/dev/sda", ConfigImage: "/data/config.img"}, Run: run, Copy: cp, Mounts: work}
	req := goodRequest()
	req.Encrypt, req.UseTPM = true, true
	if err := in.Start(req); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400 && in.Progress().Running; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	p := in.Progress()
	if !p.Done || p.Error != "" || p.Percent != 100 || len(p.RecoveryKey) != 39 {
		t.Fatalf("progress %+v", p)
	}
	want := []string{"/data/config.img>/dev/nvme0n1p1", "/dev/sda2>/dev/nvme0n1p2", "/dev/sda4>/dev/nvme0n1p4"}
	if strings.Join(copies, " ") != strings.Join(want, " ") {
		t.Errorf("copies %v", copies)
	}
	all := strings.Join(cmds, "\n")
	for _, w := range []string{"wipefs -a -f /dev/nvme0n1", "sfdisk --wipe always", "cryptsetup luksFormat --type luks2", "systemd-cryptenroll --tpm2-device=auto", "mkfs.ext4 -q -F -L data /dev/mapper/nocap-install-data", "cryptsetup close nocap-install-data"} {
		if !strings.Contains(all, w) {
			t.Errorf("missing %q in:\n%s", w, all)
		}
	}
	// The seed landed on the data partition for the first start.
	b, err := os.ReadFile(filepath.Join(work, "data", osimage.SeedName))
	if err != nil || !strings.Contains(string(b), `"username":"sha"`) || !strings.Contains(string(b), `"usage":"desktop-web"`) {
		t.Fatalf("seed %s %v", b, err)
	}
	if err := in.Start(req); err == nil {
		t.Error("a second install started")
	}
}
