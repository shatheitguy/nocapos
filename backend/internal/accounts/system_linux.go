//go:build linux

package accounts

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/store"
)

// System is the directory of real Linux accounts.
type System struct {
	chkpwd     string // unix_chkpwd: the PAM helper that checks a password against /etc/shadow
	adminGroup string // sudo (Debian/Ubuntu) or wheel (Fedora/RHEL)
	uidMin     int
	mu         sync.Mutex // serialises account changes
}

// NewSystem returns the Linux directory, or an error explaining why real
// accounts can't be used here (not root, in a container, missing tools).
func NewSystem() (*System, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("NoCapOS isn't running as root")
	}
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return nil, errors.New("NoCapOS is running in a container")
		}
	}
	chk := ""
	for _, p := range []string{"/usr/sbin/unix_chkpwd", "/sbin/unix_chkpwd", "/usr/bin/unix_chkpwd"} {
		if _, err := os.Stat(p); err == nil {
			chk = p
			break
		}
	}
	if chk == "" {
		return nil, errors.New("unix_chkpwd (PAM) isn't installed")
	}
	for _, tool := range []string{"useradd", "userdel", "usermod", "chpasswd", "gpasswd"} {
		if _, err := exec.LookPath(tool); err != nil {
			return nil, fmt.Errorf("%s isn't installed", tool)
		}
	}
	s := &System{chkpwd: chk, uidMin: loginDefsInt("UID_MIN", 1000)}
	groups, _ := readGroups()
	for _, g := range []string{"sudo", "wheel", "admin"} {
		if _, ok := groups[g]; ok {
			s.adminGroup = g
			break
		}
	}
	if s.adminGroup == "" {
		return nil, errors.New("no sudo or wheel group to hold administrators")
	}
	return s, nil
}

func (s *System) Mode() Mode { return ModeSystem }

// ---------- reading /etc/passwd, /etc/group, /etc/shadow ----------

type pwent struct {
	name, gecos, home, shell string
	uid                      int
}

func readPasswd() ([]pwent, error) {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []pwent
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) < 7 || strings.HasPrefix(p[0], "#") {
			continue
		}
		uid, err := strconv.Atoi(p[2])
		if err != nil {
			continue
		}
		out = append(out, pwent{name: p[0], uid: uid, gecos: strings.Split(p[4], ",")[0], home: p[5], shell: p[6]})
	}
	return out, sc.Err()
}

// readGroups maps group name → members.
func readGroups() (map[string][]string, error) {
	f, err := os.Open("/etc/group")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string][]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) < 4 {
			continue
		}
		var members []string
		if p[3] != "" {
			members = strings.Split(p[3], ",")
		}
		out[p[0]] = members
	}
	return out, sc.Err()
}

// lockedAccounts lists accounts whose shadow password is locked ("!…").
func lockedAccounts() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile("/etc/shadow")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		p := strings.Split(line, ":")
		if len(p) >= 2 && strings.HasPrefix(p[1], "!") {
			out[p[0]] = true
		}
	}
	return out
}

func loginDefsInt(key string, def int) int {
	b, err := os.ReadFile("/etc/login.defs")
	if err != nil {
		return def
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == key {
			if v, err := strconv.Atoi(f[1]); err == nil {
				return v
			}
		}
	}
	return def
}

func noLogin(shell string) bool {
	return strings.HasSuffix(shell, "/nologin") || strings.HasSuffix(shell, "/false") || shell == ""
}

// human reports whether an account is a person who can sign in: root, or a
// regular user (UID_MIN…<65534) with a real shell.
func (s *System) human(p pwent) bool {
	if p.uid == 0 {
		return true
	}
	return p.uid >= s.uidMin && p.uid < 65534 && !noLogin(p.shell)
}

