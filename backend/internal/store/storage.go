package store

import (
	"context"
	"time"
)

// StorageLocation is a pool or disk shown in Files.
type StorageLocation struct {
	ID        string
	Kind      string // pool | disk
	Target    string // pool name or disk label
	Name      string
	Path      string
	CreatedAt time.Time
}

func (s *Store) StorageLocations(ctx context.Context) ([]StorageLocation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, target, name, path, created_at FROM storage_locations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StorageLocation
	for rows.Next() {
		var l StorageLocation
		var created int64
		if err := rows.Scan(&l.ID, &l.Kind, &l.Target, &l.Name, &l.Path, &created); err != nil {
			return nil, err
		}
		l.CreatedAt = unixOrZero(created)
		out = append(out, l)
	}
	return out, rows.Err()
}

// SaveStorageLocation adds a location or updates the one with that id.
func (s *Store) SaveStorageLocation(ctx context.Context, l StorageLocation) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO storage_locations (id, kind, target, name, path, created_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, target = excluded.target, name = excluded.name, path = excluded.path`,
		l.ID, l.Kind, l.Target, l.Name, l.Path, time.Now().Unix())
	return err
}

func (s *Store) DeleteStorageLocation(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM storage_locations WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
