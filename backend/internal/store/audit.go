package store

import (
	"context"
	"database/sql"
	"time"
)

type AuditEntry struct {
	UserID  string
	Action  string
	Target  string
	IP      string
	Success bool
	Detail  string
}

func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	var uid sql.NullString
	if e.UserID != "" {
		uid = sql.NullString{String: e.UserID, Valid: true}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (ts, user_id, action, target, ip, success, detail) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		time.Now().Unix(), uid, e.Action, e.Target, e.IP, boolInt(e.Success), e.Detail)
	return err
}

// AuditRetention is how long audit entries are kept.
const AuditRetention = 90 * 24 * time.Hour

// PurgeAudit deletes audit entries older than before.
func (s *Store) PurgeAudit(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE ts < ?`, before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
