// Package filesync keeps folders the same on the two servers of a failover
// pair, file by file.
//
// Each server keeps an index of its files (Index: one row per file, the
// latest version, deletions as tombstones) and brings it up to date with
// what's on disk (Scan: after a change is seen, and every few minutes).
// The other server reads the index's changes in order and applies them
// (Apply): the newest version wins (Lamport clock, then the higher server
// ID); a file changed on both servers before they synced keeps the losing
// version next to it as "name (conflict from <server>).ext"; deletions
// move the file to the location's recycle bin (.recycle, as Files does)
// instead of removing it; downloads land in .nocap-partial first, resume where they stopped,
// and only replace the file once their SHA-256 matches.
package filesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Folders inside a location that never sync.
const (
	TrashDir   = ".recycle" // the Files recycle bin: each server keeps its own
	PartialDir = ".nocap-partial"
)

// Entry is one file's version.
type Entry struct {
	Path    string `json:"path"` // slash-separated, inside the location
	Size    int64  `json:"size"`
	MTime   int64  `json:"mtime"` // unix nanoseconds
	Mode    uint32 `json:"mode"`  // permission bits
	Hash    string `json:"hash"`  // SHA-256, hex ("" for a deletion)
	Deleted bool   `json:"deleted,omitempty"`
	Seq     int64  `json:"seq"`
	Lamport int64  `json:"lamport"`
	Origin  string `json:"origin"` // the server that wrote it ("" = this one, in the index)
}

// Change is an entry of a location, as the other server reads it.
type Change struct {
	Loc string `json:"loc"`
	Entry
}

// Index is where a server keeps its files' versions (the store).
type Index interface {
	// Files lists a location's entries by path (tombstones too).
	Files(ctx context.Context, loc string) (map[string]Entry, error)
	// PutLocal records a change made on this server (new seq, clock + 1).
	PutLocal(ctx context.Context, loc string, e Entry) error
	// PutRemote records a version taken from the other server.
	PutRemote(ctx context.Context, loc string, e Entry) error
	// Touch records new times for unchanged content (no new version).
	Touch(ctx context.Context, loc, path string, size, mtime int64) error
	// PathWithHash finds a file of the location with this content.
	PathWithHash(ctx context.Context, loc, hash string) (string, bool, error)
}

// Location is a folder kept in sync.
type Location struct {
	ID   string
	Path string
	// Skip leaves paths out (they neither sync nor get removed), e.g. the
	// folders of apps that are running. Nil: nothing is skipped.
	Skip func(rel string) bool
	// Owner says who should own a file taken from the other server (home
	// folders: the person whose folder it is). Nil: whoever runs this.
	Owner func(rel string) (uid, gid int, ok bool)
}

// own hands a taken file, and the folders made for it, to its owner.
func (l Location) own(rel string) {
	if l.Owner == nil {
		return
	}
	uid, gid, ok := l.Owner(rel)
	if !ok {
		return
	}
	parts := strings.Split(rel, "/")
	cur := l.Path
	for i, p := range parts {
		cur = filepath.Join(cur, p)
		if i == 0 && len(parts) > 1 {
			continue // the person's own top folder already is theirs
		}
		_ = os.Lchown(cur, uid, gid)
	}
}

// Skipped: rel never syncs (the trash, partial downloads, Skip).
func (l Location) Skipped(rel string) bool {
	if rel == TrashDir || rel == PartialDir || strings.HasPrefix(rel, TrashDir+"/") || strings.HasPrefix(rel, PartialDir+"/") {
		return true
	}
	return l.Skip != nil && l.Skip(rel)
}

// ValidPath: a clean, relative, slash-separated path with no way out.
func ValidPath(rel string) bool {
	if rel == "" || len(rel) > 4096 || strings.ContainsRune(rel, 0) || strings.Contains(rel, "\\") {
		return false
	}
	if path.IsAbs(rel) || path.Clean(rel) != rel || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	return true
}

// Join opens rel inside root, refusing paths that leave it or pass through
// a symbolic link.
func Join(root, rel string) (string, error) {
	if !ValidPath(rel) {
		return "", fmt.Errorf("filesync: bad path %q", rel)
	}
	parts := strings.Split(rel, "/")
	cur := root
	for _, p := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, p)
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			break // made later
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
			return "", fmt.Errorf("filesync: %q isn't a folder", cur)
		}
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// HashFile is a file's SHA-256 (hex).
func HashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---------- scanning ----------

