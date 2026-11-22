package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Replication between the two servers of a failover pair.
//
// Every table in SyncTables has three triggers that note each insert,
// update and delete in sync_rows (one row per record, the latest version).
// The other server reads this log in seq order (SyncChanges), and applies
// what it reads (ApplySyncChanges): last writer wins by Lamport clock, ties
// go to the higher NoCap ID, and a record both servers changed before they
// synced is kept in sync_conflicts for the administrator.
//
// Columns each server keeps for itself (Ignore) neither start a change nor
// travel; rows each server keeps for itself (Skip) are left out entirely.
// Sealed columns are opened by the sender, travel inside the link's TLS,
// and are sealed again with the receiver's own key.

// SyncTable is a table both servers of a failover pair keep the same.
type SyncTable struct {
	Name     string
	Category string
	Ignore   []string // columns each server keeps for itself
	Sealed   []string // columns sealed with the server's own key
	// Skip is an SQL condition for rows each server keeps for itself;
	// "{r}" stands for the row ("NEW.", "OLD." or "").
	Skip string
}

// Sync categories, as the Failover page shows them.
const (
	SyncAccounts = "accounts"
	SyncSharing  = "sharing"
	SyncApps     = "apps"
	SyncBackups  = "backups"
	SyncSettings = "settings"
	SyncNotes    = "notes"
	SyncDevices  = "devices"
)

// SyncCategories in the order they are shown.
var SyncCategories = []string{SyncAccounts, SyncSharing, SyncApps, SyncBackups, SyncSettings, SyncNotes, SyncDevices}

// localSettings are server_settings each server keeps for itself: its own
// addresses, disks, data folder, boot and set-up state.
const localSettings = `{r}key LIKE 'link.%' OR {r}key LIKE 'storage.%' OR {r}key LIKE 'failover.%' OR ` +
	`{r}key IN ('appdata.root', 'appdata.old_root', 'host.boot_id', 'setup.machine_done')`

// SyncTables are the tables a failover pair shares, parents before children.
var SyncTables = []SyncTable{
	{Name: "users", Category: SyncAccounts, Sealed: []string{"totp_secret"}},
	{Name: "user_avatars", Category: SyncAccounts},
	{Name: "user_groups", Category: SyncAccounts},
	{Name: "user_group_members", Category: SyncAccounts},
	{Name: "user_access", Category: SyncAccounts},
	{Name: "user_prefs", Category: SyncAccounts},
	{Name: "device_limits", Category: SyncAccounts},
	{Name: "clipboard_settings", Category: SyncAccounts},
	{Name: "file_favorites", Category: SyncAccounts},
	{Name: "shared_folders", Category: SyncSharing},
	{Name: "share_permissions", Category: SyncSharing},
	{Name: "app_access_mode", Category: SyncSharing},
	{Name: "app_access", Category: SyncSharing},
	{Name: "net_mounts", Category: SyncSharing, Sealed: []string{"password"}},
	{Name: "apps", Category: SyncApps},
	{Name: "app_overrides", Category: SyncApps},
	{Name: "backup_repos", Category: SyncBackups, Sealed: []string{"password", "secret"}, Ignore: []string{"last_prune"}},
	{Name: "backup_plans", Category: SyncBackups, Ignore: []string{"last_run", "last_status", "last_error", "last_snapshot", "last_bytes"}},
	{Name: "cloud_accounts", Category: SyncBackups, Sealed: []string{"config"}},
	{Name: "cloud_imports", Category: SyncBackups, Ignore: []string{"last_run", "last_status", "last_error", "last_bytes", "last_files"}},
	{Name: "server_settings", Category: SyncSettings, Skip: localSettings},
	{Name: "scripts", Category: SyncSettings, Ignore: []string{"last_run_at", "last_exit"}},
	{Name: "ai_providers", Category: SyncSettings, Sealed: []string{"api_key_enc"}},
	{Name: "ai_user_settings", Category: SyncSettings},
	{Name: "ai_memories", Category: SyncSettings},
	{Name: "remote_connections", Category: SyncSettings, Sealed: []string{"password_enc", "gateway_password_enc"}, Ignore: []string{"last_used"}},
	{Name: "notes", Category: SyncNotes},
	{Name: "devices", Category: SyncDevices, Ignore: []string{"battery", "last_seen_at", "last_ip", "app_version"}},
	{Name: "agent_tokens", Category: SyncDevices, Ignore: []string{"last_used_at"}},
}

