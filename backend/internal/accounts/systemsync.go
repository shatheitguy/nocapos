package accounts

import (
	"context"
	"strconv"
)

// SystemEntry is a person's Linux account as a failover pair keeps it the
// same on both servers: the password hash from /etc/shadow (never the
// password), the administrator group, the lock, the display name. Root and
// system accounts never travel.
type SystemEntry struct {
	Username string `json:"username"`
	FullName string `json:"full_name"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
	Hash     string `json:"hash"`    // the shadow hash without a lock "!"
	Changed  int64  `json:"changed"` // shadow's last change (days since 1970)
}

// Fingerprint changes whenever anything that travels changes.
func (e SystemEntry) Fingerprint() string {
	return e.Hash + "|" + e.Role + "|" + strconv.FormatBool(e.Disabled) + "|" + e.FullName
}

// SystemSync is implemented by directories of real Linux accounts.
type SystemSync interface {
	// ExportAccounts lists the people's accounts (not root, not system ones).
	ExportAccounts(ctx context.Context) ([]SystemEntry, error)
	// ImportAccount creates the account (with its home) or brings it in line
	// with e. It never deletes accounts and never touches root.
	ImportAccount(ctx context.Context, e SystemEntry) error
}
