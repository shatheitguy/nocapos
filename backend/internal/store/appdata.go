package store

import (
	"context"
	"time"
)

// OldVolume is a Docker volume whose files were moved into the App data
// folder, kept until an admin cleans it up.
type OldVolume struct {
	Volume  string    `json:"volume"`
	App     string    `json:"app"`
	Dir     string    `json:"dir"`
	MovedAt time.Time `json:"moved_at"`
}

func (s *Store) OldVolumes(ctx context.Context) ([]OldVolume, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT volume, app, dir, moved_at FROM appdata_old_volumes ORDER BY app, volume`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OldVolume{}
	for rows.Next() {
		var v OldVolume
		var at int64
		if err := rows.Scan(&v.Volume, &v.App, &v.Dir, &at); err != nil {
			return nil, err
		}
		v.MovedAt = time.Unix(at, 0).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) SaveOldVolume(ctx context.Context, v OldVolume) error {
	if v.MovedAt.IsZero() {
		v.MovedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO appdata_old_volumes (volume, app, dir, moved_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(volume) DO UPDATE SET app = excluded.app, dir = excluded.dir, moved_at = excluded.moved_at`,
		v.Volume, v.App, v.Dir, v.MovedAt.Unix())
	return err
}

func (s *Store) DeleteOldVolume(ctx context.Context, volume string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM appdata_old_volumes WHERE volume = ?`, volume)
	return err
}
