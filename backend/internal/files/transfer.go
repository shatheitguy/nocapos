package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Conflict says what to do when an item with the same name already exists in
// the destination folder.
type Conflict string

const (
	ConflictRename  Conflict = "rename"  // keep both: "name (1).ext"
	ConflictReplace Conflict = "replace" // the new item replaces the old one
	ConflictSkip    Conflict = "skip"    // leave the existing item, skip this one
)

// ParseConflict maps a client value to a Conflict; empty means rename.
func ParseConflict(s string) (Conflict, error) {
	switch Conflict(s) {
	case "", ConflictRename:
		return ConflictRename, nil
	case ConflictReplace, ConflictSkip:
		return Conflict(s), nil
	}
	return "", fmt.Errorf("%w: conflict must be rename, replace or skip", ErrBadPath)
}

// Progress is reported while a copy or move runs.
type Progress struct {
	BytesDone  int64  `json:"bytes_done"`
	BytesTotal int64  `json:"bytes_total"`
	ItemsDone  int    `json:"items_done"`
	ItemsTotal int    `json:"items_total"`
	Current    string `json:"current"`
}

type TransferOptions struct {
	Move     bool
	Conflict Conflict
	// OnProgress, if set, is called as data is copied (from the worker goroutine).
	OnProgress func(Progress)
}

// TransferResult lists where items ended up and which were skipped.
type TransferResult struct {
	Paths   []string `json:"paths"`
	Skipped []string `json:"skipped"`
}

// Conflicts returns the names of rels that already exist in destDir.
func (s *Service) Conflicts(rootID string, rels []string, destDir string) ([]string, error) {
	rt, err := s.open(rootID)
	if err != nil {
		return nil, err
	}
	defer rt.Close()
	var out []string
	for _, rel := range rels {
		name := path.Base(rel)
		if path.Dir(rel) == destDir {
			continue // copying or moving onto itself is handled as "keep both"
		}
		if _, err := rt.Lstat(native(path.Join(destDir, name))); err == nil {
			out = append(out, name)
		}
	}
	return out, nil
}

// TransferWith copies or moves items into destDir with progress, conflict
// handling and cancellation (ctx). Partial copies are removed on failure.
func (s *Service) TransferWith(ctx context.Context, rootID string, rels []string, destDir string, opt TransferOptions) (TransferResult, error) {
	res := TransferResult{Paths: []string{}, Skipped: []string{}}
	if opt.Conflict == "" {
		opt.Conflict = ConflictRename
	}
	if opt.Move {
		for _, rel := range rels {
			if err := s.guard(rootID, rel); err != nil {
				return res, err
			}
		}
	}
	rt, err := s.open(rootID)
	if err != nil {
		return res, err
	}
	defer rt.Close()
	if st, err := rt.Stat(native(destDir)); err != nil {
		return res, err
	} else if !st.IsDir() {
		return res, ErrNotDir
	}

	p := Progress{ItemsTotal: len(rels)}
	if !opt.Move {
		for _, rel := range rels {
			p.BytesTotal += treeSize(rt, rel, 0)
		}
	}
	report := func() {
		if opt.OnProgress != nil {
			opt.OnProgress(p)
		}
	}
	report()

	for _, rel := range rels {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if rel == "." {
			return res, ErrIsRoot
		}
		if destDir == rel || strings.HasPrefix(destDir+"/", rel+"/") {
			return res, ErrIntoSelf
		}
		p.Current = path.Base(rel)
		report()
		if opt.Move && path.Dir(rel) == destDir {
			res.Paths = append(res.Paths, rel) // already there
			p.ItemsDone++
			continue
		}

		dst := path.Join(destDir, path.Base(rel))
		_, statErr := rt.Lstat(native(dst))
		exists := statErr == nil
		replace := false
		switch {
		case !exists:
		case path.Dir(rel) == destDir || opt.Conflict == ConflictRename:
			dst = uniqueName(rt, destDir, path.Base(rel))
		case opt.Conflict == ConflictSkip:
			res.Skipped = append(res.Skipped, rel)
			p.ItemsDone++
			if !opt.Move {
				p.BytesDone += treeSize(rt, rel, 0)
			}
			report()
			continue
		case opt.Conflict == ConflictReplace:
			if err := s.guard(rootID, dst); err != nil {
				return res, err
			}
			replace = true
		}

		// Write to a temporary name first when replacing, so the old item is
		// only removed once the new one is complete.
		target := dst
		if replace {
			target = uniqueName(rt, destDir, uploadPrefix+randHex()+uploadSuffix)
		}
		if opt.Move {
			err = rt.Rename(native(rel), native(target))
		} else {
			err = copyTreeCtx(ctx, rt, rel, target, 0, func(n int64) {
				p.BytesDone += n
				report()
			})
			if err != nil {
				_ = rt.RemoveAll(native(target))
			}
		}
		if err != nil {
			return res, err
		}
		if replace {
			if err := rt.RemoveAll(native(dst)); err != nil {
				return res, err
			}
			if err := rt.Rename(native(target), native(dst)); err != nil {
				return res, err
			}
		}
		res.Paths = append(res.Paths, dst)
		p.ItemsDone++
		report()
	}
	p.Current = ""
	report()
	return res, nil
}

