package netdrive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebDAVServesDownloadsOnly(t *testing.T) {
	ctx := context.Background()
	m, _, drive := setup(t)
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if err := m.SetPassword(ctx, "share-pass-1"); err != nil {
		t.Fatal(err)
	}
	on := true
	if err := m.SetProtocols(ctx, nil, &on); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddShare(ctx, "Media", "drive", "/Media", false); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"evil.html": "<script>alert(1)</script>", "evil.svg": "<svg onload=alert(1)>", "evil.js": "alert(1)", "pic.png": "png"} {
		if err := os.WriteFile(filepath.Join(drive, "Media", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(m.DAVHandler(ClientIP))
	defer srv.Close()
	for _, tc := range []struct {
		method, name, wantType string
	}{
		{"GET", "evil.html", "application/octet-stream"},
		{"HEAD", "evil.html", "application/octet-stream"},
		{"POST", "evil.html", "application/octet-stream"},
		{"GET", "evil.svg", "application/octet-stream"},
		{"GET", "evil.js", "application/octet-stream"},
		{"GET", "pic.png", "image/png"},
	} {
		req, _ := http.NewRequest(tc.method, srv.URL+"/dav/Media/"+tc.name, nil)
		req.SetBasicAuth("nocapos", "share-pass-1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: %d", tc.method, tc.name, res.StatusCode)
		}
		h := res.Header
		if got := h.Get("Content-Type"); got != tc.wantType {
			t.Errorf("%s %s: Content-Type %q, want %q", tc.method, tc.name, got, tc.wantType)
		}
		if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
			t.Errorf("%s %s: Content-Disposition %q", tc.method, tc.name, h.Get("Content-Disposition"))
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s %s: headers %v", tc.method, tc.name, h)
		}
	}
}

func TestDavAuthEvictsOldestNotEveryone(t *testing.T) {
	a := newDavAuth()
	for i := 0; i < 5; i++ {
		a.failed("attacker")
	}
	if !a.blocked("attacker") {
		t.Fatal("attacker not blocked")
	}
	for i := 0; i < maxDavFails+50; i++ {
		a.failed(fmt.Sprintf("10.0.%d.%d", i/250, i%250))
	}
	if len(a.fails) > maxDavFails {
		t.Errorf("table grew to %d", len(a.fails))
	}
	// A flood of other addresses doesn't lift the attacker's wait (before,
	// the whole table was wiped at 10,000 entries).
	if !a.blocked("attacker") {
		t.Error("flood of addresses lifted the attacker's wait")
	}
}

func TestSambaAccountsNeverForRootOrDisabled(t *testing.T) {
	ctx := context.Background()
	m, h, _ := setup(t)
	on := true
	if err := m.SetProtocols(ctx, &on, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root", "gone", "nobody-here"} {
		if err := m.SyncUser(ctx, name, "some-pass-1"); err != nil {
			t.Fatal(err)
		}
		if err := m.KeepUser(ctx, name, "some-pass-1"); err != nil {
			t.Fatal(err)
		}
		if c := h.called("smbpasswd -a -s " + name); len(c) != 0 {
			t.Errorf("Samba account made for %s: %v", name, c)
		}
		if c := h.called("smbpasswd -e " + name); len(c) != 0 {
			t.Errorf("Samba account enabled for %s: %v", name, c)
		}
	}
	m.SetUserEnabled(ctx, "root", true)
	if c := h.called("smbpasswd -e root"); len(c) != 0 {
		t.Errorf("root enabled: %v", c)
	}

	// An administrator signing in doesn't get one without opting in…
	if err := m.KeepUser(ctx, "admin", "admin-pass-1"); err != nil {
		t.Fatal(err)
	}
	if c := h.called("smbpasswd -a -s admin"); len(c) != 0 {
		t.Errorf("admin got a Samba account just by signing in: %v", c)
	}
	// …but one set up by an administrator is kept in step afterwards.
	if err := m.SyncUser(ctx, "admin", "admin-pass-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.KeepUser(ctx, "admin", "admin-pass-2"); err != nil {
		t.Fatal(err)
	}
	if c := h.called("smbpasswd -a -s admin"); len(c) != 2 {
		t.Errorf("admin Samba account: %v", c)
	}
	// A person with a home gets theirs on sign-in, and NoCapOS remembers the
	// accounts it made for uninstalling.
	if err := m.KeepUser(ctx, "alex", "alex-pass-1"); err != nil {
		t.Fatal(err)
	}
	if !m.listed(sambaUsersFile, "alex") || !m.listed(sambaUsersFile, "admin") || m.listed(sambaUsersFile, "root") {
		t.Errorf("remembered accounts wrong")
	}

	conf := RenderSamba(SambaConfig{SMB: true, Homes: true, Shares: []SambaShare{{Name: "X", Path: "/x", Valid: []string{"alex"}}}})
	if strings.Count(conf, "invalid users = root") != 2 {
		t.Errorf("root not refused everywhere:\n%s", conf)
	}
}
