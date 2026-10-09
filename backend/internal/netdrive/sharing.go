package netdrive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

// Sharing: folders from the storage locations, shared over SMB (Samba, for
// Windows/macOS/Linux file managers) and WebDAV (built in, works from phones
// and through the NoCapOS address). One sharing account, "nocapos", with its
// own password, so the admin's own password never goes into other devices.

// ShareUser is the account other devices sign in with.
const ShareUser = "nocapos"

const (
	keyPassword = "share.password" // argon2 hash
	keySMB      = "share.smb"
	keyWebDAV   = "share.webdav"
	confName    = "nocapos.conf"
	includeLine = "include = /etc/samba/nocapos.conf"
)

// Share is a shared folder as the UI sees it.
type Share struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Root     string `json:"root"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"read_only"`
	Missing  bool   `json:"missing,omitempty"` // the folder or its location is gone
}

// Sharing is the sharing setup as the UI sees it.
type Sharing struct {
	User        string  `json:"user"`
	PasswordSet bool    `json:"password_set"`
	SMB         bool    `json:"smb"`
	WebDAV      bool    `json:"webdav"`
	Shares      []Share `json:"shares"`
}

func (m *Manager) flag(ctx context.Context, key string) bool {
	v, _ := m.st.Setting(ctx, key)
	return v == "1"
}

func (m *Manager) Sharing(ctx context.Context) (Sharing, error) {
	hash, err := m.st.Setting(ctx, keyPassword)
	if err != nil {
		return Sharing{}, err
	}
	shares, err := m.shares(ctx)
	if err != nil {
		return Sharing{}, err
	}
	return Sharing{User: ShareUser, PasswordSet: hash != "", SMB: m.flag(ctx, keySMB), WebDAV: m.flag(ctx, keyWebDAV), Shares: shares}, nil
}

// sharePath is where a share's folder is on disk ("" if its location is gone).
func (m *Manager) sharePath(sh *store.NetShare) string {
	r, err := m.files.Root(sh.Root)
	if err != nil {
		return ""
	}
	return filepath.Join(r.Path, filepath.FromSlash(strings.TrimPrefix(sh.Path, "/")))
}

func (m *Manager) shares(ctx context.Context) ([]Share, error) {
	rows, err := m.st.NetShares(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Share, 0, len(rows))
	for _, sh := range rows {
		p := m.sharePath(sh)
		st, err := os.Stat(p)
		out = append(out, Share{ID: sh.ID, Name: sh.Name, Root: sh.Root, Path: sh.Path, ReadOnly: sh.ReadOnly, Missing: p == "" || err != nil || !st.IsDir()})
	}
	return out, nil
}

