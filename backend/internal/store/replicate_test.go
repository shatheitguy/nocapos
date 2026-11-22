package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/notes"
)

// syncPair is two servers' databases and how far each has read the other.
type syncPair struct {
	a, b       *Store
	aID, bID   string
	aRead      int64 // how far A has read B's log
	bRead      int64 // how far B has read A's log
	ackedByA   int64 // what B knows of aRead (the cursor A last asked with)
	ackedByB   int64 // what A knows of bRead
	t          *testing.T
	ctx        context.Context
	sealA      func(string) []byte
	sealB      func(string) []byte
	openA      func([]byte) (string, error)
	openB      func([]byte) (string, error)
	conflictsA int
	conflictsB int
}

func openSyncStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// Each "server" seals by prefixing its own name: what was sealed by one
// can't be opened by the other.
func sealer(name string) (func(string) []byte, func([]byte) (string, error)) {
	return func(p string) []byte { return []byte(name + ":" + p) },
		func(b []byte) (string, error) {
			s, ok := strings.CutPrefix(string(b), name+":")
			if !ok {
				return "", errNotMine
			}
			return s, nil
		}
}

var errNotMine = ErrConflict

func newSyncPair(t *testing.T) *syncPair {
	p := &syncPair{a: openSyncStore(t), b: openSyncStore(t), aID: "aaaa", bID: "bbbb", t: t, ctx: context.Background()}
	p.sealA, p.openA = sealer("A")
	p.sealB, p.openB = sealer("B")
	return p
}

// pullB: B reads A's log and applies it.
func (p *syncPair) pullB(force bool) *SyncResult {
	p.t.Helper()
	return p.pull(p.a, p.b, p.aID, p.bID, &p.bRead, &p.ackedByB, max(p.ackedByA, p.aRead), p.openA, p.sealB, force, &p.conflictsB)
}

// ready: after the first sync, each side starts where the other is now.
func (p *syncPair) ready() {
	p.t.Helper()
	p.ackedByB = p.bRead
	seq, err := p.b.SyncSeq(p.ctx)
	if err != nil {
		p.t.Fatal(err)
	}
	p.aRead, p.ackedByA = seq, seq
}

func (p *syncPair) pullA() *SyncResult {
	p.t.Helper()
	return p.pull(p.b, p.a, p.bID, p.aID, &p.aRead, &p.ackedByA, max(p.ackedByB, p.bRead), p.openB, p.sealA, false, &p.conflictsA)
}

// Like the servers do: the reader learns how far the sender has read its
// own log from the feed (the sender's cursor), or from the cursor the
// sender last asked with.
func (p *syncPair) pull(from, to *Store, fromID, toID string, cursor, seenBySender *int64, acked int64, open func([]byte) (string, error), seal func(string) []byte, force bool, conflicts *int) *SyncResult {
	p.t.Helper()
	var all []SyncChange
	for {
		*seenBySender = *cursor
		feed, err := from.SyncChanges(p.ctx, *cursor, fromID, toID, 3, 0, open)
		if err != nil {
			p.t.Fatal(err)
		}
		all = append(all, feed.Changes...)
		*cursor = feed.Next
		if !feed.More {
			break
		}
	}
	res, err := to.ApplySyncChanges(p.ctx, all, SyncApply{Self: toID, Peer: fromID, Acked: acked, Force: force, Seal: seal})
	if err != nil {
		p.t.Fatal(err)
	}
	*conflicts += res.Conflicts
	return res
}

