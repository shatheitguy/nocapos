package netdrive

import (
	"context"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"alfaos/alfad/internal/spaces"
)

// WebDAV serves shared folders at /dav/<Folder>/… and each person's home at
// /dav/<their user name>/, for people signing in with their NoCapOS user
// name and password (or the old shared account while it's still in use).
// Mount it from Windows ("Map network drive"), macOS Finder ("Connect to
// Server"), Linux file managers (davs://) or phone file apps.

// davAuth slows down password guessing: after 5 failures an address waits.
type davAuth struct {
	mu    sync.Mutex
	fails map[string]*failCount
}

type failCount struct {
	n     int
	until time.Time
	last  time.Time
}

// maxDavFails bounds the failure table. When it's full, entries whose wait
// is over go first, then the least recently seen, so a flood of addresses
// can't wipe the wait of the one actually guessing.
const maxDavFails = 10000

func newDavAuth() *davAuth { return &davAuth{fails: map[string]*failCount{}} }

func (a *davAuth) blocked(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.fails[ip]
	return f != nil && time.Now().Before(f.until)
}

func (a *davAuth) failed(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.fails[ip]
	if f == nil {
		if len(a.fails) >= maxDavFails {
			a.evictLocked(time.Now())
		}
		f = &failCount{}
		a.fails[ip] = f
	}
	f.n++
	f.last = time.Now()
	if f.n >= 5 {
		f.until = time.Now().Add(time.Duration(f.n-4) * 30 * time.Second)
	}
}

func (a *davAuth) evictLocked(now time.Time) {
	for k, f := range a.fails {
		if !now.Before(f.until) && now.Sub(f.last) > 10*time.Minute {
			delete(a.fails, k)
		}
	}
	for len(a.fails) >= maxDavFails {
		// The least recently seen address that isn't waiting right now; only
		// if every address is waiting does one of those go.
		var oldest, oldestBlocked string
		var at, atBlocked time.Time
		for k, f := range a.fails {
			if now.Before(f.until) {
				if oldestBlocked == "" || f.last.Before(atBlocked) {
					oldestBlocked, atBlocked = k, f.last
				}
			} else if oldest == "" || f.last.Before(at) {
				oldest, at = k, f.last
			}
		}
		if oldest == "" {
			oldest = oldestBlocked
		}
		delete(a.fails, oldest)
	}
}

func (a *davAuth) ok(ip string) {
	a.mu.Lock()
	delete(a.fails, ip)
	a.mu.Unlock()
}

func (a *davAuth) reset() {
	a.mu.Lock()
	a.fails = map[string]*failCount{}
	a.mu.Unlock()
}

// DAVPrefix is where WebDAV is served.
const DAVPrefix = "/dav"

