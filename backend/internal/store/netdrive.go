package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NetMount is a network share NoCapOS mounts. Password is sealed.
type NetMount struct {
	ID        int64
	Name      string
	Kind      string
	Host      string
	Share     string
	Username  string
	Password  []byte
	Auto      bool
	CreatedAt time.Time
}

func (s *Store) NetMounts(ctx context.Context) ([]*NetMount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, host, share, username, password, auto, created_at FROM net_mounts ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NetMount
	for rows.Next() {
		var m NetMount
		var auto int
		var created int64
		if err := rows.Scan(&m.ID, &m.Name, &m.Kind, &m.Host, &m.Share, &m.Username, &m.Password, &auto, &created); err != nil {
			return nil, err
		}
		m.Auto, m.CreatedAt = auto != 0, unixOrZero(created)
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (s *Store) NetMount(ctx context.Context, id int64) (*NetMount, error) {
	all, err := s.NetMounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range all {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) CreateNetMount(ctx context.Context, m *NetMount) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO net_mounts (name, kind, host, share, username, password, auto, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Name, m.Kind, m.Host, m.Share, m.Username, m.Password, boolInt(m.Auto), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SetNetMountAuto(ctx context.Context, id int64, auto bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE net_mounts SET auto = ? WHERE id = ?`, boolInt(auto), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteNetMount(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM net_mounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// NetShare is a folder NoCapOS shares over SMB and WebDAV.
type NetShare struct {
	ID        int64
	Name      string
	Root      string
	Path      string
	ReadOnly  bool
	CreatedAt time.Time
}

func (s *Store) NetShares(ctx context.Context) ([]*NetShare, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, root, path, read_only, created_at FROM net_shares ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NetShare
	for rows.Next() {
		var sh NetShare
		var ro int
		var created int64
		if err := rows.Scan(&sh.ID, &sh.Name, &sh.Root, &sh.Path, &ro, &created); err != nil {
			return nil, err
		}
		sh.ReadOnly, sh.CreatedAt = ro != 0, unixOrZero(created)
		out = append(out, &sh)
	}
	return out, rows.Err()
}

// ErrExists means a row with that name already exists.
var ErrExists = errors.New("already exists")

func (s *Store) CreateNetShare(ctx context.Context, sh *NetShare) (int64, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM net_shares WHERE name = ? COLLATE NOCASE`, sh.Name).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, ErrExists
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO net_shares (name, root, path, read_only, created_at) VALUES (?, ?, ?, ?, ?)`,
		sh.Name, sh.Root, sh.Path, boolInt(sh.ReadOnly), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) SetNetShareReadOnly(ctx context.Context, id int64, ro bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE net_shares SET read_only = ? WHERE id = ?`, boolInt(ro), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteNetShare(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM net_shares WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Setting reads a server-wide setting ("" when unset).
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO server_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
