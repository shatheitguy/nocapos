//go:build linux

package accounts

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"alfaos/alfad/internal/store"
)

type shadowEnt struct {
	hash    string
	changed int64
}

func readShadow() (map[string]shadowEnt, error) {
	b, err := os.ReadFile("/etc/shadow")
	if err != nil {
		return nil, err
	}
	out := map[string]shadowEnt{}
	for _, line := range strings.Split(string(b), "\n") {
		p := strings.Split(line, ":")
		if len(p) < 3 {
			continue
		}
		ch, _ := strconv.ParseInt(p[2], 10, 64)
		out[p[0]] = shadowEnt{hash: p[1], changed: ch}
	}
	return out, nil
}

// validHash: a crypt(3) hash ("$id$…"), nothing that could unlock an
// account without a password or break usermod's argument.
func validHash(h string) bool {
	if len(h) < 13 || len(h) > 512 || !strings.HasPrefix(h, "$") {
		return false
	}
	for _, r := range h {
		if r <= ' ' || r == ':' || r > '~' {
			return false
		}
	}
	return true
}

func (s *System) ExportAccounts(ctx context.Context) ([]SystemEntry, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	sh, err := readShadow()
	if err != nil {
		return nil, err
	}
	out := []SystemEntry{}
	for _, a := range list {
		if a.Root {
			continue
		}
		e, ok := sh[a.Username]
		hash := strings.TrimLeft(e.hash, "!")
		if !ok || !validHash(hash) {
			continue // no usable password: nothing to keep in step
		}
		out = append(out, SystemEntry{Username: a.Username, FullName: a.FullName, Role: a.Role, Disabled: a.Disabled, Hash: hash, Changed: e.changed})
	}
	return out, nil
}

func (s *System) ImportAccount(ctx context.Context, e SystemEntry) error {
	if !linuxName.MatchString(e.Username) || !validHash(e.Hash) || !validRole(e.Role) || ValidFullName(e.FullName) != nil {
		return errors.New("not a person's account")
	}
	a, err := s.Lookup(ctx, e.Username)
	switch {
	case errors.Is(err, ErrNotFound):
		s.mu.Lock()
		if pw, _ := readPasswd(); pw != nil {
			for _, p := range pw {
				if p.name == e.Username { // a system account by that name
					s.mu.Unlock()
					return ErrExists
				}
			}
		}
		args := []string{"-m", "-s", "/bin/bash", "-p", e.Hash}
		if e.FullName != "" {
			args = append(args, "-c", e.FullName)
		}
		if e.Role == store.RoleAdmin {
			args = append(args, "-G", s.adminGroup)
		}
		err := run(ctx, "", "useradd", append(args, e.Username)...)
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if e.Disabled {
			return s.SetDisabled(ctx, e.Username, true)
		}
		return nil
	case err != nil:
		return err
	case a.Root:
		return ErrProtected
	}
	sh, err := readShadow()
	if err != nil {
		return err
	}
	if strings.TrimLeft(sh[e.Username].hash, "!") != e.Hash {
		s.mu.Lock()
		hash := e.Hash
		if a.Disabled {
			hash = "!" + hash
		}
		err := run(ctx, "", "usermod", "-p", hash, e.Username)
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if a.Role != e.Role {
		if err := s.SetRole(ctx, e.Username, e.Role); err != nil {
			return err
		}
	}
	if a.FullName != e.FullName {
		if err := s.SetFullName(ctx, e.Username, e.FullName); err != nil && !errors.Is(err, ErrUnsupported) {
			return err
		}
	}
	if a.Disabled != e.Disabled {
		return s.SetDisabled(ctx, e.Username, e.Disabled)
	}
	return nil
}
