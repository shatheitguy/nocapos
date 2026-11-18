package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Remote Desktop's saved computers. Every query is scoped to the owner.

// RemoteConn is one saved computer. Settings is its options as JSON; the
// sealed passwords never leave the server.
type RemoteConn struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	Hostname    string `json:"hostname"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Domain      string `json:"domain"`
	Settings    string `json:"-"`
	PasswordEnc []byte `json:"-"`
	GatewayEnc  []byte `json:"-"`
	LastUsed    *int64 `json:"last_used,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

const remoteCols = `id, name, protocol, hostname, port, username, domain, settings, password_enc, gateway_password_enc, last_used, created_at, updated_at`

func scanRemote(row interface{ Scan(...any) error }) (*RemoteConn, error) {
	var (
		c    RemoteConn
		last sql.NullInt64
	)
	if err := row.Scan(&c.ID, &c.Name, &c.Protocol, &c.Hostname, &c.Port, &c.Username, &c.Domain, &c.Settings,
		&c.PasswordEnc, &c.GatewayEnc, &last, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	if last.Valid {
		v := last.Int64
		c.LastUsed = &v
	}
	return &c, nil
}

// ListRemoteConns returns a person's saved computers, by name.
func (s *Store) ListRemoteConns(ctx context.Context, userID string) ([]*RemoteConn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+remoteCols+` FROM remote_connections WHERE user_id = ? ORDER BY name COLLATE NOCASE, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*RemoteConn{}
	for rows.Next() {
		c, err := scanRemote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RemoteConn returns one of a person's saved computers (ErrNotFound for
// anyone else's).
func (s *Store) RemoteConn(ctx context.Context, userID, id string) (*RemoteConn, error) {
	c, err := scanRemote(s.db.QueryRowContext(ctx, `SELECT `+remoteCols+` FROM remote_connections WHERE user_id = ? AND id = ?`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// CreateRemoteConn saves a new computer, unless the person has max already.
func (s *Store) CreateRemoteConn(ctx context.Context, userID string, c *RemoteConn, max int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM remote_connections WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return err
	}
	if n >= max {
		return ErrLimit
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO remote_connections (`+remoteCols+`, user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Protocol, c.Hostname, c.Port, c.Username, c.Domain, c.Settings, c.PasswordEnc, c.GatewayEnc,
		nullInt(c.LastUsed), c.CreatedAt, c.UpdatedAt, userID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrConflict
		}
		return err
	}
	return tx.Commit()
}

// UpdateRemoteConn saves changes to one of a person's computers.
func (s *Store) UpdateRemoteConn(ctx context.Context, userID string, c *RemoteConn) error {
	res, err := s.db.ExecContext(ctx, `UPDATE remote_connections SET name = ?, protocol = ?, hostname = ?, port = ?, username = ?, domain = ?,
		settings = ?, password_enc = ?, gateway_password_enc = ?, updated_at = ? WHERE user_id = ? AND id = ?`,
		c.Name, c.Protocol, c.Hostname, c.Port, c.Username, c.Domain, c.Settings, c.PasswordEnc, c.GatewayEnc, c.UpdatedAt, userID, c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchRemoteConn records that a person just connected to a computer.
func (s *Store) TouchRemoteConn(ctx context.Context, userID, id string, at int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE remote_connections SET last_used = ? WHERE user_id = ? AND id = ?`, at, userID, id)
	return err
}

// DeleteRemoteConn removes one of a person's computers.
func (s *Store) DeleteRemoteConn(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM remote_connections WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
