package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"alfaos/alfad/internal/notes"
)

// Sticky Notes. Every query is scoped to the note's owner: there is no way to
// read someone else's notes through the store, administrators included.

// ErrLimit is returned when a person already has as many notes as allowed.
var ErrLimit = errors.New("limit reached")

// Note is one sticky note. Body is the structured document as JSON.
type Note struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Body      string      `json:"-"`
	Text      string      `json:"text"`
	Color     string      `json:"color"`
	Pinned    bool        `json:"pinned"`
	Archived  bool        `json:"archived"`
	TrashedAt *int64      `json:"trashed_at,omitempty"`
	RemindAt  *int64      `json:"remind_at,omitempty"`
	Reminded  bool        `json:"reminded"`
	Desk      *notes.Desk `json:"desk,omitempty"`
	CreatedAt int64       `json:"created_at"`
	UpdatedAt int64       `json:"updated_at"`
	Version   int64       `json:"version"`
}

// MarshalJSON writes the body as a JSON value, not a string.
func (n Note) MarshalJSON() ([]byte, error) {
	type plain Note
	body := json.RawMessage(n.Body)
	if !json.Valid(body) {
		body = json.RawMessage("[]")
	}
	return json.Marshal(struct {
		plain
		Body json.RawMessage `json:"body"`
	}{plain(n), body})
}

const noteCols = `id, title, body, text, color, pinned, archived, trashed_at, remind_at, reminded, desk, created_at, updated_at, version`

func scanNote(row interface{ Scan(...any) error }) (*Note, error) {
	var (
		n                  Note
		pinned, arch, remd int
		trashed, remind    sql.NullInt64
		desk               sql.NullString
	)
	if err := row.Scan(&n.ID, &n.Title, &n.Body, &n.Text, &n.Color, &pinned, &arch, &trashed, &remind, &remd, &desk, &n.CreatedAt, &n.UpdatedAt, &n.Version); err != nil {
		return nil, err
	}
	n.Pinned, n.Archived, n.Reminded = pinned != 0, arch != 0, remd != 0
	if trashed.Valid {
		v := trashed.Int64
		n.TrashedAt = &v
	}
	if remind.Valid {
		v := remind.Int64
		n.RemindAt = &v
	}
	if desk.Valid && desk.String != "" {
		var d notes.Desk
		if json.Unmarshal([]byte(desk.String), &d) == nil {
			n.Desk = &d
		}
	}
	return &n, nil
}

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func deskJSON(d *notes.Desk) sql.NullString {
	if d == nil {
		return sql.NullString{}
	}
	b, _ := json.Marshal(d)
	return sql.NullString{String: string(b), Valid: true}
}

// NoteFilter picks which notes a list returns.
type NoteFilter string

const (
	NotesAll      NoteFilter = ""         // everything, trash included
	NotesActive   NoteFilter = "active"   // not archived, not in the trash
	NotesArchived NoteFilter = "archived" // archived, not in the trash
	NotesTrash    NoteFilter = "trash"
)

