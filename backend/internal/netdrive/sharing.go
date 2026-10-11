package netdrive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/spaces"
	"alfaos/alfad/internal/store"
)

// Sharing: shared folders (see package spaces) offered to other devices over
// SMB (Samba, for Windows/macOS/Linux file managers) and WebDAV (built in,
// works from phones and through the NoCapOS address).
//
// Everyone signs in as themselves with their NoCapOS password, and sees the
// shared folders they've been given access to plus their own home. Each
// person gets a real Samba account whose password follows their NoCapOS one.
//
// Before per-person permissions existed there was one sharing account,
// "nocapos", with its own password. It keeps working exactly as before until
// an administrator gives the first person access to a shared folder; from
// then on the shared account is switched off (its Samba account disabled and
// WebDAV refusing it), so nobody keeps a back door into every folder.

// ShareUser is the single shared network account from before people had
// their own.
const ShareUser = "nocapos"

const (
	keyPassword = "share.password" // argon2 hash
	keySMB      = "share.smb"
	keyWebDAV   = "share.webdav"
	confName    = "nocapos.conf"
	includeLine = "include = /etc/samba/nocapos.conf"
)

// UserCheck verifies a person's NoCapOS password for the network services.
// It returns their user name as stored, and whether they're an administrator.
type UserCheck func(ctx context.Context, username, password string) (name string, admin bool, ok bool)

// UseSpaces connects sharing to the shared folders and accounts, and keeps
// Samba in step with them.
func (m *Manager) UseSpaces(sp *spaces.Manager, check UserCheck) {
	m.spaces, m.checkUser = sp, check
	sp.OnChange(func(ctx context.Context) {
		if err := m.ApplySamba(ctx); err != nil {
			m.log.Warn("sharing: update samba", "err", err)
		}
	})
}

// Share is a shared folder as the Files sidebar sees it.
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
	// PerUser: people have been given access to folders, so everyone signs
	// in as themselves and the shared account is off.
	PerUser bool `json:"per_user"`
	// SambaReady: Samba can run here (native Linux, installed).
	SambaReady bool `json:"samba_ready"`
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
	out := Sharing{User: ShareUser, PasswordSet: hash != "", SMB: m.flag(ctx, keySMB), WebDAV: m.flag(ctx, keyWebDAV), Shares: []Share{}, SambaReady: m.sambaReady()}
	if m.spaces != nil {
		out.PerUser = m.spaces.PerUserSharing()
		for _, f := range m.spaces.Folders() {
			out.Shares = append(out.Shares, Share{ID: f.ID, Name: f.Name, Root: f.Root, Path: f.Path, ReadOnly: f.ReadOnly, Missing: f.Missing})
		}
	}
	return out, nil
}

func (m *Manager) sambaReady() bool { return m.native() && m.has("samba") }

// legacy reports whether the shared "nocapos" account is in use: it has a
// password and nobody has been given their own access yet.
func (m *Manager) legacy(ctx context.Context) bool {
	hash, _ := m.st.Setting(ctx, keyPassword)
	return hash != "" && (m.spaces == nil || !m.spaces.PerUserSharing())
}

// SetPassword sets the shared account's password (and its Samba account's, when Samba is here).
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
	if m.sambaReady() {
		return m.sambaUser(ctx, ShareUser, pw, true)
	}
	return nil
}

