//go:build !windows

package appdata

import (
	"io/fs"
	"os"
	"strconv"
	"syscall"
)

// ownerOf is "uid:gid" of a file.
func ownerOf(info fs.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(st.Uid), 10) + ":" + strconv.FormatUint(uint64(st.Gid), 10)
	}
	return ""
}

// chownLike gives p the owner of info (best effort: needs root).
func chownLike(p string, info fs.FileInfo) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = os.Lchown(p, int(st.Uid), int(st.Gid))
	}
}
