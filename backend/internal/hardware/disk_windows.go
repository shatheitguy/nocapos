//go:build windows

package hardware

import "golang.org/x/sys/windows"

func diskUsage(path string) (total, used, free uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	var avail, totalFree uint64
	if err = windows.GetDiskFreeSpaceEx(p, &avail, &total, &totalFree); err != nil {
		return
	}
	return total, total - totalFree, avail, nil
}
