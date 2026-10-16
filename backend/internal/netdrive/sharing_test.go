package netdrive

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alfaos/alfad/internal/spaces"
)

func TestRenderSambaGolden(t *testing.T) {
	got := RenderSamba(SambaConfig{
		SMB:       true,
		Homes:     true,
		HomesPath: "/srv/nocap/homes",
		Skipped:   []string{"Empty: nobody has access yet"},
		Shares: []SambaShare{
			{Name: "Media", Comment: "Movies\nand music", Path: "/srv/nocap/Media", Valid: []string{"admin", "alex", "sam"}, Read: []string{"sam"}, Write: []string{"admin", "alex"}, Browseable: true, Recycle: true},
			{Name: "Hidden", Path: "/srv/nocap/Hidden", Valid: []string{"admin"}, Write: []string{"admin"}},
		},
	})
	want := `# Managed by NoCapOS (Settings → File Sharing). Changes here are overwritten.
# Empty: nobody has access yet

[homes]
   comment = Home
   path = /srv/nocap/homes/%S
   force user = root
   force group = root
   valid users = %S
   invalid users = root
   browseable = no
   read only = no
   create mask = 0640
   directory mask = 0750
   veto files = /.recycle/.upload-*.part/
   delete veto files = no
   vfs objects = recycle
   recycle:repository = .recycle
   recycle:keeptree = yes
   recycle:versions = yes
   recycle:touch = yes

[Media]
   comment = Movies and music
   path = /srv/nocap/Media
   valid users = admin alex sam
   invalid users = root
   read list = sam
   write list = admin alex
   read only = yes
   browseable = yes
   force user = root
   force group = root
   create mask = 0664
   directory mask = 0775
   veto files = /.recycle/.upload-*.part/
   delete veto files = no
   vfs objects = recycle
   recycle:repository = .recycle
   recycle:keeptree = yes
   recycle:versions = yes
   recycle:touch = yes

[Hidden]
   path = /srv/nocap/Hidden
   valid users = admin
   invalid users = root
   write list = admin
   read only = yes
   browseable = no
   force user = root
   force group = root
   create mask = 0664
   directory mask = 0775
   veto files = /.recycle/.upload-*.part/
   delete veto files = no
`
	if got != want {
		t.Errorf("samba config:\n%s\nwant:\n%s", got, want)
	}

	// Linux homes: Samba's own [homes] (the passwd home, served as the person).
	linux := RenderSamba(SambaConfig{SMB: true, Homes: true})
	if strings.Contains(linux, "path =") || strings.Contains(linux, "force user") || !strings.Contains(linux, "[homes]\n   comment = Home\n   valid users = %S\n") {
		t.Errorf("linux homes:\n%s", linux)
	}
	if off := RenderSamba(SambaConfig{SMB: false, Homes: true, Shares: []SambaShare{{Name: "X"}}}); strings.Contains(off, "[") {
		t.Errorf("SMB off still has shares:\n%s", off)
	}
}

