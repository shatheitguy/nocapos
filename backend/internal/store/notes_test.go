package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/notes"
)

func notesStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now()
	for _, id := range []string{"u1", "u2"} {
		if err := st.CreateUser(ctx, &User{ID: id, Username: "user-" + id, PasswordHash: "x", Role: "user", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	return st, ctx
}

func newNote(id string, ms int64) *Note {
	return &Note{ID: id, Title: "t-" + id, Body: `[{"type":"p","spans":[{"text":"hi"}]}]`, Text: "hi", Color: "glass", CreatedAt: ms, UpdatedAt: ms}
}

func TestNotesMigrationAndOwnership(t *testing.T) {
	st, ctx := notesStore(t)
	var v int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 34`).Scan(&v); err != nil || v != 1 {
		t.Fatalf("migration 034: %d %v", v, err)
	}
	if err := st.CreateNote(ctx, "u1", newNote("n1", 1000), notes.MaxNotes); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateNote(ctx, "u2", newNote("n1", 1000), notes.MaxNotes); !errors.Is(err, ErrConflict) {
		t.Fatalf("same id twice: %v", err)
	}
	if _, err := st.Note(ctx, "u2", "n1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("u2 reads u1's note: %v", err)
	}
	if _, err := st.UpdateNote(ctx, "u2", "n1", func(n *Note) bool { n.Title = "x"; return true }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("u2 edits u1's note: %v", err)
	}
	if err := st.DeleteNote(ctx, "u2", "n1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("u2 deletes u1's note: %v", err)
	}
	if moved, _ := st.SetNoteDesks(ctx, "u2", []NoteDeskPos{{ID: "n1", Desk: &notes.Desk{X: 1}}}); len(moved) != 0 {
		t.Fatalf("u2 moves u1's note: %+v", moved)
	}
	if l, _ := st.ListNotes(ctx, "u2", NotesAll); len(l) != 0 {
		t.Fatalf("u2 lists: %+v", l)
	}
	n, err := st.UpdateNote(ctx, "u1", "n1", func(n *Note) bool { n.Pinned = true; return true })
	if err != nil || !n.Pinned || n.Version != 2 {
		t.Fatalf("update: %+v %v", n, err)
	}
	// The body comes out as JSON, not a string.
	b, _ := n.MarshalJSON()
	if want := `"body":[{"type":"p","spans":[{"text":"hi"}]}]`; !strings.Contains(string(b), want) {
		t.Fatalf("json: %s", b)
	}
	// Notes go with their owner.
	if err := st.DeleteUser(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.CountNotes(ctx, "u1"); c != 0 {
		t.Fatalf("notes outlived their owner: %d", c)
	}
}

func TestNotesLimitFiltersAndTrash(t *testing.T) {
	st, ctx := notesStore(t)
	for i, id := range []string{"a1aaaaaa", "b2bbbbbb", "c3cccccc"} {
		if err := st.CreateNote(ctx, "u1", newNote(id, int64(1000+i)), 3); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CreateNote(ctx, "u1", newNote("d4dddddd", 2000), 3); !errors.Is(err, ErrLimit) {
		t.Fatalf("over the limit: %v", err)
	}
	if err := st.CreateNote(ctx, "u2", newNote("e5eeeeee", 2000), 3); err != nil {
		t.Fatalf("the limit is per person: %v", err)
	}
	trashed := int64(5000)
	if _, err := st.UpdateNote(ctx, "u1", "a1aaaaaa", func(n *Note) bool { n.TrashedAt = &trashed; return true }); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateNote(ctx, "u1", "b2bbbbbb", func(n *Note) bool { n.Archived = true; return true }); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[NoteFilter]string{NotesActive: "c3cccccc", NotesArchived: "b2bbbbbb", NotesTrash: "a1aaaaaa"} {
		l, err := st.ListNotes(ctx, "u1", f)
		if err != nil || len(l) != 1 || l[0].ID != want {
			t.Fatalf("%s: %+v %v", f, l, err)
		}
	}
	if l, _ := st.ListNotes(ctx, "u1", NotesAll); len(l) != 3 || l[0].ID != "c3cccccc" {
		t.Fatalf("all, newest first: %+v", l)
	}
	// Old trash is purged; fresh trash stays.
	if gone, err := st.PurgeNotesTrash(ctx, 4000); err != nil || len(gone) != 0 {
		t.Fatalf("fresh trash purged: %v %v", gone, err)
	}
	if gone, err := st.PurgeNotesTrash(ctx, 6000); err != nil || len(gone["u1"]) != 1 {
		t.Fatalf("old trash: %v %v", gone, err)
	}
	if _, err := st.UpdateNote(ctx, "u1", "c3cccccc", func(n *Note) bool { n.TrashedAt = &trashed; return true }); err != nil {
		t.Fatal(err)
	}
	if ids, err := st.EmptyNotesTrash(ctx, "u1"); err != nil || len(ids) != 1 || ids[0] != "c3cccccc" {
		t.Fatalf("empty trash: %v %v", ids, err)
	}
	if c, _ := st.CountNotes(ctx, "u2"); c != 1 {
		t.Fatalf("someone else's notes went: %d", c)
	}
}

func TestNotesDeskAndReminders(t *testing.T) {
	st, ctx := notesStore(t)
	n := newNote("r1rrrrrr", 1000)
	at := int64(10_000)
	n.RemindAt = &at
	if err := st.CreateNote(ctx, "u1", n, 10); err != nil {
		t.Fatal(err)
	}
	moved, err := st.SetNoteDesks(ctx, "u1", []NoteDeskPos{{ID: "r1rrrrrr", Desk: &notes.Desk{X: 10, Y: 20, W: 200, H: 180}}, {ID: "nope0000"}})
	if err != nil || len(moved) != 1 || moved[0].Desk == nil || moved[0].Desk.W != 200 || moved[0].UpdatedAt != 1000 || moved[0].Version != 2 {
		t.Fatalf("desk: %+v %v", moved, err)
	}
	if due, _ := st.ClaimDueReminders(ctx, 9_999); len(due) != 0 {
		t.Fatalf("early: %+v", due)
	}
	due, err := st.ClaimDueReminders(ctx, 10_000)
	if err != nil || len(due) != 1 || due[0].UserID != "u1" || due[0].Note.ID != "r1rrrrrr" || !due[0].Note.Reminded {
		t.Fatalf("due: %+v %v", due, err)
	}
	if due, _ := st.ClaimDueReminders(ctx, 20_000); len(due) != 0 {
		t.Fatalf("fired twice: %+v", due)
	}
}

// Export & Restore copies the whole database, so notes go with it.
func TestNotesInBackup(t *testing.T) {
	st, ctx := notesStore(t)
	if err := st.CreateNote(ctx, "u1", newNote("k1kkkkkk", 1000), 10); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := st.BackupTo(ctx, filepath.Join(dir, "alfa.db")); err != nil {
		t.Fatal(err)
	}
	if err := CheckBackup(ctx, filepath.Join(dir, "alfa.db")); err != nil {
		t.Fatal(err)
	}
	back, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	if n, err := back.Note(ctx, "u1", "k1kkkkkk"); err != nil || n.Text != "hi" {
		t.Fatalf("restored: %+v %v", n, err)
	}
}
