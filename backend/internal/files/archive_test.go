package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeAt(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCompressAndExtractZip(t *testing.T) {
	s, dir := newTestService(t)
	ctx := context.Background()
	writeAt(t, filepath.Join(dir, "Docs", "a.txt"), "alpha")
	writeAt(t, filepath.Join(dir, "Docs", "sub", "b.txt"), "bravo")
	writeAt(t, filepath.Join(dir, "c.md"), "charlie")

	var last Progress
	zipRel, err := s.Compress(ctx, "t", []string{"Docs", "c.md"}, ".", "Bundle", func(p Progress) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	if zipRel != "Bundle.zip" {
		t.Fatalf("zip at %q, want Bundle.zip", zipRel)
	}
	if last.BytesDone != 17 || last.BytesTotal != 17 {
		t.Errorf("progress %+v, want 17 of 17 bytes", last)
	}
	// A second archive with the same name gets a number, not an overwrite.
	if again, err := s.Compress(ctx, "t", []string{"c.md"}, ".", "Bundle.zip", nil); err != nil || again != "Bundle (1).zip" {
		t.Fatalf("second zip = %q, %v", again, err)
	}

	out, err := s.Extract(ctx, "t", "Bundle.zip", ".", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Bundle" {
		t.Fatalf("extracted to %q, want Bundle", out)
	}
	if got := readFile(t, filepath.Join(dir, "Bundle", "Docs", "sub", "b.txt")); got != "bravo" {
		t.Errorf("b.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "Bundle", "c.md")); got != "charlie" {
		t.Errorf("c.md = %q", got)
	}
	// Extracting again keeps the first folder.
	if out, err := s.Extract(ctx, "t", "Bundle.zip", ".", nil); err != nil || out != "Bundle (1)" {
		t.Fatalf("second extract = %q, %v", out, err)
	}
	// No temporary files are left behind.
	des, _ := os.ReadDir(dir)
	for _, de := range des {
		if filepath.Ext(de.Name()) == uploadSuffix {
			t.Errorf("leftover %s", de.Name())
		}
	}
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractRefusesZipSlip(t *testing.T) {
	s, dir := newTestService(t)
	outside := filepath.Join(filepath.Dir(dir), "pwned.txt")
	for _, evil := range []string{"../pwned.txt", "ok/../../pwned.txt", `..\pwned.txt`, "/etc/pwned.txt", "C:/pwned.txt"} {
		writeAt(t, filepath.Join(dir, "in", "evil.zip"), string(makeZip(t, map[string]string{"fine.txt": "x", evil: "gotcha"})))
		_, err := s.Extract(context.Background(), "t", "in/evil.zip", "in", nil)
		if !errors.Is(err, ErrUnsafeArchive) {
			t.Errorf("%q: err = %v, want ErrUnsafeArchive", evil, err)
		}
		if _, err := os.Stat(outside); err == nil {
			t.Fatalf("%q escaped the storage location", evil)
		}
		// Nothing at all was extracted, not even the harmless file.
		des, _ := os.ReadDir(filepath.Join(dir, "in"))
		if len(des) != 1 {
			t.Errorf("%q: left %d items in the folder, want only the archive", evil, len(des))
		}
	}
}

func TestExtractTarGz(t *testing.T) {
	s, dir := newTestService(t)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	add(&tar.Header{Name: "pics/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: "pics/cat.txt", Typeflag: tar.TypeReg, Mode: 0o644}, "meow")
	add(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, "")
	tw.Close()
	gz.Close()
	writeAt(t, filepath.Join(dir, "pics.tar.gz"), buf.String())

	out, err := s.Extract(context.Background(), "t", "pics.tar.gz", ".", nil)
	if err != nil || out != "pics" {
		t.Fatalf("extract = %q, %v", out, err)
	}
	if got := readFile(t, filepath.Join(dir, "pics", "pics", "cat.txt")); got != "meow" {
		t.Errorf("cat.txt = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "pics", "link")); err == nil {
		t.Error("symlinks inside archives must be skipped")
	}

	// A tar with a climbing path is refused too.
	buf.Reset()
	tw = tar.NewWriter(&buf)
	add(&tar.Header{Name: "../../evil.txt", Typeflag: tar.TypeReg, Mode: 0o644}, "x")
	tw.Close()
	writeAt(t, filepath.Join(dir, "evil.tar"), buf.String())
	if _, err := s.Extract(context.Background(), "t", "evil.tar", ".", nil); !errors.Is(err, ErrUnsafeArchive) {
		t.Errorf("evil.tar: err = %v, want ErrUnsafeArchive", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Error("a refused tar left a folder behind")
	}
}

func TestArchiveRespectsProtection(t *testing.T) {
	s, dir := newTestService(t)
	writeAt(t, filepath.Join(dir, "app", "secret.key"), "k")
	writeAt(t, filepath.Join(dir, "free", "x.zip"), string(makeZip(t, map[string]string{"y.txt": "y"})))
	s.Protect(filepath.Join(dir, "app"))
	ctx := context.Background()
	if _, err := s.Compress(ctx, "t", []string{"app"}, ".", "a.zip", nil); !errors.Is(err, ErrProtected) {
		t.Errorf("compress protected: %v", err)
	}
	if _, err := s.Extract(ctx, "t", "free/x.zip", "app", nil); !errors.Is(err, ErrProtected) {
		t.Errorf("extract into protected: %v", err)
	}
	if _, err := s.Extract(ctx, "t", "free/x.zip", "free", nil); err != nil {
		t.Errorf("extract elsewhere: %v", err)
	}
	if _, err := s.Extract(ctx, "t", "app/secret.key", ".", nil); !errors.Is(err, ErrNotArchive) {
		t.Errorf("non-archive: %v", err)
	}
}

func TestExtractCanceled(t *testing.T) {
	s, dir := newTestService(t)
	writeAt(t, filepath.Join(dir, "x.zip"), string(makeZip(t, map[string]string{"a.txt": "a", "b.txt": "b"})))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Extract(ctx, "t", "x.zip", ".", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	des, _ := os.ReadDir(dir)
	if len(des) != 1 {
		t.Errorf("canceled extract left %d items", len(des))
	}
}

func TestParseMountinfo(t *testing.T) {
	info := `22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw
40 22 8:17 / /media/sha/USB\040Stick rw,nosuid - vfat /dev/sdb1 rw
41 22 8:33 / /run/media/sha/Backup rw - exfat /dev/sdc1 rw
42 22 0:50 / /mnt/nocapos/1-nas rw - cifs //nas/share rw
43 22 0:51 / /media/ram rw - tmpfs tmpfs rw
44 22 7:0 / /mnt/snap rw - squashfs /dev/loop0 ro
45 22 8:49 / /mnt/nocapos/2-disk rw - ext4 /dev/sdd1 rw
`
	got := parseMountinfo(info, "/mnt/nocapos")
	if len(got) != 2 || got[0].Name != "Backup" || got[1].Name != "USB Stick" || got[1].Path != "/media/sha/USB Stick" {
		t.Fatalf("got %+v", got)
	}
}