var syncTableByName = func() map[string]*SyncTable {
	m := map[string]*SyncTable{}
	for i := range SyncTables {
		m[SyncTables[i].Name] = &SyncTables[i]
	}
	return m
}()

// SyncTableInfo is a shared table as this server has it.
func SyncTableInfo(name string) (SyncTable, bool) {
	t, ok := syncTableByName[name]
	if !ok {
		return SyncTable{}, false
	}
	return *t, true
}

func (t *SyncTable) skip(prefix string) string {
	if t.Skip == "" {
		return ""
	}
	return strings.ReplaceAll(t.Skip, "{r}", prefix)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// tableShape: a table's columns and primary key, as this database has them.
type tableShape struct {
	cols []string // every column, in order
	pk   []string // primary key columns, in key order
	sync []string // columns that travel (cols minus Ignore)
}

type shapeCache struct {
	mu sync.Mutex
	m  map[string]*tableShape
}

var shapes = shapeCache{m: map[string]*tableShape{}}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Store) shape(ctx context.Context, q queryer, t *SyncTable) (*tableShape, error) {
	key := fmt.Sprintf("%p/%s", s, t.Name)
	shapes.mu.Lock()
	sh := shapes.m[key]
	shapes.mu.Unlock()
	if sh != nil {
		return sh, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT name, pk FROM pragma_table_info(?)`, t.Name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sh = &tableShape{}
	type pkCol struct {
		name string
		n    int
	}
	var pks []pkCol
	for rows.Next() {
		var name string
		var pk int
		if err := rows.Scan(&name, &pk); err != nil {
			return nil, err
		}
		sh.cols = append(sh.cols, name)
		if pk > 0 {
			pks = append(pks, pkCol{name, pk})
		}
		if !contains(t.Ignore, name) {
			sh.sync = append(sh.sync, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(sh.cols) == 0 {
		return nil, nil // no such table here
	}
	sort.Slice(pks, func(i, j int) bool { return pks[i].n < pks[j].n })
	for _, p := range pks {
		sh.pk = append(sh.pk, p.name)
	}
	if len(sh.pk) == 0 {
		return nil, fmt.Errorf("sync: %s has no primary key", t.Name)
	}
	shapes.mu.Lock()
	shapes.m[key] = sh
	shapes.mu.Unlock()
	return sh, nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func quoteList(cols []string, prefix string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = prefix + quoteIdent(c)
	}
	return strings.Join(out, ", ")
}

func pkJSON(sh *tableShape, prefix string) string {
	return "json_array(" + quoteList(sh.pk, prefix) + ")"
}

func sqlString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// logStmt notes one record in the change log from inside a trigger.
func logStmt(t *SyncTable, sh *tableShape, prefix, where string) string {
	cond := "c.id = 1 AND x.id = 1"
	if where != "" {
		cond += " AND " + where
	}
	// A trigger's own conflict clause gives way to the outer statement's
	// (an upsert or INSERT OR IGNORE), so the old version is deleted first.
	del := `DELETE FROM sync_rows WHERE tbl = ` + sqlString(t.Name) + ` AND pk = ` + pkJSON(sh, prefix)
	if where != "" {
		del += " AND " + where
	}
	return `UPDATE sync_clock SET seq = seq + 1, lamport = lamport + (SELECT origin = '' FROM sync_ctx WHERE id = 1) WHERE id = 1;
  ` + del + `;
  INSERT INTO sync_rows (tbl, pk, seq, lamport, origin, at)
    SELECT ` + sqlString(t.Name) + `, ` + pkJSON(sh, prefix) + `, c.seq, CASE WHEN x.origin = '' THEN c.lamport ELSE x.lamport END, x.origin, CAST(strftime('%s', 'now') AS INTEGER)
    FROM sync_clock c, sync_ctx x WHERE ` + cond + `;`
}

func triggerName(table, kind string) string { return quoteIdent("nocap_sync_" + table + "_" + kind) }

// prepareSync (re)creates the change log's triggers and notes records that
// existed before them. It runs at every start, after the migrations.
func (s *Store) prepareSync(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := range SyncTables {
		t := &SyncTables[i]
		sh, err := s.shape(ctx, tx, t)
		if err != nil {
			return err
		}
		for _, kind := range []string{"i", "u", "d"} {
			if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+triggerName(t.Name, kind)); err != nil {
				return err
			}
		}
		if sh == nil {
			continue
		}
		whenNew, whenOld := "", ""
		if t.Skip != "" {
			whenNew = " WHEN NOT (" + t.skip("NEW.") + ")"
			whenOld = " WHEN NOT (" + t.skip("OLD.") + ")"
		}
		of := ""
		if len(t.Ignore) > 0 {
			of = " OF " + quoteList(sh.sync, "")
		}
		stmts := []string{
			`CREATE TRIGGER ` + triggerName(t.Name, "i") + ` AFTER INSERT ON ` + quoteIdent(t.Name) + whenNew + ` BEGIN
  ` + logStmt(t, sh, "NEW.", "") + `
END`,
			`CREATE TRIGGER ` + triggerName(t.Name, "u") + ` AFTER UPDATE` + of + ` ON ` + quoteIdent(t.Name) + whenNew + ` BEGIN
  ` + logStmt(t, sh, "NEW.", "") + `
  ` + logStmt(t, sh, "OLD.", pkJSON(sh, "OLD.")+" != "+pkJSON(sh, "NEW.")) + `
END`,
			`CREATE TRIGGER ` + triggerName(t.Name, "d") + ` AFTER DELETE ON ` + quoteIdent(t.Name) + whenOld + ` BEGIN
  ` + logStmt(t, sh, "OLD.", "") + `
END`,
		}
		for _, q := range stmts {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("sync trigger on %s: %w", t.Name, err)
			}
		}
		// Records from before the triggers: noted once, as written here.
		if err := seedSyncRows(ctx, tx, t, sh); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func seedSyncRows(ctx context.Context, tx *sql.Tx, t *SyncTable, sh *tableShape) error {
	where := ""
	if t.Skip != "" {
		where = " WHERE NOT (" + t.skip("") + ")"
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+pkJSON(sh, "")+` FROM `+quoteIdent(t.Name)+where+
		` EXCEPT SELECT pk FROM sync_rows WHERE tbl = ?`, t.Name)
	if err != nil {
		return err
	}
	var missing []string
	for rows.Next() {
		var pk string
		if err := rows.Scan(&pk); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, pk)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(missing) == 0 {
		return err
	}
	now := time.Now().Unix()
	for _, pk := range missing {
		if _, err := tx.ExecContext(ctx, `UPDATE sync_clock SET seq = seq + 1, lamport = lamport + 1 WHERE id = 1`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO sync_rows (tbl, pk, seq, lamport, origin, at)
			SELECT ?, ?, seq, lamport, '', ? FROM sync_clock WHERE id = 1`, t.Name, pk, now); err != nil {
			return err
		}
	}
	return nil
}

