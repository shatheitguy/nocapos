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
	"alfaos/alfad/internal/store"
)

func TestRenderSambaGuest(t *testing.T) {
	got := RenderSamba(SambaConfig{SMB: true, Shares: []SambaShare{
		{Name: "Public", Path: "/srv/Public", Write: []string{"admin"}, Browseable: true, Guest: store.GuestRO},
		{Name: "Dropbox", Path: "/srv/Dropbox", Write: []string{"admin", "sam"}, Browseable: true, Guest: store.GuestRW},
		{Name: "Private", Path: "/srv/Private", Valid: []string{"admin"}, Write: []string{"admin"}},
	}})
	for _, want := range []string{
		"[global]\n   map to guest = Bad User\n   guest account = nobody\n",
		"[Public]\n   path = /srv/Public\n   guest ok = yes\n   write list = admin\n   read only = yes\n",
		"[Dropbox]\n   path = /srv/Dropbox\n   guest ok = yes\n   write list = admin sam\n   read only = no\n",
		"[Private]\n   path = /srv/Private\n   valid users = admin\n   invalid users = root\n   write list = admin\n   read only = yes\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(got, "guest ok") != 2 {
		t.Errorf("guest ok on a private share:\n%s", got)
	}
	// No guest shares: nothing global, nothing open.
	plain := RenderSamba(SambaConfig{SMB: true, Shares: []SambaShare{{Name: "Private", Path: "/p", Valid: []string{"admin"}}}})
	if strings.Contains(plain, "[global]") || strings.Contains(plain, "guest") {
		t.Errorf("guest settings without guest shares:\n%s", plain)
	}
}

