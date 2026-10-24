package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Access types: who can sign in to the web desktop.
const (
	AccessDesktop = "desktop" // the web desktop, plus file sharing
	AccessFiles   = "files"   // file sharing only (SMB / WebDAV)
)

// Guest access to a shared folder (no password).
const (
	GuestRO = "ro"
	GuestRW = "rw"
)

// Shared-folder permissions. "none" is the absence of a row.
const (
	PermRW   = "rw"
	PermRO   = "ro"
	PermNone = "none"
)

// ---------- access type ----------

// UserAccess returns a person's access type (desktop when never set).
func (s *Store) UserAccess(ctx context.Context, username string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT access FROM user_access WHERE username = ?`, username).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessDesktop, nil
	}
	return v, err
}

// UserAccessAll maps lower-case user names to their access type (only rows that exist).
func (s *Store) UserAccessAll(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT username, access FROM user_access`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var u, a string
		if err := rows.Scan(&u, &a); err != nil {
			return nil, err
		}
		out[strings.ToLower(u)] = a
	}
	return out, rows.Err()
}

// SetUserAccess stores a person's access type.
func (s *Store) SetUserAccess(ctx context.Context, username, access string) error {
	if access != AccessDesktop && access != AccessFiles {
		return errors.New("access must be desktop or files")
	}
	if access == AccessDesktop {
		_, err := s.db.ExecContext(ctx, `DELETE FROM user_access WHERE username = ?`, username)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_access (username, access) VALUES (?, ?)
		ON CONFLICT(username) DO UPDATE SET access = excluded.access`, username, access)
	return err
}

// ForgetUser removes a deleted person's access type, folder permissions and
// app access (their sessions, chats and prefs cascade with the users row).
func (s *Store) ForgetUser(ctx context.Context, username string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM user_access WHERE username = ?`,
		`DELETE FROM share_permissions WHERE username = ?`,
		`DELETE FROM app_access WHERE username = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, username); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------- shared folders ----------

// SharedFolder is a folder shared with people: in Files, over SMB and WebDAV.
type SharedFolder struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Root        string    `json:"root"` // storage location id
	Path        string    `json:"path"` // "/a/b" inside the location
	Description string    `json:"description"`
	Recycle     bool      `json:"recycle"`   // deletes go to the folder's recycle bin
	Network     bool      `json:"network"`   // visible when browsing the network
	ReadOnly    bool      `json:"read_only"` // nobody can change it, whatever their permission
	Guest       string    `json:"guest"`     // anyone, no password: "" (off), GuestRO or GuestRW
	CreatedAt   time.Time `json:"created_at"`
}

const folderCols = `id, name, root, path, description, recycle, network, read_only, guest, created_at`

func scanFolder(row scanner) (*SharedFolder, error) {
	var (
		f                    SharedFolder
		recycle, network, ro int
		created              int64
	)
	if err := row.Scan(&f.ID, &f.Name, &f.Root, &f.Path, &f.Description, &recycle, &network, &ro, &f.Guest, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	f.Recycle, f.Network, f.ReadOnly, f.CreatedAt = recycle != 0, network != 0, ro != 0, unixOrZero(created)
	return &f, nil
}

func (s *Store) SharedFolders(ctx context.Context) ([]*SharedFolder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+folderCols+` FROM shared_folders ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SharedFolder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) SharedFolder(ctx context.Context, id int64) (*SharedFolder, error) {
	return scanFolder(s.db.QueryRowContext(ctx, `SELECT `+folderCols+` FROM shared_folders WHERE id = ?`, id))
}

// CreateSharedFolder inserts f and sets its id. ErrExists if the name is taken.
func (s *Store) CreateSharedFolder(ctx context.Context, f *SharedFolder) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO shared_folders (name, root, path, description, recycle, network, read_only, guest, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Name, f.Root, f.Path, f.Description, boolInt(f.Recycle), boolInt(f.Network), boolInt(f.ReadOnly), f.Guest, time.Now().Unix())
	if isUniqueViolation(err) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	f.ID, err = res.LastInsertId()
	return err
}

