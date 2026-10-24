package terminal

import (
	"bufio"
	"errors"
	"os"
	"os/user"
	"strconv"
	"strings"
)

// ErrNoLinuxAccount: host shells run as the person's own Linux account, and
// this NoCapOS account isn't one.
var ErrNoLinuxAccount = errors.New("host shells run as your own Linux account, and this NoCapOS account isn't one; an administrator can open a root shell with their password")

// ErrNoLogin: the Linux account has no login shell.
var ErrNoLogin = errors.New("this Linux account can't log in (its shell is nologin)")

// Account is the Linux account a host shell runs as.
type Account struct {
	Username string
	UID, GID uint32
	Groups   []uint32
	Home     string
	Shell    string
}

// Root reports whether the account is the superuser.
func (a *Account) Root() bool { return a != nil && a.UID == 0 }

// LookupAccount reads a Linux account from /etc/passwd (shell included,
// which os/user doesn't give) and its supplementary groups.
func LookupAccount(username string) (*Account, error) {
	return lookupAccount("/etc/passwd", username)
}

func lookupAccount(passwd, username string) (*Account, error) {
	f, err := os.Open(passwd)
	if err != nil {
		return nil, ErrNoLinuxAccount
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) < 7 || p[0] != username {
			continue
		}
		uid, err1 := strconv.ParseUint(p[2], 10, 32)
		gid, err2 := strconv.ParseUint(p[3], 10, 32)
		if err1 != nil || err2 != nil {
			return nil, ErrNoLinuxAccount
		}
		a := &Account{Username: username, UID: uint32(uid), GID: uint32(gid), Home: p[5], Shell: p[6]}
		if u, err := user.Lookup(username); err == nil {
			if ids, err := u.GroupIds(); err == nil {
				for _, id := range ids {
					if g, err := strconv.ParseUint(id, 10, 32); err == nil {
						a.Groups = append(a.Groups, uint32(g))
					}
				}
			}
		}
		return a, nil
	}
	return nil, ErrNoLinuxAccount
}

func noLogin(shell string) bool {
	return strings.HasSuffix(shell, "/nologin") || strings.HasSuffix(shell, "/false")
}
