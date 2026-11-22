//go:build linux

package api

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// homeOwner: files in a person's home folder belong to that person (their
// account may have another number on each server).
func homeOwner(root string) func(rel string) (int, int, bool) {
	return func(rel string) (int, int, bool) {
		top, _, _ := strings.Cut(rel, "/")
		fi, err := os.Stat(filepath.Join(root, top))
		if err != nil {
			return 0, 0, false
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return 0, 0, false
		}
		return int(st.Uid), int(st.Gid), true
	}
}