// ListNotes returns a person's notes, most recently edited first.
func (s *Store) ListNotes(ctx context.Context, userID string, f NoteFilter) ([]*Note, error) {
	q := `SELECT ` + noteCols + ` FROM notes WHERE user_id = ?`
	switch f {
	case NotesActive:
		q += ` AND archived = 0 AND trashed_at IS NULL`
	case NotesArchived:
		q += ` AND archived = 1 AND trashed_at IS NULL`
	case NotesTrash:
		q += ` AND trashed_at IS NOT NULL`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY updated_at DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Note returns one of a person's notes (ErrNotFound for anyone else's).
func (s *Store) Note(ctx context.Context, userID, id string) (*Note, error) {
	n, err := scanNote(s.db.QueryRowContext(ctx, `SELECT `+noteCols+` FROM notes WHERE user_id = ? AND id = ?`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// CountNotes counts a person's notes, trash included.
func (s *Store) CountNotes(ctx context.Context, userID string) (int, error) {
	var c int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes WHERE user_id = ?`, userID).Scan(&c)
	return c, err
}

// CreateNote saves a new note for userID, unless they already have max notes.
func (s *Store) CreateNote(ctx context.Context, userID string, n *Note, max int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var c int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes WHERE user_id = ?`, userID).Scan(&c); err != nil {
		return err
	}
	if c >= max {
		return ErrLimit
	}
	if n.Version == 0 {
		n.Version = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notes (`+noteCols+`, user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Title, n.Body, n.Text, n.Color, boolInt(n.Pinned), boolInt(n.Archived), nullInt(n.TrashedAt), nullInt(n.RemindAt),
		boolInt(n.Reminded), deskJSON(n.Desk), n.CreatedAt, n.UpdatedAt, n.Version, userID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrConflict
		}
		return err
	}
	return tx.Commit()
}

// UpdateNote reads a person's note, lets edit change it and saves it with the
// next version. edit returns false to leave it as it was.
func (s *Store) UpdateNote(ctx context.Context, userID, id string, edit func(*Note) bool) (*Note, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	n, err := scanNote(tx.QueryRowContext(ctx, `SELECT `+noteCols+` FROM notes WHERE user_id = ? AND id = ?`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !edit(n) {
		return n, nil
	}
	n.Version++
	if _, err := tx.ExecContext(ctx, `UPDATE notes SET title = ?, body = ?, text = ?, color = ?, pinned = ?, archived = ?, trashed_at = ?,
		remind_at = ?, reminded = ?, desk = ?, updated_at = ?, version = ? WHERE user_id = ? AND id = ?`,
		n.Title, n.Body, n.Text, n.Color, boolInt(n.Pinned), boolInt(n.Archived), nullInt(n.TrashedAt), nullInt(n.RemindAt),
		boolInt(n.Reminded), deskJSON(n.Desk), n.UpdatedAt, n.Version, userID, id); err != nil {
		return nil, err
	}
	return n, tx.Commit()
}

// DeleteNote removes one of a person's notes for good.
func (s *Store) DeleteNote(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return ErrNotFound
	}
	return nil
}

// EmptyNotesTrash deletes a person's trashed notes and returns their IDs.
func (s *Store) EmptyNotesTrash(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `DELETE FROM notes WHERE user_id = ? AND trashed_at IS NOT NULL RETURNING id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PurgeNotesTrash deletes everyone's notes trashed before beforeMs, and
// returns the IDs gone per person.
func (s *Store) PurgeNotesTrash(ctx context.Context, beforeMs int64) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `DELETE FROM notes WHERE trashed_at IS NOT NULL AND trashed_at < ? RETURNING user_id, id`, beforeMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var uid, id string
		if err := rows.Scan(&uid, &id); err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], id)
	}
	return out, rows.Err()
}

// NoteDeskPos is one note's spot on the web desktop (nil: not stuck there).
type NoteDeskPos struct {
	ID   string
	Desk *notes.Desk
}

// SetNoteDesks moves a person's notes on the desktop. Moving isn't editing:
// updated_at stays, the version moves on so other screens follow. It returns
// the notes that were found.
func (s *Store) SetNoteDesks(ctx context.Context, userID string, pos []NoteDeskPos) ([]*Note, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := []*Note{}
	for _, p := range pos {
		n, err := scanNote(tx.QueryRowContext(ctx, `UPDATE notes SET desk = ?, version = version + 1 WHERE user_id = ? AND id = ? RETURNING `+noteCols,
			deskJSON(p.Desk), userID, p.ID))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, tx.Commit()
}

// DueReminder is a note whose reminder just came due.
type DueReminder struct {
	UserID string
	Note   *Note
}

// ClaimDueReminders marks every reminder due at nowMs as fired and returns
// them, each exactly once (notes in the trash don't remind).
func (s *Store) ClaimDueReminders(ctx context.Context, nowMs int64) ([]DueReminder, error) {
	rows, err := s.db.QueryContext(ctx, `UPDATE notes SET reminded = 1, version = version + 1
		WHERE remind_at IS NOT NULL AND remind_at <= ? AND reminded = 0 AND trashed_at IS NULL
		RETURNING user_id, `+noteCols, nowMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueReminder
	for rows.Next() {
		var uid string
		n, err := scanNote(scanPrefix{rows, &uid})
		if err != nil {
			return nil, err
		}
		out = append(out, DueReminder{UserID: uid, Note: n})
	}
	return out, rows.Err()
}

// scanPrefix scans one extra leading column before a note's.
type scanPrefix struct {
	rows  *sql.Rows
	first any
}

func (p scanPrefix) Scan(dest ...any) error { return p.rows.Scan(append([]any{p.first}, dest...)...) }