// DAVHandler serves WebDAV (when turned on) behind Basic authentication.
func (m *Manager) DAVHandler(clientIP func(*http.Request) string) http.Handler {
	locks := webdav.NewMemLS()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !m.flag(ctx, keyWebDAV) || m.spaces == nil {
			http.NotFound(w, r)
			return
		}
		ip := clientIP(r)
		if m.dav.blocked(ip) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many wrong passwords; try again in a minute", http.StatusTooManyRequests)
			return
		}
		user, pw, hasAuth := r.BasicAuth()
		if !hasAuth {
			if g := m.davGuest(r.URL.Path); g != nil { // a folder open to guests (guest.go)
				(&webdav.Handler{Prefix: DAVPrefix, FileSystem: &davFS{m: m, entries: g}, LockSystem: locks}).ServeHTTP(w, r)
				return
			}
		}
		var entries []davEntry
		ok := false
		if hasAuth {
			entries, ok = m.davEntries(ctx, user, pw)
		}
		if !ok {
			if hasAuth {
				m.dav.failed(ip)
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="NoCapOS", charset="UTF-8"`)
			http.Error(w, "sign in with your NoCapOS user name and password", http.StatusUnauthorized)
			return
		}
		m.dav.ok(ip)
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodPost: // all three serve the file
			downloadOnly(w, r.URL.Path)
		}
		h := &webdav.Handler{Prefix: DAVPrefix, FileSystem: &davFS{m: m, entries: entries}, LockSystem: locks}
		h.ServeHTTP(w, r)
	})
}

// downloadOnly makes a file served from the NoCapOS address a download and
// never a page: people's files share the origin with the desktop, so an
// uploaded .html or .svg opened in the browser must not run script there.
func downloadOnly(w http.ResponseWriter, p string) {
	h := w.Header()
	h.Set("Content-Type", davContentType(p))
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(p)}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

// davContentType keeps harmless types (pictures, audio, video, plain text,
// PDF) and sends everything else, notably HTML, SVG, XML and scripts, as
// opaque bytes.
func davContentType(p string) string {
	t := mime.TypeByExtension(strings.ToLower(path.Ext(p)))
	base, _, _ := strings.Cut(t, ";")
	base = strings.TrimSpace(strings.ToLower(base))
	switch {
	case base == "", strings.Contains(base, "xml"), strings.Contains(base, "html"), strings.Contains(base, "svg"),
		strings.Contains(base, "script"), strings.Contains(base, "xsl"):
		return "application/octet-stream"
	case strings.HasPrefix(base, "image/"), strings.HasPrefix(base, "audio/"), strings.HasPrefix(base, "video/"),
		base == "text/plain", base == "application/pdf":
		return t
	}
	return "application/octet-stream"
}

// davEntries signs someone in and lists the folders they can reach.
func (m *Manager) davEntries(ctx context.Context, user, pw string) ([]davEntry, bool) {
	if pw == "" {
		return nil, false
	}
	people, err := m.spaces.People(ctx)
	if err != nil {
		return nil, false
	}
	if strings.EqualFold(user, ShareUser) {
		if !m.CheckPassword(ctx, user, pw) {
			return nil, false
		}
		var out []davEntry
		for _, f := range m.spaces.NetFolders(people) {
			out = append(out, davEntry{name: f.Name, root: spaces.ShareRootID(f.ID), path: f.FullPath, write: !f.ReadOnly, recycle: f.Recycle, listed: f.Network})
		}
		return out, true
	}
	if m.checkUser == nil {
		return nil, false
	}
	name, admin, ok := m.checkUser(ctx, user, pw)
	if !ok {
		return nil, false
	}
	var out []davEntry
	for _, f := range m.spaces.NetFolders(people) {
		p := spaces.FolderPerm(spaces.Viewer{Name: name, Admin: admin}, &f.SharedFolder, nil)
		for who, perm := range f.Perms {
			if strings.EqualFold(who, name) {
				p = perm
			}
		}
		if p == spaces.None {
			continue
		}
		out = append(out, davEntry{name: f.Name, root: spaces.ShareRootID(f.ID), path: f.FullPath, write: p == spaces.ReadWrite, recycle: f.Recycle, listed: f.Network})
	}
	if home, err := m.spaces.EnsureHome(name); err == nil {
		// Shared folders win a name clash, as with Samba's [homes].
		out = append(out, davEntry{name: name, root: spaces.HomeRootID(name), path: home, write: true, recycle: true, listed: true})
	}
	return out, true
}

// davEntry is one top-level folder in someone's WebDAV view.
type davEntry struct {
	name    string
	root    string // storage location id (for protection checks and the recycle bin)
	path    string // real folder on disk
	write   bool
	recycle bool
	listed  bool // shown at the top level (hidden folders open by name)
}

// davFS is a virtual folder of the person's folders. Each folder is opened
// through os.Root, so "..", absolute paths and symlinks can't lead out of it.
type davFS struct {
	m       *Manager
	entries []davEntry
}

type davTarget struct {
	e   *davEntry
	rel string // inside the folder, "." for the folder itself
}

// resolve maps "/Folder/a/b" to the folder and "a/b"; a nil entry means the top level.
func (d *davFS) resolve(name string) (*davTarget, error) {
	name = path.Clean("/" + name)
	if name == "/" {
		return &davTarget{rel: "."}, nil
	}
	first, rest, _ := strings.Cut(strings.TrimPrefix(name, "/"), "/")
	for i := range d.entries {
		if strings.EqualFold(d.entries[i].name, first) {
			rel := rest
			if rel == "" {
				rel = "."
			}
			return &davTarget{e: &d.entries[i], rel: rel}, nil
		}
	}
	return nil, os.ErrNotExist
}

func (t *davTarget) writable() error {
	if t.e == nil || !t.e.write {
		return os.ErrPermission
	}
	return nil
}

func (t *davTarget) open() (*os.Root, error) { return os.OpenRoot(t.e.path) }

func (d *davFS) Mkdir(_ context.Context, name string, perm os.FileMode) error {
	t, err := d.resolve(name)
	if err != nil {
		return err
	}
	if err := t.writable(); err != nil || t.rel == "." {
		return os.ErrPermission
	}
	rt, err := t.open()
	if err != nil {
		return err
	}
	defer rt.Close()
	if err := rt.Mkdir(t.rel, perm); err != nil {
		return err
	}
	d.m.spaces.Own(t.e.root, t.rel) // in a Linux home: the person's own
	return nil
}

const writeFlags = os.O_WRONLY | os.O_RDWR | os.O_CREATE | os.O_TRUNC | os.O_APPEND

func (d *davFS) OpenFile(_ context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	t, err := d.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.e == nil {
		if flag&writeFlags != 0 {
			return nil, os.ErrPermission
		}
		return d.top(), nil
	}
	if flag&writeFlags != 0 {
		if err := t.writable(); err != nil || t.rel == "." {
			return nil, os.ErrPermission
		}
	}
	rt, err := t.open()
	if err != nil {
		return nil, err
	}
	defer rt.Close() // the opened file stays valid
	f, err := rt.OpenFile(t.rel, flag, perm)
	if err != nil {
		return nil, err // not a typed nil inside the interface
	}
	if flag&os.O_CREATE != 0 {
		d.m.spaces.Own(t.e.root, t.rel)
	}
	return f, nil
}

func (d *davFS) RemoveAll(_ context.Context, name string) error {
	t, err := d.resolve(name)
	if err != nil {
		return err
	}
	if err := t.writable(); err != nil || t.rel == "." {
		return os.ErrPermission
	}
	if err := d.m.files.CheckChange(t.e.root, t.rel); err != nil {
		return os.ErrPermission
	}
	if t.e.recycle {
		return d.m.files.Delete(t.e.root, []string{t.rel}, false)
	}
	rt, err := t.open()
	if err != nil {
		return err
	}
	defer rt.Close()
	return rt.RemoveAll(t.rel)
}

func (d *davFS) Rename(_ context.Context, oldName, newName string) error {
	from, err := d.resolve(oldName)
	if err != nil {
		return err
	}
	to, err := d.resolve(newName)
	if err != nil {
		return err
	}
	if from.writable() != nil || to.writable() != nil || from.e != to.e || from.rel == "." || to.rel == "." {
		return os.ErrPermission
	}
	if err := d.m.files.CheckChange(from.e.root, from.rel); err != nil {
		return os.ErrPermission
	}
	rt, err := from.open()
	if err != nil {
		return err
	}
	defer rt.Close()
	return rt.Rename(from.rel, to.rel)
}

func (d *davFS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	t, err := d.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.e == nil {
		return dirInfo{name: "/"}, nil
	}
	rt, err := t.open()
	if err != nil {
		return nil, err
	}
	defer rt.Close()
	fi, err := rt.Stat(t.rel)
	if err == nil && t.rel == "." {
		return named{fi, t.e.name}, nil
	}
	return fi, err
}

// top is the read-only folder listing the person's folders.
func (d *davFS) top() webdav.File {
	var infos []fs.FileInfo
	for _, e := range d.entries {
		if !e.listed {
			continue
		}
		if fi, err := os.Stat(e.path); err == nil && fi.IsDir() {
			infos = append(infos, named{fi, e.name})
		}
	}
	return &topDir{infos: infos}
}

type named struct {
	fs.FileInfo
	name string
}

func (n named) Name() string { return n.name }

type dirInfo struct{ name string }

func (d dirInfo) Name() string       { return d.name }
func (d dirInfo) Size() int64        { return 0 }
func (d dirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (d dirInfo) ModTime() time.Time { return time.Time{} }
func (d dirInfo) IsDir() bool        { return true }
func (d dirInfo) Sys() any           { return nil }

type topDir struct {
	infos []fs.FileInfo
	read  bool
}

func (t *topDir) Close() error                   { return nil }
func (t *topDir) Read([]byte) (int, error)       { return 0, io.EOF }
func (t *topDir) Seek(int64, int) (int64, error) { return 0, nil }
func (t *topDir) Write([]byte) (int, error)      { return 0, os.ErrPermission }
func (t *topDir) Stat() (fs.FileInfo, error)     { return dirInfo{name: "/"}, nil }
func (t *topDir) Readdir(count int) ([]fs.FileInfo, error) {
	if t.read {
		if count > 0 {
			return nil, io.EOF
		}
		return nil, nil
	}
	t.read = true
	return t.infos, nil
}

// ClientIP is the default address function (no proxy headers).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
