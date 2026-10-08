package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Script is a saved shell script for the Quick Script Launcher.
type Script struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Body        string     `json:"body"`
	Confirm     bool       `json:"confirm"` // ask before running
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
	LastExit    *int       `json:"last_exit,omitempty"`
}

const scriptCols = `id, name, description, body, confirm, created_at, updated_at, last_run_at, last_exit`

func scanScript(row scanner) (*Script, error) {
	var (
		sc                Script
		confirm           int
		created, updated  int64
		lastRun, lastExit sql.NullInt64
	)
	if err := row.Scan(&sc.ID, &sc.Name, &sc.Description, &sc.Body, &confirm, &created, &updated, &lastRun, &lastExit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	sc.Confirm = confirm != 0
	sc.CreatedAt = time.Unix(created, 0).UTC()
	sc.UpdatedAt = time.Unix(updated, 0).UTC()
	if lastRun.Valid {
		t := time.Unix(lastRun.Int64, 0).UTC()
		sc.LastRunAt = &t
	}
	if lastExit.Valid {
		v := int(lastExit.Int64)
		sc.LastExit = &v
	}
	return &sc, nil
}

func (s *Store) ListScripts(ctx context.Context) ([]Script, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scriptCols+` FROM scripts ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Script{}
	for rows.Next() {
		sc, err := scanScript(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, rows.Err()
}

func (s *Store) GetScript(ctx context.Context, id string) (*Script, error) {
	return scanScript(s.db.QueryRowContext(ctx, `SELECT `+scriptCols+` FROM scripts WHERE id = ?`, id))
}

func (s *Store) CreateScript(ctx context.Context, sc *Script) error {
	now := time.Now().UTC().Truncate(time.Second)
	sc.ID, sc.CreatedAt, sc.UpdatedAt = NewID(), now, now
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scripts (id, name, description, body, confirm, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sc.ID, sc.Name, sc.Description, sc.Body, boolInt(sc.Confirm), now.Unix(), now.Unix())
	return err
}

func (s *Store) UpdateScript(ctx context.Context, sc *Script) error {
	return s.updateUser(ctx,
		`UPDATE scripts SET name = ?, description = ?, body = ?, confirm = ?, updated_at = ? WHERE id = ?`,
		sc.Name, sc.Description, sc.Body, boolInt(sc.Confirm), time.Now().Unix(), sc.ID)
}

func (s *Store) DeleteScript(ctx context.Context, id string) error {
	return s.updateUser(ctx, `DELETE FROM scripts WHERE id = ?`, id)
}

// SetScriptResult records when a script last ran and how it exited.
func (s *Store) SetScriptResult(ctx context.Context, id string, at time.Time, exit int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE scripts SET last_run_at = ?, last_exit = ? WHERE id = ?`, at.Unix(), exit, id)
	return err
}
