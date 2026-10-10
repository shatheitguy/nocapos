package photos

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

// Recently deleted: photos deleted from Photos go to the recycle bin of their
// drive (as in Files). The store remembers where each one came from, so the
// app can list them and put them back.

// Details is what the info panel shows beyond the library item.
type Details struct {
	Camera *Camera `json:"camera,omitempty"`
}

// Info reads the camera details of one photo.
func (l *Library) Info(root, rel string) (Details, error) {
	rel, err := files.Clean(rel)
	if err != nil {
		return Details{}, err
	}
	ext := strings.ToLower(path.Ext(rel))
	if !imageExt[ext] && !videoExt[ext] {
		return Details{}, files.ErrBadPath
	}
	f, info, err := l.files.Open(root, rel)
	if err != nil {
		return Details{}, err
	}
	defer f.Close()
	if info.IsDir() {
		return Details{}, files.ErrBadPath
	}
	var d Details
	if ext == ".jpg" || ext == ".jpeg" {
		if ex, err := readJPEGExif(f); err == nil && ex.camera != (Camera{}) {
			c := ex.camera
			d.Camera = &c
		}
	}
	return d, nil
}

// recycleNames lists what is in the recycle bin of a drive.
func (l *Library) recycleNames(root string) (map[string]bool, error) {
	entries, err := l.files.List(root, files.RecycleDir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[e.Name] = true
	}
	return out, nil
}

