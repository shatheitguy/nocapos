package store

import (
	"context"
	"strings"
	"time"
)

// PhotoKey addresses a photo: storage location id + slash path.
type PhotoKey struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

// PhotoMeta is what Photos learned by reading a picture. It stays valid while
// the file keeps the same size and modification time.
type PhotoMeta struct {
	PhotoKey
	Size   int64
	MTime  int64
	Taken  int64
	Width  int
	Height int
}

// PhotoMetas returns every cached entry, keyed by root + "\x00" + path.
func (s *Store) PhotoMetas(ctx context.Context) (map[string]PhotoMeta, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT root, path, size, mtime, taken, width, height FROM photo_meta`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]PhotoMeta{}
	for rows.Next() {
		var m PhotoMeta
		if err := rows.Scan(&m.Root, &m.Path, &m.Size, &m.MTime, &m.Taken, &m.Width, &m.Height); err != nil {
			return nil, err
		}
		out[m.Root+"\x00"+m.Path] = m
	}
	return out, rows.Err()
}

// SavePhotoMetas inserts or replaces entries in one transaction.
func (s *Store) SavePhotoMetas(ctx context.Context, metas []PhotoMeta) error {
	if len(metas) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO photo_meta (root, path, size, mtime, taken, width, height) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range metas {
		if _, err := stmt.ExecContext(ctx, m.Root, m.Path, m.Size, m.MTime, m.Taken, m.Width, m.Height); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PrunePhotoMetas drops cached entries for photos that no longer exist.
func (s *Store) PrunePhotoMetas(ctx context.Context, keep map[string]bool) error {
	all, err := s.PhotoMetas(ctx)
	if err != nil {
		return err
	}
	for k, m := range all {
		if !keep[k] {
			if _, err := s.db.ExecContext(ctx, `DELETE FROM photo_meta WHERE root = ? AND path = ?`, m.Root, m.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

// PhotoFavorites returns the user's favorites, keyed by root + "\x00" + path.
func (s *Store) PhotoFavorites(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT root, path FROM photo_favorites WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var root, path string
		if err := rows.Scan(&root, &path); err != nil {
			return nil, err
		}
		out[root+"\x00"+path] = true
	}
	return out, rows.Err()
}

func (s *Store) SetPhotoFavorite(ctx context.Context, userID string, keys []PhotoKey, on bool) error {
	for _, k := range keys {
		var err error
		if on {
			_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO photo_favorites (user_id, root, path) VALUES (?, ?, ?)`, userID, k.Root, k.Path)
		} else {
			_, err = s.db.ExecContext(ctx, `DELETE FROM photo_favorites WHERE user_id = ? AND root = ? AND path = ?`, userID, k.Root, k.Path)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// PhotoAlbum is a user's album; Items lists its photos, newest added first.
type PhotoAlbum struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	Items     []PhotoKey `json:"items"`
}

func (s *Store) PhotoAlbums(ctx context.Context, userID string) ([]*PhotoAlbum, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at FROM photo_albums WHERE user_id = ? ORDER BY name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	var albums []*PhotoAlbum
	byID := map[int64]*PhotoAlbum{}
	for rows.Next() {
		a := &PhotoAlbum{Items: []PhotoKey{}}
		var created int64
		if err := rows.Scan(&a.ID, &a.Name, &created); err != nil {
			rows.Close()
			return nil, err
		}
		a.CreatedAt = time.Unix(created, 0).UTC()
		albums = append(albums, a)
		byID[a.ID] = a
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items, err := s.db.QueryContext(ctx, `SELECT i.album_id, i.root, i.path FROM photo_album_items i
		JOIN photo_albums a ON a.id = i.album_id WHERE a.user_id = ? ORDER BY i.added_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer items.Close()
	for items.Next() {
		var id int64
		var k PhotoKey
		if err := items.Scan(&id, &k.Root, &k.Path); err != nil {
			return nil, err
		}
		if a := byID[id]; a != nil {
			a.Items = append(a.Items, k)
		}
	}
	return albums, items.Err()
}

func (s *Store) CreatePhotoAlbum(ctx context.Context, userID, name string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO photo_albums (user_id, name, created_at) VALUES (?, ?, ?)`, userID, strings.TrimSpace(name), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ownsAlbum checks the album belongs to the user.
func (s *Store) ownsAlbum(ctx context.Context, userID string, id int64) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM photo_albums WHERE id = ? AND user_id = ?`, id, userID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RenamePhotoAlbum(ctx context.Context, userID string, id int64, name string) error {
	if err := s.ownsAlbum(ctx, userID, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE photo_albums SET name = ? WHERE id = ?`, strings.TrimSpace(name), id)
	return err
}

func (s *Store) DeletePhotoAlbum(ctx context.Context, userID string, id int64) error {
	if err := s.ownsAlbum(ctx, userID, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM photo_albums WHERE id = ?`, id)
	return err
}

// AddToPhotoAlbum / RemoveFromPhotoAlbum change an album's photos.
func (s *Store) AddToPhotoAlbum(ctx context.Context, userID string, id int64, keys []PhotoKey) error {
	if err := s.ownsAlbum(ctx, userID, id); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, k := range keys {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO photo_album_items (album_id, root, path, added_at) VALUES (?, ?, ?, ?)`, id, k.Root, k.Path, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RemoveFromPhotoAlbum(ctx context.Context, userID string, id int64, keys []PhotoKey) error {
	if err := s.ownsAlbum(ctx, userID, id); err != nil {
		return err
	}
	for _, k := range keys {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM photo_album_items WHERE album_id = ? AND root = ? AND path = ?`, id, k.Root, k.Path); err != nil {
			return err
		}
	}
	return nil
}

// ForgetPhotos removes deleted photos from favorites and albums.
func (s *Store) ForgetPhotos(ctx context.Context, keys []PhotoKey) error {
	for _, k := range keys {
		for _, q := range []string{
			`DELETE FROM photo_favorites WHERE root = ? AND path = ?`,
			`DELETE FROM photo_album_items WHERE root = ? AND path = ?`,
			`DELETE FROM photo_meta WHERE root = ? AND path = ?`,
		} {
			if _, err := s.db.ExecContext(ctx, q, k.Root, k.Path); err != nil {
				return err
			}
		}
	}
	return nil
}
