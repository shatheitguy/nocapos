package store

import (
	"context"
	"time"
)

// Session kinds: how a login session signed in.
const (
	SessionWeb     = "web"     // a browser
	SessionPhone   = "phone"   // the NoCap OS app (a linked phone, or a web sign-in it opened)
	SessionDesktop = "desktop" // NoCap Desktop on this machine's own screen
	SessionCLI     = "cli"     // a script or command-line tool
)

// SessionInfo describes one login session (a refresh token family).
type SessionInfo struct {
	ID         string // the refresh token family
	UserID     string
	Kind       string
	DeviceID   string // the linked device that opened it, if any
	DeviceKey  string // kind + browser + OS: "the same device" for new-device checks
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// ActiveSession is a live login session with its details.
type ActiveSession struct {
	SessionInfo
	Username string
}

// SaveSessionInfo records a new session's details.
func (s *Store) SaveSessionInfo(ctx context.Context, si *SessionInfo) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO session_info (id, user_id, kind, device_id, device_key, user_agent, ip, created_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET user_agent = excluded.user_agent, ip = excluded.ip, last_seen_at = excluded.last_seen_at`,
		si.ID, si.UserID, si.Kind, si.DeviceID, si.DeviceKey, si.UserAgent, si.IP, si.CreatedAt.Unix(), si.LastSeenAt.Unix())
	return err
}

// TouchSession records that a session was active at at, and from where
// (ip and userAgent are kept as they were when empty).
func (s *Store) TouchSession(ctx context.Context, id, ip, userAgent string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE session_info SET last_seen_at = MAX(last_seen_at, ?),
		   ip = CASE WHEN ? = '' THEN ip ELSE ? END,
		   user_agent = CASE WHEN ? = '' THEN user_agent ELSE ? END
		 WHERE id = ?`,
		at.Unix(), ip, ip, userAgent, userAgent, id)
	return err
}

// KnownDevice reports whether userID signed in before (in a session other
// than except) from a device with this key, and whether they have any
// recorded sign-ins at all.
func (s *Store) KnownDevice(ctx context.Context, userID, deviceKey, except string) (known, any bool, err error) {
	var k, a int
	err = s.db.QueryRowContext(ctx,
		`SELECT
		   EXISTS (SELECT 1 FROM session_info WHERE user_id = ? AND device_key = ? AND id <> ?),
		   EXISTS (SELECT 1 FROM session_info WHERE user_id = ? AND id <> ?)`,
		userID, deviceKey, except, userID, except).Scan(&k, &a)
	return k == 1, a == 1, err
}

// activeSessionsSQL lists live sessions: families with an unrevoked,
// unexpired refresh token. Sessions from before details were recorded fall
// back to what their refresh tokens know.
const activeSessionsSQL = `
SELECT r.family_id, r.user_id, COALESCE(u.username, ''),
       COALESCE(si.kind, ''), COALESCE(si.device_id, ''), COALESCE(si.device_key, ''),
       COALESCE(NULLIF(si.user_agent, ''), r.user_agent), COALESCE(NULLIF(si.ip, ''), r.ip),
       COALESCE(si.created_at, (SELECT MIN(f.created_at) FROM refresh_tokens f WHERE f.family_id = r.family_id)),
       MAX(COALESCE(si.last_seen_at, 0), r.created_at)
FROM refresh_tokens r
LEFT JOIN users u ON u.id = r.user_id
LEFT JOIN session_info si ON si.id = r.family_id
WHERE r.revoked_at IS NULL AND r.expires_at > ?`

func (s *Store) querySessions(ctx context.Context, where string, args ...any) ([]ActiveSession, error) {
	rows, err := s.db.QueryContext(ctx, activeSessionsSQL+where+` ORDER BY 10 DESC, r.family_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActiveSession{}
	seen := map[string]bool{}
	for rows.Next() {
		var (
			a             ActiveSession
			created, last int64
		)
		if err := rows.Scan(&a.ID, &a.UserID, &a.Username, &a.Kind, &a.DeviceID, &a.DeviceKey,
			&a.UserAgent, &a.IP, &created, &last); err != nil {
			return nil, err
		}
		if seen[a.ID] { // a rotation race can leave two live tokens for a moment
			continue
		}
		seen[a.ID] = true
		a.CreatedAt = time.Unix(created, 0).UTC()
		a.LastSeenAt = time.Unix(last, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActiveSessions lists live login sessions, most recently active first:
// one person's, or everyone's when userID is empty.
func (s *Store) ActiveSessions(ctx context.Context, userID string, now time.Time) ([]ActiveSession, error) {
	if userID == "" {
		return s.querySessions(ctx, "", now.Unix())
	}
	return s.querySessions(ctx, ` AND r.user_id = ?`, now.Unix(), userID)
}

// ActiveSession finds one live login session.
func (s *Store) ActiveSession(ctx context.Context, id string, now time.Time) (*ActiveSession, error) {
	list, err := s.querySessions(ctx, ` AND r.family_id = ?`, now.Unix(), id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// RevokeUserSessionsExcept ends every login session of userID but keep, and
// returns the IDs it ended.
func (s *Store) RevokeUserSessionsExcept(ctx context.Context, userID, keep string, now time.Time) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT family_id FROM refresh_tokens
		 WHERE user_id = ? AND family_id <> ? AND revoked_at IS NULL AND expires_at > ?`,
		userID, keep, now.Unix())
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked_at = ? WHERE user_id = ? AND family_id <> ? AND revoked_at IS NULL`,
		now.Unix(), userID, keep); err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}

// SessionInfoRetention: how long a session's details are kept after it was
// last active (for "is this a new device?").
const SessionInfoRetention = 90 * 24 * time.Hour

// PurgeSessionInfo forgets sessions last active before before that have no
// live refresh token left.
func (s *Store) PurgeSessionInfo(ctx context.Context, before, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM session_info WHERE last_seen_at < ? AND NOT EXISTS (
		   SELECT 1 FROM refresh_tokens r WHERE r.family_id = session_info.id AND r.revoked_at IS NULL AND r.expires_at > ?)`,
		before.Unix(), now.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