// treeSize sums regular file sizes under rel (symlinks not followed).
func treeSize(rt *os.Root, rel string, depth int) int64 {
	if depth > maxCopyDepth {
		return 0
	}
	st, err := rt.Lstat(native(rel))
	if err != nil {
		return 0
	}
	if st.Mode().IsRegular() {
		return st.Size()
	}
	if !st.IsDir() {
		return 0
	}
	f, err := rt.Open(native(rel))
	if err != nil {
		return 0
	}
	des, _ := f.ReadDir(-1)
	f.Close()
	var n int64
	for _, de := range des {
		n += treeSize(rt, path.Join(rel, de.Name()), depth+1)
	}
	return n
}

// ctxReader stops reading when ctx is canceled and reports bytes read.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
	add func(int64)
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	if n > 0 && c.add != nil {
		c.add(int64(n))
	}
	return n, err
}

func copyTreeCtx(ctx context.Context, rt *os.Root, src, dst string, depth int, add func(int64)) error {
	if depth > maxCopyDepth {
		return fmt.Errorf("copy %s: folder nesting too deep", src)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	st, err := rt.Lstat(native(src))
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		return nil
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
			if err := copyTreeCtx(ctx, rt, path.Join(src, de.Name()), path.Join(dst, de.Name()), depth+1, add); err != nil {
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
		if _, err := io.CopyBuffer(out, &ctxReader{ctx: ctx, r: in, add: add}, make([]byte, 256<<10)); err != nil {
			out.Close()
			_ = rt.Remove(native(dst))
			return err
		}
		return out.Close()
	default:
		return nil
	}
}

// UploadWith is Upload with a conflict choice. A skipped upload returns "".
func (s *Service) UploadWith(rootID, dir, name string, r io.Reader, conflict Conflict) (string, error) {
	if conflict == ConflictRename || conflict == "" {
		return s.Upload(rootID, dir, name, r)
	}
	if err := ValidName(name); err != nil {
		return "", err
	}
	rt, err := s.open(rootID)
	if err != nil {
		return "", err
	}
	defer rt.Close()
	dst := path.Join(dir, name)
	st, err := rt.Lstat(native(dst))
	if errors.Is(err, fs.ErrNotExist) {
		return s.Upload(rootID, dir, name, r)
	}
	if err != nil {
		return "", err
	}
	if conflict == ConflictSkip {
		_, _ = io.Copy(io.Discard, r)
		return "", nil
	}
	if st.IsDir() {
		return "", fmt.Errorf("%w: a folder named %q is in the way", ErrIsDir, name)
	}
	if err := s.guard(rootID, dst); err != nil {
		return "", err
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
	if err := rt.Rename(native(tmp), native(dst)); err != nil {
		// Windows can't rename over an existing file: remove it first.
		if rmErr := rt.Remove(native(dst)); rmErr == nil {
			err = rt.Rename(native(tmp), native(dst))
		}
		if err != nil {
			_ = rt.Remove(native(tmp))
			return "", err
		}
	}
	return dst, nil
}
