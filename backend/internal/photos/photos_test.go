package photos

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

// exifJPEG makes a w×h JPEG carrying an EXIF orientation, date taken and time offset.
func exifJPEG(t *testing.T, w, h, orientation int, date, offset string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 6), uint8(y * 6), 90, 255})
		}
	}
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, img, nil); err != nil {
		t.Fatal(err)
	}

	// TIFF, little-endian: IFD0 at 8 with 2 entries, Exif IFD after it with 2 entries, then strings.
	le := binary.LittleEndian
	tiff := make([]byte, 0, 128)
	u16 := func(v uint16) { tiff = le.AppendUint16(tiff, v) }
	u32 := func(v uint32) { tiff = le.AppendUint32(tiff, v) }
	entry := func(tag, typ uint16, count, value uint32) { u16(tag); u16(typ); u32(count); u32(value) }
	const ifd0, exifIFD = 8, 8 + 2 + 2*12 + 4
	const strs = exifIFD + 2 + 2*12 + 4
	tiff = append(tiff, 'I', 'I')
	u16(42)
	u32(ifd0)
	u16(2)
	entry(0x0112, 3, 1, uint32(orientation))
	entry(0x8769, 4, 1, exifIFD)
	u32(0)
	u16(2)
	entry(0x9003, 2, uint32(len(date)+1), strs)
	entry(0x9011, 2, uint32(len(offset)+1), strs+uint32(len(date)+1))
	u32(0)
	tiff = append(tiff, date...)
	tiff = append(tiff, 0)
	tiff = append(tiff, offset...)
	tiff = append(tiff, 0)

	app1 := append([]byte("Exif\x00\x00"), tiff...)
	var out bytes.Buffer
	out.Write(plain.Bytes()[:2]) // SOI
	out.Write([]byte{0xFF, 0xE1})
	_ = binary.Write(&out, binary.BigEndian, uint16(len(app1)+2))
	out.Write(app1)
	out.Write(plain.Bytes()[2:])
	return out.Bytes()
}

func TestReadJPEGExif(t *testing.T) {
	data := exifJPEG(t, 8, 4, 6, "2024:05:06 07:08:09", "+04:00")
	info, err := readJPEGExif(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if info.orientation != 6 {
		t.Errorf("orientation = %d, want 6", info.orientation)
	}
	want := time.Date(2024, 5, 6, 3, 8, 9, 0, time.UTC)
	if !info.taken.Equal(want) {
		t.Errorf("taken = %v, want %v", info.taken.UTC(), want)
	}

	// No EXIF at all: upright, undated, no error.
	var plain bytes.Buffer
	_ = jpeg.Encode(&plain, image.NewGray(image.Rect(0, 0, 2, 2)), nil)
	info, err = readJPEGExif(&plain)
	if err != nil || info.orientation != 1 || !info.taken.IsZero() {
		t.Errorf("plain jpeg: %+v, %v", info, err)
	}
}

func mp4With(created time.Time) []byte {
	be := binary.BigEndian
	secs := uint32(created.Sub(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)) / time.Second)
	mvhd := make([]byte, 0, 108)
	mvhd = be.AppendUint32(mvhd, 108)
	mvhd = append(mvhd, "mvhd"...)
	mvhd = append(mvhd, 0, 0, 0, 0) // version 0, flags
	mvhd = be.AppendUint32(mvhd, secs)
	mvhd = append(mvhd, make([]byte, 108-len(mvhd))...)
	var out []byte
	out = be.AppendUint32(out, 16)
	out = append(out, "ftypisom"...)
	out = append(out, 0, 0, 0, 0)
	out = be.AppendUint32(out, uint32(8+len(mvhd)))
	out = append(out, "moov"...)
	return append(out, mvhd...)
}

func TestReadMP4Created(t *testing.T) {
	want := time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC)
	got, err := readMP4Created(bytes.NewReader(mp4With(want)))
	if err != nil || !got.Equal(want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
	if _, err := readMP4Created(bytes.NewReader([]byte("not a movie at all"))); err == nil {
		t.Error("expected an error for a non-MP4")
	}
}

func TestOrient(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255}) // top-left marker
	got := orient(src, 6).(*image.RGBA)       // 90° clockwise
	if b := got.Bounds(); b.Dx() != 2 || b.Dy() != 3 {
		t.Fatalf("size %v, want 2x3", b)
	}
	if c := got.RGBAAt(1, 0); c.R != 255 {
		t.Errorf("top-left should move to top-right, got %v", c)
	}
}