// ---------- values on the wire ----------

// SyncVal is one column's value: t is n (NULL), i (integer), f (real),
// s (text), b (bytes, base64) or x (a secret, opened: only ever inside the
// link's TLS, never stored).
type SyncVal struct {
	T string `json:"t"`
	V string `json:"v,omitempty"`
}

// SyncRow is a record's values by column.
type SyncRow map[string]SyncVal

func encodeVal(v any) SyncVal {
	switch x := v.(type) {
	case nil:
		return SyncVal{T: "n"}
	case int64:
		return SyncVal{T: "i", V: strconv.FormatInt(x, 10)}
	case int:
		return SyncVal{T: "i", V: strconv.Itoa(x)}
	case float64:
		return SyncVal{T: "f", V: strconv.FormatFloat(x, 'g', -1, 64)}
	case bool:
		if x {
			return SyncVal{T: "i", V: "1"}
		}
		return SyncVal{T: "i", V: "0"}
	case string:
		return SyncVal{T: "s", V: x}
	case []byte:
		return SyncVal{T: "b", V: base64.StdEncoding.EncodeToString(x)}
	case time.Time:
		return SyncVal{T: "s", V: x.Format(time.RFC3339Nano)}
	}
	return SyncVal{T: "s", V: fmt.Sprint(v)}
}

