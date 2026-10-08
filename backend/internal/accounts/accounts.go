// Package accounts manages the people who can sign in to NoCapOS.
//
// On a native Linux install (alfad running as root) the accounts ARE the
// machine's Linux users: created with useradd, passwords checked by the
// system's own unix_chkpwd, administrators = root + the sudo/wheel group.
// Elsewhere (Docker, Windows) NoCapOS keeps its own accounts, starting with
// the "admin" created at setup.
package accounts

import (
	"context"
	"errors"
	"regexp"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/store"
)

type Mode string

const (
	ModeSystem Mode = "system" // real Linux accounts
	ModeApp    Mode = "app"    // NoCapOS's own accounts
)

type Account struct {
	Username string `json:"username"`
	FullName string `json:"full_name,omitempty"`
	Role     string `json:"role"` // admin | user
	Disabled bool   `json:"disabled"`
	Root     bool   `json:"root,omitempty"` // the Linux superuser
	UID      int    `json:"uid"`            // -1 for NoCapOS accounts
	Home     string `json:"home,omitempty"`
	Shell    string `json:"shell,omitempty"`
}

type NewAccount struct {
	Username string
	FullName string
	Password string
	Role     string
}

var (
	ErrNotFound  = auth.ErrNoAccount
	ErrExists    = errors.New("that user name is already taken")
	ErrBadName   = errors.New("user names are 1–32 characters: lowercase letters, digits, '-' or '_', starting with a letter or '_'")
	ErrProtected = errors.New("the root account can't be changed from NoCapOS")
)

// Directory is where accounts live.
type Directory interface {
	Mode() Mode
	List(ctx context.Context) ([]Account, error)
	Lookup(ctx context.Context, username string) (*Account, error)
	Create(ctx context.Context, a NewAccount) error
	Delete(ctx context.Context, username string, removeHome bool) error
	SetRole(ctx context.Context, username, role string) error
	SetPassword(ctx context.Context, username, password string) error
	SetDisabled(ctx context.Context, username string, disabled bool) error
}

// Linux login names (what useradd accepts by default on Debian/Fedora).
var linuxName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func validRole(r string) bool { return r == store.RoleAdmin || r == store.RoleUser }

// ---------- NoCapOS's own accounts ----------

type appDir struct{ st *store.Store }

// NewApp returns the directory backed by NoCapOS's user table.
func NewApp(st *store.Store) Directory { return &appDir{st: st} }

func (d *appDir) Mode() Mode { return ModeApp }

func toAccount(u store.User) Account {
	return Account{Username: u.Username, Role: u.Role, Disabled: u.Disabled, UID: -1}
}

func (d *appDir) List(ctx context.Context) ([]Account, error) {
	us, err := d.st.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(us))
	for _, u := range us {
		out = append(out, toAccount(u))
	}
	return out, nil
}

func (d *appDir) get(ctx context.Context, username string) (*store.User, error) {
	u, err := d.st.UserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	return u, err
}

func (d *appDir) Lookup(ctx context.Context, username string) (*Account, error) {
	u, err := d.get(ctx, username)
	if err != nil {
		return nil, err
	}
	a := toAccount(*u)
	return &a, nil
}

func (d *appDir) Create(ctx context.Context, a NewAccount) error {
	if err := auth.ValidateUsername(a.Username); err != nil {
		return err
	}
	if err := auth.ValidatePassword(a.Password); err != nil {
		return err
	}
	if !validRole(a.Role) {
		a.Role = store.RoleUser
	}
	hash, err := auth.HashPassword(a.Password)
	if err != nil {
		return err
	}
	now := time.Now()
	u := &store.User{ID: store.NewID(), Username: a.Username, PasswordHash: hash, Role: a.Role, CreatedAt: now, UpdatedAt: now}
	err = d.st.CreateUser(ctx, u)
	if errors.Is(err, store.ErrConflict) {
		return ErrExists
	}
	return err
}

func (d *appDir) Delete(ctx context.Context, username string, _ bool) error {
	u, err := d.get(ctx, username)
	if err != nil {
		return err
	}
	return d.st.DeleteUser(ctx, u.ID)
}

func (d *appDir) SetRole(ctx context.Context, username, role string) error {
	if !validRole(role) {
		return errors.New("role must be admin or user")
	}
	u, err := d.get(ctx, username)
	if err != nil {
		return err
	}
	return d.st.SetRole(ctx, u.ID, role)
}

func (d *appDir) SetPassword(ctx context.Context, username, password string) error {
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}
	u, err := d.get(ctx, username)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return d.st.SetPassword(ctx, u.ID, hash)
}

func (d *appDir) SetDisabled(ctx context.Context, username string, disabled bool) error {
	u, err := d.get(ctx, username)
	if err != nil {
		return err
	}
	return d.st.SetDisabled(ctx, u.ID, disabled)
}