// ScanResult is what a scan found.
type ScanResult struct {
	Files   int   // files in the location
	Bytes   int64 // their size
	Changed int   // new versions recorded
}

// Scan brings the index of a location up to date with the disk: new and
// changed files get a new version (hashed only when size or time
// changed), files gone from disk a tombstone.
func Scan(ctx context.Context, idx Index, loc Location) (ScanResult, error) {
	var res ScanResult
	known, err := idx.Files(ctx, loc.ID)
	if err != nil {
		return res, err
	}
	if _, err := os.Stat(loc.Path); err != nil {
		return res, err // never treat a missing location (an unplugged disk) as "everything deleted"
	}
	seen := map[string]bool{}
	err = filepath.WalkDir(loc.Path, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if p == loc.Path {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir // unreadable: leave what's inside alone
			}
			return nil
		}
		rel, rerr := filepath.Rel(loc.Path, p)
		if rerr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if loc.Skipped(rel) {
			if d.IsDir() {
				markTree(known, seen, rel)
				return fs.SkipDir
			}
			seen[rel] = true
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil // folders follow their files; links and devices don't sync
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		seen[rel] = true
		res.Files++
		res.Bytes += fi.Size()
		e, ok := known[rel]
		mtime := fi.ModTime().UnixNano()
		if ok && !e.Deleted && e.Size == fi.Size() && e.MTime == mtime {
			return nil
		}
		hash, err := HashFile(p)
		if err != nil {
			return nil // busy or gone: next time
		}
		if ok && !e.Deleted && e.Hash == hash {
			return idx.Touch(ctx, loc.ID, rel, fi.Size(), mtime)
		}
		res.Changed++
		return idx.PutLocal(ctx, loc.ID, Entry{Path: rel, Size: fi.Size(), MTime: mtime, Mode: uint32(fi.Mode().Perm()), Hash: hash})
	})
	if err != nil {
		return res, err
	}
	for rel, e := range known {
		if e.Deleted || seen[rel] {
			continue
		}
		res.Changed++
		if err := idx.PutLocal(ctx, loc.ID, Entry{Path: rel, Deleted: true, MTime: time.Now().UnixNano()}); err != nil {
			return res, err
		}
	}
	return res, nil
}

// markTree notes a skipped folder's known files as seen (they stay as they are).
func markTree(known map[string]Entry, seen map[string]bool, dir string) {
	for rel := range known {
		if strings.HasPrefix(rel, dir+"/") {
			seen[rel] = true
		}
	}
}

// ---------- applying the other server's changes ----------

// Fetch reads a file of the other server from offset on.
type Fetch func(ctx context.Context, loc, rel string, offset int64) (io.ReadCloser, error)

// Apply says whose changes these are and how to take them.
type Apply struct {
	Self, SelfName string // this server's NoCap ID and name
	Peer           string // the other server's NoCap ID
	// Acked: how far the other server has read this one's index; newer
	// local versions are news to it (both changed the file: a conflict).
	Acked int64
	Fetch Fetch
	// Wrap slows downloads to the bandwidth limit (nil: full speed).
	Wrap func(io.Reader) io.Reader
	Now  func() time.Time
}

// ApplyResult is what applying did.
type ApplyResult struct {
	Applied, Conflicts, Skipped int
	Bytes                       int64 // downloaded
}

func wins(aL int64, aOrigin string, bL int64, bOrigin string) bool {
	if aL != bL {
		return aL > bL
	}
	return aOrigin > bOrigin
}

func sameContent(a, b Entry) bool {
	if a.Deleted || b.Deleted {
		return a.Deleted == b.Deleted
	}
	return a.Hash == b.Hash
}

// ConflictName is "name (conflict from server).ext" next to rel.
func ConflictName(rel, server string) string {
	dir, base := path.Split(rel)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" { // ".bashrc"
		stem, ext = base, ""
	}
	server = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 || r == ':' {
			return '-'
		}
		return r
	}, server)
	return dir + stem + " (conflict from " + server + ")" + ext
}