// binName splits a recycle bin name ("20261010-120000_a (1).jpg") into the
// original name ("a.jpg") and the number added to keep it unique (1).
func binName(name string) (orig string, n int) {
	if len(name) > 16 && name[8] == '-' && name[15] == '_' {
		name = name[16:]
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if i := strings.LastIndex(stem, " ("); i > 0 && strings.HasSuffix(stem, ")") {
		if v, err := strconv.Atoi(stem[i+2 : len(stem)-1]); err == nil && v > 0 {
			return stem[:i] + ext, v
		}
	}
	return name, 0
}

// TrashRoot moves photos of one drive to its recycle bin and remembers them.
func (l *Library) TrashRoot(ctx context.Context, root string, rels []string) error {
	before, err := l.recycleNames(root)
	if err != nil {
		return err
	}
	delErr := l.files.Delete(root, rels, false)
	after, err := l.recycleNames(root)
	if err != nil {
		if delErr != nil {
			return delErr
		}
		return err
	}
	// The new names in the bin, matched to what was deleted by original name.
	fresh := map[string][]string{}
	for name := range after {
		if !before[name] {
			orig, _ := binName(name)
			fresh[orig] = append(fresh[orig], name)
		}
	}
	for _, names := range fresh {
		sort.Slice(names, func(i, j int) bool {
			_, a := binName(names[i])
			_, b := binName(names[j])
			return a < b
		})
	}
	metas, _ := l.store.PhotoMetas(ctx)
	now := time.Now()
	var recs []store.PhotoTrash
	for _, rel := range rels {
		base := path.Base(rel)
		names := fresh[base]
		if len(names) == 0 {
			continue
		}
		name := names[0]
		fresh[base] = names[1:]
		t := store.PhotoTrash{Root: root, TrashPath: "/" + files.RecycleDir + "/" + name, OrigPath: "/" + rel, DeletedAt: now}
		if m, ok := metas[root+"\x00/"+rel]; ok {
			t.Size, t.Taken, t.Width, t.Height, t.DurationMS = m.Size, time.Unix(m.Taken, 0), m.Width, m.Height, m.DurationMS
		} else if f, info, err := l.files.Open(root, files.RecycleDir+"/"+name); err == nil {
			t.Size, t.Taken = info.Size(), info.ModTime()
			f.Close()
		}
		recs = append(recs, t)
	}
	if err := l.store.AddPhotoTrash(ctx, recs); err != nil {
		l.log.Warn("photos: remember deleted", "err", err)
	}
	return delErr
}

// Deleted lists recently deleted photos still in a recycle bin. Ones removed
// from the bin some other way (emptied in Files) are forgotten.
func (l *Library) Deleted(ctx context.Context) ([]store.PhotoTrash, error) {
	all, err := l.store.PhotoTrashItems(ctx)
	if err != nil {
		return nil, err
	}
	bins := map[string]map[string]bool{}
	out := make([]store.PhotoTrash, 0, len(all))
	var gone []int64
	for _, t := range all {
		names, seen := bins[t.Root]
		if !seen {
			var err error
			names, err = l.recycleNames(t.Root)
			if err != nil {
				names = nil // drive not there right now: hide, keep the record
			}
			bins[t.Root] = names
		}
		if names == nil {
			if _, err := l.files.Root(t.Root); errors.Is(err, files.ErrUnknownRoot) {
				gone = append(gone, t.ID)
			}
			continue
		}
		if !names[path.Base(t.TrashPath)] {
			gone = append(gone, t.ID)
			continue
		}
		out = append(out, t)
	}
	if len(gone) > 0 {
		if err := l.store.RemovePhotoTrash(ctx, gone); err != nil {
			l.log.Warn("photos: forget deleted", "err", err)
		}
	}
	return out, nil
}

func pick(all []store.PhotoTrash, ids []int64) []store.PhotoTrash {
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []store.PhotoTrash
	for _, t := range all {
		if want[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// ensureDir creates a folder (and its parents) inside a drive.
func (l *Library) ensureDir(root, dir string) error {
	cur := "."
	for _, part := range strings.Split(dir, "/") {
		next := path.Join(cur, part)
		if _, err := l.files.Mkdir(root, cur, part); err != nil && !errors.Is(err, fs.ErrExist) {
			// Mkdir refuses existing names in different ways; check it is a folder.
			if _, lerr := l.files.List(root, next); lerr != nil {
				return err
			}
		}
		cur = next
	}
	return nil
}

// Restore puts deleted photos back where they were (or into the Photos folder
// when that folder can't be made), under their original name when it is free.
// It returns where each one went.
func (l *Library) Restore(ctx context.Context, ids []int64) ([]store.PhotoKey, error) {
	all, err := l.store.PhotoTrashItems(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.PhotoKey
	for _, t := range pick(all, ids) {
		src, err := files.Clean(t.TrashPath)
		if err != nil || !strings.HasPrefix(src, files.RecycleDir+"/") {
			continue
		}
		orig, err := files.Clean(t.OrigPath)
		if err != nil || orig == "." || strings.HasPrefix(orig, files.RecycleDir) {
			orig = Folder + "/" + path.Base(t.OrigPath)
		}
		dir := path.Dir(orig)
		if err := l.ensureDir(t.Root, dir); err != nil {
			dir = Folder
			if err := l.ensureDir(t.Root, dir); err != nil {
				return out, err
			}
		}
		res, err := l.files.TransferWith(ctx, t.Root, []string{src}, dir, files.TransferOptions{Move: true, Conflict: files.ConflictRename})
		if err != nil {
			return out, err
		}
		if len(res.Paths) == 0 {
			continue
		}
		moved := res.Paths[0]
		want := path.Base(orig)
		ext := path.Ext(want)
		for i := 0; i < 100 && path.Base(moved) != want; i++ {
			name := want
			if i > 0 {
				name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(want, ext), i, ext)
			}
			if p, err := l.files.Rename(t.Root, moved, name); err == nil {
				moved = p
				break
			} else if !errors.Is(err, fs.ErrExist) {
				break
			}
		}
		if err := l.store.RemovePhotoTrash(ctx, []int64{t.ID}); err != nil {
			return out, err
		}
		out = append(out, store.PhotoKey{Root: t.Root, Path: "/" + moved})
	}
	return out, nil
}

// Purge deletes photos from the recycle bin for good.
func (l *Library) Purge(ctx context.Context, ids []int64) error {
	all, err := l.store.PhotoTrashItems(ctx)
	if err != nil {
		return err
	}
	for _, t := range pick(all, ids) {
		rel, err := files.Clean(t.TrashPath)
		if err != nil || !strings.HasPrefix(rel, files.RecycleDir+"/") {
			continue
		}
		if err := l.files.Delete(t.Root, []string{rel}, true); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := l.store.RemovePhotoTrash(ctx, []int64{t.ID}); err != nil {
			return err
		}
	}
	return nil
}