// waitFor polls until ok or fails after a few seconds.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestLibrary(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	drive := filepath.Join(dir, "drive")
	write := func(rel string, data []byte, mod time.Time) {
		p := filepath.Join(drive, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, mod, mod)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	write("Photos/beach.jpg", exifJPEG(t, 40, 20, 6, "2024:05:06 07:08:09", "+00:00"), time.Now())
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 1000, 500)))
	write("Photos/2019/scan.png", pngBuf.Bytes(), old)
	write("Photos/clip.mp4", mp4With(time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)), time.Now())
	write("Photos/notes.txt", []byte("not a photo"), time.Now())
	write("Documents/elsewhere.jpg", exifJPEG(t, 4, 4, 1, "2024:01:01 00:00:00", ""), time.Now())

	fsvc, err := files.New([]files.Root{{ID: "drive", Name: "Drive", Path: drive}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	defer close(done)
	ix := files.NewIndex(fsvc, done)
	waitFor(t, "index", func() bool { n, building, _ := ix.Stats(); return n > 0 && !building })

	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	lib := New(fsvc, ix, st, filepath.Join(dir, "data"), slog.New(slog.NewTextHandler(io.Discard, nil)))

	items, _, err := lib.List(ctx, "nobody")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3 (jpg, png, mp4): %+v", len(items), items)
	}
	waitFor(t, "dating", func() bool { return !lib.Scanning() })
	items, _, _ = lib.List(ctx, "nobody")
	order := []string{items[0].Path, items[1].Path, items[2].Path}
	want := []string{"/Photos/clip.mp4", "/Photos/beach.jpg", "/Photos/2019/scan.png"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v, want %v", order, want)
		}
	}
	beach := items[1]
	if !beach.Taken.Equal(time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)) || beach.Width != 20 || beach.Height != 40 {
		t.Errorf("beach: taken %v, %dx%d; want 2024-05-06 07:08:09 UTC, 20x40 (rotated)", beach.Taken, beach.Width, beach.Height)
	}
	if !items[0].Video {
		t.Error("clip.mp4 should be a video")
	}

	// Thumbnails: upright, no larger than the size, cached.
	p, err := lib.Thumb(ctx, "drive", "/Photos/beach.jpg", "s")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width != 20 || cfg.Height != 40 {
		t.Errorf("beach thumb %dx%d (%v), want 20x40", cfg.Width, cfg.Height, err)
	}
	p2, err := lib.Thumb(ctx, "drive", "/Photos/2019/scan.png", "s")
	if err != nil {
		t.Fatal(err)
	}
	f, _ = os.Open(p2)
	cfg, _ = jpeg.DecodeConfig(f)
	f.Close()
	if cfg.Width != 480 || cfg.Height != 240 {
		t.Errorf("scan thumb %dx%d, want 480x240", cfg.Width, cfg.Height)
	}
	if again, _ := lib.Thumb(ctx, "drive", "/Photos/2019/scan.png", "s"); again != p2 {
		t.Error("second request should hit the cache")
	}
	if _, err := lib.Thumb(ctx, "drive", "/Photos/notes.txt", "s"); err != ErrNoThumb {
		t.Errorf("notes.txt: %v, want ErrNoThumb", err)
	}
	if _, err := lib.Thumb(ctx, "drive", "/../outside.jpg", "s"); err == nil {
		t.Error("a path outside the location must be refused")
	}
}

// tiffEntry is one EXIF tag for cameraJPEG: ascii (string), rational ([2]uint32) or short (uint16).
type tiffEntry struct {
	tag uint16
	val any
}

