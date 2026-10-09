package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CloudAccount is a connected cloud account; Config is sealed JSON.
type CloudAccount struct {
	ID        int64
	Name      string
	Kind      string
	Config    []byte
	CreatedAt time.Time
}

func (s *Store) CloudAccounts(ctx context.Context) ([]*CloudAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, config, created_at FROM cloud_accounts ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CloudAccount
	for rows.Next() {
		var a CloudAccount
		var created int64
		if err := rows.Scan(&a.ID, &a.Name, &a.Kind, &a.Config, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = unixOrZero(created)
		out = append(out, &a)
	}
	return out, rows.Err()
}

func (s *Store) CloudAccount(ctx context.Context, id int64) (*CloudAccount, error) {
	var a CloudAccount
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, name, kind, config, created_at FROM cloud_accounts WHERE id = ?`, id).Scan(&a.ID, &a.Name, &a.Kind, &a.Config, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	a.CreatedAt = unixOrZero(created)
	return &a, err
}

func (s *Store) CreateCloudAccount(ctx context.Context, a *CloudAccount) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO cloud_accounts (name, kind, config, created_at) VALUES (?, ?, ?, ?)`, a.Name, a.Kind, a.Config, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetCloudAccountConfig saves refreshed settings (e.g. a renewed token).
func (s *Store) SetCloudAccountConfig(ctx context.Context, id int64, config []byte) error {
	_, err := s.db.ExecContext(ctx, `UPDATE cloud_accounts SET config = ? WHERE id = ?`, config, id)
	return err
}

func (s *Store) DeleteCloudAccount(ctx context.Context, id int64) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cloud_imports WHERE account_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrInUse
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM cloud_accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNotFound
	}
	return nil
}

// CloudImport copies a folder from a cloud account into a storage location.
type CloudImport struct {
	ID         int64
	AccountID  int64
	Name       string
	Source     string
	DestRoot   string
	DestPath   string
	Schedule   string
	LastRun    time.Time
	LastStatus string
	LastError  string
	LastBytes  int64
	LastFiles  int64
	CreatedAt  time.Time
}

const importCols = `id, account_id, name, source, dest_root, dest_path, schedule, last_run, last_status, last_error, last_bytes, last_files, created_at`

func scanImport(sc interface{ Scan(...any) error }) (*CloudImport, error) {
	var c CloudImport
	var last, created int64
	if err := sc.Scan(&c.ID, &c.AccountID, &c.Name, &c.Source, &c.DestRoot, &c.DestPath, &c.Schedule, &last, &c.LastStatus, &c.LastError, &c.LastBytes, &c.LastFiles, &created); err != nil {
		return nil, err
	}
	c.LastRun, c.CreatedAt = unixOrZero(last), unixOrZero(created)
	return &c, nil
}

func (s *Store) CloudImports(ctx context.Context) ([]*CloudImport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+importCols+` FROM cloud_imports ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CloudImport
	for rows.Next() {
		c, err := scanImport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CloudImport(ctx context.Context, id int64) (*CloudImport, error) {
	c, err := scanImport(s.db.QueryRowContext(ctx, `SELECT `+importCols+` FROM cloud_imports WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// SaveCloudImport creates (ID 0) or updates an import's settings.
func (s *Store) SaveCloudImport(ctx context.Context, c *CloudImport) (int64, error) {
	if c.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO cloud_imports (account_id, name, source, dest_root, dest_path, schedule, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			c.AccountID, c.Name, c.Source, c.DestRoot, c.DestPath, c.Schedule, time.Now().Unix())
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	res, err := s.db.ExecContext(ctx, `UPDATE cloud_imports SET account_id = ?, name = ?, source = ?, dest_root = ?, dest_path = ?, schedule = ? WHERE id = ?`,
		c.AccountID, c.Name, c.Source, c.DestRoot, c.DestPath, c.Schedule, c.ID)
	if err != nil {
		return 0, err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return 0, ErrNotFound
	}
	return c.ID, nil
}

func (s *Store) SetCloudImportResult(ctx context.Context, id int64, at time.Time, status, errMsg string, bytes, files int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE cloud_imports SET last_run = ?, last_status = ?, last_error = ?, last_bytes = ?, last_files = ? WHERE id = ?`,
		at.Unix(), status, errMsg, bytes, files, id)
	return err
}

func (s *Store) DeleteCloudImport(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM cloud_imports WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNotFound
	}
	return nil
}
