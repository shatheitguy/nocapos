package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// Files inside a container, through the engine's archive API (and a plain
// `ls` when the container runs): list, read, download and upload. Every
// path is checked by CleanPath first; the engine resolves it inside the
// container's own filesystem, so nothing here reaches the host's files.

var (
	ErrBadPath  = errors.New("that isn't a valid path inside the container")
	ErrIsDir    = errors.New("that's a folder")
	ErrReadOnly = errors.New("files can't be uploaded there")
)

const maxPathLen = 4096

// CleanPath checks a path inside a container and returns it tidied: it
// must be absolute, without "..", control characters or NUL.
func CleanPath(p string) (string, error) {
	if p == "" || len(p) > maxPathLen || !strings.HasPrefix(p, "/") {
		return "", ErrBadPath
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", ErrBadPath
		}
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", ErrBadPath
		}
	}
	return path.Clean(p), nil
}

// CleanName checks a file name for an upload: one path element.
func CleanName(name string) (string, error) {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, `/\`) {
		return "", ErrBadPath
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", ErrBadPath
		}
	}
	return name, nil
}

// uploadBlocked are the kernel's own folders: writing there means nothing
// good (and may hang or hurt the host).
var uploadBlocked = []string{"/proc", "/sys", "/dev"}

// UploadAllowed reports whether files may be put into dir.
func UploadAllowed(dir string) bool {
	for _, b := range uploadBlocked {
		if dir == b || strings.HasPrefix(dir, b+"/") {
			return false
		}
	}
	return true
}

// FileEntry is one file or folder inside a container.
type FileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Dir        bool      `json:"dir"`
	Link       bool      `json:"link"`
	LinkTarget string    `json:"link_target,omitempty"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	MTime      time.Time `json:"mtime"`
}

type pathStat struct {
	Name       string      `json:"name"`
	Size       int64       `json:"size"`
	Mode       fs.FileMode `json:"mode"`
	Mtime      time.Time   `json:"mtime"`
	LinkTarget string      `json:"linkTarget"`
}

func archivePath(id string) string { return "/containers/" + url.PathEscape(id) + "/archive" }

// StatPath describes one path in a container (without reading it).
func (c *Client) StatPath(ctx context.Context, id, p string) (*FileEntry, error) {
	resp, err := c.send(ctx, c.hc, http.MethodHead, archivePath(id), url.Values{"path": {p}})
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	raw, err := base64.StdEncoding.DecodeString(resp.Header.Get("X-Docker-Container-Path-Stat"))
	if err != nil {
		return nil, fmt.Errorf("docker: bad path stat: %w", err)
	}
	var st pathStat
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("docker: bad path stat: %w", err)
	}
	return &FileEntry{
		Name: st.Name, Path: p, Dir: st.Mode.IsDir(), Link: st.Mode&fs.ModeSymlink != 0, LinkTarget: st.LinkTarget,
		Size: st.Size, Mode: st.Mode.String(), MTime: st.Mtime.UTC(),
	}, nil
}

// Listing limits: a folder with more entries is cut short.
const (
	maxListEntries = 2000
	scanBudget     = 64 << 20 // bytes of archive read when `ls` isn't there
)

// Listing is a folder's contents.
type Listing struct {
	Path      string      `json:"path"`
	Entries   []FileEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}

// ListDir lists a folder. A running container is asked with `ls` (then
// each entry is described by the engine); otherwise, or when the image has
// no `ls`, the folder's archive is read up to a budget.
func (c *Client) ListDir(ctx context.Context, id, dir string, running bool) (*Listing, error) {
	st, err := c.StatPath(ctx, id, dir)
	if err != nil {
		return nil, err
	}
	if !st.Dir && !st.Link {
		return nil, fmt.Errorf("%w: %s", errNotDir, dir)
	}
	if running {
		if l, err := c.listWithLs(ctx, id, dir); err == nil {
			return l, nil
		}
	}
	return c.listFromArchive(ctx, id, dir)
}

var errNotDir = errors.New("not a folder")

func (c *Client) listWithLs(ctx context.Context, id, dir string) (*Listing, error) {
	res, err := c.ExecRun(ctx, id, []string{"ls", "-1A", "--", dir + "/"}, 1<<20)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("ls exited with %d", res.ExitCode)
	}
	var names []string
	for _, n := range strings.Split(res.Stdout, "\n") {
		if n = strings.TrimSuffix(n, "\r"); n != "" {
			names = append(names, n)
		}
	}
	l := &Listing{Path: dir, Entries: make([]FileEntry, 0, len(names))}
	if len(names) > maxListEntries {
		names, l.Truncated = names[:maxListEntries], true
	}
	out := make([]*FileEntry, len(names))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, n := range names {
		if _, err := CleanName(n); err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p := path.Join(dir, n)
			e, err := c.StatPath(ctx, id, p)
			if err != nil {
				e = &FileEntry{Path: p}
			}
			e.Name, e.Path = n, p
			out[i] = e
		}()
	}
	wg.Wait()
	for _, e := range out {
		if e != nil {
			l.Entries = append(l.Entries, *e)
		}
	}
	sortEntries(l.Entries)
	return l, nil
}

// countingReader stops reading once a budget is spent.
type countingReader struct {
	r    io.Reader
	left int64
}

var errBudget = errors.New("budget spent")