// cameraJPEG makes a small JPEG whose EXIF carries the given IFD0 and Exif IFD tags.
func cameraJPEG(t *testing.T, ifd0, exif []tiffEntry) []byte {
	t.Helper()
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	n0, n1 := len(ifd0)+1, len(exif)
	ifd0Off := uint32(8)
	exifOff := ifd0Off + 2 + uint32(n0)*12 + 4
	dataOff := exifOff + 2 + uint32(n1)*12 + 4
	var head, data []byte
	put := func(e tiffEntry) {
		var typ uint16
		var count uint32
		var value []byte
		switch v := e.val.(type) {
		case string:
			typ, count = 2, uint32(len(v)+1)
			value = append([]byte(v), 0)
		case [2]uint32:
			typ, count = 5, 1
			value = le.AppendUint32(le.AppendUint32(nil, v[0]), v[1])
		case uint16:
			typ, count = 3, 1
			value = le.AppendUint16(nil, v)
		case uint32:
			typ, count = 4, 1
			value = le.AppendUint32(nil, v)
		}
		head = le.AppendUint16(head, e.tag)
		head = le.AppendUint16(head, typ)
		head = le.AppendUint32(head, count)
		if len(value) <= 4 {
			head = append(head, append(value, make([]byte, 4-len(value))...)...)
		} else {
			head = le.AppendUint32(head, dataOff+uint32(len(data)))
			data = append(data, value...)
		}
	}
	head = append(head, 'I', 'I')
	head = le.AppendUint16(head, 42)
	head = le.AppendUint32(head, ifd0Off)
	head = le.AppendUint16(head, uint16(n0))
	for _, e := range ifd0 {
		put(e)
	}
	put(tiffEntry{0x8769, exifOff})
	head = le.AppendUint32(head, 0)
	head = le.AppendUint16(head, uint16(n1))
	for _, e := range exif {
		put(e)
	}
	head = le.AppendUint32(head, 0)
	app1 := append([]byte("Exif\x00\x00"), append(head, data...)...)
	var out bytes.Buffer
	out.Write(plain.Bytes()[:2])
	out.Write([]byte{0xFF, 0xE1})
	_ = binary.Write(&out, binary.BigEndian, uint16(len(app1)+2))
	out.Write(app1)
	out.Write(plain.Bytes()[2:])
	return out.Bytes()
}