// CheckPassword verifies the shared account's password. It fails once
// people have their own access.
func (m *Manager) CheckPassword(ctx context.Context, user, pw string) bool {
	if !strings.EqualFold(user, ShareUser) || pw == "" || !m.legacy(ctx) {
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
	return m.ApplySamba(ctx)
}

func boolFlag(b bool) string {
	if b {
		return "1"
	}
	return ""
}

// AddShare shares a folder (the "Share on Network…" flow in Files).
func (m *Manager) AddShare(ctx context.Context, name, root, path string, readOnly bool) (Share, error) {
	if m.spaces == nil {
		return Share{}, errors.New("sharing isn't ready")
	}
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	f, err := m.spaces.CreateFolder(ctx, spaces.FolderInput{Name: &name, Root: root, Path: path, ReadOnly: &readOnly})
	if err != nil {
		return Share{}, err
	}
	return Share{ID: f.ID, Name: f.Name, Root: f.Root, Path: f.Path, ReadOnly: f.ReadOnly}, nil
}

func (m *Manager) SetShareReadOnly(ctx context.Context, id int64, ro bool) error {
	if m.spaces == nil {
		return store.ErrNotFound
	}
	_, err := m.spaces.UpdateFolder(ctx, id, spaces.FolderInput{ReadOnly: &ro})
	return err
}

// DeleteShare stops sharing a folder; its files stay.
func (m *Manager) DeleteShare(ctx context.Context, id int64) error {
	if m.spaces == nil {
		return store.ErrNotFound
	}
	return m.spaces.DeleteFolder(ctx, id, false)
}

// ---------- Samba configuration ----------

// SambaShare is one [share] section.
type SambaShare struct {
	Name, Comment, Path string
	Valid, Read, Write  []string // user names
	Browseable, Recycle bool
}

// SambaConfig is everything NoCapOS's Samba include file holds.
type SambaConfig struct {
	SMB    bool         // SMB sharing is on
	Shares []SambaShare //
	Homes  bool         // offer each person their home as a private share
	// HomesPath holds everyone's home when homes aren't the Linux users' own
	// ("" = Samba's default: the home directory from /etc/passwd).
	HomesPath string
	Skipped   []string // notes on folders left out
}

// RenderSamba writes the include file. Writes happen as root (like Files
// does), so access is decided by Samba's valid users / read list / write
// list, which come from NoCapOS's permissions.
func RenderSamba(c SambaConfig) string {
	var b strings.Builder
	b.WriteString("# Managed by NoCapOS (Settings → File Sharing). Changes here are overwritten.\n")
	if !c.SMB {
		return b.String()
	}
	for _, note := range c.Skipped {
		fmt.Fprintf(&b, "# %s\n", oneLine(note))
	}
	if c.Homes {
		b.WriteString("\n[homes]\n")
		b.WriteString("   comment = Home\n")
		if c.HomesPath != "" {
			fmt.Fprintf(&b, "   path = %s/%%S\n", strings.TrimSuffix(filepath.ToSlash(c.HomesPath), "/"))
			b.WriteString("   force user = root\n   force group = root\n")
		}
		// Linux homes: Samba serves each person's own home directory as
		// that person, so files they make are theirs.
		b.WriteString("   valid users = %S\n")
		b.WriteString("   browseable = no\n   read only = no\n")
		b.WriteString("   create mask = 0640\n   directory mask = 0750\n")
		b.WriteString("   veto files = /.recycle/.upload-*.part/\n   delete veto files = no\n")
		writeRecycle(&b)
	}
	for _, sh := range c.Shares {
		fmt.Fprintf(&b, "\n[%s]\n", sh.Name)
		if sh.Comment != "" {
			fmt.Fprintf(&b, "   comment = %s\n", oneLine(sh.Comment))
		}
		fmt.Fprintf(&b, "   path = %s\n", filepath.ToSlash(sh.Path))
		fmt.Fprintf(&b, "   valid users = %s\n", strings.Join(sh.Valid, " "))
		if len(sh.Read) > 0 {
			fmt.Fprintf(&b, "   read list = %s\n", strings.Join(sh.Read, " "))
		}
		if len(sh.Write) > 0 {
			fmt.Fprintf(&b, "   write list = %s\n", strings.Join(sh.Write, " "))
		}
		b.WriteString("   read only = yes\n")
		fmt.Fprintf(&b, "   browseable = %s\n", yesNo(sh.Browseable))
		b.WriteString("   force user = root\n   force group = root\n")
		b.WriteString("   create mask = 0664\n   directory mask = 0775\n")
		b.WriteString("   veto files = /.recycle/.upload-*.part/\n   delete veto files = no\n")
		if sh.Recycle {
			writeRecycle(&b)
		}
	}
	return b.String()
}

// writeRecycle sends deletes into the same .recycle folder Files uses.
func writeRecycle(b *strings.Builder) {
	b.WriteString("   vfs objects = recycle\n   recycle:repository = .recycle\n   recycle:keeptree = yes\n   recycle:versions = yes\n   recycle:touch = yes\n")
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// oneLine keeps a value on one config line without control characters.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.TrimSpace(s)
}

// sambaConfig gathers NoCapOS's shares and permissions for Samba.
func (m *Manager) sambaConfig(ctx context.Context) (SambaConfig, error) {
	c := SambaConfig{SMB: m.flag(ctx, keySMB)}
	if !c.SMB || m.spaces == nil {
		return c, nil
	}
	people, err := m.spaces.People(ctx)
	if err != nil {
		return c, err
	}
	legacy := m.legacy(ctx)
	if m.spaces.LinuxHomes() {
		c.Homes = true
	} else if p := m.spaces.HomesPath(); p != "" && !strings.ContainsAny(p, "%\n") {
		c.Homes, c.HomesPath = true, p
	}
	for _, f := range m.spaces.NetFolders(people) {
		if strings.ContainsAny(f.FullPath, "%\n") {
			c.Skipped = append(c.Skipped, f.Name+": its path can't be written in a Samba configuration")
			continue
		}
		sh := SambaShare{Name: f.Name, Comment: f.Description, Path: f.FullPath, Browseable: f.Network, Recycle: f.Recycle}
		names := make([]string, 0, len(f.Perms))
		for name := range f.Perms {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
		for _, name := range names {
			sh.Valid = append(sh.Valid, name)
			if f.Perms[name] == spaces.ReadWrite {
				sh.Write = append(sh.Write, name)
			} else {
				sh.Read = append(sh.Read, name)
			}
		}
		if legacy {
			sh.Valid = append(sh.Valid, ShareUser)
			if f.ReadOnly {
				sh.Read = append(sh.Read, ShareUser)
			} else {
				sh.Write = append(sh.Write, ShareUser)
			}
		}
		if len(sh.Valid) == 0 {
			// An empty list would let any Samba user in.
			c.Skipped = append(c.Skipped, f.Name+": nobody has access yet")
			continue
		}
		c.Shares = append(c.Shares, sh)
	}
	return c, nil
}

// ApplySamba writes NoCapOS's Samba shares, makes sure smb.conf includes them,
// and reloads Samba. Nothing happens where Samba isn't installed.
func (m *Manager) ApplySamba(ctx context.Context) error {
	if !m.sambaReady() {
		return nil
	}
	m.sambaMu.Lock()
	defer m.sambaMu.Unlock()
	c, err := m.sambaConfig(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.sambaDir, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(m.sambaDir, confName), []byte(RenderSamba(c)), 0o644); err != nil {
		return err
	}
	if err := m.ensureInclude(); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if hash, _ := m.st.Setting(ctx, keyPassword); hash != "" && !m.legacy(ctx) {
		// People have their own access now: retire the shared account.
		_, _ = m.run(cctx, "", "smbpasswd", "-d", ShareUser)
	}
	unit := m.sambaUnit(cctx)
	if c.SMB {
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

// ---------- per-person Samba accounts ----------

// SyncUser gives a person a Samba account with this password, so they sign
// in over SMB with their NoCapOS password. Samba needs a Linux account of
// the same name: Linux users already have one; for NoCapOS's own accounts a
// login-less system account is made. A no-op where Samba can't run.
func (m *Manager) SyncUser(ctx context.Context, username, pw string) error {
	if !m.sambaReady() || strings.EqualFold(username, ShareUser) {
		return nil
	}
	return m.sambaUser(ctx, username, pw, false)
}

// SetUserEnabled turns a person's Samba account on or off (disabled accounts).
func (m *Manager) SetUserEnabled(ctx context.Context, username string, enabled bool) {
	if !m.sambaReady() {
		return
	}
	flag := "-d"
	if enabled {
		flag = "-e"
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _ = m.run(cctx, "", "smbpasswd", flag, username)
}

// RemoveUser deletes a person's Samba account (their files stay).
func (m *Manager) RemoveUser(ctx context.Context, username string) {
	if !m.sambaReady() {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _ = m.run(cctx, "", "smbpasswd", "-x", username)
}

// sambaUser makes sure a Linux account exists (system: no shell, no home)
// and sets its Samba password.
func (m *Manager) sambaUser(ctx context.Context, username, pw string, system bool) error {
	if strings.ContainsAny(pw, "\n\r") {
		return errors.New("passwords can't contain line breaks")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := m.run(cctx, "", "id", "-u", username); err != nil {
		nologin := "/usr/sbin/nologin"
		if _, err := os.Stat(nologin); err != nil {
			nologin = "/sbin/nologin"
		}
		what := "the sharing account"
		if !system {
			what = "a file sharing account for " + username
		}
		if out, err := m.run(cctx, "", "useradd", "--system", "--no-create-home", "--shell", nologin, username); err != nil {
			return fmt.Errorf("create %s: %s", what, lastLine(out))
		}
	}
	if out, err := m.run(cctx, pw+"\n"+pw+"\n", "smbpasswd", "-a", "-s", username); err != nil {
		return fmt.Errorf("set the Samba password: %s", lastLine(out))
	}
	if out, err := m.run(cctx, "", "smbpasswd", "-e", username); err != nil {
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
