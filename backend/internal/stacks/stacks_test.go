package stacks

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

const sample = "services:\n  web:\n    image: nginx:alpine\n    ports:\n      - \"8080:80\"\n"

func TestSaveListDelete(t *testing.T) {
	ctx := context.Background()
	m := NewManager(t.TempDir(), "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, bad := range []string{"", "Web", "../x", "a b", "-x"} {
		if err := m.Save(ctx, bad, sample, "", true); err == nil {
			t.Errorf("name %q should be refused", bad)
		}
	}
	if err := m.Save(ctx, "web", "  ", "", true); err == nil {
		t.Error("an empty compose file should be refused")
	}
	if err := m.Save(ctx, "web", sample, "TZ=UTC\n", true); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(ctx, "web", sample, "", true); err != ErrExists {
		t.Errorf("creating twice: %v", err)
	}
	if err := m.Save(ctx, "nope", sample, "", false); err != ErrNotFound {
		t.Errorf("updating a missing stack: %v", err)
	}
	c, env, err := m.Files("web")
	if err != nil || c != sample || env != "TZ=UTC\n" {
		t.Fatalf("files: %q %q %v", c, env, err)
	}
	if fi, err := os.Stat(filepath.Join(m.Dir("web"), envFile)); err != nil || fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf(".env must be private: %v %v", fi, err)
	}

	// Clearing the variables removes the .env file.
	if err := m.Save(ctx, "web", sample, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.Dir("web"), envFile)); !os.IsNotExist(err) {
		t.Error(".env should be gone")
	}
	entries, _ := os.ReadDir(m.Dir("web"))
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}

	list, err := m.List()
	if err != nil || len(list) != 1 || list[0].Name != "web" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := m.Run(ctx, "web", "explode"); err == nil {
		t.Error("unknown actions should be refused")
	}
	if err := m.Delete("web"); err != nil {
		t.Fatal(err)
	}
	if m.Exists("web") {
		t.Error("still there after delete")
	}
}

func TestOutputKeepsTail(t *testing.T) {
	o := &op{}
	big := make([]byte, maxOutput)
	for i := range big {
		big[i] = 'a'
	}
	_, _ = o.Write(big)
	_, _ = o.Write([]byte("END"))
	if o.buf.Len() != maxOutput || string(o.buf.Bytes()[maxOutput-3:]) != "END" {
		t.Errorf("tail not kept: len %d", o.buf.Len())
	}
}
