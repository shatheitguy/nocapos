package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// InstalledApp is an app installed from the App Store catalog.
type InstalledApp struct {
	ID          string
	Version     string
	WebPort     int
	Secrets     map[string]string
	InstalledAt time.Time
	UpdatedAt   time.Time
}

func (s *Store) InstalledApps(ctx context.Context) (map[string]*InstalledApp, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, version, web_port, secrets, installed_at, updated_at FROM apps`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*InstalledApp{}
	for rows.Next() {
		var a InstalledApp
		var secrets string
		var inst, upd int64
		if err := rows.Scan(&a.ID, &a.Version, &a.WebPort, &secrets, &inst, &upd); err != nil {
			return nil, err
		}
		a.Secrets = map[string]string{}
		_ = json.Unmarshal([]byte(secrets), &a.Secrets)
		a.InstalledAt, a.UpdatedAt = time.Unix(inst, 0).UTC(), time.Unix(upd, 0).UTC()
		out[a.ID] = &a
	}
	return out, rows.Err()
}

func (s *Store) InstalledApp(ctx context.Context, id string) (*InstalledApp, error) {
	all, err := s.InstalledApps(ctx)
	if err != nil {
		return nil, err
	}
	if a, ok := all[id]; ok {
		return a, nil
	}
	return nil, ErrNotFound
}

// SaveInstalledApp inserts or updates an installed app.
func (s *Store) SaveInstalledApp(ctx context.Context, a *InstalledApp) error {
	secrets, err := json.Marshal(a.Secrets)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO apps (id, version, web_port, secrets, installed_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET version = excluded.version, web_port = excluded.web_port,
		   secrets = excluded.secrets, updated_at = excluded.updated_at`,
		a.ID, a.Version, a.WebPort, string(secrets), now, now)
	return err
}

func (s *Store) DeleteInstalledApp(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// AppOverrides returns the saved container changes of an app, by service.
func (s *Store) AppOverrides(ctx context.Context, appID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT service, spec FROM app_overrides WHERE app_id = ?`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var svc, spec string
		if err := rows.Scan(&svc, &spec); err != nil {
			return nil, err
		}
		out[svc] = spec
	}
	return out, rows.Err()
}

// SaveAppOverride stores the changes made to one of an app's containers.
func (s *Store) SaveAppOverride(ctx context.Context, appID, service, spec string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_overrides (app_id, service, spec, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(app_id, service) DO UPDATE SET spec = excluded.spec, updated_at = excluded.updated_at`, appID, service, spec, time.Now().Unix())
	return err
}

// DeleteAppOverrides forgets an app's container changes.
func (s *Store) DeleteAppOverrides(ctx context.Context, appID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_overrides WHERE app_id = ?`, appID)
	return err
}
