package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/ws"
)

func TestLocalNetworkAddr(t *testing.T) {
	cases := map[string]bool{
		// this machine
		"127.0.0.1": true, "127.8.9.10": true, "::1": true,
		// RFC 1918
		"10.0.0.1": true, "10.255.255.255": true, "172.16.0.1": true, "172.31.255.254": true, "192.168.1.20": true,
		// link-local
		"169.254.10.10": true, "fe80::1": true,
		// IPv6 unique local (fc00::/7)
		"fc00::1": true, "fd12:3456:789a::1": true,
		// IPv4-mapped IPv6 follows the IPv4 rules
		"::ffff:192.168.1.5": true, "::ffff:8.8.8.8": false,
		// carrier-grade NAT is shared by strangers: not local
		"100.64.0.1": false, "100.127.255.254": false,
		// public and odd addresses
		"8.8.8.8": false, "172.32.0.1": false, "172.15.255.255": false, "192.169.0.1": false, "11.0.0.1": false,
		"2001:db8::1": false, "2606:4700::1111": false, "fec0::1": false,
		"0.0.0.0": false, "::": false, "224.0.0.1": false, "ff02::1": false, "255.255.255.255": false,
	}
	for s, want := range cases {
		if got := localNetworkAddr(netip.MustParseAddr(s)); got != want {
			t.Errorf("localNetworkAddr(%s) = %v, want %v", s, got, want)
		}
	}
	if localNetworkAddr(netip.Addr{}) {
		t.Error("the zero address must not count as local")
	}
}

func TestSetupPeerIgnoresSpoofedHeaders(t *testing.T) {
	loopbackProxy := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	cases := []struct {
		name    string
		remote  string
		xff     []string
		trusted []netip.Prefix
		local   bool
		other   map[string]string
	}{
		// Proxy headers a browser never sends: a proxy nobody told us about
		// (a tunnel, another machine's reverse proxy) is in front: fail closed.
		{"LAN peer with X-Real-IP", "192.168.1.10:5000", nil, loopbackProxy, false, map[string]string{"X-Real-IP": "192.168.1.1"}},
		{"untrusted loopback tunnel with Forwarded", "127.0.0.1:5000", nil, nil, false, map[string]string{"Forwarded": "for=127.0.0.1"}},
		{"untrusted loopback tunnel with a CDN header", "[::1]:5000", nil, nil, false, map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}},
		{"trusted proxy naming the client another way", "127.0.0.1:5000", nil, loopbackProxy, false, map[string]string{"X-Real-IP": "192.168.1.1"}},
		{"trusted proxy with X-Forwarded-For and X-Real-IP", "127.0.0.1:5000", []string{"192.168.1.10"}, loopbackProxy, true, map[string]string{"X-Real-IP": "192.168.1.10"}},
		{"public peer claims LAN", "203.0.113.9:5000", []string{"192.168.1.10"}, loopbackProxy, false, nil},
		{"public peer claims loopback", "203.0.113.9:5000", []string{"127.0.0.1"}, loopbackProxy, false, nil},
		{"public peer, real IP header ignored", "203.0.113.9:5000", nil, nil, false, nil},
		{"CGNAT peer", "100.80.1.2:5000", []string{"10.0.0.2"}, loopbackProxy, false, nil},
		{"LAN peer", "192.168.1.10:5000", nil, loopbackProxy, true, nil},
		{"untrusted LAN proxy forwarding a public client", "192.168.1.10:5000", []string{"203.0.113.9"}, loopbackProxy, false, nil},
		{"IPv6 link-local with zone", "[fe80::1%eth0]:5000", nil, loopbackProxy, true, nil},
		{"IPv6 ULA", "[fd00::5]:5000", nil, nil, true, nil},
		{"IPv6 public", "[2001:db8::5]:5000", nil, nil, false, nil},
		{"trusted proxy, public client", "127.0.0.1:5000", []string{"203.0.113.9"}, loopbackProxy, false, nil},
		{"trusted proxy, client spoofs left-most", "127.0.0.1:5000", []string{"192.168.1.10, 203.0.113.9"}, loopbackProxy, false, nil},
		{"trusted proxy, spoof in an earlier header", "127.0.0.1:5000", []string{"192.168.1.10", "203.0.113.9"}, loopbackProxy, false, nil},
		{"trusted proxy, LAN client", "127.0.0.1:5000", []string{"203.0.113.9, 192.168.1.10"}, loopbackProxy, true, nil},
		{"trusted proxy, garbage: fail closed", "127.0.0.1:5000", []string{"not-an-ip"}, loopbackProxy, false, nil},
		{"trusted proxy, no header: the proxy host itself", "127.0.0.1:5000", nil, loopbackProxy, true, nil},
		{"untrusted loopback proxy: fail closed", "127.0.0.1:5000", []string{"203.0.113.9"}, nil, false, nil},
		{"unparseable peer", "@", nil, loopbackProxy, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/auth/setup", nil)
			r.RemoteAddr = c.remote
			for _, v := range c.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			for k, v := range c.other {
				r.Header.Set(k, v) // never trusted: only ever makes it stricter
			}
			s := &Server{Deps: Deps{Config: &config.Config{TrustedProxies: c.trusted}}}
			if got := s.setupFromLocal(r); got != c.local {
				a, ok := setupPeer(r, c.trusted)
				t.Fatalf("setupFromLocal = %v, want %v (peer %v, ok %v)", got, c.local, a, ok)
			}
		})
	}
}