// ApplyChanges takes the other server's changes to one location.
func ApplyChanges(ctx context.Context, idx Index, loc Location, changes []Entry, o Apply) (ApplyResult, error) {
	var res ApplyResult
	if o.Now == nil {
		o.Now = time.Now
	}
	known, err := idx.Files(ctx, loc.ID)
	if err != nil {
		return res, err
	}
	if _, err := os.Stat(loc.Path); err != nil {
		return res, err
	}
	for _, r := range changes {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if !ValidPath(r.Path) || loc.Skipped(r.Path) {
			res.Skipped++
			continue
		}
		l, have := known[r.Path]
		localOrigin := l.Origin
		if localOrigin == "" {
			localOrigin = o.Self
		}
		unsent := have && l.Origin == "" && l.Seq > o.Acked
		if have && !wins(r.Lamport, o.Peer, l.Lamport, localOrigin) {
			if unsent && !sameContent(l, r) {
				res.Conflicts++ // ours stands; the other server keeps its own as a conflict copy
			}
			continue
		}
		dst, err := Join(loc.Path, r.Path)
		if err != nil {
			res.Skipped++
			continue
		}
		if !have || l.Deleted {
			fi, err := os.Lstat(dst)
			switch {
			case err != nil:
			case r.Deleted:
				// A file here the last scan didn't see yet: not the one deleted there.
				r.Origin = o.Peer
				if err := idx.PutRemote(ctx, loc.ID, r); err != nil {
					return res, err
				}
				res.Applied++
				continue
			case fi.Mode().IsRegular():
				if h, _ := HashFile(dst); h != r.Hash {
					res.Conflicts++
					if err := keepConflict(ctx, idx, loc, Entry{Path: r.Path}, o.SelfName); err != nil {
						return res, err
					}
				}
			default:
				res.Skipped++ // a folder or link in the way
				continue
			}
		}
		// Changed on disk since the last scan: leave it for the next one.
		if have && !l.Deleted {
			if fi, err := os.Lstat(dst); err == nil && (fi.Size() != l.Size || fi.ModTime().UnixNano() != l.MTime) {
				res.Skipped++
				continue
			}
		}
		if unsent && !sameContent(l, r) {
			res.Conflicts++
			if !l.Deleted {
				if err := keepConflict(ctx, idx, loc, l, o.SelfName); err != nil {
					return res, err
				}
			}
		}
		n, err := take(ctx, idx, loc, dst, r, o)
		if err != nil {
			return res, fmt.Errorf("%s: %w", r.Path, err)
		}
		res.Bytes += n
		res.Applied++
	}
	return res, nil
}

// keepConflict renames this server's version of a file out of the way
// ("name (conflict from <this server>).ext"); the next scan syncs it.
func keepConflict(ctx context.Context, idx Index, loc Location, l Entry, selfName string) error {
	src, err := Join(loc.Path, l.Path)
	if err != nil {
		return err
	}
	name := ConflictName(l.Path, selfName)
	for i := 2; ; i++ {
		dst, err := Join(loc.Path, name)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
			if err := os.Rename(src, dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return nil
		}
		name = ConflictName(l.Path, fmt.Sprintf("%s %d", selfName, i))
	}
}

// take makes the file what r says (or moves it to the trash).
func take(ctx context.Context, idx Index, loc Location, dst string, r Entry, o Apply) (int64, error) {
	r.Origin = o.Peer
	if r.Deleted {
		if err := toTrash(loc, dst, r.Path, o.Now()); err != nil {
			return 0, err
		}
		return 0, idx.PutRemote(ctx, loc.ID, r)
	}
	var got int64
	if h, err := HashFile(dst); err == nil && h == r.Hash {
		// Already there (copied by hand, or both made the same file).
	} else if src, ok, _ := idx.PathWithHash(ctx, loc.ID, r.Hash); ok && src != r.Path && copyLocal(loc, src, dst, r.Hash) == nil {
		// A moved or copied file: taken from the copy here, nothing downloaded.
	} else {
		n, err := download(ctx, loc, dst, r, o)
		if err != nil {
			return n, err
		}
		got = n
	}
	mode := fs.FileMode(r.Mode).Perm()
	if mode == 0 {
		mode = 0o644
	}
	_ = os.Chmod(dst, mode)
	loc.own(r.Path)
	mt := time.Unix(0, r.MTime)
	if err := os.Chtimes(dst, mt, mt); err != nil {
		return got, err
	}
	return got, idx.PutRemote(ctx, loc.ID, r)
}

