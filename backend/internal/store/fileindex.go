package store

import (
	"context"
	"database/sql"
	"errors"

	"alfaos/alfad/internal/filesync"
)

// FileIndex is a failover pair's index of synced files (filesync.Index).
type FileIndex struct{ s *Store }

// FileIndex returns the store's file index.
func (s *Store) FileIndex() *FileIndex { return &FileIndex{s: s} }

const fileCols = `path, size, mtime, mode, hash, deleted, seq, lamport, origin`

func scanFileEntry(row scanner) (filesync.Entry, error) {
	var e filesync.Entry
	var del int
	err := row.Scan(&e.Path, &e.Size, &e.MTime, &e.Mode, &e.Hash, &del, &e.Seq, &e.Lamport, &e.Origin)
	e.Deleted = del != 0
	return e, err
}

func (x *FileIndex) Files(ctx context.Context, loc string) (map[string]filesync.Entry, error) {
	rows, err := x.s.db.QueryContext(ctx, `SELECT `+fileCols+` FROM file_index WHERE loc = ?`, loc)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]filesync.Entry{}
	for rows.Next() {
		e, err := scanFileEntry(rows)
		if err != nil {
			return nil, err
		}
		out[e.Path] = e
	}
	return out, rows.Err()
}

func (x *FileIndex) put(ctx context.Context, loc string, e filesync.Entry, local bool) error {
	tx, err := x.s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if local {
		_, err = tx.ExecContext(ctx, `UPDATE file_clock SET seq = seq + 1, lamport = lamport + 1 WHERE id = 1`)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE file_clock SET seq = seq + 1, lamport = MAX(lamport, ?) WHERE id = 1`, e.Lamport)
	}
	if err != nil {
		return err
	}
	var seq, lamport int64
	if err := tx.QueryRowContext(ctx, `SELECT seq, lamport FROM file_clock WHERE id = 1`).Scan(&seq, &lamport); err != nil {
		return err
	}
	origin := e.Origin
	if local {
		e.Lamport, origin = lamport, ""
	}
	if e.Deleted {
		e.Size, e.Hash = 0, ""
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO file_index (loc, `+fileCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		loc, e.Path, e.Size, e.MTime, e.Mode, e.Hash, boolInt(e.Deleted), seq, e.Lamport, origin); err != nil {
		return err
	}
	return tx.Commit()
}

func (x *FileIndex) PutLocal(ctx context.Context, loc string, e filesync.Entry) error {
	return x.put(ctx, loc, e, true)
}

func (x *FileIndex) PutRemote(ctx context.Context, loc string, e filesync.Entry) error {
	return x.put(ctx, loc, e, false)
}

func (x *FileIndex) Touch(ctx context.Context, loc, path string, size, mtime int64) error {
	_, err := x.s.db.ExecContext(ctx, `UPDATE file_index SET size = ?, mtime = ? WHERE loc = ? AND path = ?`, size, mtime, loc, path)
	return err
}

func (x *FileIndex) PathWithHash(ctx context.Context, loc, hash string) (string, bool, error) {
	var p string
	err := x.s.db.QueryRowContext(ctx, `SELECT path FROM file_index WHERE loc = ? AND hash = ? AND deleted = 0 LIMIT 1`, loc, hash).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return p, err == nil, err
}

// Entry is one file's version ("" origin: this server's).
func (x *FileIndex) Entry(ctx context.Context, loc, path string) (*filesync.Entry, error) {
	e, err := scanFileEntry(x.s.db.QueryRowContext(ctx, `SELECT `+fileCols+` FROM file_index WHERE loc = ? AND path = ?`, loc, path))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// Changes reads the index after since for the other server: only the
// locations it syncs, without what it wrote itself (exclude), at most
// limit entries. self fills in this server's own origin.
func (x *FileIndex) Changes(ctx context.Context, since int64, locs []string, self, exclude string, limit int) ([]filesync.Change, int64, bool, error) {
	if limit <= 0 {
		limit = 1000
	}
	want := map[string]bool{}
	for _, l := range locs {
		want[l] = true
	}
	rows, err := x.s.db.QueryContext(ctx, `SELECT loc, `+fileCols+` FROM file_index WHERE seq > ? ORDER BY seq LIMIT ?`, since, limit+1)
	if err != nil {
		return nil, since, false, err
	}
	defer rows.Close()
	out := []filesync.Change{}
	next, n, more := since, 0, false
	for rows.Next() {
		if n == limit {
			more = true
			break
		}
		n++
		var c filesync.Change
		var del int
		if err := rows.Scan(&c.Loc, &c.Path, &c.Size, &c.MTime, &c.Mode, &c.Hash, &del, &c.Seq, &c.Lamport, &c.Origin); err != nil {
			return nil, since, false, err
		}
		c.Deleted = del != 0
		next = c.Seq
		if c.Origin == "" {
			c.Origin = self
		}
		if c.Origin == exclude || !want[c.Loc] {
			continue
		}
		out = append(out, c)
	}
	return out, next, more, rows.Err()
}

// FileQueue counts this server's file changes the other one hasn't read
// (after acked), and their size, per location.
func (x *FileIndex) FileQueue(ctx context.Context, acked int64) (map[string][2]int64, error) {
	rows, err := x.s.db.QueryContext(ctx, `SELECT loc, COUNT(*), COALESCE(SUM(size), 0) FROM file_index WHERE origin = '' AND seq > ? GROUP BY loc`, acked)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var loc string
		var n, size int64
		if err := rows.Scan(&loc, &n, &size); err != nil {
			return nil, err
		}
		out[loc] = [2]int64{n, size}
	}
	return out, rows.Err()
}

// FileSeq is how far this server's file index has got.
func (x *FileIndex) FileSeq(ctx context.Context) (int64, error) {
	var n int64
	err := x.s.db.QueryRowContext(ctx, `SELECT seq FROM file_clock WHERE id = 1`).Scan(&n)
	return n, err
}

// ForgetLocation drops a location's index (it no longer syncs).
func (x *FileIndex) ForgetLocation(ctx context.Context, loc string) error {
	_, err := x.s.db.ExecContext(ctx, `DELETE FROM file_index WHERE loc = ?`, loc)
	return err
}

// FailoverFileCursors: how far this server has read the other's file index
// (cursor), and the other this one's (acked).
func (s *Store) FailoverFileCursors(ctx context.Context, nocapID string) (cursor, acked int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT file_cursor, file_acked FROM failover_pairs WHERE nocap_id = ?`, nocapID).Scan(&cursor, &acked)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// SetFailoverFileCursor records how far this server has read the other's file index.
func (s *Store) SetFailoverFileCursor(ctx context.Context, nocapID string, cursor int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE failover_pairs SET file_cursor = MAX(file_cursor, ?) WHERE nocap_id = ?`, cursor, nocapID)
	return err
}

// SetFailoverFileAcked records how far the other server has read this one's (only forward).
func (s *Store) SetFailoverFileAcked(ctx context.Context, nocapID string, acked int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE failover_pairs SET file_acked = MAX(file_acked, ?) WHERE nocap_id = ?`, acked, nocapID)
	return err
}

// SetFailoverOptions saves a pair's options (JSON).
func (s *Store) SetFailoverOptions(ctx context.Context, nocapID, options string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE failover_pairs SET options = ? WHERE nocap_id = ?`, options, nocapID)
	return err
}

// ClearFileIndex forgets every location (failover turned off: a new pair
// starts from what's on disk).
func (x *FileIndex) ClearFileIndex(ctx context.Context) error {
	_, err := x.s.db.ExecContext(ctx, `DELETE FROM file_index`)
	return err
}
