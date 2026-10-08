//go:build !windows

package files

import (
	"io/fs"
	"os/user"
	"strconv"
	"sync"
	"syscall"
)

var (
	nameMu sync.Mutex
	users  = map[uint32]string{}
	groups = map[uint32]string{}
)

// owners returns the owning user and group names (numeric ids if unknown).
func owners(info fs.FileInfo) (string, string) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}
	nameMu.Lock()
	defer nameMu.Unlock()
	return lookup(users, st.Uid, func(id string) (string, error) {
			u, err := user.LookupId(id)
			if err != nil {
				return "", err
			}
			return u.Username, nil
		}), lookup(groups, st.Gid, func(id string) (string, error) {
			g, err := user.LookupGroupId(id)
			if err != nil {
				return "", err
			}
			return g.Name, nil
		})
}

func lookup(cache map[uint32]string, id uint32, find func(string) (string, error)) string {
	if n, ok := cache[id]; ok {
		return n
	}
	sid := strconv.FormatUint(uint64(id), 10)
	n, err := find(sid)
	if err != nil {
		n = sid
	}
	cache[id] = n
	return n
}
