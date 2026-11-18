package appdata

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"alfaos/alfad/internal/files"
)

// The App data folder: one folder for every app's files, with one folder per
// app inside it named after the app, and the app's own folders inside that:
//
//	/srv/appdata/jellyfin/config
//	/srv/appdata/jellyfin/cache
//	/srv/appdata/immich/upload
//
// It is a real folder on the data disk, never inside Docker's own folder.

// DefaultRoot is where App data lives unless the admin moves it.
const DefaultRoot = "/srv/appdata"

// RootID is the Files location of the whole App data folder (admins only).
const RootID = "appdata"

// DefaultRootFor is the App data folder to use when none was chosen:
// env (the image OS points it at its data partition), else /srv/appdata
// on Linux, else a folder in NoCapOS's data folder (other systems, tests).
func DefaultRootFor(env, dataDir string) string {
	if env != "" {
		return filepath.Clean(env)
	}
	if runtime.GOOS == "linux" {
		return DefaultRoot
	}
	return filepath.Join(dataDir, "apps", "appdata")
}

// SafeName turns an app, container, project or volume name into a folder
// name: lowercase letters, numbers, ".", "_" and "-", never "." or "..".
func SafeName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) > 64 {
		out = strings.Trim(out[:64], "-.")
	}
	if out == "" {
		return "app"
	}
	return out
}

// AppDir is an app's own folder: <root>/<app>.
func AppDir(root, app string) string { return filepath.Join(root, SafeName(app)) }

// VolumeDir is one of an app's folders: <root>/<app>/<name>.
func VolumeDir(root, app, name string) string {
	return filepath.Join(root, SafeName(app), SafeName(name))
}

// Suggest is the folder offered by default when someone adds storage to a
// container or stack: <root>/<container or project>/<name>.
func Suggest(root, owner, name string) string {
	if strings.TrimSpace(name) == "" {
		name = "data"
	}
	return filepath.ToSlash(VolumeDir(root, owner, name))
}

// ValidateRoot checks a folder can hold App data: a full path, not the
// whole disk, not a core OS folder (or one holding them), not inside
// Docker's own folder and not NoCapOS's own files (its apps/ folder aside).
// The folder need not exist yet.
func ValidateRoot(p string, protected []string, dockerRoot string) error {
	clean, err := absClean(p)
	if err != nil {
		return err
	}
	if isTop(clean) {
		return errors.New("the whole disk can't be the App data folder")
	}
	for _, t := range files.SystemPoints {
		if clean == t {
			return errors.New("pick a folder inside " + t + ", not " + t + " itself")
		}
	}
	if err := checkSystem(clean); err != nil {
		return err
	}
	if dockerRoot != "" && (files.Within(clean, real(dockerRoot)) || files.Within(real(dockerRoot), clean)) {
		return errors.New("App data can't live in Docker's own folder")
	}
	for _, d := range protected {
		if files.Within(d, clean) {
			return errors.New("that folder holds NoCapOS's own files")
		}
		if files.Within(clean, d) && !appOwned(clean, d) {
			return errors.New("that folder is inside NoCapOS's own files")
		}
	}
	return nil
}

// hostFiles may be given to containers although they live in system
// folders: the clock and time zone (read only) and Docker's socket.
var hostFiles = map[string]bool{
	"/etc/localtime": true, "/etc/timezone": true,
	"/var/run/docker.sock": true, "/run/docker.sock": true,
}

// CheckBind checks a folder on the server someone wants to give a
// container: the same rules as App data folders (not the whole disk, not a
// core OS folder, not NoCapOS's own files), plus a few well-known host
// files. It need not exist yet.
func CheckBind(src string, protected []string, dockerRoot string) error {
	// The well-known host files are links on many systems (/etc/localtime
	// points into /usr/share/zoneinfo), so they're matched as written.
	if hostFiles[filepath.ToSlash(filepath.Clean(strings.TrimSpace(src)))] {
		return nil
	}
	clean, err := absClean(src)
	if err != nil {
		return err
	}
	if hostFiles[filepath.ToSlash(clean)] {
		return nil
	}
	if isTop(clean) {
		return errors.New(src + ": the whole disk can't be given to a container")
	}
	for _, t := range files.SystemPoints {
		if filepath.ToSlash(clean) == t {
			return errors.New(src + ": a top-level system folder; pick a folder inside it")
		}
	}
	if err := checkSystem(clean); err != nil {
		return errors.New(src + ": " + err.Error())
	}
	if dockerRoot != "" && files.Within(real(dockerRoot), clean) {
		return errors.New(src + ": holds Docker's own folder")
	}
	for _, d := range protected {
		if files.Within(d, clean) {
			return errors.New(src + ": holds NoCapOS's own files")
		}
		if files.Within(clean, d) && !appOwned(clean, d) {
			return errors.New(src + ": inside NoCapOS's own files")
		}
	}
	return nil
}

// checkSystem refuses core OS folders and folders holding them.
func checkSystem(p string) error {
	slash := filepath.ToSlash(p)
	for _, t := range files.SystemTrees {
		if within(t, slash) {
			return errors.New("holds core system folders")
		}
		if within(slash, t) {
			return errors.New("a core system folder")
		}
	}
	return nil
}

// within is files.Within on slash paths (system folders are Linux paths).
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// absClean cleans a full path and follows links in the part that exists,
// so a link can't point a folder somewhere it may not go.
func absClean(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || !(strings.HasPrefix(p, "/") || filepath.IsAbs(p)) {
		return "", errors.New("use a full path, like " + DefaultRoot)
	}
	if strings.ContainsAny(p, "\n\r\x00") {
		return "", errors.New("that path has characters folders can't have")
	}
	return real(filepath.Clean(p)), nil
}

// real follows links in the longest part of p that exists.
func real(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; cur = filepath.Dir(cur) {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return r
			}
			return filepath.Join(r, rest)
		}
		if filepath.Dir(cur) == cur {
			return p
		}
		if rest == "" {
			rest = filepath.Base(cur)
		} else {
			rest = filepath.Join(filepath.Base(cur), rest)
		}
	}
}

// Under reports whether p is inside root (not root itself).
func Under(p, root string) bool {
	if root == "" {
		return false
	}
	p, root = filepath.Clean(p), filepath.Clean(root)
	return p != root && files.Within(p, root)
}

// EnsureDir makes a folder for an app (and its parents in the App data
// folder), owned by uid:gid when given (-1 = leave as is). Folders are never
// world-writable. Only folders it creates are given an owner, so existing
// data keeps its own.
func EnsureDir(root, dir string, uid, gid int) error {
	if !Under(dir, root) {
		return errors.New("not inside the App data folder")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	cur := root
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if err == nil {
			if !st.IsDir() {
				return errors.New(cur + " isn't a folder")
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.Mkdir(cur, 0o755); err != nil && !os.IsExist(err) {
			return err
		}
		// The app's own folders belong to the app; its top folder stays root's.
		if i > 0 && (uid >= 0 || gid >= 0) {
			_ = os.Lchown(cur, uid, gid)
		}
	}
	return nil
}