func (s *System) account(p pwent, groups map[string][]string, locked map[string]bool) Account {
	role := store.RoleUser
	if p.uid == 0 || contains(groups[s.adminGroup], p.name) {
		role = store.RoleAdmin
	}
	return Account{Username: p.name, FullName: p.gecos, Role: role, Disabled: locked[p.name], Root: p.uid == 0, UID: p.uid, Home: p.home, Shell: p.shell}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *System) List(context.Context) ([]Account, error) {
	pw, err := readPasswd()
	if err != nil {
		return nil, err
	}
	groups, _ := readGroups()
	locked := lockedAccounts()
	var out []Account
	for _, p := range pw {
		if s.human(p) {
			out = append(out, s.account(p, groups, locked))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

func (s *System) Lookup(_ context.Context, username string) (*Account, error) {
	pw, err := readPasswd()
	if err != nil {
		return nil, err
	}
	for _, p := range pw {
		if p.name == username && s.human(p) {
			groups, _ := readGroups()
			a := s.account(p, groups, lockedAccounts())
			return &a, nil
		}
	}
	return nil, ErrNotFound
}

// ---------- auth.SystemAccounts ----------

// Verify asks unix_chkpwd — the helper PAM itself uses — so every hash
// scheme the distro supports (yescrypt, sha512-crypt, …) works.
func (s *System) Verify(ctx context.Context, username, password string) (bool, error) {
	if !linuxName.MatchString(username) || strings.ContainsRune(password, 0) {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.chkpwd, username, "nonull")
	cmd.Stdin = bytes.NewReader(append([]byte(password), 0))
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false, nil // wrong password / locked / unknown user
	}
	return false, err
}

func (s *System) RoleOf(ctx context.Context, username string) (string, bool, error) {
	a, err := s.Lookup(ctx, username)
	if err != nil {
		return "", false, err
	}
	return a.Role, a.Disabled, nil
}

// ---------- changes ----------

func run(ctx context.Context, stdin string, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s: %s", name, msg)
	}
	return nil
}

func (s *System) Create(ctx context.Context, a NewAccount) error {
	if !linuxName.MatchString(a.Username) {
		return ErrBadName
	}
	if err := auth.ValidatePassword(a.Password); err != nil {
		return err
	}
	if strings.ContainsAny(a.FullName, ":,\n") || strings.ContainsAny(a.Password, "\n") {
		return errors.New("names and passwords can't contain ':', ',' or line breaks")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if pw, _ := readPasswd(); pw != nil {
		for _, p := range pw {
			if p.name == a.Username {
				return ErrExists
			}
		}
	}
	args := []string{"-m", "-s", "/bin/bash"}
	if a.FullName != "" {
		args = append(args, "-c", a.FullName)
	}
	if a.Role == store.RoleAdmin {
		args = append(args, "-G", s.adminGroup)
	}
	args = append(args, a.Username)
	if err := run(ctx, "", "useradd", args...); err != nil {
		return err
	}
	if err := run(ctx, a.Username+":"+a.Password+"\n", "chpasswd"); err != nil {
		_ = run(ctx, "", "userdel", "-r", a.Username) // don't leave a half-made account
		return err
	}
	return nil
}

// guard refuses changes to root and to non-person system accounts.
func (s *System) guard(ctx context.Context, username string) (*Account, error) {
	a, err := s.Lookup(ctx, username)
	if err != nil {
		return nil, err
	}
	if a.Root {
		return nil, ErrProtected
	}
	return a, nil
}

func (s *System) Delete(ctx context.Context, username string, removeHome bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.guard(ctx, username); err != nil {
		return err
	}
	args := []string{username}
	if removeHome {
		args = []string{"-r", username}
	}
	return run(ctx, "", "userdel", args...)
}

func (s *System) SetRole(ctx context.Context, username, role string) error {
	if !validRole(role) {
		return errors.New("role must be admin or user")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.guard(ctx, username)
	if err != nil {
		return err
	}
	if a.Role == role {
		return nil
	}
	if role == store.RoleAdmin {
		return run(ctx, "", "gpasswd", "-a", username, s.adminGroup)
	}
	return run(ctx, "", "gpasswd", "-d", username, s.adminGroup)
}

func (s *System) SetPassword(ctx context.Context, username, password string) error {
	if strings.ContainsAny(password, "\n") {
		return errors.New("passwords can't contain line breaks")
	}
	// Any person account, root included (root changes its own in Security).
	if _, err := s.Lookup(ctx, username); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return run(ctx, username+":"+password+"\n", "chpasswd")
}

func (s *System) SetDisabled(ctx context.Context, username string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.guard(ctx, username); err != nil {
		return err
	}
	flag := "-U"
	if disabled {
		flag = "-L"
	}
	return run(ctx, "", "usermod", flag, username)
}
