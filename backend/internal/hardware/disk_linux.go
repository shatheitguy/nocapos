//go:build linux

package hardware

import "syscall"

func diskUsage(path string) (total, used, free uint64, err error) {
	var st syscall.Statfs_t
	if err = syscall.Statfs(path, &st); err != nil {
		return
	}
	bsize := uint64(st.Bsize)
	total = uint64(st.Blocks) * bsize
	free = uint64(st.Bavail) * bsize
	used = (uint64(st.Blocks) - uint64(st.Bfree)) * bsize
	return
}
