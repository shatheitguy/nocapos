// Package files implements the NAS file service behind the Files app.
//
// Every operation goes through os.Root, which makes the kernel-level lookups
// traversal-resistant: "..", absolute paths and symlinks cannot escape the
// configured storage roots, even under concurrent filesystem changes.
package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrUnknownRoot = errors.New("unknown storage location")
	ErrBadPath     = errors.New("invalid path")
	ErrBadName     = errors.New(`invalid name: use up to 255 characters without / \ : * ? " < > |`)
	ErrIsRoot      = errors.New("not allowed on the top-level folder")
	ErrNotDir      = errors.New("not a folder")
	ErrIsDir       = errors.New("is a folder")
	ErrTooLarge    = errors.New("file is too large")
	ErrNotText     = errors.New("not a UTF-8 text file")
	ErrChanged     = errors.New("file was changed by someone else; reload it first")
	ErrIntoSelf    = errors.New("cannot copy or move a folder into itself")
	ErrProtected   = errors.New("NoCapOS needs this to run, so it can't be deleted, moved or renamed from Files")
)

// RecycleDir holds deleted items at the top of each root.
const RecycleDir = ".recycle"

const (
	uploadPrefix = ".upload-"
	uploadSuffix = ".part"
	maxCopyDepth = 64
)

type Root struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"-"`
}

type Entry struct {
	Name    string    `json:"name"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Symlink bool      `json:"symlink,omitempty"`
	// Mode is the ls-style permission string, e.g. "drwxr-xr-x".
	Mode string `json:"mode"`
	// Perm is the permission bits (incl. setuid/setgid/sticky) as a number.
	Perm  uint32 `json:"perm"`
	Owner string `json:"owner,omitempty"` // Unix only
	Group string `json:"group,omitempty"` // Unix only
}

// UnixPerms reports whether Mode/Perm are real Unix permissions (false on
// Windows, where Go synthesizes them from the read-only attribute).
const UnixPerms = runtime.GOOS != "windows"

func permBits(m fs.FileMode) uint32 {
	p := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		p |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		p |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		p |= 0o1000
	}
	return p
}

type Service struct {
	roots []Root
	// protected trees (NoCapOS's own program, data and source folders):
	// Delete, move and rename refuse them, anything inside them, and any
	// folder that contains them.
	protected []string
	// points are folders that must not be deleted, moved or renamed
	// themselves (nor any folder containing them), but whose contents are
	// ordinary files — e.g. /home or /srv on a root install.
	points []string
}

// ProtectPoints marks folders that may not themselves be removed or moved.
func (s *Service) ProtectPoints(dirs ...string) {
	for _, d := range dirs {
		if abs, err := filepath.Abs(d); err == nil {
			s.points = append(s.points, filepath.Clean(abs))
		}
	}
}

// SystemTrees are the core OS folders a root install protects completely.
var SystemTrees = []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32", "/proc", "/run", "/sbin", "/sys", "/usr", "/var/lib", "/snap"}

// SystemPoints are top-level folders a root install keeps in place while
// leaving their contents usable.
var SystemPoints = []string{"/", "/home", "/root", "/srv", "/opt", "/var", "/mnt", "/media", "/tmp"}

// Protect marks folders NoCapOS needs to run. Storage locations that live
// inside a protected folder (e.g. NoCap Drive in the data folder) stay usable.
func (s *Service) Protect(dirs ...string) {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if abs, err := filepath.Abs(d); err == nil {
			if real, err := filepath.EvalSymlinks(abs); err == nil {
				abs = real
			}
			s.protected = append(s.protected, filepath.Clean(abs))
		}
	}
}

