package appdata

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Copier copies a folder's contents into another (empty or missing) folder,
// keeping owners, permissions, extended attributes, ACLs and hard links.
type Copier func(ctx context.Context, src, dst string) error

// Tree sums up a folder for checking a copy.
type Tree struct {
	Files int   `json:"files"`
	Dirs  int   `json:"dirs"`
	Links int   `json:"links"`
	Bytes int64 `json:"bytes"`
	// sums of small files, by path inside the folder
	sums map[string][32]byte
	// modes and owners, by path inside the folder
	meta map[string]string
}

// smallFile is the largest file whose checksum is compared after a copy;
// checksumBudget caps how much is read in all.
const (
	smallFile      = 1 << 20
	checksumBudget = 256 << 20
)

// Survey walks a folder: counts, sizes, and checksums of small files.
func Survey(ctx context.Context, dir string) (Tree, error) {
	t := Tree{sums: map[string][32]byte{}, meta: map[string]string{}}
	budget := int64(checksumBudget)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		t.meta[rel] = info.Mode().String() + " " + ownerOf(info)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			t.Links++
		case d.IsDir():
			t.Dirs++
		case d.Type().IsRegular():
			t.Files++
			t.Bytes += info.Size()
			if info.Size() <= smallFile && budget >= info.Size() {
				budget -= info.Size()
				sum, err := fileSum(p)
				if err != nil {
					return err
				}
				t.sums[rel] = sum
			}
		default:
			t.Files++ // sockets, fifos, devices: counted, not read
		}
		return nil
	})
	return t, err
}

func fileSum(p string) ([32]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// Verify checks dst is a faithful copy of src: the same number of files,
// folders and links, the same total size, the same permissions and owners,
// and the same content for small files.
func Verify(ctx context.Context, src, dst string) (Tree, error) {
	a, err := Survey(ctx, src)
	if err != nil {
		return a, fmt.Errorf("read the original: %w", err)
	}
	b, err := Survey(ctx, dst)
	if err != nil {
		return a, fmt.Errorf("read the copy: %w", err)
	}
	if a.Files != b.Files || a.Dirs != b.Dirs || a.Links != b.Links {
		return a, fmt.Errorf("the copy has %d files, %d folders and %d links; the original %d, %d and %d", b.Files, b.Dirs, b.Links, a.Files, a.Dirs, a.Links)
	}
	if a.Bytes != b.Bytes {
		return a, fmt.Errorf("the copy holds %d bytes, the original %d", b.Bytes, a.Bytes)
	}
	keys := make([]string, 0, len(a.meta))
	for k := range a.meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if b.meta[k] != a.meta[k] {
			return a, fmt.Errorf("%s: permissions or owner differ in the copy (%s, was %s)", k, b.meta[k], a.meta[k])
		}
		if s, ok := a.sums[k]; ok && b.sums[k] != s {
			return a, fmt.Errorf("%s: the copy's content differs", k)
		}
	}
	return a, nil
}

// emptyOrMissing reports whether dir has nothing in it (or isn't there).
func emptyOrMissing(dir string) (bool, error) {
	f, err := os.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return len(names) == 0, err
}

// GoCopy is a plain copy (files, folders, links, permissions, times and,
// where the system has them, owners). It keeps hard links as separate
// files, so the Linux build uses the system's copy tools instead.
func GoCopy(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if st, err := os.Stat(src); err == nil {
		_ = os.Chmod(dst, st.Mode().Perm())
		chownLike(dst, st)
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		to := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, to); err != nil {
				return err
			}
			chownLike(to, info)
			return nil
		case d.IsDir():
			if err := os.Mkdir(to, info.Mode().Perm()); err != nil && !os.IsExist(err) {
				return err
			}
			_ = os.Chmod(to, info.Mode().Perm())
		case d.Type().IsRegular():
			if err := copyFile(p, to, info.Mode().Perm()); err != nil {
				return err
			}
		default:
			return nil // sockets and the like aren't data
		}
		chownLike(to, info)
		_ = os.Chtimes(to, info.ModTime(), info.ModTime())
		return nil
	})
}

func copyFile(from, to string, perm fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(to, perm)
}

// DirSize adds up the sizes of the files in a folder (best effort).
func DirSize(ctx context.Context, dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
