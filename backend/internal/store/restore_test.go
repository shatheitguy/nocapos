package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageAndApplyRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "marker", "backup"); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "b.db")
	if err := st.BackupTo(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "marker", "after"); err != nil {
		t.Fatal(err)
	}

	// Junk is refused and nothing is staged.
	if err := StageRestore(ctx, dir, strings.NewReader("hello")); !errors.Is(err, ErrBadBackup) {
		t.Fatalf("junk: %v", err)
	}
	if HasStagedRestore(dir) {
		t.Fatal("junk was staged")
	}

	b, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := StageRestore(ctx, dir, bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	if !HasStagedRestore(dir) {
		t.Fatal("not staged")
	}
	if err := CancelStagedRestore(dir); err != nil || HasStagedRestore(dir) {
		t.Fatalf("cancel: %v", err)
	}
	if err := StageRestore(ctx, dir, bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	st.Close()

	applied, err := ApplyStagedRestore(dir)
	if err != nil || !applied {
		t.Fatalf("apply: %v %v", applied, err)
	}
	if HasStagedRestore(dir) {
		t.Fatal("still staged after apply")
	}
	kept, _ := filepath.Glob(filepath.Join(dir, "alfa.db.before-restore-*"))
	if len(kept) == 0 {
		t.Fatal("the replaced database wasn't kept")
	}
	st, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, _ := st.Setting(ctx, "marker"); v != "backup" {
		t.Fatalf("marker = %q, want backup", v)
	}
	if applied, _ := ApplyStagedRestore(dir); applied {
		t.Fatal("applied twice")
	}
}
