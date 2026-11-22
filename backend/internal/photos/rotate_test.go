package photos

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

func TestTurnOrientation(t *testing.T) {
	cases := []struct{ from, turns, want int }{
		{1, 1, 6}, {6, 1, 3}, {3, 1, 8}, {8, 1, 1},
		{1, -1, 8}, {6, -1, 1}, {1, 2, 3}, {1, 4, 1}, {1, -5, 8},
		{2, 1, 7}, {7, 1, 4}, {4, 1, 5}, {5, 1, 2}, {2, -1, 5},
		{0, 1, 6}, // no valid value: treated as upright
	}
	for _, c := range cases {
		if got := turnOrientation(c.from, c.turns); got != c.want {
			t.Errorf("turnOrientation(%d, %d) = %d, want %d", c.from, c.turns, got, c.want)
		}
	}
}

func TestOrientationAt(t *testing.T) {
	data := exifJPEG(t, 8, 4, 6, "2024:05:06 07:08:09", "+04:00")
	off, val, bo, ok := orientationAt(data)
	if !ok || val != 6 {
		t.Fatalf("orientationAt: ok=%v val=%d, want 6", ok, val)
	}
	if got := int(bo.Uint16(data[off:])); got != 6 {
		t.Fatalf("value at offset %d = %d, want 6", off, got)
	}
	if _, _, _, ok := orientationAt([]byte("not a jpeg at all")); ok {
		t.Fatal("found an orientation in something that isn't a JPEG")
	}
	// Cut short before the EXIF block ends: not found, no panic.
	if _, _, _, ok := orientationAt(data[:20]); ok {
		t.Fatal("found an orientation in a truncated file")
	}
}

func TestRotateInPlace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	drive := filepath.Join(dir, "drive")
	if err := os.MkdirAll(filepath.Join(drive, "Photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := exifJPEG(t, 8, 4, 1, "2024:05:06 07:08:09", "")
	pic := filepath.Join(drive, "Photos", "a.jpg")
	if err := os.WriteFile(pic, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drive, "Photos", "b.png"), []byte("\x89PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsvc, err := files.New([]files.Root{{ID: "drive", Name: "Drive", Path: drive}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	defer close(done)
	st, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	lib := New(fsvc, files.NewIndex(fsvc, done), st, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))

	o, err := lib.Rotate("drive", "Photos/a.jpg", 1)
	if err != nil || o != 6 {
		t.Fatalf("rotate: %d, %v; want 6", o, err)
	}
	o, err = lib.Rotate("drive", "/Photos/a.jpg", 2)
	if err != nil || o != 8 {
		t.Fatalf("rotate twice more: %d, %v; want 8", o, err)
	}
	after, err := os.ReadFile(pic)
	if err != nil {
		t.Fatal(err)
	}
	// Only the two orientation bytes changed: same size, the pixels untouched.
	if len(after) != len(orig) {
		t.Fatalf("size changed: %d → %d", len(orig), len(after))
	}
	diff := 0
	for i := range orig {
		if orig[i] != after[i] {
			diff++
		}
	}
	if diff == 0 || diff > 2 {
		t.Fatalf("%d bytes changed, want 1 or 2", diff)
	}
	if ex, err := readJPEGExif(bytes.NewReader(after)); err != nil || ex.orientation != 8 {
		t.Fatalf("reads back as %d, %v; want 8", ex.orientation, err)
	}
	if _, err := lib.Rotate("drive", "Photos/b.png", 1); !errors.Is(err, ErrNoOrientation) {
		t.Fatalf("png: %v, want ErrNoOrientation", err)
	}
	if _, err := lib.Rotate("drive", "Photos/../../x.jpg", 1); err == nil {
		t.Fatal("rotated a path outside the drive")
	}
}
