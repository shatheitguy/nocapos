package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// Group is a named set of people (Settings → Users & Groups).
type Group struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Color       string    `json:"color"`
	Members     []string  `json:"members"` // user names, sorted
	CreatedAt   time.Time `json:"created_at"`
}

// Groups lists every group with its members, by name.
func (s *Store) Groups(ctx context.Context) ([]*Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, description, color, created_at FROM user_groups ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	out := []*Group{}
	byID := map[int64]*Group{}
	for rows.Next() {
		var (
			g       Group
			created int64
		)
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.Color, &created); err != nil {
			rows.Close()
			return nil, err
		}
		g.CreatedAt = time.Unix(created, 0).UTC()
		g.Members = []string{}
		out = append(out, &g)
		byID[g.ID] = &g
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	mrows, err := s.db.QueryContext(ctx, `SELECT group_id, username FROM user_group_members ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var (
			id   int64
			name string
		)
		if err := mrows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if g := byID[id]; g != nil {
			g.Members = append(g.Members, name)
		}
	}
	return out, mrows.Err()
}

// Group finds one group.
func (s *Store) Group(ctx context.Context, id int64) (*Group, error) {
	list, err := s.Groups(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range list {
		if g.ID == id {
			return g, nil
		}
	}
	return nil, ErrNotFound
}

// GroupsByMember maps lower-case user names to the names of their groups.
func (s *Store) GroupsByMember(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.username, g.name FROM user_group_members m JOIN user_groups g ON g.id = m.group_id
		 ORDER BY g.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var u, g string
		if err := rows.Scan(&u, &g); err != nil {
			return nil, err
		}
		k := strings.ToLower(u)
		out[k] = append(out[k], g)
	}
	return out, rows.Err()
}

// CreateGroup adds a group with its members; ErrConflict when the name is taken.
func (s *Store) CreateGroup(ctx context.Context, g *Group) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now().UTC()
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO user_groups (name, description, color, created_at) VALUES (?, ?, ?, ?)`,
		g.Name, g.Description, g.Color, g.CreatedAt.Unix())
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if g.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	if err := setMembers(ctx, tx, g.ID, g.Members); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateGroup changes a group's name, note, color and members.
func (s *Store) UpdateGroup(ctx context.Context, g *Group) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE user_groups SET name = ?, description = ?, color = ? WHERE id = ?`,
		g.Name, g.Description, g.Color, g.ID)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := setMembers(ctx, tx, g.ID, g.Members); err != nil {
		return err
	}
	return tx.Commit()
}

func setMembers(ctx context.Context, tx *sql.Tx, id int64, members []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_group_members WHERE group_id = ?`, id); err != nil {
		return err
	}
	for _, m := range members {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_group_members (group_id, username) VALUES (?, ?)`, id, m); err != nil {
			return err
		}
	}
	return nil
}

// DeleteGroup removes a group (its members stay, of course).
func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// LastSeenAll maps user IDs to when each was last active in any session
// (a browser, the NoCap OS app or NoCap Desktop).
func (s *Store) LastSeenAll(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, MAX(t) FROM (
		  SELECT user_id, last_seen_at AS t FROM session_info
		  UNION ALL SELECT user_id, created_at FROM refresh_tokens
		  UNION ALL SELECT user_id, last_seen_at FROM devices
		) GROUP BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var (
			id string
			t  int64
		)
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		if t > 0 {
			out[id] = time.Unix(t, 0).UTC()
		}
	}
	return out, rows.Err()
}

// AvatarOwners lists the user IDs that have a profile photo.
func (s *Store) AvatarOwners(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM user_avatars`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SortedMembers returns names de-duplicated (ignoring case) and sorted.
func SortedMembers(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		k := strings.ToLower(n)
		if n == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

var errNoGroupName = errors.New("a group needs a name")

// ValidGroupName checks a group name: 1–40 characters, no control characters.
func ValidGroupName(name string) error {
	if name == "" || len([]rune(name)) > 40 {
		return errNoGroupName
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return errNoGroupName
		}
	}
	return nil
}
