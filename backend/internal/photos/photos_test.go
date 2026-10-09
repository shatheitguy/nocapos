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
