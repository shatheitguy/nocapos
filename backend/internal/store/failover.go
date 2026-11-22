package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// FailoverPair is this server's failover pair with a linked server.
type FailoverPair struct {
	NocapID     string // the other server
	State       string // FailoverAsked | FailoverAsking | FailoverStarting | FailoverActive
	PrimaryID   string // whose data wins the first sync
	TheirCursor int64  // how far this server has read the other's change log
	Acked       int64  // how far the other has read this one's
	Paused      bool
	AskedBy     string
	Options     string // JSON
	CreatedAt   time.Time
	ActiveAt    time.Time
	LastPull    time.Time // last attempt
	LastOK      time.Time // last successful sync
	LastError   string
	Failures    int // in a row
}

// Failover pair states.
const (
	FailoverAsked    = "asked"    // this server asked the other
	FailoverAsking   = "asking"   // the other asked this one
	FailoverStarting = "starting" // the first sync runs
	FailoverActive   = "active"
)

// RoleLinkFailover: the two servers of a failover pair.
const RoleLinkFailover = "failover"

const failoverCols = `nocap_id, state, primary_id, their_cursor, acked, paused, asked_by, options, created_at, active_at, last_pull, last_ok, last_error, failures`

func scanFailover(row scanner) (*FailoverPair, error) {
	var (
		p                                 FailoverPair
		paused                            int
		created, active, lastPull, lastOK int64
	)
	if err := row.Scan(&p.NocapID, &p.State, &p.PrimaryID, &p.TheirCursor, &p.Acked, &paused, &p.AskedBy, &p.Options,
		&created, &active, &lastPull, &lastOK, &p.LastError, &p.Failures); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.Paused = paused != 0
	p.CreatedAt = time.Unix(created, 0).UTC()
	p.ActiveAt, p.LastPull, p.LastOK = unixOrZero(active), unixOrZero(lastPull), unixOrZero(lastOK)
	return &p, nil
}

// FailoverPairs lists this server's failover pairs (at most one in use).
func (s *Store) FailoverPairs(ctx context.Context) ([]*FailoverPair, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+failoverCols+` FROM failover_pairs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FailoverPair{}
	for rows.Next() {
		p, err := scanFailover(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// FailoverPair returns the pair with one server.
func (s *Store) FailoverPair(ctx context.Context, nocapID string) (*FailoverPair, error) {
	return scanFailover(s.db.QueryRowContext(ctx, `SELECT `+failoverCols+` FROM failover_pairs WHERE nocap_id = ?`, nocapID))
}

func unixOrNil(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// SaveFailoverPair adds a pair or replaces the one with the same server.
func (s *Store) SaveFailoverPair(ctx context.Context, p *FailoverPair) error {
	opts := p.Options
	if opts == "" {
		opts = "{}"
	}
	// An upsert, not a replace: columns added later keep their values, and
	// the cursors only move forward (a sync may have moved them meanwhile).
	_, err := s.db.ExecContext(ctx, `INSERT INTO failover_pairs (`+failoverCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (nocap_id) DO UPDATE SET state = excluded.state, primary_id = excluded.primary_id, their_cursor = MAX(their_cursor, excluded.their_cursor),
		acked = MAX(acked, excluded.acked), paused = excluded.paused, asked_by = excluded.asked_by, options = excluded.options, created_at = excluded.created_at,
		active_at = excluded.active_at, last_pull = excluded.last_pull, last_ok = excluded.last_ok, last_error = excluded.last_error, failures = excluded.failures`,
		p.NocapID, p.State, p.PrimaryID, p.TheirCursor, p.Acked, boolInt(p.Paused), p.AskedBy, opts,
		p.CreatedAt.Unix(), unixOrNil(p.ActiveAt), unixOrNil(p.LastPull), unixOrNil(p.LastOK), p.LastError, p.Failures)
	return err
}

// DeleteFailoverPair ends a pair (ErrNotFound when there is none).
func (s *Store) DeleteFailoverPair(ctx context.Context, nocapID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM failover_pairs WHERE nocap_id = ?`, nocapID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetFailoverAcked records how far the other server has read this one's
// log (it only moves forward).
func (s *Store) SetFailoverAcked(ctx context.Context, nocapID string, acked int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE failover_pairs SET acked = MAX(acked, ?) WHERE nocap_id = ?`, acked, nocapID)
	return err
}

// NoteFailoverSync records a sync attempt: with ok, how far this server has
// now read the other's log; without, what went wrong.
func (s *Store) NoteFailoverSync(ctx context.Context, nocapID string, ok bool, cursor int64, errText string, at time.Time) error {
	if ok {
		_, err := s.db.ExecContext(ctx, `UPDATE failover_pairs SET their_cursor = MAX(their_cursor, ?), last_pull = ?, last_ok = ?, last_error = '', failures = 0 WHERE nocap_id = ?`,
			cursor, at.Unix(), at.Unix(), nocapID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE failover_pairs SET last_pull = ?, last_error = ?, failures = failures + 1 WHERE nocap_id = ?`,
		at.Unix(), errText, nocapID)
	return err
}

// SetServerLinkRole says what a link is for (RoleLinkPeer, RoleLinkFailover).
func (s *Store) SetServerLinkRole(ctx context.Context, nocapID, role string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE server_links SET role = ? WHERE nocap_id = ?`, role, nocapID)
	return err
}
