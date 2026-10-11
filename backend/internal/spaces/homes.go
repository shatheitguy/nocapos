package spaces

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"alfaos/alfad/internal/files"
)

// Homes says where people's private homes are.
//
// On a native Linux install every NoCapOS person is a real Linux user, so
// their home is their Linux home directory (from /etc/passwd), owned by
// them. Elsewhere (Docker, Windows, NoCapOS's own accounts) homes live in a
// "homes" folder of the main storage location.
type Homes interface {
	// Home is a person's home folder ("" if they have none).
	Home(name string) string
	// Base is the folder holding everyone's homes (the admins' Homes view).
	Base() string
	// Linux reports real Linux homes: files made in them belong to the person.
	Linux() bool
}

// storageHomes keeps homes in <main storage location>/homes/<name>.
type storageHomes struct{ m *Manager }

func (h storageHomes) Base() string {
	st, ok := h.m.Storage()
	if !ok {
		return ""
	}
	return filepath.Join(st.Path, HomesDir)
}

func (h storageHomes) Home(name string) string {
	dir := h.Base()
	if dir == "" || !validHomeName(name) {
		return ""
	}
	return filepath.Join(dir, name)
}

func (storageHomes) Linux() bool { return false }

// LinuxHomes are the Linux users' own home directories.
type LinuxHomes struct {
	// Lookup returns the home directory of a Linux user from their passwd entry.
	Lookup func(name string) (home string, ok bool)
	// Dir holds everyone's homes (usually /home).
	Dir string
}

func (h LinuxHomes) Base() string { return h.Dir }
func (LinuxHomes) Linux() bool    { return true }

func (h LinuxHomes) Home(name string) string {
	if h.Lookup == nil || !validHomeName(name) {
		return ""
	}
	p, ok := h.Lookup(name)
	if !ok || !filepath.IsAbs(p) {
		return ""
	}
	p = filepath.Clean(p)
	// A home that is the whole disk or a core OS folder (system accounts
	// have /, /root, /var/lib/…) is never treated as a private home.
	for _, sys := range files.SystemPoints {
		if p == sys {
			return "" // "/", "/home" itself, "/root", "/tmp", …
		}
	}
	for _, sys := range files.SystemTrees {
		if within(p, sys) {
			return ""
		}
	}
	return p
}

// SetHomes switches where homes are (main wires Linux homes on native installs).
func (m *Manager) SetHomes(h Homes) {
	m.homes = h
	m.protectHomes()
}

// SetOwner replaces how a person's uid/gid are found (tests).
func (m *Manager) SetOwner(fn func(username string) (uid, gid int, ok bool)) { m.owner = fn }

// HomesPath is the folder holding everyone's homes ("" if none).
func (m *Manager) HomesPath() string { return m.homes.Base() }

// HomePath is where a person's home is ("" if they can't have one).
func (m *Manager) HomePath(name string) string { return m.homes.Home(name) }

// LinuxHomes reports whether homes are the Linux users' own.
func (m *Manager) LinuxHomes() bool { return m.homes.Linux() }

// validHomeName keeps a user name usable as one folder name.
func validHomeName(name string) bool {
	return files.ValidName(name) == nil && !strings.HasPrefix(name, ".") && !strings.ContainsAny(name, " %")
}

// EnsureHome creates a person's home if it's missing and returns its path.
// A new home is private (0700) and, for Linux users, owned by them.
func (m *Manager) EnsureHome(name string) (string, error) {
	p := m.HomePath(name)
	if p == "" {
		return "", errors.New("no home folder for " + name)
	}
	parent := filepath.Dir(p)
	if !m.LinuxHomes() {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", err
		}
	}
	if !IsInside(p, parent) {
		return "", ErrOutside // a link where the home should be
	}
	err := os.Mkdir(p, 0o700)
	if errors.Is(err, fs.ErrExist) {
		return p, nil
	}
	if err != nil {
		return "", err
	}
	m.chownTo(p, name)
	return p, nil
}

// SecureHome makes a freshly created person's home private to them
// (0750, owned by them) on Linux installs.
func (m *Manager) SecureHome(name string) {
	if !m.LinuxHomes() {
		return
	}
	if p := m.HomePath(name); p != "" {
		_ = os.Chmod(p, 0o750)
		m.chownTo(p, name)
	}
}

func (m *Manager) chownTo(p, name string) {
	if m.owner == nil {
		return
	}
	if uid, gid, ok := m.owner(name); ok {
		_ = os.Lchown(p, uid, gid)
	}
}

// linuxOwner looks a Linux user up, when NoCapOS runs as root on Linux.
func linuxOwner(username string) (int, int, bool) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return 0, 0, false
	}
	u, err := user.Lookup(username)
	if err != nil {
		return 0, 0, false
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	return uid, gid, err1 == nil && err2 == nil
}

// Own gives files just made in a person's home (by NoCapOS, which runs as
// root) to that person, so ownership on disk is right. rels are paths
// inside the home location; folders are handed over with their contents.
// Links are never followed. Only Linux homes are touched.
func (m *Manager) Own(rootID string, rels ...string) {
	if !m.LinuxHomes() || m.owner == nil || !strings.HasPrefix(rootID, homePrefix) {
		return
	}
	var todo []string
	for _, rel := range rels {
		if c := strings.Trim(rel, "/"); c != "" && c != "." {
			todo = append(todo, rel) // never the whole home
		}
	}
	name := strings.TrimPrefix(rootID, homePrefix)
	home := m.HomePath(name)
	if home == "" || len(todo) == 0 {
		return
	}
	uid, gid, ok := m.owner(name)
	if !ok {
		return
	}
	for _, rel := range todo {
		full, err := Inside(home, rel)
		if err != nil || full == "" {
			continue
		}
		_ = filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			_ = os.Lchown(p, uid, gid)
			return nil
		})
	}
}

// IndexPlace finds which indexed fixed location holds a folder, and where
// inside it ("/a/b", or "" for the whole location), so search results from
// the index can be matched to it.
func (m *Manager) IndexPlace(dir string) (rootID, rel string, ok bool) {
	rd, err := realPath(dir)
	if err != nil {
		return "", "", false
	}
	best := ""
	for _, r := range m.files.Roots() {
		if r.ID == "system" || strings.HasPrefix(r.ID, "net:") {
			continue // not indexed
		}
		rr, err := realPath(r.Path)
		if err != nil || !within(rd, rr) || len(rr) <= len(best) {
			continue
		}
		best, rootID = rr, r.ID
	}
	if best == "" {
		return "", "", false
	}
	r, err := filepath.Rel(best, rd)
	if err != nil {
		return "", "", false
	}
	if r = filepath.ToSlash(r); r == "." {
		return rootID, "", true
	}
	return rootID, "/" + r, true
}
