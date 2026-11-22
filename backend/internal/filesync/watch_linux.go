//go:build linux

package filesync

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// maxWatches keeps one location from using up the system's inotify watches;
// past it, the periodic scan still catches changes.
const maxWatches = 50000

// Watch follows root and its folders with inotify and calls changed with
// the (slash-separated) path of what changed, until ctx ends.
func Watch(ctx context.Context, root string, changed func(rel string)) error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	const mask = unix.IN_CREATE | unix.IN_CLOSE_WRITE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_ATTRIB | unix.IN_DELETE_SELF
	var mu sync.Mutex
	dirs := map[int]string{} // watch descriptor → folder (relative)
	add := func(dir string) {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				if err != nil && d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if rel == TrashDir || rel == PartialDir {
				return fs.SkipDir
			}
			mu.Lock()
			n := len(dirs)
			mu.Unlock()
			if n >= maxWatches {
				return fs.SkipAll
			}
			wd, err := unix.InotifyAddWatch(fd, p, mask)
			if err == nil {
				mu.Lock()
				dirs[wd] = rel
				mu.Unlock()
			}
			return nil
		})
	}
	add(root)
	go func() {
		<-ctx.Done()
		unix.Close(fd)
	}()
	go func() {
		buf := make([]byte, 64<<10)
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for ctx.Err() == nil {
			if _, err := unix.Poll(pfd, 1000); err != nil && err != unix.EINTR {
				return
			}
			n, err := unix.Read(fd, buf)
			if err != nil || n <= 0 {
				if err == unix.EAGAIN || err == unix.EINTR {
					continue
				}
				return
			}
			for off := 0; off+unix.SizeofInotifyEvent <= n; {
				ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
				name := ""
				if ev.Len > 0 {
					end := off + unix.SizeofInotifyEvent + int(ev.Len)
					if end > n {
						break
					}
					name = strings.TrimRight(string(buf[off+unix.SizeofInotifyEvent:end]), "\x00")
				}
				off += unix.SizeofInotifyEvent + int(ev.Len)
				mu.Lock()
				dir, ok := dirs[int(ev.Wd)]
				mu.Unlock()
				if !ok {
					continue
				}
				rel := name
				if dir != "." && dir != "" {
					rel = dir + "/" + name
				}
				if rel == "" {
					rel = "."
				}
				if ev.Mask&unix.IN_ISDIR != 0 && ev.Mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 {
					add(filepath.Join(root, filepath.FromSlash(rel)))
				}
				changed(rel)
			}
		}
	}()
	return nil
}
