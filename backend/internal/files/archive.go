package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
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

// Compress to .zip and extract .zip / .tar / .tar.gz, entirely inside one
// storage location. Both write to a hidden temporary name first, so a failed
// or canceled job never leaves a half-written archive or folder behind.

const (
	MaxCompressBytes  = 8 << 30  // total size of what goes into a zip
	MaxExtractBytes   = 16 << 30 // total size written when extracting
	MaxArchiveEntries = 100_000
)

var (
	ErrNotArchive    = errors.New("only .zip, .tar, .tar.gz and .tgz files can be extracted")
	ErrUnsafeArchive = errors.New("the archive contains paths that point outside its folder, so it was not extracted")
)

// ArchiveStem is the name an archive extracts to ("photos.tar.gz" → "photos"),
// or "" if the file is not an archive Extract understands.
func ArchiveStem(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".tgz", ".tar", ".zip"} {
		if strings.HasSuffix(lower, ext) && len(name) > len(ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return ""
}

// guardDest refuses to write into NoCapOS's own folders and core OS folders
// (storage locations that live inside them stay usable). Unlike guard it
// allows the top of a location and folders such as /home, whose contents are
// ordinary files.
func (s *Service) guardDest(rootID, rel string) error {
	r, err := s.Root(rootID)
	if err != nil {
		return err
	}
	full := filepath.Clean(filepath.Join(r.Path, native(rel)))
	if real, err := filepath.EvalSymlinks(full); err == nil {
		full = real
	}
	for _, p := range s.protected {
		if !within(full, p) {
			continue
		}
		ok := false
		for _, sr := range s.Roots() {
			if rp, err := filepath.Abs(sr.Path); err == nil && within(rp, p) && within(full, rp) {
				ok = true
				break
			}
		}
		if !ok {
			return ErrProtected
		}
	}
	return nil
}

// Compress zips rels (all in one location) into destDir/name and returns the
// archive's path. name gets ".zip" added if missing; an existing file with
// that name is kept and the new archive gets a number.
func (s *Service) Compress(ctx context.Context, rootID string, rels []string, destDir, name string, onProgress func(Progress)) (string, error) {
	if len(rels) == 0 {
		return "", ErrBadPath
	}
	if !strings.HasSuffix(strings.ToLower(name), ".zip") {
		name += ".zip"
	}
	if err := ValidName(name); err != nil {
		return "", err
	}
	for _, rel := range rels {
		if rel == "." || rel == RecycleDir {
			return "", ErrIsRoot
		}
		if err := s.guard(rootID, rel); err != nil {
			return "", err
		}
	}
	if err := s.guardDest(rootID, destDir); err != nil {
		return "", err
	}
	rt, err := s.open(rootID)
	if err != nil {
		return "", err
	}
	defer rt.Close()
	if st, err := rt.Stat(native(destDir)); err != nil {
		return "", err
	} else if !st.IsDir() {
		return "", ErrNotDir
	}

	p := Progress{ItemsTotal: len(rels)}
	for _, rel := range rels {
		if _, err := rt.Lstat(native(rel)); err != nil {
			return "", err
		}
		p.BytesTotal += treeSize(rt, rel, 0)
	}
	if p.BytesTotal > MaxCompressBytes {
		return "", fmt.Errorf("%w: zip files are limited to %d GB", ErrTooLarge, MaxCompressBytes>>30)
	}
	report := func() {
		if onProgress != nil {
			onProgress(p)
		}
	}
	report()

	tmp := path.Join(destDir, uploadPrefix+randHex()+uploadSuffix)
	out, err := rt.OpenFile(native(tmp), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		out.Close()
		_ = rt.Remove(native(tmp))
		return "", err
	}
	zw := zip.NewWriter(out)
	entries := 0
	var add func(rel, inZip string, depth int) error
	add = func(rel, inZip string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > maxCopyDepth {
			return fmt.Errorf("%s: folder nesting too deep", rel)
		}
		if entries++; entries > MaxArchiveEntries {
			return fmt.Errorf("%w: more than %d items", ErrTooLarge, MaxArchiveEntries)
		}
		st, err := rt.Lstat(native(rel))
		if err != nil {
			return err
		}
		switch {
		case st.IsDir():
			h := &zip.FileHeader{Name: inZip + "/", Modified: st.ModTime()}
			h.SetMode(st.Mode())
			if _, err := zw.CreateHeader(h); err != nil {
				return err
			}
			f, err := rt.Open(native(rel))
			if err != nil {
				return err
			}
			des, err := f.ReadDir(-1)
			f.Close()
			if err != nil {
				return err
			}
			for _, de := range des {
				n := de.Name()
				if strings.HasPrefix(n, uploadPrefix) && strings.HasSuffix(n, uploadSuffix) {
					continue
				}
				if err := add(path.Join(rel, n), inZip+"/"+n, depth+1); err != nil {
					return err
				}
			}
			return nil
		case st.Mode().IsRegular():
			h, err := zip.FileInfoHeader(st)
			if err != nil {
				return err
			}
			h.Name, h.Method = inZip, zip.Deflate
			w, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			in, err := rt.Open(native(rel))
			if err != nil {
				return err
			}
			defer in.Close()
			p.Current = path.Base(rel)
			_, err = io.CopyBuffer(w, &ctxReader{ctx: ctx, r: in, add: func(n int64) {
				p.BytesDone += n
				report()
			}}, make([]byte, 256<<10))
			return err
		default:
			return nil // symlinks, devices and sockets are left out
		}
	}
	for _, rel := range rels {
		if err := add(rel, path.Base(rel), 0); err != nil {
			return fail(err)
		}
		p.ItemsDone++
		report()
	}
	if err := zw.Close(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = rt.Remove(native(tmp))
		return "", err
	}
	final := uniqueName(rt, destDir, name)
	if err := rt.Rename(native(tmp), native(final)); err != nil {
		_ = rt.Remove(native(tmp))
		return "", err
	}
	p.Current = ""
	report()
	return final, nil
}

// safeEntry turns an archive member name into a relative slash path, or
// fails if it is absolute or climbs out with "..". "" means skip (".").
func safeEntry(name string) (string, error) {
	n := strings.ReplaceAll(name, `\`, "/")
	if strings.ContainsRune(n, 0) || len(n) > 4096 || strings.HasPrefix(n, "/") ||
		(len(n) >= 2 && n[1] == ':') { // "C:..." on Windows
		return "", ErrUnsafeArchive
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." {
			return "", ErrUnsafeArchive
		}
	}
	c := path.Clean(n)
	if c == "." || c == "" {
		return "", nil
	}
	return c, nil
}

// Extract unpacks the archive at rel into a new folder in destDir named after
// it ("Photos.zip" → "Photos", or "Photos (1)" if that exists) and returns
// the folder's path. Unsafe member paths abort the whole extraction; links
// and special files inside the archive are skipped.
func (s *Service) Extract(ctx context.Context, rootID, rel, destDir string, onProgress func(Progress)) (string, error) {
	stem := ArchiveStem(path.Base(rel))
	if stem == "" {
		return "", ErrNotArchive
	}
	if ValidName(stem) != nil {
		stem = "Extracted"
	}
	if err := s.guardDest(rootID, destDir); err != nil {
		return "", err
	}
	rt, err := s.open(rootID)
	if err != nil {
		return "", err
	}
	defer rt.Close()
	if st, err := rt.Stat(native(destDir)); err != nil {
		return "", err
	} else if !st.IsDir() {
		return "", ErrNotDir
	}
	f, err := rt.Open(native(rel))
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", ErrIsDir
	}

	tmp := path.Join(destDir, uploadPrefix+randHex()+uploadSuffix)
	if err := rt.Mkdir(native(tmp), 0o755); err != nil {
		return "", err
	}
	x := &extractor{ctx: ctx, rt: rt, base: tmp, onProgress: onProgress}
	if strings.HasSuffix(strings.ToLower(rel), ".zip") {
		err = x.zip(f, st.Size())
	} else {
		err = x.tar(f, st.Size(), !strings.HasSuffix(strings.ToLower(rel), ".tar"))
	}
	if err != nil {
		_ = rt.RemoveAll(native(tmp))
		return "", err
	}
	final := uniqueName(rt, destDir, stem)
	if err := rt.Rename(native(tmp), native(final)); err != nil {
		_ = rt.RemoveAll(native(tmp))
		return "", err
	}
	x.p.Current = ""
	x.report()
	return final, nil
}

type extractor struct {
	ctx        context.Context
	rt         *os.Root
	base       string
	onProgress func(Progress)
	p          Progress
	written    int64
	entries    int
}

func (x *extractor) report() {
	if x.onProgress != nil {
		x.onProgress(x.p)
	}
}

// target checks one member and returns where it goes (base-relative), "" to skip.
func (x *extractor) target(name string) (string, error) {
	if err := x.ctx.Err(); err != nil {
		return "", err
	}
	if x.entries++; x.entries > MaxArchiveEntries {
		return "", fmt.Errorf("%w: more than %d items", ErrTooLarge, MaxArchiveEntries)
	}
	rel, err := safeEntry(name)
	if err != nil || rel == "" {
		return "", err
	}
	return path.Join(x.base, rel), nil
}

func (x *extractor) mkdir(dst string) error {
	return x.rt.MkdirAll(native(dst), 0o755)
}

func (x *extractor) file(dst string, r io.Reader, mode fs.FileMode, mod time.Time, countBytes bool) error {
	if err := x.rt.MkdirAll(native(path.Dir(dst)), 0o755); err != nil {
		return err
	}
	if st, err := x.rt.Lstat(native(dst)); err == nil && st.IsDir() {
		return fmt.Errorf("%w: %s is both a file and a folder", ErrUnsafeArchive, path.Base(dst))
	}
	out, err := x.rt.OpenFile(native(dst), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm()|0o600)
	if err != nil {
		return err
	}
	x.p.Current = path.Base(dst)
	left := MaxExtractBytes - x.written
	n, err := io.CopyBuffer(out, &ctxReader{ctx: x.ctx, r: io.LimitReader(r, left+1), add: func(n int64) {
		if countBytes {
			x.p.BytesDone += n
			x.report()
		}
	}}, make([]byte, 256<<10))
	x.written += n
	if err == nil && n > left {
		err = fmt.Errorf("%w: archives unpack to at most %d GB", ErrTooLarge, MaxExtractBytes>>30)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && !mod.IsZero() {
		_ = x.rt.Chtimes(native(dst), mod, mod)
	}
	return err
}

func (x *extractor) zip(f *os.File, size int64) error {
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return fmt.Errorf("%w: not a valid zip file", ErrNotArchive)
	}
	if len(zr.File) > MaxArchiveEntries {
		return fmt.Errorf("%w: more than %d items", ErrTooLarge, MaxArchiveEntries)
	}
	// Check every name before writing anything.
	for _, zf := range zr.File {
		if _, err := safeEntry(zf.Name); err != nil {
			return err
		}
		x.p.BytesTotal += int64(zf.UncompressedSize64)
	}
	x.p.ItemsTotal = len(zr.File)
	x.report()
	for _, zf := range zr.File {
		dst, err := x.target(zf.Name)
		if err != nil {
			return err
		}
		mode := zf.Mode()
		switch {
		case dst == "":
		case zf.FileInfo().IsDir():
			err = x.mkdir(dst)
		case mode.IsRegular():
			var rc io.ReadCloser
			if rc, err = zf.Open(); err == nil {
				err = x.file(dst, rc, mode, zf.Modified, true)
				rc.Close()
			}
		}
		if err != nil {
			return err
		}
		x.p.ItemsDone++
		x.report()
	}
	return nil
}

func (x *extractor) tar(f *os.File, size int64, gz bool) error {
	// Progress follows the archive file itself (tar has no index up front).
	x.p.BytesTotal = size
	var raw io.Reader = &ctxReader{ctx: x.ctx, r: f, add: func(n int64) {
		x.p.BytesDone += n
		x.report()
	}}
	if gz {
		zr, err := gzip.NewReader(raw)
		if err != nil {
			return fmt.Errorf("%w: not a valid .tar.gz file", ErrNotArchive)
		}
		defer zr.Close()
		raw = zr
	}
	tr := tar.NewReader(raw)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return fmt.Errorf("%w: the archive is damaged (%v)", ErrNotArchive, err)
		}
		dst, err := x.target(h.Name)
		if err != nil {
			return err
		}
		switch {
		case dst == "":
		case h.Typeflag == tar.TypeDir:
			err = x.mkdir(dst)
		case h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA: //nolint:staticcheck // old tars use TypeRegA
			err = x.file(dst, tr, fs.FileMode(h.Mode), h.ModTime, false)
		}
		if err != nil {
			return err
		}
		x.p.ItemsDone++
	}
}