// within reports whether p is dir or inside it (case-insensitive on Windows).
func within(p, dir string) bool {
	if runtime.GOOS == "windows" {
		p, dir = strings.ToLower(p), strings.ToLower(dir)
	}
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// guard refuses to delete, move or rename protected paths.
func (s *Service) guard(rootID, rel string) error {
	r, err := s.Root(rootID)
	if err != nil {
		return err
	}
	full := filepath.Clean(filepath.Join(r.Path, native(rel)))
	if real, err := filepath.EvalSymlinks(full); err == nil {
		full = real
	}
	for _, p := range s.points {
		if within(p, full) { // the item is a protected folder or contains one
			return ErrProtected
		}
	}
	for _, p := range s.protected {
		if within(p, full) { // the item is a protected folder or contains one
			return ErrProtected
		}
		if within(full, p) {
			// Inside a protected folder: allowed only within a storage location
			// that itself lives there.
			inRoot := false
			for _, sr := range s.roots {
				if rp, err := filepath.Abs(sr.Path); err == nil && within(rp, p) && within(full, rp) && !within(rp, full) {
					inRoot = true
					break
				}
			}
			if !inRoot {
				return ErrProtected
			}
		}
	}
	return nil
}

func New(roots []Root) (*Service, error) {
	if len(roots) == 0 {
		return nil, errors.New("no storage roots configured")
	}
	seen := map[string]bool{}
	for _, r := range roots {
		if r.ID == "" || seen[r.ID] {
			return nil, fmt.Errorf("storage root %q: missing or duplicate id", r.Name)
		}
		seen[r.ID] = true
		if err := os.MkdirAll(r.Path, 0o750); err != nil {
			return nil, fmt.Errorf("storage root %q: %w", r.Name, err)
		}
	}
	return &Service{roots: roots}, nil
}

func (s *Service) Roots() []Root { return s.roots }

func (s *Service) Root(id string) (*Root, error) {
	for i := range s.roots {
		if s.roots[i].ID == id {
			return &s.roots[i], nil
		}
	}
	return nil, ErrUnknownRoot
}

func (s *Service) open(id string) (*os.Root, error) {
	r, err := s.Root(id)
	if err != nil {
		return nil, err
	}
	return os.OpenRoot(r.Path)
}

// Clean turns a client path ("/a/b", "a\\b", "") into a root-relative slash
// path ("a/b", or "." for the root). Lexical cleaning removes "..";
// os.Root enforces containment for everything else.
func Clean(p string) (string, error) {
	if strings.ContainsRune(p, 0) || len(p) > 4096 {
		return "", ErrBadPath
	}
	c := path.Clean("/" + strings.ReplaceAll(p, `\`, "/"))
	rel := strings.TrimPrefix(c, "/")
	if rel == "" {
		return ".", nil
	}
	return rel, nil
}

// ValidName rejects names that are unsafe or non-portable across Linux,
// Windows and SMB clients.
func ValidName(n string) error {
	if n == "" || n == "." || n == ".." || len(n) > 255 || !utf8.ValidString(n) ||
		strings.ContainsAny(n, "/\\:*?\"<>|\x00") || strings.TrimSpace(n) != n || strings.HasSuffix(n, ".") {
		return ErrBadName
	}
	for _, r := range n {
		if r < 0x20 {
			return ErrBadName
		}
	}
	return nil
}

func native(rel string) string { return filepath.FromSlash(rel) }

func inRecycle(rel string) bool { return rel == RecycleDir || strings.HasPrefix(rel, RecycleDir+"/") }

// List returns the entries of a folder, folders first.
func (s *Service) List(rootID, rel string) ([]Entry, error) {
	rt, err := s.open(rootID)
	if err != nil {
		return nil, err
	}
	defer rt.Close()
	f, err := rt.Open(native(rel))
	if errors.Is(err, fs.ErrNotExist) && rel == RecycleDir {
		return []Entry{}, nil // the bin is created on first delete
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, ErrNotDir
	}
	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if (rel == "." && name == RecycleDir) || (strings.HasPrefix(name, uploadPrefix) && strings.HasSuffix(name, uploadSuffix)) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue // vanished while listing
		}
		e := Entry{Name: name}
		if de.Type()&fs.ModeSymlink != 0 {
			e.Symlink = true
			// Stat through the root: links pointing outside it fail and are shown as files.
			if ti, err := rt.Stat(native(path.Join(rel, name))); err == nil {
				info = ti
			}
		}
		e.Dir = info.IsDir()
		if !e.Dir {
			e.Size = info.Size()
		}
		e.ModTime = info.ModTime().UTC()
		e.Mode, e.Perm = info.Mode().String(), permBits(info.Mode())
		e.Owner, e.Group = owners(info)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Open returns a regular file for reading.
func (s *Service) Open(rootID, rel string) (*os.File, fs.FileInfo, error) {
	rt, err := s.open(rootID)
	if err != nil {
		return nil, nil, err
	}
	defer rt.Close() // the returned file stays valid
	f, err := rt.Open(native(rel))
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if st.IsDir() {
		f.Close()
		return nil, nil, ErrIsDir
	}
	return f, st, nil
}

func (s *Service) ReadText(rootID, rel string, max int64) (string, fs.FileInfo, error) {
	f, st, err := s.Open(rootID, rel)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	if st.Size() > max {
		return "", nil, ErrTooLarge
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return "", nil, err
	}
	if int64(len(b)) > max {
		return "", nil, ErrTooLarge
	}
	if !utf8.Valid(b) {
		return "", nil, ErrNotText
	}
	return string(b), st, nil
}

// WriteText atomically replaces (or creates) a text file. If expected is set
// and the file's modification time differs, ErrChanged is returned.
func (s *Service) WriteText(rootID, rel, content string, expected *time.Time) (fs.FileInfo, error) {
	if rel == "." {
		return nil, ErrIsRoot
	}
	if err := ValidName(path.Base(rel)); err != nil {
		return nil, err
	}
	rt, err := s.open(rootID)
	if err != nil {
		return nil, err
	}
	defer rt.Close()
	if st, err := rt.Stat(native(rel)); err == nil {
		if st.IsDir() {
			return nil, ErrIsDir
		}
		if expected != nil && !st.ModTime().Equal(*expected) {
			return nil, ErrChanged
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	tmp := path.Join(path.Dir(rel), uploadPrefix+randHex()+uploadSuffix)
	if err := rt.WriteFile(native(tmp), []byte(content), 0o644); err != nil {
		return nil, err
	}
	if err := rt.Rename(native(tmp), native(rel)); err != nil {
		_ = rt.Remove(native(tmp))
		return nil, err
	}
	return rt.Stat(native(rel))
}

func (s *Service) Mkdir(rootID, dir, name string) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	rt, err := s.open(rootID)
	if err != nil {
		return "", err
	}
	defer rt.Close()
	rel := path.Join(dir, name)
	return rel, rt.Mkdir(native(rel), 0o755)
}

func (s *Service) Rename(rootID, rel, newName string) (string, error) {
	if rel == "." || rel == RecycleDir {
		return "", ErrIsRoot
	}
	if err := ValidName(newName); err != nil {
		return "", err
	}
	if err := s.guard(rootID, rel); err != nil {
		return "", err
	}
	rt, err := s.open(rootID)
	if err != nil {
		return "", err
	}
	defer rt.Close()
	dst := path.Join(path.Dir(rel), newName)
	if dst == rel {
		return dst, nil
	}
	// Allow case-only renames ("a.txt" → "A.txt") on case-insensitive filesystems.
	if !strings.EqualFold(dst, rel) {
		if _, err := rt.Lstat(native(dst)); err == nil {
			return "", fs.ErrExist
		}
	}
	return dst, rt.Rename(native(rel), native(dst))
}

// Delete moves items to the recycle bin, or removes them for good when
// permanent is set or they are already in the bin.
func (s *Service) Delete(rootID string, rels []string, permanent bool) error {
	for _, rel := range rels {
		if err := s.guard(rootID, rel); err != nil {
			return err
		}
	}
	rt, err := s.open(rootID)
	if err != nil {
		return err
	}
	defer rt.Close()
	stamp := time.Now().Format("20060102-150405")
	for _, rel := range rels {
		if rel == "." {
			return ErrIsRoot
		}
		if permanent || inRecycle(rel) {
			if rel == RecycleDir { // empty the bin, keep the folder
				if err := rt.RemoveAll(native(rel)); err != nil {
					return err
				}
				if err := rt.Mkdir(native(RecycleDir), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
					return err
				}
				continue
			}
			if err := rt.RemoveAll(native(rel)); err != nil {
				return err
			}
			continue
		}
		if err := rt.MkdirAll(native(RecycleDir), 0o755); err != nil {
			return err
		}
		dst := uniqueName(rt, RecycleDir, stamp+"_"+path.Base(rel))
		if err := rt.Rename(native(rel), native(dst)); err != nil {
			return err
		}
	}
	return nil
}

// Transfer copies or moves items into destDir, renaming on conflict. It
// returns the new relative paths.
func (s *Service) Transfer(rootID string, rels []string, destDir string, move bool) ([]string, error) {
	if move {
		for _, rel := range rels {
			if err := s.guard(rootID, rel); err != nil {
				return nil, err
			}
		}
	}
	rt, err := s.open(rootID)
	if err != nil {
		return nil, err
	}
	defer rt.Close()
	if st, err := rt.Stat(native(destDir)); err != nil {
		return nil, err
	} else if !st.IsDir() {
		return nil, ErrNotDir
	}
	var out []string
	for _, rel := range rels {
		if rel == "." {
			return out, ErrIsRoot
		}
		if destDir == rel || strings.HasPrefix(destDir+"/", rel+"/") {
			return out, ErrIntoSelf
		}
		if move && path.Dir(rel) == destDir {
			out = append(out, rel) // already there
			continue
		}
		dst := uniqueName(rt, destDir, path.Base(rel))
		if move {
			err = rt.Rename(native(rel), native(dst))
		} else {
			err = copyTree(rt, rel, dst, 0)
		}
		if err != nil {
			return out, err
		}
		out = append(out, dst)
	}
	return out, nil
}

func copyTree(rt *os.Root, src, dst string, depth int) error {
	if depth > maxCopyDepth {
		return fmt.Errorf("copy %s: folder nesting too deep", src)
	}
	st, err := rt.Lstat(native(src))
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		return nil // links are not followed or duplicated
	case st.IsDir():
		if err := rt.Mkdir(native(dst), 0o755); err != nil {
			return err
		}
		f, err := rt.Open(native(src))
		if err != nil {
			return err
		}
		des, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			return err
		}
		for _, de := range des {
			if err := copyTree(rt, path.Join(src, de.Name()), path.Join(dst, de.Name()), depth+1); err != nil {
				return err
			}
		}
		return nil
	case st.Mode().IsRegular():
		in, err := rt.Open(native(src))
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := rt.OpenFile(native(dst), os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm()|0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			_ = rt.Remove(native(dst))
			return err
		}
		return out.Close()
	default:
		return nil // devices, sockets, pipes
	}
}

// Upload streams r into dir/name (renamed on conflict) via a temp file, so a
// partial upload never appears under the final name. Returns the final path.
func (s *Service) Upload(rootID, dir, name string, r io.Reader) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	rt, err := s.open(rootID)
	if err != nil {
		return "", err
	}
	defer rt.Close()
	if st, err := rt.Stat(native(dir)); err != nil {
		return "", err
	} else if !st.IsDir() {
		return "", ErrNotDir
	}
	tmp := path.Join(dir, uploadPrefix+randHex()+uploadSuffix)
	f, err := rt.OpenFile(native(tmp), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		_ = rt.Remove(native(tmp))
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = rt.Remove(native(tmp))
		return "", err
	}
	final := uniqueName(rt, dir, name)
	if err := rt.Rename(native(tmp), native(final)); err != nil {
		_ = rt.Remove(native(tmp))
		return "", err
	}
	return final, nil
}

// uniqueName returns dir/name, or dir/"name (n).ext" if that already exists.
func uniqueName(rt *os.Root, dir, name string) string {
	candidate := path.Join(dir, name)
	if _, err := rt.Lstat(native(candidate)); errors.Is(err, fs.ErrNotExist) {
		return candidate
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if ext == name { // dotfile like ".env"
		stem, ext = name, ""
	}
	for i := 1; i < 10000; i++ {
		candidate = path.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := rt.Lstat(native(candidate)); errors.Is(err, fs.ErrNotExist) {
			return candidate
		}
	}
	return path.Join(dir, stem+"-"+randHex()+ext)
}

func randHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
