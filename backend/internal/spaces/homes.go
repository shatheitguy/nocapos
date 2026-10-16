package spaces

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path"
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
// Only Linux homes are touched.
//
// The home belongs to its owner, who can swap a folder for a link to /etc
// while this runs. So the walk goes through an os.Root opened on the home:
// every step is resolved relative to an open directory and can't leave the
// home, links are changed themselves (never followed), and a folder is only
// descended into through its own Root.
func (m *Manager) Own(rootID string, rels ...string) {
	if !m.LinuxHomes() || m.owner == nil || !strings.HasPrefix(rootID, homePrefix) {
		return
	}
	var todo []string
	for _, rel := range rels {
		if c := strings.Trim(rel, "/"); c != "" && c != "." {
			todo = append(todo, c) // never the whole home
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
	r, err := os.OpenRoot(home)
	if err != nil {
		return
	}
	defer r.Close()
	for _, rel := range todo {
		if _, err := Inside(home, rel); err != nil {
			continue
		}
		dir, base := path.Split(filepath.ToSlash(rel))
		parent := r
		if dir = strings.Trim(dir, "/"); dir != "" {
			if parent, err = r.OpenRoot(dir); err != nil {
				continue
			}
		}
		ownTree(parent, base, uid, gid, 0)
		if parent != r {
			parent.Close()
		}
	}
}

// lchownIn changes one entry's owner without following a link; replaceable
// in tests.
var lchownIn = func(r *os.Root, name string, uid, gid int) error { return r.Lchown(name, uid, gid) }

const maxOwnDepth = 128

func ownTree(r *os.Root, name string, uid, gid, depth int) {
	fi, err := r.Lstat(name)
	if err != nil {
		return
	}
	_ = lchownIn(r, name, uid, gid)
	if !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 || depth >= maxOwnDepth {
		return
	}
	sub, err := r.OpenRoot(name)
	if err != nil {
		return
	}
	defer sub.Close()
	// Swapped for a link since Lstat? OpenRoot followed it, but only within
	// r, and the target isn't a folder we were asked about: leave it.
	if fi2, err := sub.Stat("."); err != nil || !os.SameFile(fi, fi2) {
		return
	}
	d, err := sub.Open(".")
	if err != nil {
		return
	}
	entries, _ := d.ReadDir(-1)
	d.Close()
	for _, e := range entries {
		ownTree(sub, e.Name(), uid, gid, depth+1)
	}
}

// EnsureHomeFolder makes a folder at the top of a person's home (Photos,
// say) without following links, and gives it to them.
func (m *Manager) EnsureHomeFolder(name, folder string) error {
	home, err := m.EnsureHome(name)
	if err != nil {
		return err
	}
	if !validHomeName(folder) {
		return ErrOutside
	}
	r, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := r.Mkdir(folder, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if fi, err := r.Lstat(folder); err != nil || !fi.IsDir() {
		return ErrOutside // a link or a file where the folder should be
	}
	m.Own(HomeRootID(name), "/"+folder)
	return nil
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