func TestReadCamera(t *testing.T) {
	data := cameraJPEG(t,
		[]tiffEntry{{0x010F, "Fujifilm"}, {0x0110, "X-T5"}},
		[]tiffEntry{{0x9003, "2023:08:01 10:00:00"}, {0x829A, [2]uint32{1, 250}}, {0x829D, [2]uint32{28, 10}}, {0x8827, uint16(400)},
			{0x920A, [2]uint32{230, 10}}, {0xA405, uint16(35)}, {0xA434, "XF23mmF1.4 R"}})
	info, err := readJPEGExif(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := Camera{Make: "Fujifilm", Model: "X-T5", Lens: "XF23mmF1.4 R", Exposure: 0.004, FNumber: 2.8, ISO: 400, Focal: 23, Focal35: 35}
	if info.camera != want {
		t.Errorf("camera = %+v, want %+v", info.camera, want)
	}
	if info.taken.IsZero() {
		t.Error("date taken should still be read")
	}
}

func TestReadMP4Length(t *testing.T) {
	data := mp4With(time.Date(2023, 1, 2, 3, 4, 5, 0, time.UTC))
	// mvhd body starts after ftyp (16) + moov header (8) + mvhd header (8); timescale at +12, duration at +16.
	body := 16 + 8 + 8
	binary.BigEndian.PutUint32(data[body+12:], 600)
	binary.BigEndian.PutUint32(data[body+16:], 600*75+300)
	_, d, err := readMP4Header(bytes.NewReader(data))
	if err != nil || d != 75500*time.Millisecond {
		t.Fatalf("length %v, %v; want 1m15.5s", d, err)
	}
}

func TestBinName(t *testing.T) {
	for in, want := range map[string]struct {
		name string
		n    int
	}{
		"20261010-120000_a.jpg":     {"a.jpg", 0},
		"20261010-120000_a (3).jpg": {"a.jpg", 3},
		"20261010-120000_b (x).jpg": {"b (x).jpg", 0},
		"plain.png":                 {"plain.png", 0},
	} {
		if name, n := binName(in); name != want.name || n != want.n {
			t.Errorf("binName(%q) = %q, %d; want %q, %d", in, name, n, want.name, want.n)
		}
	}
}

func TestTrashAndRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	drive := filepath.Join(dir, "drive")
	for _, rel := range []string{"Photos/2021/a.jpg", "Photos/2022/a.jpg", "Photos/b.jpg"} {
		p := filepath.Join(drive, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, exifJPEG(t, 4, 4, 1, "2022:01:01 00:00:00", ""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fsvc, err := files.New([]files.Root{{ID: "drive", Name: "Drive", Path: drive}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	defer close(done)
	ix := files.NewIndex(fsvc, done)
	st, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	lib := New(fsvc, ix, st, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := lib.TrashRoot(ctx, "drive", []string{"Photos/2021/a.jpg", "Photos/2022/a.jpg", "Photos/b.jpg"}); err != nil {
		t.Fatal(err)
	}
	items, err := lib.Deleted(ctx)
	if err != nil || len(items) != 3 {
		t.Fatalf("deleted: %d items, %v; want 3", len(items), err)
	}
	byOrig := map[string]store.PhotoTrash{}
	for _, it := range items {
		byOrig[it.OrigPath] = it
		if _, err := os.Stat(filepath.Join(drive, filepath.FromSlash(it.TrashPath))); err != nil {
			t.Errorf("%s: not in the bin at %s", it.OrigPath, it.TrashPath)
		}
	}
	if byOrig["/Photos/2021/a.jpg"].TrashPath == byOrig["/Photos/2022/a.jpg"].TrashPath {
		t.Fatal("two photos with the same name must map to different bin entries")
	}

	// The 2022 folder is gone by now: restoring makes it again, under the original name.
	if err := os.RemoveAll(filepath.Join(drive, "Photos", "2022")); err != nil {
		t.Fatal(err)
	}
	keys, err := lib.Restore(ctx, []int64{byOrig["/Photos/2022/a.jpg"].ID, byOrig["/Photos/b.jpg"].ID})
	if err != nil || len(keys) != 2 {
		t.Fatalf("restore: %v, %v", keys, err)
	}
	for _, rel := range []string{"Photos/2022/a.jpg", "Photos/b.jpg"} {
		if _, err := os.Stat(filepath.Join(drive, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s not restored: %v", rel, err)
		}
	}

	// Deleting for good removes it from the bin and the list.
	a := byOrig["/Photos/2021/a.jpg"]
	if err := lib.Purge(ctx, []int64{a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(drive, filepath.FromSlash(a.TrashPath))); !os.IsNotExist(err) {
		t.Errorf("purged file still there: %v", err)
	}
	if items, _ := lib.Deleted(ctx); len(items) != 0 {
		t.Errorf("after restore and purge: %d items left", len(items))
	}

	// Something already gone from the bin is forgotten.
	if err := lib.TrashRoot(ctx, "drive", []string{"Photos/b.jpg"}); err != nil {
		t.Fatal(err)
	}
	items, _ = lib.Deleted(ctx)
	if len(items) != 1 {
		t.Fatalf("got %d, want 1", len(items))
	}
	_ = os.Remove(filepath.Join(drive, filepath.FromSlash(items[0].TrashPath)))
	if items, _ := lib.Deleted(ctx); len(items) != 0 {
		t.Errorf("emptied bin: %d items still listed", len(items))
	}
	// Bin paths from the store are never trusted outside the bin.
	if err := st.AddPhotoTrash(ctx, []store.PhotoTrash{{Root: "drive", TrashPath: "/Photos/2022/a.jpg", OrigPath: "/Photos/x.jpg", DeletedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	all, _ := st.PhotoTrashItems(ctx)
	if err := lib.Purge(ctx, []int64{all[0].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(drive, "Photos", "2022", "a.jpg")); err != nil {
		t.Error("purge must only delete inside the recycle bin")
	}
}

func TestAlbumCover(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	for _, id := range []string{"u1", "u2"} {
		if err := st.CreateUser(ctx, &store.User{ID: id, Username: id, Role: "admin", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	id, err := st.CreatePhotoAlbum(ctx, "u1", "Trip")
	if err != nil {
		t.Fatal(err)
	}
	k := store.PhotoKey{Root: "drive", Path: "/Photos/a.jpg"}
	if err := st.SetPhotoAlbumCover(ctx, "u2", id, &k); err != store.ErrNotFound {
		t.Errorf("another user's album: %v, want ErrNotFound", err)
	}
	if err := st.SetPhotoAlbumCover(ctx, "u1", id, &k); err != nil {
		t.Fatal(err)
	}
	albums, _ := st.PhotoAlbums(ctx, "u1")
	if len(albums) != 1 || albums[0].Cover == nil || *albums[0].Cover != k {
		t.Fatalf("cover not saved: %+v", albums[0])
	}
	// Deleting the photo resets the cover.
	if err := st.ForgetPhotos(ctx, []store.PhotoKey{k}); err != nil {
		t.Fatal(err)
	}
	if albums, _ = st.PhotoAlbums(ctx, "u1"); albums[0].Cover != nil {
		t.Errorf("cover should reset, got %+v", albums[0].Cover)
	}
}