func (c *countingReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errBudget
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

func (c *Client) listFromArchive(ctx context.Context, id, dir string) (*Listing, error) {
	resp, err := c.send(ctx, c.stream, http.MethodGet, archivePath(id), url.Values{"path": {dir + "/."}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	l := &Listing{Path: dir, Entries: []FileEntry{}}
	l.Truncated = scanTar(&countingReader{r: resp.Body, left: scanBudget}, func(h *tar.Header) bool {
		e, ok := childEntry(dir, h)
		if ok {
			l.Entries = append(l.Entries, e)
		}
		return len(l.Entries) < maxListEntries
	})
	sortEntries(l.Entries)
	return l, nil
}

// scanTar calls fn for each header until it returns false or the archive
// ends. It reports whether it stopped early (budget, limit or a bad archive).
func scanTar(r io.Reader, fn func(*tar.Header) bool) (stopped bool) {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return true
		}
		if !fn(h) {
			return true
		}
	}
}

// childEntry turns an archive header of "<dir>/." into a direct child of
// dir (ok is false for deeper entries and the folder itself).
func childEntry(dir string, h *tar.Header) (FileEntry, bool) {
	name := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
	// The archive of "<dir>/." holds "./name" or "<base>/name".
	parts := strings.Split(name, "/")
	if len(parts) > 0 && (parts[0] == "." || parts[0] == path.Base(dir)) {
		parts = parts[1:]
	}
	if len(parts) != 1 || parts[0] == "" || parts[0] == "." {
		return FileEntry{}, false
	}
	fi := h.FileInfo()
	return FileEntry{
		Name: parts[0], Path: path.Join(dir, parts[0]), Dir: fi.IsDir(), Link: h.Typeflag == tar.TypeSymlink,
		LinkTarget: h.Linkname, Size: h.Size, Mode: fi.Mode().String(), MTime: h.ModTime.UTC(),
	}, true
}

func sortEntries(es []FileEntry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].Dir != es[j].Dir {
			return es[i].Dir
		}
		return strings.ToLower(es[i].Name) < strings.ToLower(es[j].Name)
	})
}

// FileReader is one file read out of a container.
type FileReader struct {
	io.Reader
	Size   int64
	Name   string
	MTime  time.Time
	closer io.Closer
}

func (f *FileReader) Close() error { return f.closer.Close() }

// OpenFile reads a regular file from a container. Folders give ErrIsDir.
func (c *Client) OpenFile(ctx context.Context, id, p string) (*FileReader, error) {
	resp, err := c.send(ctx, c.stream, http.MethodGet, archivePath(id), url.Values{"path": {p}})
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(resp.Body)
	h, err := tr.Next()
	if err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("docker: read archive: %w", err)
	}
	switch h.Typeflag {
	case tar.TypeReg:
	case tar.TypeDir:
		resp.Body.Close()
		return nil, ErrIsDir
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("%w: not a regular file", ErrBadPath)
	}
	return &FileReader{Reader: tr, Size: h.Size, Name: path.Base(h.Name), MTime: h.ModTime, closer: resp.Body}, nil
}

// OpenArchive streams a path (usually a folder) as a tar archive.
func (c *Client) OpenArchive(ctx context.Context, id, p string) (io.ReadCloser, error) {
	resp, err := c.send(ctx, c.stream, http.MethodGet, archivePath(id), url.Values{"path": {p}})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// PutFile writes one file (size bytes from r) into dir inside a container.
// An existing file of that name is replaced; a folder of that name isn't.
func (c *Client) PutFile(ctx context.Context, id, dir, name string, r io.Reader, size int64) error {
	if !UploadAllowed(dir) {
		return ErrReadOnly
	}
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: size, ModTime: time.Now(), Typeflag: tar.TypeReg})
		if err == nil {
			var n int64
			n, err = io.Copy(tw, io.LimitReader(r, size))
			if err == nil && n != size {
				err = io.ErrUnexpectedEOF
			}
		}
		if err == nil {
			err = tw.Close()
		}
		pw.CloseWithError(err)
	}()
	q := url.Values{"path": {dir}, "noOverwriteDirNonDir": {"true"}}
	resp, err := c.sendReader(ctx, c.stream, http.MethodPut, archivePath(id), q, pr, "application/x-tar")
	if err != nil {
		pr.CloseWithError(err)
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	return nil
}

// --- running one command ---

// ExecResult is what a one-off command printed.
type ExecResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
}

// limitBuffer keeps the first max bytes written to it.
type limitBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
			b.truncated = true
		} else {
			b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

// ExecRun runs cmd in a running container without a terminal and returns
// its output (each stream capped at limit bytes) and exit code. ctx bounds it.
func (c *Client) ExecRun(ctx context.Context, container string, cmd []string, limit int) (*ExecResult, error) {
	cfg := execConfig{AttachStdout: true, AttachStderr: true, Cmd: cmd}
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.doBody(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", nil, cfg, &created); err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, errors.New("docker: exec create returned no id")
	}
	resp, err := c.sendBody(ctx, c.stream, http.MethodPost, "/exec/"+created.ID+"/start", nil, map[string]bool{"Detach": false, "Tty": false})
	if err != nil {
		return nil, err
	}
	stdout, stderr := &limitBuffer{max: limit}, &limitBuffer{max: limit}
	err = demux(resp.Body, stdout, stderr)
	resp.Body.Close()
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var inspect struct {
		Running  bool `json:"Running"`
		ExitCode int  `json:"ExitCode"`
	}
	if err := c.do(context.WithoutCancel(ctx), http.MethodGet, "/exec/"+created.ID+"/json", nil, &inspect); err != nil {
		return nil, err
	}
	return &ExecResult{
		Stdout: stdout.buf.String(), Stderr: stderr.buf.String(), ExitCode: inspect.ExitCode,
		Truncated: stdout.truncated || stderr.truncated,
	}, nil
}
