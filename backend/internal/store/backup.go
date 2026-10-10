package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// BackupRepo is a restic repository. Config is JSON owned by the backup
// package; Password and Secret are sealed.
type BackupRepo struct {
	ID        int64
	Name      string
	Kind      string
	Config    string
	Password  []byte
	Secret    []byte
	LastPrune time.Time
	CreatedAt time.Time
}

// BackupPlan is what to back up, where, when and for how long. Sources,
// Schedule and Keep are JSON owned by the backup package.
type BackupPlan struct {
	ID           int64
	RepoID       int64
	Name         string
	Sources      string
	Schedule     string
	Keep         string
	Enabled      bool
	LastRun      time.Time
	LastStatus   string
	LastError    string
	LastSnapshot string
	LastBytes    int64
	CreatedAt    time.Time
}

func unixOrZero(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}

func (s *Store) BackupRepos(ctx context.Context) ([]*BackupRepo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, config, password, secret, last_prune, created_at FROM backup_repos ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BackupRepo
	for rows.Next() {
		var r BackupRepo
		var prune, created int64
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &r.Config, &r.Password, &r.Secret, &prune, &created); err != nil {
			return nil, err
		}
		r.LastPrune, r.CreatedAt = unixOrZero(prune), unixOrZero(created)
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *Store) BackupRepo(ctx context.Context, id int64) (*BackupRepo, error) {
	all, err := s.BackupRepos(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range all {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) CreateBackupRepo(ctx context.Context, r *BackupRepo) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO backup_repos (name, kind, config, password, secret, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		r.Name, r.Kind, r.Config, r.Password, r.Secret, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SetBackupRepoPruned(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backup_repos SET last_prune = ? WHERE id = ?`, at.Unix(), id)
	return err
}

// ErrInUse means a destination still has backup plans.
var ErrInUse = errors.New("still used by a backup plan")

func (s *Store) DeleteBackupRepo(ctx context.Context, id int64) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_plans WHERE repo_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrInUse
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM backup_repos WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNotFound
	}
	return nil
}

const planCols = `id, repo_id, name, sources, schedule, keep, enabled, last_run, last_status, last_error, last_snapshot, last_bytes, created_at`

func scanPlan(sc interface{ Scan(...any) error }) (*BackupPlan, error) {
	var p BackupPlan
	var enabled int
	var last, created int64
	if err := sc.Scan(&p.ID, &p.RepoID, &p.Name, &p.Sources, &p.Schedule, &p.Keep, &enabled, &last, &p.LastStatus, &p.LastError, &p.LastSnapshot, &p.LastBytes, &created); err != nil {
		return nil, err
	}
	p.Enabled, p.LastRun, p.CreatedAt = enabled != 0, unixOrZero(last), unixOrZero(created)
	return &p, nil
}

func (s *Store) BackupPlans(ctx context.Context) ([]*BackupPlan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+planCols+` FROM backup_plans ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BackupPlan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) BackupPlan(ctx context.Context, id int64) (*BackupPlan, error) {
	p, err := scanPlan(s.db.QueryRowContext(ctx, `SELECT `+planCols+` FROM backup_plans WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// SaveBackupPlan creates the plan (ID 0) or updates its settings.
func (s *Store) SaveBackupPlan(ctx context.Context, p *BackupPlan) (int64, error) {
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	if p.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO backup_plans (repo_id, name, sources, schedule, keep, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			p.RepoID, p.Name, p.Sources, p.Schedule, p.Keep, enabled, time.Now().Unix())
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	res, err := s.db.ExecContext(ctx, `UPDATE backup_plans SET repo_id = ?, name = ?, sources = ?, schedule = ?, keep = ?, enabled = ? WHERE id = ?`,
		p.RepoID, p.Name, p.Sources, p.Schedule, p.Keep, enabled, p.ID)
	if err != nil {
		return 0, err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return 0, ErrNotFound
	}
	return p.ID, nil
}

// SetBackupPlanResult records how the last run went.
func (s *Store) SetBackupPlanResult(ctx context.Context, id int64, at time.Time, status, errMsg, snapshot string, added int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backup_plans SET last_run = ?, last_status = ?, last_error = ?, last_snapshot = CASE WHEN ? = '' THEN last_snapshot ELSE ? END, last_bytes = ? WHERE id = ?`,
		at.Unix(), status, errMsg, snapshot, snapshot, added, id)
	return err
}

func (s *Store) DeleteBackupPlan(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM backup_plans WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNotFound
	}
	return nil
}
