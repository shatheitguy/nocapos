package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Restoring a backup from Settings → Export & Restore. The database can't be
// swapped under a running alfad, so the uploaded file is checked and staged
// next to it, and swapped in the next time alfad starts (before Open).

const (
	dbName      = "alfa.db"
	stagedName  = "alfa.db.restore"
	sqliteMagic = "SQLite format 3\x00"
)

// ErrBadBackup is returned for a file that isn't a NoCapOS backup this version can use.
var ErrBadBackup = errors.New("bad backup")

// StagedRestorePath is where a checked backup waits for the next start.
func StagedRestorePath(dataDir string) string { return filepath.Join(dataDir, stagedName) }

// HasStagedRestore reports whether a restore is waiting for the next start.
func HasStagedRestore(dataDir string) bool {
	_, err := os.Stat(StagedRestorePath(dataDir))
	return err == nil
}

// CancelStagedRestore drops a waiting restore (nothing to do if there's none).
func CancelStagedRestore(dataDir string) error {
	err := os.Remove(StagedRestorePath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// StageRestore copies a backup from src, checks it, and stages it for the
// next start. Nothing is staged if the check fails.
func StageRestore(ctx context.Context, dataDir string, src io.Reader) error {
	tmp, err := os.CreateTemp(dataDir, ".restore-*.db")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // gone after the rename below; cleans up on failure
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := CheckBackup(ctx, tmpName); err != nil {
		return err
	}
	return os.Rename(tmpName, StagedRestorePath(dataDir))
}

// CheckBackup makes sure path is an intact NoCapOS database that this
// version can open (not one from a newer version).
func CheckBackup(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	head := make([]byte, len(sqliteMagic))
	_, err = io.ReadFull(f, head)
	f.Close()
	if err != nil || !bytes.Equal(head, []byte(sqliteMagic)) {
		return fmt.Errorf("%w: not a NoCapOS backup file", ErrBadBackup)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadBackup, err)
	}
	defer db.Close()
	var check string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		return fmt.Errorf("%w: the file is damaged", ErrBadBackup)
	}
	var version sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || !version.Valid {
		return fmt.Errorf("%w: not a NoCapOS backup file", ErrBadBackup)
	}
	if latest, err := latestMigration(); err == nil && int(version.Int64) > latest {
		return fmt.Errorf("%w: it was made by a newer NoCapOS; update first, then restore", ErrBadBackup)
	}
	var users int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		return fmt.Errorf("%w: not a NoCapOS backup file", ErrBadBackup)
	}
	return nil
}

func latestMigration() (int, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return 0, err
	}
	latest := 0
	for _, e := range entries {
		prefix, _, _ := strings.Cut(e.Name(), "_")
		if v, err := strconv.Atoi(prefix); err == nil && v > latest {
			latest = v
		}
	}
	return latest, nil
}

// ApplyStagedRestore swaps a staged backup in before the database is opened.
// The database it replaces is kept as alfa.db.before-restore-<time> (with its
// -wal and -shm files, if any). It reports whether a restore was applied.
func ApplyStagedRestore(dataDir string) (bool, error) {
	staged := StagedRestorePath(dataDir)
	if _, err := os.Stat(staged); err != nil {
		return false, nil
	}
	db := filepath.Join(dataDir, dbName)
	keep := db + ".before-restore-" + time.Now().UTC().Format("20060102-150405")
	if _, err := os.Stat(db); err == nil {
		if err := os.Rename(db, keep); err != nil {
			return false, err
		}
		for _, sfx := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(db + sfx); err == nil {
				if err := os.Rename(db+sfx, keep+sfx); err != nil {
					return false, err
				}
			}
		}
	}
	if err := os.Rename(staged, db); err != nil {
		return false, err
	}
	return true, nil
}