func readConf(t *testing.T, m *Manager) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(m.sambaDir, confName))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSharingSamba(t *testing.T) {
	ctx := context.Background()
	m, h, _ := setup(t)
	if err := m.SetPassword(ctx, "short"); err == nil {
		t.Error("short password accepted")
	}
	if err := m.SetPassword(ctx, "share-pass-1"); err != nil {
		t.Fatal(err)
	}
	if c := h.called("useradd --system"); len(c) != 1 {
		t.Errorf("useradd: %v", h.calls)
	}
	var sawPw bool
	for i, c := range h.calls {
		if strings.HasPrefix(c, "smbpasswd -a -s nocapos") && h.stdin[i] == "share-pass-1\nshare-pass-1\n" {
			sawPw = true
		}
	}
	if !sawPw {
		t.Error("samba password not set through stdin")
	}
	if !m.CheckPassword(ctx, "NoCapOS", "share-pass-1") || m.CheckPassword(ctx, "nocapos", "nope") || m.CheckPassword(ctx, "root", "share-pass-1") {
		t.Error("password check")
	}

	on := true
	if err := m.SetProtocols(ctx, &on, &on); err != nil {
		t.Fatal(err)
	}
	media, err := m.AddShare(ctx, "Media", "drive", "/Media", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddShare(ctx, "media", "drive", "/Docs", false); err == nil {
		t.Error("duplicate share name accepted")
	}
	for _, bad := range [][3]string{{"IPC$", "drive", "/Docs"}, {"homes", "drive", "/Docs"}, {"Docs", "system", "/etc"}, {"Docs", "drive", "/nope"}, {"[x]", "drive", "/Docs"}, {"Up", "drive", "/../.."}} {
		// "Up" cleans to the whole drive, which holds people's homes.
		if _, err := m.AddShare(ctx, bad[0], bad[1], bad[2], false); err == nil {
			t.Errorf("share %v accepted", bad)
		}
	}
	docs, err := m.AddShare(ctx, "Docs", "drive", "/Docs", true)
	if err != nil {
		t.Fatal(err)
	}

	// Before anyone has their own access, the shared account keeps working.
	conf := readConf(t, m)
	for _, want := range []string{"[Media]", "valid users = admin nocapos", "write list = admin nocapos", "[Docs]", "read list = admin nocapos", "[homes]"} {
		if !strings.Contains(conf, want) {
			t.Errorf("legacy nocapos.conf lacks %q:\n%s", want, conf)
		}
	}
	main, _ := os.ReadFile(filepath.Join(m.sambaDir, "smb.conf"))
	if strings.Count(string(main), includeLine) != 1 {
		t.Errorf("smb.conf include line count wrong:\n%s", main)
	}
	if len(h.called("systemctl enable --now smbd")) == 0 {
		t.Errorf("samba not started: %v", h.called("systemctl"))
	}

	// Per-person permissions: the shared account is retired.
	if err := m.spaces.SetPermission(ctx, media.ID, "alex", spaces.ReadWrite); err != nil {
		t.Fatal(err)
	}
	if err := m.spaces.SetPermission(ctx, media.ID, "sam", spaces.ReadOnly); err != nil {
		t.Fatal(err)
	}
	if err := m.spaces.SetPermission(ctx, docs.ID, "sam", spaces.ReadWrite); err != nil {
		t.Fatal(err)
	}
	if err := m.spaces.SetPermission(ctx, media.ID, "gone", spaces.ReadWrite); err != nil {
		t.Fatal(err)
	}
	conf = readConf(t, m)
	for _, want := range []string{
		"[Media]\n   path = ", "valid users = admin alex sam\n   invalid users = root\n   read list = sam\n   write list = admin alex\n",
		// Docs is read-only for everyone, whatever their permission.
		"valid users = admin sam\n   invalid users = root\n   read list = admin sam\n   read only = yes",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("per-user nocapos.conf lacks %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "nocapos\n") || strings.Contains(conf, "gone") {
		t.Errorf("shared account or disabled person still configured:\n%s", conf)
	}
	if len(h.called("smbpasswd -d nocapos")) == 0 {
		t.Error("shared Samba account not disabled")
	}
	if m.CheckPassword(ctx, "nocapos", "share-pass-1") {
		t.Error("shared account still accepted after per-person access")
	}

	// People's Samba passwords follow their NoCapOS ones.
	if err := m.SyncUser(ctx, "alex", "alex-new-pass"); err != nil {
		t.Fatal(err)
	}
	var synced bool
	for i, c := range h.calls {
		if c == "smbpasswd -a -s alex" && h.stdin[i] == "alex-new-pass\nalex-new-pass\n" {
			synced = true
		}
	}
	if !synced {
		t.Errorf("alex's Samba password not set: %v", h.called("smbpasswd"))
	}

	off := false
	if err := m.SetProtocols(ctx, &off, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readConf(t, m), "[Media]") {
		t.Error("shares still configured after SMB was turned off")
	}
}

func TestSambaNoopWithoutSamba(t *testing.T) {
	ctx := context.Background()
	m, h, _ := setup(t)
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	on := true
	if err := m.SetProtocols(ctx, &on, nil); err == nil {
		t.Error("SMB turned on without Samba")
	}
	if err := m.SyncUser(ctx, "alex", "alex-pass-1"); err != nil {
		t.Error(err)
	}
	if len(h.called("smbpasswd")) != 0 {
		t.Errorf("ran smbpasswd without Samba: %v", h.calls)
	}
	if sh, _ := m.Sharing(ctx); sh.SambaReady {
		t.Error("Samba reported ready")
	}
}

func TestWebDAV(t *testing.T) {
	ctx := context.Background()
	m, _, drive := setup(t)
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") } // no Samba: WebDAV only
	if err := m.SetPassword(ctx, "share-pass-1"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(m.DAVHandler(ClientIP))
	defer srv.Close()
	do := func(method, path, user, pw, body string, hdr ...string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if user != "" {
			req.SetBasicAuth(user, pw)
		}
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}

	if r := do("PROPFIND", "/dav/", "nocapos", "share-pass-1", "", "Depth", "1"); r.StatusCode != http.StatusNotFound {
		t.Fatalf("WebDAV off: %d", r.StatusCode)
	}
	on := true
	if err := m.SetProtocols(ctx, nil, &on); err != nil {
		t.Fatal(err)
	}
	media, err := m.AddShare(ctx, "Media", "drive", "/Media", false)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := m.AddShare(ctx, "Docs", "drive", "/Docs", true)
	if err != nil {
		t.Fatal(err)
	}

	// The old shared account, while nobody has their own access.
	if r := do("PROPFIND", "/dav/", "", "", "", "Depth", "1"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no password: %d", r.StatusCode)
	}
	if r := do("PROPFIND", "/dav/", "nocapos", "share-pass-1", "", "Depth", "1"); r.StatusCode != http.StatusMultiStatus {
		t.Fatalf("list shares: %d", r.StatusCode)
	}
	if r := do("PUT", "/dav/Media/hello.txt", "nocapos", "share-pass-1", "hi there"); r.StatusCode != http.StatusCreated {
		t.Fatalf("put: %d", r.StatusCode)
	}
	if b, _ := os.ReadFile(filepath.Join(drive, "Media", "hello.txt")); string(b) != "hi there" {
		t.Fatalf("file on disk %q", b)
	}
	if r := do("PUT", "/dav/Docs/nope.txt", "nocapos", "share-pass-1", "x"); r.StatusCode < 400 {
		t.Errorf("write to a read-only share: %d", r.StatusCode)
	}

	// Per-person access.
	if err := m.spaces.SetPermission(ctx, media.ID, "alex", spaces.ReadWrite); err != nil {
		t.Fatal(err)
	}
	if err := m.spaces.SetPermission(ctx, docs.ID, "sam", spaces.ReadOnly); err != nil {
		t.Fatal(err)
	}
	if r := do("PROPFIND", "/dav/", "nocapos", "share-pass-1", "", "Depth", "1"); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("shared account after per-person access: %d", r.StatusCode)
	}
	if r := do("PUT", "/dav/Media/alex.txt", "alex", "alex-pass-1", "mine"); r.StatusCode != http.StatusCreated {
		t.Errorf("alex writes Media: %d", r.StatusCode)
	}
	if r := do("PROPFIND", "/dav/Docs/", "alex", "alex-pass-1", "", "Depth", "1"); r.StatusCode != http.StatusNotFound {
		t.Errorf("alex sees Docs: %d", r.StatusCode)
	}
	if r := do("PROPFIND", "/dav/Docs/", "sam", "sam-pass-1", "", "Depth", "1"); r.StatusCode != http.StatusMultiStatus {
		t.Errorf("sam reads Docs: %d", r.StatusCode)
	}
	if r := do("PUT", "/dav/Docs/x.txt", "sam", "sam-pass-1", "x"); r.StatusCode < 400 {
		t.Errorf("sam writes read-only Docs: %d", r.StatusCode)
	}
	if r := do("PUT", "/dav/Media/x.txt", "sam", "sam-pass-1", "x"); r.StatusCode < 400 {
		t.Errorf("sam writes Media without access: %d", r.StatusCode)
	}
	// Each person's home is theirs alone.
	if r := do("PUT", "/dav/alex/notes.txt", "alex", "alex-pass-1", "private"); r.StatusCode != http.StatusCreated {
		t.Errorf("alex writes home: %d", r.StatusCode)
	}
	if r := do("PROPFIND", "/dav/alex/", "sam", "sam-pass-1", "", "Depth", "1"); r.StatusCode != http.StatusNotFound {
		t.Errorf("sam sees alex's home: %d", r.StatusCode)
	}
	if b, _ := os.ReadFile(filepath.Join(m.spaces.HomePath("alex"), "notes.txt")); string(b) != "private" {
		t.Errorf("home file %q", b)
	}
	// Links and ".." can't lead out of a folder.
	if err := os.Symlink(filepath.Join(drive, "Docs"), filepath.Join(drive, "Media", "escape")); err == nil {
		if r := do("PROPFIND", "/dav/Media/escape/", "alex", "alex-pass-1", "", "Depth", "1"); r.StatusCode < 400 {
			t.Errorf("followed a link out of Media: %d", r.StatusCode)
		}
	}
	if r := do("PUT", "/dav/Media/../Docs/y.txt", "alex", "alex-pass-1", "x"); r.StatusCode < 400 {
		if _, err := os.Stat(filepath.Join(drive, "Docs", "y.txt")); err == nil {
			t.Error("wrote into Docs through ..")
		}
	}
	// Deleting with the recycle bin on keeps a copy.
	if r := do("DELETE", "/dav/Media/alex.txt", "alex", "alex-pass-1", ""); r.StatusCode != http.StatusNoContent {
		t.Errorf("delete a file: %d", r.StatusCode)
	}
	if ents, _ := os.ReadDir(filepath.Join(drive, "Media", ".recycle")); len(ents) != 1 {
		t.Errorf("recycle bin has %d items", len(ents))
	}

	// Guessing gets slowed down.
	for i := 0; i < 5; i++ {
		do("PROPFIND", "/dav/", "alex", "guess", "", "Depth", "0")
	}
	if r := do("PROPFIND", "/dav/", "alex", "alex-pass-1", "", "Depth", "0"); r.StatusCode != http.StatusTooManyRequests {
		t.Errorf("after 5 wrong passwords: %d", r.StatusCode)
	}
}