// SetPassword sets the sharing password (and the Samba account's, when Samba is here).
func (m *Manager) SetPassword(ctx context.Context, pw string) error {
	if len(pw) < 8 || len(pw) > 128 || strings.ContainsAny(pw, "\n\r") {
		return errors.New("use a password of 8 to 128 characters")
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := m.st.SetSetting(ctx, keyPassword, hash); err != nil {
		return err
	}
	m.dav.reset()
	if m.native() && m.has("samba") {
		return m.sambaUser(ctx, pw)
	}
	return nil
}

// CheckPassword verifies the sharing account's password.
func (m *Manager) CheckPassword(ctx context.Context, user, pw string) bool {
	if !strings.EqualFold(user, ShareUser) || pw == "" {
		return false
	}
	hash, err := m.st.Setting(ctx, keyPassword)
	if err != nil || hash == "" {
		return false
	}
	ok, err := auth.VerifyPassword(pw, hash)
	return err == nil && ok
}

// SetProtocols turns SMB and/or WebDAV sharing on or off.
func (m *Manager) SetProtocols(ctx context.Context, smb, webdav *bool) error {
	hash, _ := m.st.Setting(ctx, keyPassword)
	if ((smb != nil && *smb) || (webdav != nil && *webdav)) && hash == "" {
		return errors.New("set a sharing password first")
	}
	if smb != nil {
		if *smb && !m.native() {
			return errors.New("SMB sharing needs NoCapOS installed on Linux")
		}
		if *smb && !m.has("samba") {
			return errors.New("install Samba first")
		}
		if err := m.st.SetSetting(ctx, keySMB, boolFlag(*smb)); err != nil {
			return err
		}
	}
	if webdav != nil {
		if err := m.st.SetSetting(ctx, keyWebDAV, boolFlag(*webdav)); err != nil {
			return err
		}
	}
	return m.applySamba(ctx)
}

func boolFlag(b bool) string {
	if b {
		return "1"
	}
	return ""
}

var shareNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]{0,39}$`)

// reservedShares are names Samba (or common setups) use for themselves.
var reservedShares = map[string]bool{"global": true, "homes": true, "printers": true, "print$": true, "ipc$": true}

// AddShare shares a folder.
func (m *Manager) AddShare(ctx context.Context, name, root, path string, readOnly bool) (Share, error) {
	name = strings.TrimSpace(name)
	if !shareNameRe.MatchString(name) || reservedShares[strings.ToLower(name)] {
		return Share{}, errors.New("name the share with letters, numbers, spaces, - _ or . (up to 40)")
	}
	if root == "system" {
		return Share{}, errors.New("share folders from your drives; the whole-server System view can't be shared")
	}
	r, err := m.files.Root(root)
	if err != nil {
		return Share{}, errors.New("choose a storage location")
	}
	rel, err := files.Clean(path)
	if err != nil {
		return Share{}, err
	}
	full := filepath.Join(r.Path, filepath.FromSlash(rel))
	if st, err := os.Stat(full); err != nil || !st.IsDir() {
		return Share{}, errors.New("that folder doesn't exist")
	}
	sh := &store.NetShare{Name: name, Root: root, Path: "/" + strings.TrimPrefix(rel, "."), ReadOnly: readOnly}
	if sh.Path == "/." || sh.Path == "" {
		sh.Path = "/"
	}
	id, err := m.st.CreateNetShare(ctx, sh)
	if errors.Is(err, store.ErrExists) {
		return Share{}, fmt.Errorf("there's already a share called %q", name)
	}
	if err != nil {
		return Share{}, err
	}
	if err := m.applySamba(ctx); err != nil {
		m.log.Warn("sharing: update samba", "err", err)
	}
	return Share{ID: id, Name: sh.Name, Root: sh.Root, Path: sh.Path, ReadOnly: sh.ReadOnly}, nil
}

func (m *Manager) SetShareReadOnly(ctx context.Context, id int64, ro bool) error {
	if err := m.st.SetNetShareReadOnly(ctx, id, ro); err != nil {
		return err
	}
	return m.applySamba(ctx)
}

func (m *Manager) DeleteShare(ctx context.Context, id int64) error {
	if err := m.st.DeleteNetShare(ctx, id); err != nil {
		return err
	}
	return m.applySamba(ctx)
}

// sambaConfig renders NoCapOS's share definitions for Samba.
func (m *Manager) sambaConfig(ctx context.Context) (string, error) {
	var b strings.Builder
	b.WriteString("# Managed by NoCapOS (Settings → File Sharing). Changes here are overwritten.\n")
	if !m.flag(ctx, keySMB) {
		return b.String(), nil
	}
	rows, err := m.st.NetShares(ctx)
	if err != nil {
		return "", err
	}
	for _, sh := range rows {
		p := m.sharePath(sh)
		if p == "" {
			continue
		}
		ro := "no"
		if sh.ReadOnly {
			ro = "yes"
		}
		fmt.Fprintf(&b, "\n[%s]\n", sh.Name)
		fmt.Fprintf(&b, "   path = %s\n", p)
		fmt.Fprintf(&b, "   valid users = %s\n", ShareUser)
		b.WriteString("   force user = root\n   force group = root\n")
		fmt.Fprintf(&b, "   read only = %s\n", ro)
		b.WriteString("   browseable = yes\n   create mask = 0664\n   directory mask = 0775\n")
		b.WriteString("   veto files = /.recycle/.upload-*.part/\n   delete veto files = no\n")
	}
	return b.String(), nil
}

// applySamba writes NoCapOS's Samba shares, makes sure smb.conf includes them,
// and reloads Samba. Nothing happens where Samba isn't installed.
func (m *Manager) applySamba(ctx context.Context) error {
	if !m.native() || !m.has("samba") {
		return nil
	}
	conf, err := m.sambaConfig(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.sambaDir, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(m.sambaDir, confName), []byte(conf), 0o644); err != nil {
		return err
	}
	if err := m.ensureInclude(); err != nil {
		return err
	}
	unit := m.sambaUnit(ctx)
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if m.flag(ctx, keySMB) {
		if out, err := m.run(cctx, "", "systemctl", "enable", "--now", unit); err != nil {
			return fmt.Errorf("start Samba: %s", lastLine(out))
		}
		if out, err := m.run(cctx, "", "systemctl", "reload-or-restart", unit); err != nil {
			return fmt.Errorf("reload Samba: %s", lastLine(out))
		}
	} else if out, err := m.run(cctx, "", "systemctl", "try-reload-or-restart", unit); err != nil {
		m.log.Warn("sharing: reload samba", "out", lastLine(out))
	}
	return nil
}

// sambaUnit is "smbd" on Debian/Ubuntu and "smb" on Fedora/Arch/openSUSE.
func (m *Manager) sambaUnit(ctx context.Context) string {
	if out, err := m.run(ctx, "", "systemctl", "list-unit-files", "smbd.service", "--no-legend"); err == nil && strings.Contains(out, "smbd.service") {
		return "smbd"
	}
	return "smb"
}

// ensureInclude adds NoCapOS's include line to smb.conf once (creating a
// minimal smb.conf if there is none). The rest of the file is left alone.
func (m *Manager) ensureInclude() error {
	path := filepath.Join(m.sambaDir, "smb.conf")
	cur, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.Contains(string(cur), includeLine) {
		return nil
	}
	text := string(cur)
	if len(cur) == 0 {
		text = "[global]\n   workgroup = WORKGROUP\n   server string = NoCapOS\n   server min protocol = SMB2\n"
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "\n# Shares managed by NoCapOS\n" + includeLine + "\n"
	return writeFileAtomic(path, []byte(text), 0o644)
}

// sambaUser makes the "nocapos" system account (no shell, no home) and sets its Samba password.
func (m *Manager) sambaUser(ctx context.Context, pw string) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := m.run(cctx, "", "id", "-u", ShareUser); err != nil {
		nologin := "/usr/sbin/nologin"
		if _, err := os.Stat(nologin); err != nil {
			nologin = "/sbin/nologin"
		}
		if out, err := m.run(cctx, "", "useradd", "--system", "--no-create-home", "--shell", nologin, ShareUser); err != nil {
			return fmt.Errorf("create the sharing account: %s", lastLine(out))
		}
	}
	if out, err := m.run(cctx, pw+"\n"+pw+"\n", "smbpasswd", "-a", "-s", ShareUser); err != nil {
		return fmt.Errorf("set the Samba password: %s", lastLine(out))
	}
	if out, err := m.run(cctx, "", "smbpasswd", "-e", ShareUser); err != nil {
		return fmt.Errorf("enable the Samba account: %s", lastLine(out))
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".nocapos-tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