func toTrash(loc Location, dst, rel string, now time.Time) error {
	if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	// As Files names what it recycles: "<when>_<name>", numbered when taken.
	name := now.Format("20060102-150405") + "_" + path.Base(rel)
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		cand := name
		if i > 0 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		trash, err := Join(loc.Path, TrashDir+"/"+cand)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(trash); errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(trash), 0o755); err != nil {
				return err
			}
			return os.Rename(dst, trash)
		}
	}
	return errors.New("the recycle bin is full of that name")
}

func copyLocal(loc Location, srcRel, dst, hash string) error {
	src, err := Join(loc.Path, srcRel)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := partialFile(loc, hash+".copy")
	if err != nil {
		return err
	}
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != hash {
		err = errors.New("the local copy changed")
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return place(tmp, dst)
}

func partialFile(loc Location, name string) (string, error) {
	dir := filepath.Join(loc.Path, PartialDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// place moves a finished download into its spot.
func place(tmp, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// download fetches r into .nocap-partial (resuming what's there), checks
// its SHA-256, then puts it in place.
func download(ctx context.Context, loc Location, dst string, r Entry, o Apply) (int64, error) {
	if o.Fetch == nil {
		return 0, errors.New("nothing to download with")
	}
	if len(r.Hash) != 64 {
		return 0, errors.New("the other server sent no checksum")
	}
	tmp, err := partialFile(loc, r.Hash+".part")
	if err != nil {
		return 0, err
	}
	var offset int64
	if fi, err := os.Stat(tmp); err == nil && fi.Size() <= r.Size {
		offset = fi.Size()
	} else if err == nil {
		os.Remove(tmp)
	}
	var n int64
	if offset < r.Size || r.Size == 0 {
		body, err := o.Fetch(ctx, loc.ID, r.Path, offset)
		if err != nil {
			return 0, err
		}
		defer body.Close()
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return 0, err
		}
		var src io.Reader = io.LimitReader(body, r.Size-offset)
		if o.Wrap != nil {
			src = o.Wrap(src)
		}
		n, err = io.Copy(out, src)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return n, err // the part stays: the next try resumes
		}
	}
	h, err := HashFile(tmp)
	if err != nil {
		return n, err
	}
	if h != r.Hash {
		os.Remove(tmp)
		return n, errors.New("the download doesn't match its checksum (the file changed meanwhile?)")
	}
	return n, place(tmp, dst)
}

// ---------- bandwidth ----------

// Limiter shares a bandwidth limit (bytes per second) between readers.
type Limiter struct {
	rate  float64
	ch    chan struct{}
	avail float64
	last  time.Time
}

// NewLimiter limits to bytesPerSec (0 or less: no limit, nil).
func NewLimiter(bytesPerSec int64) *Limiter {
	if bytesPerSec <= 0 {
		return nil
	}
	l := &Limiter{rate: float64(bytesPerSec), ch: make(chan struct{}, 1), last: time.Now()}
	l.ch <- struct{}{}
	return l
}

// Reader slows r to the limit.
func (l *Limiter) Reader(r io.Reader) io.Reader {
	if l == nil {
		return r
	}
	return &limited{l: l, r: r}
}

func (l *Limiter) wait(n int) {
	<-l.ch
	now := time.Now()
	l.avail += now.Sub(l.last).Seconds() * l.rate
	if l.avail > l.rate {
		l.avail = l.rate // at most a second's worth saved up
	}
	l.last = now
	l.avail -= float64(n)
	var sleep time.Duration
	if l.avail < 0 {
		sleep = time.Duration(-l.avail / l.rate * float64(time.Second))
	}
	l.ch <- struct{}{}
	time.Sleep(sleep)
}

type limited struct {
	l *Limiter
	r io.Reader
}

func (x *limited) Read(p []byte) (int, error) {
	if max := int(x.l.rate / 4); len(p) > max && max > 0 {
		p = p[:max]
	}
	n, err := x.r.Read(p)
	if n > 0 {
		x.l.wait(n)
	}
	return n, err
}
