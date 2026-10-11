// Package spaces gives every person their own space on NoCapOS: a private
// home folder, the shared folders they've been given access to, the App Store
// apps they may use, and whether they sign in to the desktop at all.
//
// The rules live here as small pure functions (permission resolution, path
// containment, app visibility) so they can be tested on their own; Manager
// applies them to the database and the storage locations.
package spaces

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"alfaos/alfad/internal/store"
)

// Perm is what someone may do in a location.
type Perm string

const (
	None      Perm = store.PermNone
	ReadOnly  Perm = store.PermRO
	ReadWrite Perm = store.PermRW
)

// CanRead and CanWrite read naturally at call sites.
func (p Perm) CanRead() bool  { return p == ReadOnly || p == ReadWrite }
func (p Perm) CanWrite() bool { return p == ReadWrite }

// ParsePerm accepts rw, ro and none.
func ParsePerm(s string) (Perm, error) {
	switch Perm(s) {
	case ReadWrite, ReadOnly, None:
		return Perm(s), nil
	}
	return None, errors.New("permission must be rw, ro or none")
}

// Viewer is the person asking.
type Viewer struct {
	Name  string
	Admin bool
}

// FolderPerm resolves what a person may do in a shared folder. Administrators
// can always reach every shared folder; everyone else gets what the folder's
// permission list says (nothing when they aren't on it). A folder locked
// read-only is read-only for everyone.
func FolderPerm(v Viewer, f *store.SharedFolder, perms map[string]string) Perm {
	if f == nil || v.Name == "" {
		return None
	}
	p := None
	if v.Admin {
		p = ReadWrite
	} else if got, ok := perms[strings.ToLower(v.Name)]; ok {
		p = Perm(got)
	}
	if p == ReadWrite && f.ReadOnly {
		p = ReadOnly
	}
	if p != ReadWrite && p != ReadOnly {
		return None
	}
	return p
}

// AppAllowed reports whether a person may use an installed app. Admins can
// use every app; apps without rules are open to everyone.
func AppAllowed(rule *store.AppAccess, v Viewer) bool {
	if v.Admin || rule == nil || rule.Everyone {
		return true
	}
	for _, u := range rule.Users {
		if strings.EqualFold(u, v.Name) {
			return true
		}
	}
	return false
}

// within reports whether p is dir or inside it (case-insensitive on Windows).
func within(p, dir string) bool {
	if runtime.GOOS == "windows" {
		p, dir = strings.ToLower(p), strings.ToLower(dir)
	}
	dir = strings.TrimSuffix(dir, string(filepath.Separator))
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// realPath resolves symlinks in p. For a path that doesn't exist (yet), the
// longest existing parent is resolved and the rest appended, so a link
// anywhere along the way is still followed.
func realPath(p string) (string, error) {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p, nil
	}
	rp, err := realPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(rp, filepath.Base(p)), nil
}

// ErrOutside means a path leads out of the folder it must stay in.
var ErrOutside = errors.New("that path leads outside the folder")

// Inside joins rel (a slash path from a client; ".." and absolute forms are
// cleaned away) onto base and resolves symlinks, failing with ErrOutside
// when the result isn't base or inside it. It returns the real path.
func Inside(base, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", ErrOutside
	}
	rb, err := realPath(base)
	if err != nil {
		return "", err
	}
	clean := filepath.FromSlash(strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(rel, `\`, "/")), "/"))
	full, err := realPath(filepath.Join(rb, clean))
	if err != nil {
		return "", err
	}
	if !within(full, rb) {
		return "", ErrOutside
	}
	return full, nil
}

// IsInside reports whether path (resolving symlinks) is dir or inside it.
func IsInside(p, dir string) bool {
	rp, err := realPath(p)
	if err != nil {
		return false
	}
	rd, err := realPath(dir)
	if err != nil {
		return false
	}
	return within(rp, rd)
}
