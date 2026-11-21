package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ServerLink is another NoCap server linked to this one (Settings → Your
// Squad). TheirToken is what this server presents to the other one (sealed
// when Sealed); OurTokenHash is the hash of what the other presents here.
type ServerLink struct {
	NocapID      string
	Name         string
	URL          string
	Fingerprint  string
	TheirToken   []byte
	Sealed       bool
	OurTokenHash string
	Version      string
	Role         string // what the link is for: RoleLinkPeer for now (failover later)
	LinkedBy     string
	CreatedAt    time.Time
	LastSeen     time.Time // zero = never
}

// RoleLinkPeer: two servers in the same squad.
const RoleLinkPeer = "peer"

const serverLinkCols = `nocap_id, name, url, fingerprint, their_token, sealed, our_token_hash, version, role, linked_by, created_at, last_seen`

func scanServerLink(row scanner) (*ServerLink, error) {
	var (
		l             ServerLink
		sealed        int
		created, seen int64
	)
	if err := row.Scan(&l.NocapID, &l.Name, &l.URL, &l.Fingerprint, &l.TheirToken, &sealed, &l.OurTokenHash, &l.Version, &l.Role, &l.LinkedBy, &created, &seen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	l.Sealed = sealed != 0
	l.CreatedAt = time.Unix(created, 0).UTC()
	l.LastSeen = unixOrZero(seen)
	return &l, nil
}

// ListServerLinks returns every linked server, by name.
func (s *Store) ListServerLinks(ctx context.Context) ([]*ServerLink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serverLinkCols+` FROM server_links ORDER BY name COLLATE NOCASE, nocap_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ServerLink{}
	for rows.Next() {
		l, err := scanServerLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ServerLink returns the link with one server.
func (s *Store) ServerLink(ctx context.Context, nocapID string) (*ServerLink, error) {
	return scanServerLink(s.db.QueryRowContext(ctx, `SELECT `+serverLinkCols+` FROM server_links WHERE nocap_id = ?`, nocapID))
}

// ServerLinkByToken finds the server that presents the token with this hash.
func (s *Store) ServerLinkByToken(ctx context.Context, tokenHash string) (*ServerLink, error) {
	return scanServerLink(s.db.QueryRowContext(ctx, `SELECT `+serverLinkCols+` FROM server_links WHERE our_token_hash = ?`, tokenHash))
}

// SaveServerLink adds a link, or replaces the one with the same server.
func (s *Store) SaveServerLink(ctx context.Context, l *ServerLink) error {
	sealed := 0
	if l.Sealed {
		sealed = 1
	}
	var seen int64
	if !l.LastSeen.IsZero() {
		seen = l.LastSeen.Unix()
	}
	role := l.Role
	if role == "" {
		role = RoleLinkPeer
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO server_links (`+serverLinkCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(nocap_id) DO UPDATE SET name = excluded.name, url = excluded.url, fingerprint = excluded.fingerprint,
		   their_token = excluded.their_token, sealed = excluded.sealed, our_token_hash = excluded.our_token_hash,
		   version = excluded.version, role = excluded.role, linked_by = excluded.linked_by, created_at = excluded.created_at, last_seen = excluded.last_seen`,
		l.NocapID, l.Name, l.URL, l.Fingerprint, l.TheirToken, sealed, l.OurTokenHash, l.Version, role, l.LinkedBy, l.CreatedAt.Unix(), seen)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// TouchServerLink records that a linked server answered, with what it says
// about itself now.
func (s *Store) TouchServerLink(ctx context.Context, nocapID, name, version string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE server_links SET name = ?, version = ?, last_seen = ? WHERE nocap_id = ?`, name, version, at.Unix(), nocapID)
	return err
}

// DeleteServerLink removes the link with one server (ErrNotFound when there
// is none).
func (s *Store) DeleteServerLink(ctx context.Context, nocapID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM server_links WHERE nocap_id = ?`, nocapID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
