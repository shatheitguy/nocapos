package netdrive

import (
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"alfaos/alfad/internal/store"
)

// WebDAV serves the shared folders at /dav/<Share>/… for the "nocapos"
// account. Mount it from Windows ("Map network drive"), macOS Finder ("Connect
// to Server"), Linux file managers (davs://) or phone file apps.

// davAuth slows down password guessing: after 5 failures an address waits.
type davAuth struct {
	mu    sync.Mutex
	fails map[string]*failCount
}

type failCount struct {
	n     int
	until time.Time
}

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
		if len(a.fails) > 10000 {
			a.fails = map[string]*failCount{}
		}
		f = &failCount{}
		a.fails[ip] = f
	}
	f.n++
	if f.n >= 5 {
		f.until = time.Now().Add(time.Duration(f.n-4) * 30 * time.Second)
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
		if !m.flag(ctx, keyWebDAV) {
			http.NotFound(w, r)
			return
		}
		ip := clientIP(r)
		if m.dav.blocked(ip) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many wrong passwords; try again in a minute", http.StatusTooManyRequests)
			return
		}
		user, pw, ok := r.BasicAuth()
		if !ok || !m.CheckPassword(ctx, user, pw) {
			if ok {
				m.dav.failed(ip)
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="NoCapOS", charset="UTF-8"`)
			http.Error(w, "sign in with the NoCapOS sharing account", http.StatusUnauthorized)
			return
		}
		m.dav.ok(ip)
		shares, err := m.st.NetShares(ctx)
		if err != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		h := &webdav.Handler{Prefix: DAVPrefix, FileSystem: &davFS{m: m, shares: shares}, LockSystem: locks}
		h.ServeHTTP(w, r)
	})
}

// davFS is a virtual folder of the shares, each mapped to its folder on disk.
type davFS struct {
	m      *Manager
	shares []*store.NetShare
}

type davTarget struct {
	share *store.NetShare
	dir   webdav.Dir
	rest  string // path inside the share, "/…"
}

// resolve maps "/Share/a/b" to the share and "/a/b"; a nil share means the top level.
func (d *davFS) resolve(name string) (*davTarget, error) {
	name = path.Clean("/" + name)
	if name == "/" {
		return &davTarget{rest: "/"}, nil
	}
	first, rest, _ := strings.Cut(strings.TrimPrefix(name, "/"), "/")
	for _, sh := range d.shares {
		if strings.EqualFold(sh.Name, first) {
			p := d.m.sharePath(sh)
			if p == "" {
				return nil, os.ErrNotExist
			}
			return &davTarget{share: sh, dir: webdav.Dir(p), rest: "/" + rest}, nil
		}
	}
	return nil, os.ErrNotExist
}

// rel is the path inside the storage location, for NoCapOS's protection checks.
func (t *davTarget) rel() string {
	return strings.TrimPrefix(path.Join(t.share.Path, t.rest), "/")
}

func (t *davTarget) writable() error {
	if t.share == nil || t.share.ReadOnly {
		return os.ErrPermission
	}
	return nil
}

func (d *davFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	t, err := d.resolve(name)
	if err != nil {
		return err
	}
	if err := t.writable(); err != nil {
		return err
	}
	return t.dir.Mkdir(ctx, t.rest, perm)
}

const writeFlags = os.O_WRONLY | os.O_RDWR | os.O_CREATE | os.O_TRUNC | os.O_APPEND

func (d *davFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	t, err := d.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.share == nil {
		if flag&writeFlags != 0 {
			return nil, os.ErrPermission
		}
		return d.top(), nil
	}
	if flag&writeFlags != 0 {
		if err := t.writable(); err != nil {
			return nil, err
		}
	}
	return t.dir.OpenFile(ctx, t.rest, flag, perm)
}

func (d *davFS) RemoveAll(ctx context.Context, name string) error {
	t, err := d.resolve(name)
	if err != nil {
		return err
	}
	if err := t.writable(); err != nil || t.rest == "/" {
		return os.ErrPermission
	}
	if err := d.m.files.CheckChange(t.share.Root, t.rel()); err != nil {
		return os.ErrPermission
	}
	return t.dir.RemoveAll(ctx, t.rest)
}

func (d *davFS) Rename(ctx context.Context, oldName, newName string) error {
	from, err := d.resolve(oldName)
	if err != nil {
		return err
	}
	to, err := d.resolve(newName)
	if err != nil {
		return err
	}
	if from.writable() != nil || to.writable() != nil || from.share.ID != to.share.ID || from.rest == "/" || to.rest == "/" {
		return os.ErrPermission
	}
	if err := d.m.files.CheckChange(from.share.Root, from.rel()); err != nil {
		return os.ErrPermission
	}
	return from.dir.Rename(ctx, from.rest, to.rest)
}

func (d *davFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	t, err := d.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.share == nil {
		return dirInfo{name: "/"}, nil
	}
	fi, err := t.dir.Stat(ctx, t.rest)
	if err == nil && t.rest == "/" {
		return named{fi, t.share.Name}, nil
	}
	return fi, err
}

// top is the read-only folder listing the shares.
func (d *davFS) top() webdav.File {
	var infos []fs.FileInfo
	for _, sh := range d.shares {
		p := d.m.sharePath(sh)
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			infos = append(infos, named{fi, sh.Name})
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
