package dnsblock

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
	"time"
)

func TestLocalClient(t *testing.T) {
	home := []netip.Prefix{netip.MustParsePrefix("2001:db8:1:2::/64")}
	ok := []string{
		"127.0.0.1", "::1", "10.1.2.3", "172.16.0.9", "192.168.0.20", "fd12::5", "fe80::1",
		"169.254.10.10", "100.100.1.1", "::ffff:192.168.1.4", "2001:db8:1:2::77",
	}
	for _, s := range ok {
		if !localClient(netip.MustParseAddr(s), home) {
			t.Errorf("localClient(%s) = false, want true", s)
		}
	}
	bad := []string{"8.8.8.8", "203.0.113.5", "2001:db8:9::1", "::", "0.0.0.0", "ff02::1", "224.0.0.1"}
	for _, s := range bad {
		if localClient(netip.MustParseAddr(s), home) {
			t.Errorf("localClient(%s) = true, want false", s)
		}
	}
	if localClient(netip.Addr{}, home) {
		t.Error("an unknown address must not count as local")
	}
}

func TestClientFilterRefreshes(t *testing.T) {
	now := time.Unix(1000, 0)
	reads := 0
	nets := []netip.Prefix{}
	f := &clientFilter{now: func() time.Time { return now }, read: func() []netip.Prefix { reads++; return nets }}
	a := netip.MustParseAddr("2001:db8:5::9")
	if f.allowed(a) {
		t.Fatal("not on any of our networks yet")
	}
	nets = []netip.Prefix{netip.MustParsePrefix("2001:db8:5::/64")}
	if f.allowed(a) {
		t.Fatal("networks are re-read only now and then")
	}
	now = now.Add(clientNetsEvery)
	if !f.allowed(a) {
		t.Fatal("a new network must be picked up")
	}
	if reads != 2 {
		t.Fatalf("read %d times, want 2", reads)
	}
}

func TestListRedirectStaysHTTPS(t *testing.T) {
	req := func(s string) *http.Request { u, _ := url.Parse(s); return &http.Request{URL: u} }
	if err := listRedirect(req("http://x.example/l.txt"), []*http.Request{req("https://x.example/l.txt")}); err == nil {
		t.Error("https → http must be refused")
	}
	if err := listRedirect(req("https://cdn.example/l.txt"), []*http.Request{req("https://x.example/l.txt")}); err != nil {
		t.Errorf("https → https: %v", err)
	}
	if err := listRedirect(req("https://cdn.example/l.txt"), []*http.Request{req("http://x.example/l.txt")}); err != nil {
		t.Errorf("http → https: %v", err)
	}
	five := []*http.Request{req("https://a/"), req("https://a/"), req("https://a/"), req("https://a/"), req("https://a/")}
	if err := listRedirect(req("https://a/"), five); err == nil {
		t.Error("redirect loops must stop")
	}
}

func TestListenersCloseOpenTCP(t *testing.T) {
	l := &listeners{}
	a, b := net.Pipe()
	defer b.Close()
	if !l.track(a) {
		t.Fatal("track before close")
	}
	l.close()
	if _, err := a.Write([]byte{1}); err == nil {
		t.Fatal("an open connection must be closed with the listeners")
	}
	c, d := net.Pipe()
	defer c.Close()
	defer d.Close()
	if l.track(c) {
		t.Fatal("no new connections once closed")
	}
}

func TestQueryLogRowCap(t *testing.T) {
	l, err := openQueryLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.db.Close()
	l.configure(true, true, 720)
	old := maxLogRows
	maxLogRows = 5
	defer func() { maxLogRows = old }()
	now := time.Now().UnixMilli()
	for i := range 12 {
		if _, err := l.db.Exec(`INSERT INTO queries VALUES (?, '', 'a.example', 'A', 'forwarded', '', '', 1, 'NOERROR')`, now-int64(12-i)); err != nil {
			t.Fatal(err)
		}
	}
	l.pruneOld()
	var n int
	if err := l.db.QueryRow(`SELECT COUNT(*) FROM queries`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n > 5 {
		t.Fatalf("%d rows kept, want at most 5", n)
	}
	var newest int64
	_ = l.db.QueryRow(`SELECT MAX(t) FROM queries`).Scan(&newest)
	if newest != now-1 {
		t.Fatal("the newest entries must be the ones kept")
	}
}