func mkUser(t *testing.T, st *Store, id, name string) {
	t.Helper()
	now := time.Now()
	if err := st.CreateUser(context.Background(), &User{ID: id, Username: name, PasswordHash: "hash-" + name, Role: RoleUser, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func TestSyncLogTriggers(t *testing.T) {
	st := openSyncStore(t)
	ctx := context.Background()
	seq0, _ := st.SyncSeq(ctx)
	mkUser(t, st, "u1", "ann")
	seq1, _ := st.SyncSeq(ctx)
	if seq1 <= seq0 {
		t.Fatalf("insert not logged: %d → %d", seq0, seq1)
	}
	var n int
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_rows WHERE tbl = 'users' AND pk = '["u1"]' AND origin = ''`).Scan(&n)
	if n != 1 {
		t.Fatalf("users row logged %d times", n)
	}
	// A column each server keeps for itself doesn't start a change.
	if _, err := st.db.ExecContext(ctx, `INSERT INTO devices (id, user_id, name, platform, token_hash, created_at) VALUES ('d1', 'u1', 'Pixel', 'android', 'h1', 1)`); err != nil {
		t.Fatal(err)
	}
	before, _ := st.SyncSeq(ctx)
	if _, err := st.db.ExecContext(ctx, `UPDATE devices SET battery = 50, last_seen_at = 9 WHERE id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	if after, _ := st.SyncSeq(ctx); after != before {
		t.Fatalf("battery update logged: %d → %d", before, after)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE devices SET name = 'Pixel 9' WHERE id = 'd1'`); err != nil {
		t.Fatal(err)
	}
	if after, _ := st.SyncSeq(ctx); after == before {
		t.Fatal("rename not logged")
	}
	// This server's own settings stay out of the log; shared ones go in.
	_ = st.SetSetting(ctx, "link.remote_address", "https://x")
	_ = st.SetSetting(ctx, "notify.settings", "{}")
	var local, shared int
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_rows WHERE tbl = 'server_settings' AND pk = '["link.remote_address"]'`).Scan(&local)
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_rows WHERE tbl = 'server_settings' AND pk = '["notify.settings"]'`).Scan(&shared)
	if local != 0 || shared != 1 {
		t.Fatalf("settings: local %d shared %d", local, shared)
	}
	// Deleting a user removes its devices (cascade): both deletes logged.
	if _, err := st.db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	feed, err := st.SyncChanges(ctx, 0, "self", "other", 100, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	gone := map[string]bool{}
	for _, c := range feed.Changes {
		if c.Row == nil {
			gone[c.Table+c.PK] = true
		}
		if c.Origin != "self" {
			t.Fatalf("origin %q", c.Origin)
		}
	}
	if !gone[`users["u1"]`] || !gone[`devices["d1"]`] {
		t.Fatalf("deletes: %v", gone)
	}
	if feed.Schema < 38 {
		t.Fatalf("schema %d", feed.Schema)
	}
	// Restarting (triggers made again) keeps the log as it is.
	seqBefore, _ := st.SyncSeq(ctx)
	if err := st.prepareSync(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.SyncSeq(ctx); s != seqBefore {
		t.Fatalf("restart moved the log: %d → %d", seqBefore, s)
	}
}

func TestSyncReplicatesBothWays(t *testing.T) {
	p := newSyncPair(t)
	ctx := p.ctx
	mkUser(t, p.a, "u1", "ann")
	if err := p.a.CreateNote(ctx, "u1", &Note{ID: "n1", Title: "groceries", Body: "[]", Color: "glass", CreatedAt: 1, UpdatedAt: 1}, notes.MaxNotes); err != nil {
		t.Fatal(err)
	}
	if err := p.a.SetTOTP(ctx, "u1", p.sealA("TOTPSECRET"), true); err != nil {
		t.Fatal(err)
	}
	_ = p.a.SetSetting(ctx, "notify.settings", `{"quiet":true}`)
	_ = p.a.SetSetting(ctx, "link.remote_address", "https://a.example")

	if res := p.pullB(true); res.Failed != 0 {
		t.Fatalf("first sync: %+v", res)
	}
	p.ready()
	u, err := p.b.UserByID(ctx, "u1")
	if err != nil || u.Username != "ann" || u.PasswordHash != "hash-ann" {
		t.Fatalf("B's user: %+v %v", u, err)
	}
	// The secret is sealed again with B's key.
	var sealed []byte
	_ = p.b.db.QueryRowContext(ctx, `SELECT totp_secret FROM users WHERE id = 'u1'`).Scan(&sealed)
	if string(sealed) != "B:TOTPSECRET" {
		t.Fatalf("B's sealed secret %q", sealed)
	}
	if n, err := p.b.Note(ctx, "u1", "n1"); err != nil || n.Title != "groceries" {
		t.Fatalf("B's note: %+v %v", n, err)
	}
	if v, _ := p.b.Setting(ctx, "notify.settings"); v != `{"quiet":true}` {
		t.Fatalf("shared setting %q", v)
	}
	if v, _ := p.b.Setting(ctx, "link.remote_address"); v != "" {
		t.Fatalf("A's own address went over: %q", v)
	}

	// B's applied changes don't come back to A as A's own.
	if res := p.pullA(); res.Applied != 0 {
		t.Fatalf("echo: %+v", res)
	}

	// B edits, A picks it up; then A deletes, B picks that up.
	if _, err := p.b.UpdateNote(ctx, "u1", "n1", func(n *Note) bool { n.Title = "groceries + milk"; return true }); err != nil {
		t.Fatal(err)
	}
	if res := p.pullA(); res.Applied != 1 || res.Conflicts != 0 {
		t.Fatalf("A pull: %+v", res)
	}
	if n, _ := p.a.Note(ctx, "u1", "n1"); n == nil || n.Title != "groceries + milk" {
		t.Fatalf("A's note: %+v", n)
	}
	if err := p.a.DeleteNote(ctx, "u1", "n1"); err != nil {
		t.Fatal(err)
	}
	p.pullB(false)
	if _, err := p.b.Note(ctx, "u1", "n1"); err != ErrNotFound {
		t.Fatalf("B still has the note: %v", err)
	}
	if p.conflictsA+p.conflictsB != 0 {
		t.Fatalf("unexpected conflicts %d/%d", p.conflictsA, p.conflictsB)
	}
}

func TestSyncConflictLastWriterWins(t *testing.T) {
	p := newSyncPair(t)
	ctx := p.ctx
	mkUser(t, p.a, "u1", "ann")
	if err := p.a.CreateNote(ctx, "u1", &Note{ID: "n1", Title: "v0", Body: "[]", Color: "glass", CreatedAt: 1, UpdatedAt: 1}, notes.MaxNotes); err != nil {
		t.Fatal(err)
	}
	p.pullB(true)
	p.ready()

	// Both change the same note before they sync; B writes twice (its clock is ahead).
	if _, err := p.a.UpdateNote(ctx, "u1", "n1", func(n *Note) bool { n.Title = "from A"; return true }); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"from B", "from B again"} {
		if _, err := p.b.UpdateNote(ctx, "u1", "n1", func(n *Note) bool { n.Title = title; return true }); err != nil {
			t.Fatal(err)
		}
	}
	// A pulls first: B's newer version wins on A, A keeps a conflict.
	p.pullA()
	if n, _ := p.a.Note(ctx, "u1", "n1"); n.Title != "from B again" {
		t.Fatalf("A has %q", n.Title)
	}
	// B pulls: A's version loses on B too (same rule), B notes a conflict.
	p.pullB(false)
	if n, _ := p.b.Note(ctx, "u1", "n1"); n.Title != "from B again" {
		t.Fatalf("B has %q", n.Title)
	}
	// A's version was replaced on A before it went out: only A has the
	// conflict, and B never saw "from A".
	ca, _ := p.a.SyncConflicts(ctx)
	cb, _ := p.b.SyncConflicts(ctx)
	if len(ca) != 1 || ca[0].Winner != "theirs" || ca[0].Mine["title"].V != "from A" || ca[0].Theirs["title"].V != "from B again" || ca[0].Category != SyncNotes {
		t.Fatalf("A's conflict: %+v", ca)
	}
	if len(cb) != 0 {
		t.Fatalf("B's conflicts: %+v", cb)
	}
	// The administrator on A keeps A's version: it becomes a new change both take.
	if _, err := p.a.ResolveSyncConflict(ctx, ca[0].ID, true); err != nil {
		t.Fatal(err)
	}
	p.pullB(false)
	p.pullA()
	for name, st := range map[string]*Store{"A": p.a, "B": p.b} {
		if n, _ := st.Note(ctx, "u1", "n1"); n.Title != "from A" {
			t.Fatalf("%s has %q after keeping A's", name, n.Title)
		}
	}
	if n, _ := p.a.CountSyncConflicts(ctx); n != 0 {
		t.Fatalf("A still has %d conflicts", n)
	}
	if _, err := p.a.ResolveSyncConflict(ctx, ca[0].ID, false); err != ErrNotFound {
		t.Fatalf("resolved twice: %v", err)
	}

	// The other order: B pulls A's change while B's own is still unread by
	// A. B's newer one stands on B, B keeps a conflict, and A takes B's.
	if _, err := p.a.UpdateNote(ctx, "u1", "n1", func(n *Note) bool { n.Title = "A2"; return true }); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"B2", "B3", "B4"} {
		if _, err := p.b.UpdateNote(ctx, "u1", "n1", func(n *Note) bool { n.Title = title; return true }); err != nil {
			t.Fatal(err)
		}
	}
	p.pullB(false)
	cb, _ = p.b.SyncConflicts(ctx)
	if len(cb) != 1 || cb[0].Winner != "mine" || cb[0].Theirs["title"].V != "A2" {
		t.Fatalf("B's conflict: %+v", cb)
	}
	p.pullA()
	for name, st := range map[string]*Store{"A": p.a, "B": p.b} {
		if n, _ := st.Note(ctx, "u1", "n1"); n.Title != "B4" {
			t.Fatalf("%s has %q", name, n.Title)
		}
	}
	// Keeping theirs on B brings A's "A2" back on both.
	if _, err := p.b.ResolveSyncConflict(ctx, cb[0].ID, false); err != nil {
		t.Fatal(err)
	}
	p.pullA()
	p.pullB(false)
	for name, st := range map[string]*Store{"A": p.a, "B": p.b} {
		if n, _ := st.Note(ctx, "u1", "n1"); n.Title != "A2" {
			t.Fatalf("%s has %q after keeping theirs", name, n.Title)
		}
	}
}

func TestSyncFirstSyncReplaces(t *testing.T) {
	p := newSyncPair(t)
	ctx := p.ctx
	mkUser(t, p.a, "u1", "ann")
	mkUser(t, p.b, "u2", "bob") // only on B: the primary's data wins
	_ = p.b.SetSetting(ctx, "storage.auto_scrub", "1")
	_ = p.b.SetSetting(ctx, "agents.enabled", "1")
	p.pullB(true)
	p.ready()
	if _, err := p.b.UserByID(ctx, "u2"); err != ErrNotFound {
		t.Fatalf("B's own user survived the first sync: %v", err)
	}
	if _, err := p.b.UserByID(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if v, _ := p.b.Setting(ctx, "storage.auto_scrub"); v != "1" {
		t.Fatal("B's own disk setting was removed")
	}
	if v, _ := p.b.Setting(ctx, "agents.enabled"); v != "" {
		t.Fatal("B's shared setting survived")
	}
	// Nothing B removed goes back to A as a change from B.
	if res := p.pullA(); res.Applied != 0 {
		t.Fatalf("echo after first sync: %+v", res)
	}
	if _, err := p.a.UserByID(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
}

func TestSyncUniqueClashIsAConflict(t *testing.T) {
	p := newSyncPair(t)
	ctx := p.ctx
	p.pullB(true)
	p.ready()
	mkUser(t, p.a, "u1", "sam")
	mkUser(t, p.b, "u9", "sam") // same name, different account
	res := p.pullB(false)
	if res.Failed != 1 || res.Conflicts != 1 {
		t.Fatalf("clash: %+v", res)
	}
	c, _ := p.b.SyncConflicts(ctx)
	if len(c) != 1 || c[0].Note == "" || c[0].Theirs["username"].V != "sam" {
		t.Fatalf("conflict: %+v", c)
	}
}

func TestSyncStatusCounts(t *testing.T) {
	st := openSyncStore(t)
	ctx := context.Background()
	acked, _ := st.SyncSeq(ctx)
	mkUser(t, st, "u1", "ann")
	_ = st.SetSetting(ctx, "notify.settings", "{}")
	cats, err := st.SyncStatus(ctx, acked)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]SyncCategoryStatus{}
	for _, c := range cats {
		by[c.ID] = c
	}
	if by[SyncAccounts].Queued != 1 || by[SyncSettings].Queued != 1 || by[SyncNotes].Queued != 0 || len(cats) != len(SyncCategories) {
		t.Fatalf("status: %+v", cats)
	}
	if n, err := st.DatabaseSize(ctx); err != nil || n <= 0 {
		t.Fatalf("size %d %v", n, err)
	}
}