// UpdateSharedFolder saves the editable fields of f (name, description, flags).
func (s *Store) UpdateSharedFolder(ctx context.Context, f *SharedFolder) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE shared_folders SET name = ?, description = ?, recycle = ?, network = ?, read_only = ?, guest = ? WHERE id = ?`,
		f.Name, f.Description, boolInt(f.Recycle), boolInt(f.Network), boolInt(f.ReadOnly), f.Guest, f.ID)
	if isUniqueViolation(err) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSharedFolder(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM shared_folders WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SharePermissions maps folder id → lower-case user name → rw|ro.
func (s *Store) SharePermissions(ctx context.Context) (map[int64]map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT folder_id, username, perm FROM share_permissions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]string{}
	for rows.Next() {
		var id int64
		var u, p string
		if err := rows.Scan(&id, &u, &p); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]string{}
		}
		out[id][strings.ToLower(u)] = p
	}
	return out, rows.Err()
}

// SetSharePermission sets one person's permission on a folder (PermNone removes it).
func (s *Store) SetSharePermission(ctx context.Context, folderID int64, username, perm string) error {
	switch perm {
	case PermNone:
		_, err := s.db.ExecContext(ctx, `DELETE FROM share_permissions WHERE folder_id = ? AND username = ?`, folderID, username)
		return err
	case PermRW, PermRO:
		_, err := s.db.ExecContext(ctx, `INSERT INTO share_permissions (folder_id, username, perm) VALUES (?, ?, ?)
			ON CONFLICT(folder_id, username) DO UPDATE SET perm = excluded.perm`, folderID, username, perm)
		return err
	default:
		return errors.New("permission must be rw, ro or none")
	}
}

// ---------- app access ----------

// AppAccess is who may use an installed App Store app.
type AppAccess struct {
	Everyone bool     `json:"everyone"`
	Users    []string `json:"users"` // when not everyone
}

// AppAccessAll returns the rules for every app that has any (others: everyone).
func (s *Store) AppAccessAll(ctx context.Context) (map[string]*AppAccess, error) {
	out := map[string]*AppAccess{}
	rows, err := s.db.QueryContext(ctx, `SELECT app_id, mode FROM app_access_mode`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, mode string
		if err := rows.Scan(&id, &mode); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = &AppAccess{Everyone: mode != "selected", Users: []string{}}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT app_id, username FROM app_access ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, u string
		if err := rows.Scan(&id, &u); err != nil {
			return nil, err
		}
		if a := out[id]; a != nil {
			a.Users = append(a.Users, u)
		}
	}
	return out, rows.Err()
}

// SetAppAccess replaces an app's rules.
func (s *Store) SetAppAccess(ctx context.Context, appID string, a AppAccess) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_access WHERE app_id = ?`, appID); err != nil {
		return err
	}
	if a.Everyone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_access_mode WHERE app_id = ?`, appID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_access_mode (app_id, mode) VALUES (?, 'selected')
		ON CONFLICT(app_id) DO UPDATE SET mode = excluded.mode`, appID); err != nil {
		return err
	}
	for _, u := range a.Users {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO app_access (app_id, username) VALUES (?, ?)`, appID, u); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetAppAllowed adds or removes one person from an app's selected people.
func (s *Store) SetAppAllowed(ctx context.Context, appID, username string, allowed bool) error {
	if allowed {
		_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO app_access (app_id, username) VALUES (?, ?)`, appID, username)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_access WHERE app_id = ? AND username = ?`, appID, username)
	return err
}

// ---------- personal preferences ----------

// UserPrefs returns a person's saved desktop preferences ("" if none).
func (s *Store) UserPrefs(ctx context.Context, userID string) (string, time.Time, error) {
	var data string
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT data, updated_at FROM user_prefs WHERE user_id = ?`, userID).Scan(&data, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	return data, unixOrZero(at), err
}

func (s *Store) SetUserPrefs(ctx context.Context, userID, data string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_prefs (user_id, data, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`, userID, data, time.Now().Unix())
	return err
}