func TestGuestModeAndConfig(t *testing.T) {
	for _, c := range []struct {
		f    store.SharedFolder
		want string
	}{
		{store.SharedFolder{}, ""},
		{store.SharedFolder{Guest: "ro"}, "ro"},
		{store.SharedFolder{Guest: "rw"}, "rw"},
		{store.SharedFolder{Guest: "rw", ReadOnly: true}, "ro"}, // read only for everyone wins
	} {
		if got := guestMode(c.f); got != c.want {
			t.Errorf("%+v → %q, want %q", c.f, got, c.want)
		}
	}

	ctx := context.Background()
	m, _, _ := setup(t)
	on := true
	if err := m.SetProtocols(ctx, &on, &on); err != nil {
		t.Fatal(err)
	}
	media, err := m.AddShare(ctx, "Media", "drive", "/Media", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.spaces.SetPermission(ctx, media.ID, "sam", spaces.ReadOnly); err != nil {
		t.Fatal(err)
	}
	if err := m.SetShareGuest(ctx, media.ID, "everyone"); err == nil {
		t.Error("bad guest mode accepted")
	}
	docs, err := m.AddShare(ctx, "Docs", "drive", "/Docs", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetShareGuest(ctx, docs.ID, store.GuestRO); err != nil {
		t.Fatal(err)
	}
	if err := m.SetShareGuest(ctx, media.ID, store.GuestRW); err != nil {
		t.Fatal(err)
	}
	conf := readConf(t, m)
	for _, want := range []string{
		"map to guest = Bad User",
		"[Docs]\n   path = ", "guest ok = yes\n   write list = admin\n   read only = yes",
		// rw for guests: sam (read only before) may write like anyone else.
		"[Media]\n   path = ",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("nocapos.conf lacks %q:\n%s", want, conf)
		}
	}
	mediaConf := conf[strings.Index(conf, "[Media]"):]
	if !strings.Contains(mediaConf, "read only = no") || strings.Contains(mediaConf, "read list") || !strings.Contains(mediaConf, "write list = admin sam") {
		t.Errorf("Media section:\n%s", mediaConf)
	}
	sh, _ := m.Sharing(ctx)
	for _, s := range sh.Shares {
		if (s.Name == "Docs" && s.Guest != "ro") || (s.Name == "Media" && s.Guest != "rw") {
			t.Errorf("sharing view: %+v", s)
		}
	}
	if err := m.SetShareGuest(ctx, docs.ID, ""); err != nil {
		t.Fatal(err)
	}
	conf = readConf(t, m)
	if i := strings.Index(conf, "[Docs]"); i < 0 || !strings.HasPrefix(conf[i:], "[Docs]\n   path = ") ||
		!strings.Contains(conf[i:], "valid users = admin\n") || strings.Contains(conf[i:strings.Index(conf, "[Media]")], "guest ok") {
		t.Errorf("Docs still open to guests after guest access went off:\n%s", conf)
	}
}

func TestWebDAVGuest(t *testing.T) {
	ctx := context.Background()
	m, _, drive := setup(t)
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") } // WebDAV only
	on := true
	if err := m.SetProtocols(ctx, nil, &on); err != nil {
		t.Fatal(err)
	}
	media, _ := m.AddShare(ctx, "Media", "drive", "/Media", false)
	docs, _ := m.AddShare(ctx, "Docs", "drive", "/Docs", false)
	if _, err := m.AddShare(ctx, "Private", "drive", "/Media/Movies", false); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(m.DAVHandler(ClientIP))
	defer srv.Close()
	do := func(method, path, body string) int {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Depth", "1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := do("PROPFIND", "/dav/Docs/", ""); c != http.StatusUnauthorized {
		t.Fatalf("guest before guest access: %d", c)
	}
	_ = m.SetShareGuest(ctx, docs.ID, store.GuestRO)
	_ = m.SetShareGuest(ctx, media.ID, store.GuestRW)

	if c := do("PROPFIND", "/dav/Docs/", ""); c != http.StatusMultiStatus {
		t.Errorf("guest reads Docs: %d", c)
	}
	if c := do("PUT", "/dav/Docs/x.txt", "x"); c < 400 {
		t.Errorf("guest wrote to read-only Docs: %d", c)
	}
	if c := do("PUT", "/dav/Media/drop.txt", "hello"); c != http.StatusCreated {
		t.Errorf("guest writes Media: %d", c)
	}
	if b, _ := os.ReadFile(filepath.Join(drive, "Media", "drop.txt")); string(b) != "hello" {
		t.Errorf("file on disk %q", b)
	}
	// Only that folder: not the top level, not other folders, not out through "..".
	for _, p := range []string{"/dav/", "/dav/Private/", "/dav/alex/"} {
		if c := do("PROPFIND", p, ""); c != http.StatusUnauthorized {
			t.Errorf("guest %s: %d", p, c)
		}
	}
	if c := do("PUT", "/dav/Media/../Private/x.txt", "x"); c < 400 {
		t.Errorf("guest escaped with ..: %d", c)
	}
	req, _ := http.NewRequest("MOVE", srv.URL+"/dav/Media/drop.txt", nil)
	req.Header.Set("Destination", srv.URL+"/dav/Private/drop.txt")
	if res, err := http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
		if res.StatusCode < 400 {
			t.Errorf("guest moved a file into Private: %d", res.StatusCode)
		}
	}
	// Signed-in people are unaffected.
	req, _ = http.NewRequest("PROPFIND", srv.URL+"/dav/", nil)
	req.SetBasicAuth("admin", "admin-pass-1")
	req.Header.Set("Depth", "1")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusMultiStatus {
		t.Errorf("admin listing: %v %v", res, err)
	} else {
		res.Body.Close()
	}
}

func TestDiscoveryParsers(t *testing.T) {
	avahi := `+;eth0;IPv4;Living\032Room\032NAS;_smb._tcp;local
=;eth0;IPv4;Living\032Room\032NAS;_smb._tcp;local;nas.local;192.168.1.20;445;"model=Xserve"
=;eth0;IPv6;Living\032Room\032NAS;_smb._tcp;local;nas.local;fe80::1;445;
=;wlan0;IPv4;Living\032Room\032NAS;_smb._tcp;local;nas.local;192.168.1.20;445;
=;eth0;IPv4;Office-PC;_smb._tcp;local;office-pc.local;192.168.1.31;445;`
	got := parseAvahi(avahi)
	if len(got) != 2 || got[0].Name != "Living Room NAS" || got[0].Host != "192.168.1.20" || got[0].Hostname != "nas.local" || got[1].Host != "192.168.1.31" {
		t.Errorf("avahi: %+v", got)
	}

	wild := "querying * on 192.168.1.255\n192.168.1.20 *<00>\n192.168.1.31 *<00>\n192.168.1.20 *<00>\n127.0.0.1 *<00>\n"
	if ips := parseNmbWildcard(wild); strings.Join(ips, ",") != "192.168.1.20,192.168.1.31" {
		t.Errorf("nmblookup *: %v", ips)
	}
	if ips := parseNmbWildcard("name_query failed to find name *\n"); len(ips) != 0 {
		t.Errorf("no answers: %v", ips)
	}
	status := `Looking up status of 192.168.1.20
	WORKGROUP       <00> - <GROUP> B <ACTIVE>
	NAS             <00> -         B <ACTIVE>
	NAS             <03> -         B <ACTIVE>
	NAS             <20> -         B <ACTIVE>
	..__MSBROWSE__. <01> - <GROUP> B <ACTIVE>

	MAC Address = 00-00-00-00-00-00
`
	if n := parseNmbStatus(status); n != "NAS" {
		t.Errorf("nmblookup -A: %q", n)
	}
	if n := parseNmbStatus("Looking up status of 10.0.0.9\nNo reply from 10.0.0.9\n"); n != "" {
		t.Errorf("no reply: %q", n)
	}
	list := "Disk|Media|Movies and music\nDisk|homes|Home Directories\nDisk|print$|Printer Drivers\nIPC|IPC$|IPC Service (Samba 4.19)\nPrinter|Laser|\nServer|NAS|\nWorkgroup|WORKGROUP|NAS\nDisk|backups|\n"
	if s := parseSmbclientList(list); strings.Join(s, ",") != "backups,homes,Media" {
		t.Errorf("smbclient -L: %v", s)
	}
}

func TestDiscover(t *testing.T) {
	m, _, _ := setup(t)
	m.run = func(_ context.Context, _ string, name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		switch call {
		case "avahi-browse -rtpk _smb._tcp":
			return "=;eth0;IPv4;NAS;_smb._tcp;local;nas.local;192.168.1.20;445;\n", nil
		case "nmblookup *":
			return "192.168.1.20 *<00>\n192.168.1.40 *<00>\n", nil
		case "nmblookup -A 192.168.1.20":
			return "\tNAS <20> - B <ACTIVE>\n", nil
		case "nmblookup -A 192.168.1.40":
			return "\tWIN11 <20> - B <ACTIVE>\n", nil
		case "smbclient -L //192.168.1.20 -N -g":
			return "Disk|Media|\nDisk|Photos|\n", nil
		case "smbclient -L //192.168.1.40 -N -g":
			return "session setup failed: NT_STATUS_ACCESS_DENIED", errors.New("exit status 1")
		}
		return "", errors.New("unexpected " + call)
	}
	d := m.Discover(context.Background())
	if d.Hint != "" || len(d.Servers) != 2 {
		t.Fatalf("discovery: %+v", d)
	}
	nas, win := d.Servers[0], d.Servers[1]
	if nas.Name != "NAS" || nas.Hostname != "nas.local" || !nas.Browsable || strings.Join(nas.Shares, ",") != "Media,Photos" || len(nas.Sources) != 2 {
		t.Errorf("nas: %+v", nas)
	}
	if win.Name != "WIN11" || win.Browsable || len(win.Shares) != 0 {
		t.Errorf("win: %+v", win)
	}
	if _, err := m.Shares(context.Background(), "192.168.1.40"); err == nil || !strings.Contains(err.Error(), "signing in") {
		t.Errorf("access denied: %v", err)
	}
	if _, err := m.Shares(context.Background(), "nas; reboot"); err == nil {
		t.Error("bad host accepted")
	}

	// No tools at all: an empty list with a hint, right away.
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	d = m.Discover(context.Background())
	if len(d.Servers) != 0 || !strings.Contains(d.Hint, "Samba client tools") || d.TookMS > 1000 {
		t.Errorf("no tools: %+v", d)
	}
	if _, err := m.Shares(context.Background(), "nas"); !errors.Is(err, ErrNoSMBClient) {
		t.Errorf("shares without smbclient: %v", err)
	}
}