// decodeVal turns a value back into what the database takes; secrets are
// sealed with seal (this server's key).
func decodeVal(v SyncVal, seal func(string) []byte) (any, error) {
	switch v.T {
	case "n":
		return nil, nil
	case "i":
		return strconv.ParseInt(v.V, 10, 64)
	case "f":
		return strconv.ParseFloat(v.V, 64)
	case "s":
		return v.V, nil
	case "b":
		return base64.StdEncoding.DecodeString(v.V)
	case "x":
		if seal == nil {
			return []byte(v.V), nil
		}
		return seal(v.V), nil
	}
	return nil, fmt.Errorf("sync: unknown value type %q", v.T)
}

func decodePK(pk string) ([]any, error) {
	dec := json.NewDecoder(strings.NewReader(pk))
	dec.UseNumber()
	var raw []any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("sync: bad key %q", pk)
	}
	for i, v := range raw {
		if n, ok := v.(json.Number); ok {
			if iv, err := n.Int64(); err == nil {
				raw[i] = iv
			} else if fv, err := n.Float64(); err == nil {
				raw[i] = fv
			}
		}
	}
	return raw, nil
}

func pkWhere(sh *tableShape) string {
	parts := make([]string, len(sh.pk))
	for i, c := range sh.pk {
		parts[i] = quoteIdent(c) + " = ?"
	}
	return strings.Join(parts, " AND ")
}

// readRow reads a record's travelling columns (nil when it's gone). With
// open, sealed columns are opened into secrets (x); without, they stay
// sealed bytes.
func readRow(ctx context.Context, q queryer, t *SyncTable, sh *tableShape, pk []any, open func([]byte) (string, error)) (SyncRow, error) {
	if len(pk) != len(sh.pk) {
		return nil, fmt.Errorf("sync: key of %s has %d parts, want %d", t.Name, len(pk), len(sh.pk))
	}
	rows, err := q.QueryContext(ctx, `SELECT `+quoteList(sh.sync, "")+` FROM `+quoteIdent(t.Name)+` WHERE `+pkWhere(sh), pk...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	vals := make([]any, len(sh.sync))
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	row := SyncRow{}
	for i, c := range sh.sync {
		v := vals[i]
		if open != nil && contains(t.Sealed, c) {
			b, ok := v.([]byte)
			if !ok || len(b) == 0 {
				row[c] = encodeVal(v)
				continue
			}
			plain, err := open(b)
			if err != nil {
				continue // can't open it: the other server keeps its own
			}
			row[c] = SyncVal{T: "x", V: plain}
			continue
		}
		row[c] = encodeVal(v)
	}
	return row, nil
}

// ---------- reading the log ----------

// SyncChange is one record's latest version. Row nil: deleted.
type SyncChange struct {
	Table   string  `json:"table"`
	PK      string  `json:"pk"`
	Seq     int64   `json:"seq"`
	Lamport int64   `json:"lamport"`
	Origin  string  `json:"origin"` // the server that wrote it
	Row     SyncRow `json:"row,omitempty"`
}

// SyncFeed is a page of the change log.
type SyncFeed struct {
	Changes []SyncChange `json:"changes"`
	Next    int64        `json:"next"` // read from here next time
	More    bool         `json:"more"`
	Schema  int          `json:"schema"` // database version: both servers must match
	// Seen: how far the sender has read the reader's log. The reader's
	// changes up to there aren't news to the sender (no conflict).
	Seen int64 `json:"seen"`
}

// SchemaVersion is the last migration applied.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	return int(v.Int64), err
}

