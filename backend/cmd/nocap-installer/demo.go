package main

import (
	"context"
	"os"
	"strings"
	"time"

	"alfaos/alfad/internal/installer"
)

// -demo: made-up disks and an install that only pretends, for working on
// the setup screens (and their screenshots) on any computer.

const demoLsblk = `{"blockdevices":[
 {"name":"sdz","path":"/dev/sdz","size":16008609792,"model":"Ultra Fit","vendor":"SanDisk","tran":"usb","rota":false,"rm":true,"type":"disk","mountpoints":[null],
  "children":[{"name":"sdz1","path":"/dev/sdz1","size":268435456,"type":"part","fstype":"vfat","parttype":"c12a7328-f81f-11d2-ba4b-00a0c93ec93b","mountpoints":["/run/rugix/mounts/config"]}]},
 {"name":"nvme0n1","path":"/dev/nvme0n1","size":1000204886016,"model":"Samsung SSD 990 PRO 1TB","tran":"nvme","rota":false,"rm":false,"type":"disk","mountpoints":[null],
  "children":[
   {"name":"nvme0n1p1","path":"/dev/nvme0n1p1","size":104857600,"type":"part","fstype":"vfat","parttype":"c12a7328-f81f-11d2-ba4b-00a0c93ec93b","mountpoints":[null]},
   {"name":"nvme0n1p2","path":"/dev/nvme0n1p2","size":16777216,"type":"part","parttype":"e3c9e316-0b5c-4db8-817d-f92df00215ae","mountpoints":[null]},
   {"name":"nvme0n1p3","path":"/dev/nvme0n1p3","size":120000000000,"type":"part","fstype":"ntfs","label":"Windows","parttype":"ebd0a0a2-b9e5-4433-87c0-68b6b72699c7","mountpoints":[null]},
   {"name":"nvme0n1p4","path":"/dev/nvme0n1p4","size":400000000000,"type":"part","fstype":"ntfs","label":"Data","parttype":"ebd0a0a2-b9e5-4433-87c0-68b6b72699c7","mountpoints":[null]}]},
 {"name":"sda","path":"/dev/sda","size":2000398934016,"model":"WDC WD20EZRZ","vendor":"ATA","tran":"sata","rota":true,"rm":false,"type":"disk","mountpoints":[null],
  "children":[{"name":"sda1","path":"/dev/sda1","size":2000397885440,"type":"part","fstype":"ext4","label":"Media","parttype":"0fc63daf-8483-4772-8e79-3d69d8477de4","mountpoints":[null]}]},
 {"name":"sdb","path":"/dev/sdb","size":250059350016,"model":"CT250MX500SSD1","vendor":"ATA","tran":"sata","rota":false,"rm":false,"type":"disk","mountpoints":[null]},
 {"name":"mmcblk0","path":"/dev/mmcblk0","size":15931539456,"type":"disk","rota":false,"rm":false,"mountpoints":[null]}
]}`

func demoDisks(context.Context) ([]installer.Disk, error) {
	return installer.ParseDisks([]byte(demoLsblk), bootMounts)
}

// demoInstaller fakes the commands and the copying, slowly.
func demoInstaller() *installer.Installer {
	in := installer.New(installer.Source{ConfigImage: "/demo/config.img"})
	in.Mounts = demoTemp()
	in.Run = func(ctx context.Context, stdin, name string, args ...string) (string, error) {
		time.Sleep(150 * time.Millisecond)
		return "", nil
	}
	in.Copy = func(ctx context.Context, src, dst string, progress func(done, total int64)) error {
		steps := int64(4)
		if strings.HasSuffix(src, "4") {
			steps = 40
		}
		for i := int64(1); i <= steps; i++ {
			time.Sleep(250 * time.Millisecond)
			if progress != nil {
				progress(i, steps)
			}
		}
		return nil
	}
	return in
}

func demoTemp() string {
	d, _ := os.MkdirTemp("", "nocap-installer-demo")
	return d
}