// newSetupWorld is a server with no accounts yet.
func newSetupWorld(t *testing.T, token string, trusted ...string) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := auth.NewService(st, auth.NewIssuer([]byte("0123456789abcdef0123456789abcdef"), time.Minute),
		auth.Options{RefreshTTL: time.Hour, SessionMaxAge: time.Hour, SetupToken: token})
	svc.SetSealer(plainBox{})
	cfg := &config.Config{}
	for _, p := range trusted {
		cfg.TrustedProxies = append(cfg.TrustedProxies, netip.MustParsePrefix(p))
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{
		Deps:         Deps{Config: cfg, Store: st, Auth: svc, Log: log, Accounts: accounts.NewApp(st)},
		tickets:      auth.NewTicketStore(30 * time.Second),
		loginLimiter: auth.NewLimiter(time.Millisecond, 1000),
	}
	s.hub = ws.NewHub(ctx, s.resolveTopic, log)
	return s
}

func setupCall(s *Server, remote, xff string, body map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	r := httptest.NewRequest("POST", "/api/v1/auth/setup", &buf)
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	s.authSetup(rec, r)
	return rec
}

func statusCall(s *Server, remote string) map[string]any {
	r := httptest.NewRequest("GET", "/api/v1/auth/status", nil)
	r.RemoteAddr = remote
	rec := httptest.NewRecorder()
	s.authStatus(rec, r)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestSetupWithoutTokenFromLocalNetwork(t *testing.T) {
	s := newSetupWorld(t, "tok-123")
	if st := statusCall(s, "192.168.1.10:4000"); st["setup_token_required"] != false {
		t.Fatalf("status from the LAN: %v", st)
	}
	if st := statusCall(s, "203.0.113.9:4000"); st["setup_token_required"] != true {
		t.Fatalf("status from outside: %v", st)
	}
	rec := setupCall(s, "192.168.1.10:4000", "", map[string]string{"username": "owner", "password": pw})
	if rec.Code != http.StatusCreated {
		t.Fatalf("local setup: %d %s", rec.Code, rec.Body)
	}
	if st := statusCall(s, "192.168.1.10:4000"); st["setup_required"] != false || st["setup_token_required"] != nil {
		t.Fatalf("status after setup: %v", st)
	}
	// One-shot: even with the right token, a second setup is refused.
	rec = setupCall(s, "127.0.0.1:4000", "", map[string]string{"username": "other", "password": pw, "setup_token": "tok-123"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second setup: %d %s", rec.Code, rec.Body)
	}
}

func TestSetupFromOutsideNeedsToken(t *testing.T) {
	s := newSetupWorld(t, "tok-123", "127.0.0.0/8")
	// No token, and spoofed headers don't help.
	rec := setupCall(s, "203.0.113.9:4000", "192.168.1.10", map[string]string{"username": "owner", "password": pw})
	if rec.Code != http.StatusForbidden || !bytes.Contains(rec.Body.Bytes(), []byte("setup_token_required")) {
		t.Fatalf("no token from outside: %d %s", rec.Code, rec.Body)
	}
	// Through the trusted proxy, a public client is still outside.
	rec = setupCall(s, "127.0.0.1:4000", "192.168.1.10, 100.64.3.4", map[string]string{"username": "owner", "password": pw})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CGNAT client behind the proxy: %d %s", rec.Code, rec.Body)
	}
	rec = setupCall(s, "203.0.113.9:4000", "", map[string]string{"username": "owner", "password": pw, "setup_token": "nope"})
	if rec.Code != http.StatusForbidden || !bytes.Contains(rec.Body.Bytes(), []byte("invalid_setup_token")) {
		t.Fatalf("wrong token: %d %s", rec.Code, rec.Body)
	}
	// ALFA_SETUP_TOKEN keeps working for automation.
	rec = setupCall(s, "203.0.113.9:4000", "", map[string]string{"username": "owner", "password": pw, "setup_token": " tok-123 "})
	if rec.Code != http.StatusCreated {
		t.Fatalf("right token: %d %s", rec.Code, rec.Body)
	}
}

func TestSetupRaceOnlyFirstWins(t *testing.T) {
	s := newSetupWorld(t, "tok-123")
	const n = 12
	var wg sync.WaitGroup
	codes := make([]int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			name := "owner" + string(rune('a'+i))
			codes[i] = setupCall(s, "192.168.1.10:4000", "", map[string]string{"username": name, "password": pw}).Code
		}(i)
	}
	close(start)
	wg.Wait()
	created, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if created != 1 || conflicts != n-1 {
		t.Fatalf("created %d, conflicts %d (codes %v)", created, conflicts, codes)
	}
	if users, _ := s.Store.CountUsers(context.Background()); users != 1 {
		t.Fatalf("%d accounts after the race", users)
	}
}