// SyncSeq is how far this server's change log has got.
func (s *Store) SyncSeq(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT seq FROM sync_clock WHERE id = 1`).Scan(&n)
	return n, err
}

// SyncChanges reads the change log after since for another server: self is
// this server's NoCap ID, and records that server wrote (exclude) are left
// out. At most limit records and about maxBytes of values per page.
func (s *Store) SyncChanges(ctx context.Context, since int64, self, exclude string, limit, maxBytes int, open func([]byte) (string, error)) (*SyncFeed, error) {
	if limit <= 0 {
		limit = 500
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	feed := &SyncFeed{Next: since, Changes: []SyncChange{}}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&feed.Schema); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT tbl, pk, seq, lamport, origin FROM sync_rows WHERE seq > ? ORDER BY seq LIMIT ?`, since, limit+1)
	if err != nil {
		return nil, err
	}
	var log []SyncChange
	for rows.Next() {
		var c SyncChange
		if err := rows.Scan(&c.Table, &c.PK, &c.Seq, &c.Lamport, &c.Origin); err != nil {
			rows.Close()
			return nil, err
		}
		log = append(log, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(log) > limit {
		log, feed.More = log[:limit], true
	}
	size := 0
	for i, c := range log {
		if size > maxBytes && maxBytes > 0 && i > 0 {
			feed.More = true
			break
		}
		feed.Next = c.Seq
		if c.Origin == "" {
			c.Origin = self
		}
		if c.Origin == exclude {
			continue
		}
		t := syncTableByName[c.Table]
		if t == nil {
			continue
		}
		sh, err := s.shape(ctx, tx, t)
		if err != nil || sh == nil {
			continue
		}
		pk, err := decodePK(c.PK)
		if err != nil {
			continue
		}
		if c.Row, err = readRow(ctx, tx, t, sh, pk, open); err != nil {
			return nil, err
		}
		for _, v := range c.Row {
			size += len(v.V) + 16
		}
		feed.Changes = append(feed.Changes, c)
	}
	return feed, nil
}

// ---------- applying the other server's changes ----------

// SyncApply says whose changes these are and how to take them.
type SyncApply struct {
	Self  string // this server's NoCap ID
	Peer  string // the other server's
	Acked int64  // how far the other has read this server's log: newer local changes conflict
	// Force: the first sync. The other server's data wins everything, and
	// shared records it doesn't have are removed here.
	Force bool
	Seal  func(string) []byte // seals secrets with this server's key
}

// SyncResult is what applying changed.
type SyncResult struct {
	Applied   int
	Conflicts int
	Failed    int
	Tables    map[string]bool // tables that changed
	Errors    []string
}

// wins: does version a (lamport, origin) beat b.
func wins(aL int64, aOrigin string, bL int64, bOrigin string) bool {
	if aL != bL {
		return aL > bL
	}
	return aOrigin > bOrigin
}

type localVersion struct {
	seq, lamport int64
	origin       string
}

// ApplySyncChanges applies changes read from the other server, in one
// transaction (foreign keys are checked at the end, so the order of
// parents and children inside it doesn't matter).
func (s *Store) ApplySyncChanges(ctx context.Context, changes []SyncChange, o SyncApply) (*SyncResult, error) {
	res := &SyncResult{Tables: map[string]bool{}}
	if o.Peer == "" || o.Self == "" {
		return nil, errors.New("sync: both servers' IDs are needed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return nil, err
	}
	var maxL int64
	now := time.Now().Unix()
	for _, c := range changes {
		maxL = max(maxL, c.Lamport)
	}
	if o.Force {
		// Shared records only this server has go first, so the other's can't
		// clash with them (an account with the same name): its data wins.
		seen := map[string]map[string]bool{} // table → keys the other server has
		for _, c := range changes {
			if c.Row != nil {
				if seen[c.Table] == nil {
					seen[c.Table] = map[string]bool{}
				}
				seen[c.Table][c.PK] = true
			}
		}
		for i := len(SyncTables) - 1; i >= 0; i-- {
			t := &SyncTables[i]
			sh, err := s.shape(ctx, tx, t)
			if err != nil {
				return nil, err
			}
			if sh == nil {
				continue
			}
			n, err := s.removeUnseen(ctx, tx, t, sh, seen[t.Name], o.Peer, maxL, now)
			if err != nil {
				return nil, err
			}
			if n > 0 {
				res.Applied += n
				res.Tables[t.Name] = true
			}
		}
	}
	for _, c := range changes {
		t := syncTableByName[c.Table]
		if t == nil {
			continue
		}
		sh, err := s.shape(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		if sh == nil {
			continue
		}
		pk, err := decodePK(c.PK)
		if err != nil || len(pk) != len(sh.pk) {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("%s: bad key %s", c.Table, c.PK))
			continue
		}
		var lv *localVersion
		var v localVersion
		switch err := tx.QueryRowContext(ctx, `SELECT seq, lamport, origin FROM sync_rows WHERE tbl = ? AND pk = ?`, c.Table, c.PK).Scan(&v.seq, &v.lamport, &v.origin); {
		case err == nil:
			lv = &v
		case !errors.Is(err, sql.ErrNoRows):
			return nil, err
		}
		if !o.Force && lv != nil {
			localOrigin := lv.origin
			if localOrigin == "" {
				localOrigin = o.Self
			}
			unsent := lv.origin == "" && lv.seq > o.Acked
			theirsWin := wins(c.Lamport, o.Peer, lv.lamport, localOrigin)
			if !theirsWin {
				if unsent {
					if err := s.noteConflict(ctx, tx, t, sh, pk, c, o, "mine", ""); err != nil {
						return nil, err
					}
					res.Conflicts++
				}
				continue // stale, or this server's newer version stands
			}
			if unsent {
				if err := s.noteConflict(ctx, tx, t, sh, pk, c, o, "theirs", ""); err != nil {
					return nil, err
				}
				res.Conflicts++
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sync_ctx SET origin = ?, lamport = ? WHERE id = 1`, o.Peer, c.Lamport); err != nil {
			return nil, err
		}
		if err := writeRow(ctx, tx, t, sh, pk, c.Row, o.Seal); err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("%s %s: %v", c.Table, c.PK, err))
			if cerr := s.noteConflict(ctx, tx, t, sh, pk, c, o, "mine", err.Error()); cerr != nil {
				return nil, cerr
			}
			res.Conflicts++
			continue
		}
		if err := markVersion(ctx, tx, c.Table, c.PK, c.Lamport, o.Peer, now); err != nil {
			return nil, err
		}
		res.Applied++
		res.Tables[c.Table] = true
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sync_ctx SET origin = '', lamport = 0 WHERE id = 1`); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sync_clock SET lamport = MAX(lamport, ?) WHERE id = 1`, maxL); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sync: %w", err)
	}
	return res, nil
}

// markVersion records the version a record now has, whether or not a
// trigger fired (deleting what's already gone fires none).
func markVersion(ctx context.Context, tx *sql.Tx, table, pk string, lamport int64, origin string, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE sync_clock SET seq = seq + 1 WHERE id = 1`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO sync_rows (tbl, pk, seq, lamport, origin, at)
		SELECT ?, ?, seq, ?, ?, ? FROM sync_clock WHERE id = 1`, table, pk, lamport, origin, now)
	return err
}

func (s *Store) removeUnseen(ctx context.Context, tx *sql.Tx, t *SyncTable, sh *tableShape, keep map[string]bool, peer string, lamport, now int64) (int, error) {
	where := ""
	if t.Skip != "" {
		where = " WHERE NOT (" + t.skip("") + ")"
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+pkJSON(sh, "")+` FROM `+quoteIdent(t.Name)+where)
	if err != nil {
		return 0, err
	}
	var gone []string
	for rows.Next() {
		var pk string
		if err := rows.Scan(&pk); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[pk] {
			gone = append(gone, pk)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(gone) == 0 {
		return 0, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sync_ctx SET origin = ?, lamport = ? WHERE id = 1`, peer, lamport); err != nil {
		return 0, err
	}
	for _, pk := range gone {
		vals, err := decodePK(pk)
		if err != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdent(t.Name)+` WHERE `+pkWhere(sh), vals...); err != nil {
			return 0, err
		}
		if err := markVersion(ctx, tx, t.Name, pk, lamport, peer, now); err != nil {
			return 0, err
		}
	}
	return len(gone), nil
}

// writeRow makes a record what row says (deletes it when row is nil).
func writeRow(ctx context.Context, tx *sql.Tx, t *SyncTable, sh *tableShape, pk []any, row SyncRow, seal func(string) []byte) error {
	if row == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdent(t.Name)+` WHERE `+pkWhere(sh), pk...)
		return err
	}
	var cols []string
	var args []any
	for _, c := range sh.cols {
		v, ok := row[c]
		if !ok || contains(t.Ignore, c) {
			continue
		}
		val, err := decodeVal(v, seal)
		if err != nil {
			return err
		}
		cols = append(cols, c)
		args = append(args, val)
	}
	for _, p := range sh.pk {
		if _, ok := row[p]; !ok {
			return fmt.Errorf("the record has no %s", p)
		}
	}
	var set []string
	for _, c := range cols {
		if !contains(sh.pk, c) {
			set = append(set, quoteIdent(c)+" = excluded."+quoteIdent(c))
		}
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ")
	q := `INSERT INTO ` + quoteIdent(t.Name) + ` (` + quoteList(cols, "") + `) VALUES (` + marks + `) ON CONFLICT (` + quoteList(sh.pk, "") + `) DO `
	if len(set) == 0 {
		q += `NOTHING`
	} else {
		q += `UPDATE SET ` + strings.Join(set, ", ")
	}
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}

// ---------- conflicts ----------

// SyncConflict is a record both servers changed before they synced.
type SyncConflict struct {
	ID         int64     `json:"id"`
	Table      string    `json:"table"`
	Category   string    `json:"category"`
	PK         string    `json:"pk"`
	Peer       string    `json:"peer"`
	Mine       SyncRow   `json:"mine"`   // nil: deleted here
	Theirs     SyncRow   `json:"theirs"` // nil: deleted there
	Winner     string    `json:"winner"` // mine | theirs: what last-writer-wins kept
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ResolvedAt time.Time `json:"resolved_at,omitzero"`
}

// sealRow turns secrets (x) into this server's sealed bytes, for keeping.
func sealRow(row SyncRow, seal func(string) []byte) SyncRow {
	if row == nil {
		return nil
	}
	out := SyncRow{}
	for k, v := range row {
		if v.T == "x" {
			b := []byte(v.V)
			if seal != nil {
				b = seal(v.V)
			}
			v = SyncVal{T: "b", V: base64.StdEncoding.EncodeToString(b)}
		}
		out[k] = v
	}
	return out
}

func rowJSON(row SyncRow) sql.NullString {
	if row == nil {
		return sql.NullString{}
	}
	b, _ := json.Marshal(row)
	return sql.NullString{String: string(b), Valid: true}
}

func (s *Store) noteConflict(ctx context.Context, tx *sql.Tx, t *SyncTable, sh *tableShape, pk []any, c SyncChange, o SyncApply, winner, note string) error {
	mine, err := readRow(ctx, tx, t, sh, pk, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_conflicts WHERE tbl = ? AND pk = ? AND resolved_at = 0`, c.Table, c.PK); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sync_conflicts (tbl, pk, peer, mine, theirs, winner, note, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Table, c.PK, o.Peer, rowJSON(mine), rowJSON(sealRow(c.Row, o.Seal)), winner, note, time.Now().Unix())
	return err
}

// SyncConflicts lists conflicts still waiting for the administrator.
func (s *Store) SyncConflicts(ctx context.Context) ([]*SyncConflict, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, tbl, pk, peer, mine, theirs, winner, note, created_at, resolved_at FROM sync_conflicts WHERE resolved_at = 0 ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SyncConflict{}
	for rows.Next() {
		c, err := scanConflict(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountSyncConflicts counts open conflicts.
func (s *Store) CountSyncConflicts(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_conflicts WHERE resolved_at = 0`).Scan(&n)
	return n, err
}

func scanConflict(row scanner) (*SyncConflict, error) {
	var (
		c             SyncConflict
		mine, theirs  sql.NullString
		created, done int64
	)
	if err := row.Scan(&c.ID, &c.Table, &c.PK, &c.Peer, &mine, &theirs, &c.Winner, &c.Note, &created, &done); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if mine.Valid {
		_ = json.Unmarshal([]byte(mine.String), &c.Mine)
	}
	if theirs.Valid {
		_ = json.Unmarshal([]byte(theirs.String), &c.Theirs)
	}
	if t := syncTableByName[c.Table]; t != nil {
		c.Category = t.Category
	}
	c.CreatedAt = time.Unix(created, 0).UTC()
	c.ResolvedAt = unixOrZero(done)
	return &c, nil
}

// ResolveSyncConflict settles a conflict. Keeping the version that lost
// writes it here as a new change, so the other server takes it too.
func (s *Store) ResolveSyncConflict(ctx context.Context, id int64, keepMine bool) (*SyncConflict, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := scanConflict(tx.QueryRowContext(ctx, `SELECT id, tbl, pk, peer, mine, theirs, winner, note, created_at, resolved_at FROM sync_conflicts WHERE id = ? AND resolved_at = 0`, id))
	if err != nil {
		return nil, err
	}
	want, row := "theirs", c.Theirs
	if keepMine {
		want, row = "mine", c.Mine
	}
	if want != c.Winner || c.Note != "" {
		t := syncTableByName[c.Table]
		if t == nil {
			return nil, fmt.Errorf("sync: %s isn't shared", c.Table)
		}
		sh, err := s.shape(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		if sh == nil {
			return nil, fmt.Errorf("sync: no table %s", c.Table)
		}
		pk, err := decodePK(c.PK)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
			return nil, err
		}
		// Written as this server's own change (stored rows hold no open secrets).
		if err := writeRow(ctx, tx, t, sh, pk, row, nil); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sync_clock SET seq = seq + 1, lamport = lamport + 1 WHERE id = 1`); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO sync_rows (tbl, pk, seq, lamport, origin, at)
			SELECT ?, ?, seq, lamport, '', ? FROM sync_clock WHERE id = 1`, c.Table, c.PK, time.Now().Unix()); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sync_conflicts SET resolved_at = ? WHERE id = ?`, time.Now().Unix(), id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	c.Winner = want
	return c, nil
}

// ---------- status ----------

// SyncCategoryStatus is one category's side of the log.
type SyncCategoryStatus struct {
	ID         string    `json:"id"`
	Queued     int       `json:"queued"` // changes here the other server hasn't read yet
	LastChange time.Time `json:"last_change,omitzero"`
}

// SyncStatus counts this server's changes after acked (not read by the
// other server yet) and the last change, per category.
func (s *Store) SyncStatus(ctx context.Context, acked int64) ([]SyncCategoryStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tbl, SUM(CASE WHEN origin = '' AND seq > ? THEN 1 ELSE 0 END), MAX(at) FROM sync_rows GROUP BY tbl`, acked)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]*SyncCategoryStatus{}
	for _, c := range SyncCategories {
		by[c] = &SyncCategoryStatus{ID: c}
	}
	for rows.Next() {
		var tbl string
		var queued, at int64
		if err := rows.Scan(&tbl, &queued, &at); err != nil {
			return nil, err
		}
		t := syncTableByName[tbl]
		if t == nil {
			continue
		}
		c := by[t.Category]
		c.Queued += int(queued)
		if ts := time.Unix(at, 0).UTC(); ts.After(c.LastChange) {
			c.LastChange = ts
		}
	}
	out := make([]SyncCategoryStatus, 0, len(SyncCategories))
	for _, c := range SyncCategories {
		out = append(out, *by[c])
	}
	return out, rows.Err()
}

// DatabaseSize is the database's size on disk, in bytes (what the shared
// settings take at most on the other server).
func (s *Store) DatabaseSize(ctx context.Context) (int64, error) {
	var pages, size int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size); err != nil {
		return 0, err
	}
	return pages * size, nil
}
