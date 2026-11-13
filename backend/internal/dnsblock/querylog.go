package dnsblock

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// queryLog keeps the questions devices asked, on this machine only, in its
// own database file (so clearing it is just deleting that file's rows).
// Writes are batched; when the log is off nothing is written at all.
type queryLog struct {
	db *sql.DB
	in chan Query

	mu          sync.Mutex
	enabled     bool
	withClients bool
	keep        time.Duration
}

func openQueryLog(dir string) (*queryLog, error) {
	dsn := "file:" + filepath.Join(dir, "querylog.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS queries (
		t INTEGER NOT NULL, client TEXT NOT NULL, name TEXT NOT NULL, qtype TEXT NOT NULL,
		status TEXT NOT NULL, reason TEXT NOT NULL, upstream TEXT NOT NULL, ms INTEGER NOT NULL, rcode TEXT NOT NULL);
		CREATE INDEX IF NOT EXISTS queries_t ON queries (t);
		CREATE INDEX IF NOT EXISTS queries_client ON queries (client, t)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &queryLog{db: db, in: make(chan Query, 8192), keep: 24 * time.Hour}, nil
}

func (l *queryLog) configure(enabled, clients bool, hours int) {
	l.mu.Lock()
	l.enabled, l.withClients, l.keep = enabled, clients, time.Duration(hours)*time.Hour
	l.mu.Unlock()
}

func (l *queryLog) settings() (bool, bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enabled, l.withClients, l.keep
}

// add queues q (dropped when the log is off or the queue is full).
func (l *queryLog) add(q Query) {
	on, clients, _ := l.settings()
	if !on {
		return
	}
	if !clients {
		q.Client = ""
	}
	select {
	case l.in <- q:
	default:
	}
}

// run writes queued queries every second and prunes old ones.
func (l *queryLog) run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	prune := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	defer prune.Stop()
	var batch []Query
	flush := func() {
		if len(batch) == 0 {
			return
		}
		tx, err := l.db.Begin()
		if err == nil {
			st, err := tx.Prepare(`INSERT INTO queries (t, client, name, qtype, status, reason, upstream, ms, rcode) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
			if err == nil {
				for _, q := range batch {
					_, _ = st.Exec(q.Time.UnixMilli(), q.Client, q.Name, q.Type, q.Status, q.Reason, q.Upstream, q.Ms, q.RCode)
				}
				st.Close()
				_ = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
		batch = batch[:0]
	}
	l.pruneOld()
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case q := <-l.in:
			batch = append(batch, q)
			if len(batch) >= 500 {
				flush()
			}
		case <-tick.C:
			flush()
		case <-prune.C:
			l.pruneOld()
		}
	}
}

func (l *queryLog) pruneOld() {
	_, _, keep := l.settings()
	_, _ = l.db.Exec(`DELETE FROM queries WHERE t < ?`, time.Now().Add(-keep).UnixMilli())
}

// Clear deletes the whole log.
func (l *queryLog) clear() error {
	_, err := l.db.Exec(`DELETE FROM queries`)
	if err == nil {
		_, _ = l.db.Exec(`VACUUM`)
	}
	return err
}

// LogFilter picks which log entries to show.
type LogFilter struct {
	Client string
	Search string
	Status string // "", blocked, forwarded, cached, local, error
	Before time.Time
	Limit  int
}

func (l *queryLog) list(ctx context.Context, f LogFilter) ([]Query, error) {
	q := `SELECT t, client, name, qtype, status, reason, upstream, ms, rcode FROM queries WHERE 1=1`
	var args []any
	if f.Client != "" {
		q += ` AND client = ?`
		args = append(args, f.Client)
	}
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, f.Status)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		s = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(s))
		q += ` AND name LIKE ? ESCAPE '\'`
		args = append(args, "%"+s+"%")
	}
	if !f.Before.IsZero() {
		q += ` AND t < ?`
		args = append(args, f.Before.UnixMilli())
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	q += ` ORDER BY t DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Query{}
	for rows.Next() {
		var x Query
		var t int64
		if err := rows.Scan(&t, &x.Client, &x.Name, &x.Type, &x.Status, &x.Reason, &x.Upstream, &x.Ms, &x.RCode); err != nil {
			return nil, err
		}
		x.Time = time.UnixMilli(t)
		out = append(out, x)
	}
	return out, rows.Err()
}

// Count is a name (domain or device) and how often it came up.
type Count struct {
	Name    string `json:"name"`
	Count   int    `json:"count"`
	Blocked int    `json:"blocked,omitempty"`
}

// ClientStat is one device in the per-device view.
type ClientStat struct {
	Client   string    `json:"client"`
	Total    int       `json:"total"`
	Blocked  int       `json:"blocked"`
	LastSeen time.Time `json:"last_seen"`
}

func (l *queryLog) totals(ctx context.Context, since time.Time) (total, blocked int, err error) {
	var b sql.NullInt64
	err = l.db.QueryRowContext(ctx, `SELECT COUNT(*), SUM(status = 'blocked') FROM queries WHERE t >= ?`, since.UnixMilli()).Scan(&total, &b)
	return total, int(b.Int64), err
}

func (l *queryLog) top(ctx context.Context, col string, since time.Time, onlyBlocked bool, n int) ([]Count, error) {
	if col != "name" && col != "client" {
		return nil, nil
	}
	q := `SELECT ` + col + `, COUNT(*) c, SUM(status = 'blocked') FROM queries WHERE t >= ? AND ` + col + ` != ''`
	if onlyBlocked {
		q += ` AND status = 'blocked'`
	}
	q += ` GROUP BY ` + col + ` ORDER BY c DESC LIMIT ?`
	rows, err := l.db.QueryContext(ctx, q, since.UnixMilli(), n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Count{}
	for rows.Next() {
		var c Count
		var b sql.NullInt64
		if err := rows.Scan(&c.Name, &c.Count, &b); err != nil {
			return nil, err
		}
		c.Blocked = int(b.Int64)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (l *queryLog) clients(ctx context.Context, since time.Time) ([]ClientStat, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT client, COUNT(*), SUM(status = 'blocked'), MAX(t) FROM queries
		WHERE t >= ? AND client != '' GROUP BY client ORDER BY COUNT(*) DESC LIMIT 200`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientStat{}
	for rows.Next() {
		var c ClientStat
		var b sql.NullInt64
		var last int64
		if err := rows.Scan(&c.Client, &c.Total, &b, &last); err != nil {
			return nil, err
		}
		c.Blocked, c.LastSeen = int(b.Int64), time.UnixMilli(last)
		out = append(out, c)
	}
	return out, rows.Err()
}