func TestIPConfirmUnknownToken(t *testing.T) {
	s := newSetupWorld(t, "")
	for _, tok := range []string{"", "short", "0123456789abcdef0123456789abcdef"} {
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]string{"token": tok})
		r := httptest.NewRequest("POST", "/api/v1/system/network/confirm", &buf)
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.ipConfirm(rec, r)
		if rec.Code != http.StatusGone {
			t.Fatalf("token %q: %d %s", tok, rec.Code, rec.Body)
		}
	}
}

func TestSetupMachineFlag(t *testing.T) {
	s := newSetupWorld(t, "")
	rec := setupCall(s, "127.0.0.1:1", "", map[string]string{"username": "owner", "password": pw})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d", rec.Code)
	}
	u, err := s.Store.UserByUsername(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	w := &authWorld{s: s}
	get := func() machineSetupState {
		rec := w.do(t, s.setupMachineGet, u, "GET", "/api/v1/setup/machine", nil)
		var st machineSetupState
		_ = json.Unmarshal(rec.Body.Bytes(), &st)
		return st
	}
	if get().Done {
		t.Fatal("done before anyone finished")
	}
	if rec := w.do(t, s.setupMachineDone, u, "POST", "/api/v1/setup/machine/done", map[string]string{}); rec.Code != http.StatusNoContent {
		t.Fatalf("done: %d %s", rec.Code, rec.Body)
	}
	if !get().Done {
		t.Fatal("not done after finishing")
	}
}

func TestSetupServiceRace(t *testing.T) {
	s := newSetupWorld(t, "")
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Auth.Setup(context.Background(), auth.SetupRequest{
				Username: "admin" + string(rune('a'+i)), Password: pw, Local: true,
			}, auth.ClientMeta{})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case err != auth.ErrSetupDone:
			t.Errorf("unexpected error %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d setups succeeded", ok)
	}
	// Without a token configured, nothing outside the local network can set up.
	s2 := newSetupWorld(t, "")
	if _, err := s2.Auth.Setup(context.Background(), auth.SetupRequest{Username: "x-admin", Password: pw, Token: "anything"}, auth.ClientMeta{}); err != auth.ErrSetupToken {
		t.Fatalf("no configured token: %v", err)
	}
}
