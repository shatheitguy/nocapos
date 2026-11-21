package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"alfaos/alfad/internal/devices"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/tlsutil"
)

// squadNode is a NoCap server for these tests: its own store, accounts,
// certificate and HTTPS listener with the routes other servers call.
type squadNode struct {
	w    *authWorld
	ts   *httptest.Server
	id   string
	fp   string
	skew atomic.Int64 // added to the server's clock
}

func newSquadNode(t *testing.T) *squadNode {
	t.Helper()
	n := &squadNode{w: newAuthWorld(t)}
	s := n.w.s
	dir := t.TempDir()
	certFile, keyFile, err := tlsutil.EnsureSelfSigned(filepath.Join(dir, "tls"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/pair/info", s.pairInfo)
	mux.HandleFunc("POST /api/v1/squad/hello", s.squadHello)
	mux.HandleFunc("POST /api/v1/squad/answer", s.squadAnswer)
	mux.Handle("GET /api/v1/squad/peer", s.peer(s.squadPeerHello))
	mux.Handle("DELETE /api/v1/squad/peer", s.peer(s.squadPeerUnlink))
	n.ts = httptest.NewUnstartedServer(mux)
	n.ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	n.ts.StartTLS()
	t.Cleanup(n.ts.Close)
	s.Config.DataDir, s.Config.TLSCert, s.Config.TLSKey = dir, certFile, keyFile
	s.Config.ListenAddr = n.ts.Listener.Addr().String()
	s.squad.now = func() time.Time { return time.Now().Add(time.Duration(n.skew.Load())) }
	if n.id, err = s.nocapID(); err != nil {
		t.Fatal(err)
	}
	n.fp = certFingerprint(cert.Certificate[0])
	if got := s.ownFingerprint(); got != n.fp {
		t.Fatalf("own fingerprint %q, want %q", got, n.fp)
	}
	return n
}

type callOpts struct {
	as     *store.User
	path   map[string]string
	remote string
	header map[string]string
}

func squadCall(s *Server, h http.HandlerFunc, method, target string, body any, o callOpts) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, target, &buf)
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "192.168.1.20:40000"
	if o.remote != "" {
		r.RemoteAddr = o.remote
	}
	for k, v := range o.path {
		r.SetPathValue(k, v)
	}
	for k, v := range o.header {
		r.Header.Set(k, v)
	}
	if o.as != nil {
		r = r.WithContext(context.WithValue(r.Context(), userKey, o.as))
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

type squadListResp struct {
	Self     squadSelf          `json:"self"`
	Linked   []linkedServerJSON `json:"linked"`
	Incoming []serverAsk        `json:"incoming"`
	Outgoing []serverAsk        `json:"outgoing"`
}

func (n *squadNode) list(t *testing.T) squadListResp {
	t.Helper()
	rec := squadCall(n.w.s, n.w.s.squadList, "GET", "/api/v1/squad", nil, callOpts{as: n.w.users["boss"]})
	var out squadListResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	return out
}

func (n *squadNode) send(t *testing.T, to *squadNode) *httptest.ResponseRecorder {
	t.Helper()
	return squadCall(n.w.s, n.w.s.squadSend, "POST", "/api/v1/squad/request",
		map[string]string{"url": to.ts.URL, "nocap_id": to.id}, callOpts{as: n.w.users["boss"]})
}

func (n *squadNode) forgetStatus() {
	q := n.w.s.squadInit()
	q.mu.Lock()
	q.status = map[string]peerStatus{}
	q.mu.Unlock()
}

// TestServerLinkLifecycle: request, both fingerprints, accept, linked on
// both sides with online state, pinning, unlink on both sides.
func TestServerLinkLifecycle(t *testing.T) {
	a, b := newSquadNode(t), newSquadNode(t)
	if a.fp == b.fp {
		t.Fatal("test servers share a certificate")
	}

	rec := a.send(t, b)
	var sent serverAsk
	if err := json.Unmarshal(rec.Body.Bytes(), &sent); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	if sent.NocapID != b.id || sent.Fingerprint != b.fp || sent.State != "pending" || time.Until(sent.ExpiresAt) > squadAskTTL {
		t.Fatalf("sent: %+v", sent)
	}
	// B sees A's request with A's certificate (it called A back).
	bl := b.list(t)
	if len(bl.Incoming) != 1 || bl.Incoming[0].NocapID != a.id || bl.Incoming[0].Fingerprint != a.fp || bl.Self.Fingerprint != b.fp {
		t.Fatalf("incoming: %+v", bl)
	}
	if al := a.list(t); len(al.Outgoing) != 1 || al.Self.Fingerprint != a.fp {
		t.Fatalf("outgoing: %+v", al)
	}

	// A user (not an admin) can't use the admin handlers' routes at all:
	// covered by TestSquadRoutesNeedSignIn. Accept on B.
	in := bl.Incoming[0]
	rec = squadCall(b.w.s, b.w.s.squadAccept, "POST", "/", nil, callOpts{as: b.w.users["boss"], path: map[string]string{"id": in.ID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	// Accepting twice: gone.
	if rec := squadCall(b.w.s, b.w.s.squadAccept, "POST", "/", nil, callOpts{as: b.w.users["boss"], path: map[string]string{"id": in.ID}}); rec.Code != http.StatusNotFound {
		t.Fatalf("accept again: %d", rec.Code)
	}

	la, err := a.w.st.ServerLink(context.Background(), b.id)
	if err != nil || la.Fingerprint != b.fp || la.URL != b.ts.URL || la.LinkedBy != "boss" {
		t.Fatalf("A's link: %+v %v", la, err)
	}
	lb, err := b.w.st.ServerLink(context.Background(), a.id)
	if err != nil || lb.Fingerprint != a.fp || lb.LinkedBy != "boss" {
		t.Fatalf("B's link: %+v %v", lb, err)
	}
	// Each side's token is the other's: A presents what B hashed, and back.
	tokA, _ := a.w.s.openPeerToken(la)
	tokB, _ := b.w.s.openPeerToken(lb)
	if devices.HashToken(tokA) != lb.OurTokenHash || devices.HashToken(tokB) != la.OurTokenHash || tokA == tokB {
		t.Fatal("tokens don't pair up")
	}

	// Both list each other as linked and online (they actually call).
	a.forgetStatus()
	b.forgetStatus()
	al, bl := a.list(t), b.list(t)
	if len(al.Linked) != 1 || !al.Linked[0].Online || al.Linked[0].NocapID != b.id || len(al.Outgoing) != 0 {
		t.Fatalf("A linked: %+v", al)
	}
	if len(bl.Linked) != 1 || !bl.Linked[0].Online || bl.Linked[0].NocapID != a.id || len(bl.Incoming) != 0 {
		t.Fatalf("B linked: %+v", bl)
	}

	// Pinning: a different certificate than the pinned one is "offline".
	bad := *la
	bad.Fingerprint = strings.Repeat("ab", 32)
	if err := a.w.st.SaveServerLink(context.Background(), &bad); err != nil {
		t.Fatal(err)
	}
	a.forgetStatus()
	if al := a.list(t); al.Linked[0].Online {
		t.Fatal("wrong pin still online")
	}
	if err := a.w.st.SaveServerLink(context.Background(), la); err != nil {
		t.Fatal(err)
	}

	// A stranger's token gets nothing.
	r := httptest.NewRequest("GET", "/api/v1/squad/peer", nil)
	r.Header.Set("Authorization", peerAuthPrefix+strings.Repeat("x", 43))
	rr := httptest.NewRecorder()
	b.w.s.peer(b.w.s.squadPeerHello).ServeHTTP(rr, r)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("stranger: %d", rr.Code)
	}

	// Unlink from A: gone on both sides.
	rec = squadCall(a.w.s, a.w.s.squadUnlink, "DELETE", "/", nil, callOpts{as: a.w.users["boss"], path: map[string]string{"id": b.id}})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d %s", rec.Code, rec.Body)
	}
	if _, err := a.w.st.ServerLink(context.Background(), b.id); err != store.ErrNotFound {
		t.Fatalf("A still linked: %v", err)
	}
	if _, err := b.w.st.ServerLink(context.Background(), a.id); err != store.ErrNotFound {
		t.Fatalf("B still linked: %v", err)
	}
	// Audit trail on both sides (what the administrators did).
	for _, c := range []struct {
		n       *squadNode
		actions []string
	}{{a, []string{"squad.request", "squad.unlink"}}, {b, []string{"squad.accept"}}} {
		for _, want := range c.actions {
			if n, err := c.n.w.st.CountAudit(context.Background(), c.n.w.users["boss"].ID, want); err != nil || n == 0 {
				t.Errorf("audit missing %s (%v)", want, err)
			}
		}
	}
}

func TestServerLinkDeclineAndExpiry(t *testing.T) {
	a, b := newSquadNode(t), newSquadNode(t)
	if rec := a.send(t, b); rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	in := b.list(t).Incoming[0]
	rec := squadCall(b.w.s, b.w.s.squadDecline, "POST", "/", nil, callOpts{as: b.w.users["boss"], path: map[string]string{"id": in.ID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("decline: %d %s", rec.Code, rec.Body)
	}
	if al := a.list(t); len(al.Outgoing) != 1 || al.Outgoing[0].State != "declined" {
		t.Fatalf("A after decline: %+v", al.Outgoing)
	}
	if bl := b.list(t); len(bl.Incoming) != 0 {
		t.Fatalf("B after decline: %+v", bl.Incoming)
	}

	// A new request, then ten minutes pass: it can't be accepted.
	if rec := a.send(t, b); rec.Code != http.StatusOK {
		t.Fatalf("send again: %d %s", rec.Code, rec.Body)
	}
	in = b.list(t).Incoming[0]
	b.skew.Store(int64(squadAskTTL + time.Second))
	a.skew.Store(int64(squadAskTTL + time.Second))
	rec = squadCall(b.w.s, b.w.s.squadAccept, "POST", "/", nil, callOpts{as: b.w.users["boss"], path: map[string]string{"id": in.ID}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("accept after expiry: %d %s", rec.Code, rec.Body)
	}
	if al, bl := a.list(t), b.list(t); len(al.Outgoing) != 0 || len(bl.Incoming) != 0 || len(al.Linked) != 0 {
		t.Fatalf("expired requests linger: %+v %+v", al, bl)
	}
	// A forged answer (wrong token) links nothing.
	rec = squadCall(a.w.s, a.w.s.squadAnswer, "POST", "/", map[string]any{"request_token": "nope", "nocap_id": b.id, "accept": true, "token": strings.Repeat("t", 43)}, callOpts{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("forged answer: %d", rec.Code)
	}
}

func TestSquadHelloLimits(t *testing.T) {
	n := newSquadNode(t)
	s := n.w.s
	// Beyond the home network: refused (no remote address set up).
	if rec := squadCall(s, s.squadHello, "POST", "/", map[string]string{}, callOpts{remote: "8.8.8.8:1000"}); rec.Code != http.StatusForbidden {
		t.Fatalf("internet: %d %s", rec.Code, rec.Body)
	}
	// Rate limit per address: three, then wait.
	codes := []int{}
	for i := 0; i < 4; i++ {
		codes = append(codes, squadCall(s, s.squadHello, "POST", "/", map[string]string{"nocap_id": "bad"}, callOpts{remote: "192.168.7.7:1"}).Code)
	}
	if codes[0] != http.StatusBadRequest || codes[3] != http.StatusTooManyRequests {
		t.Fatalf("codes: %v", codes)
	}
	// Another address isn't held up.
	if rec := squadCall(s, s.squadHello, "POST", "/", map[string]string{}, callOpts{remote: "192.168.7.8:1"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("other address: %d", rec.Code)
	}
}

// TestPeerClientPins: without a pin the certificate is recorded; with the
// right pin it works; with another, the call fails before anything is sent.
func TestPeerClientPins(t *testing.T) {
	n := newSquadNode(t)
	var seen string
	var info pairInfo
	if err := peerCall(context.Background(), peerClient("", &seen, false), "GET", n.ts.URL, "/api/v1/pair/info", "", nil, &info); err != nil || seen != n.fp || info.NocapID != n.id {
		t.Fatalf("probe: %v %q %+v", err, seen, info)
	}
	if err := peerCall(context.Background(), peerClient(n.fp, nil, false), "GET", n.ts.URL, "/api/v1/pair/info", "", nil, &info); err != nil {
		t.Fatalf("pinned: %v", err)
	}
	if err := peerCall(context.Background(), peerClient(strings.Repeat("00", 32), nil, false), "GET", n.ts.URL, "/api/v1/pair/info", "", nil, &info); err == nil {
		t.Fatal("wrong pin accepted")
	}
	for _, bad := range []string{"http://192.168.1.2:8443", "https://u:p@192.168.1.2", "https://192.168.1.2/x", "ftp://x"} {
		if _, err := peerBase(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if peerAddrOK("8.8.8.8", false) || !peerAddrOK("8.8.8.8", true) || !peerAddrOK("192.168.1.9", false) || !peerAddrOK("127.0.0.1", false) {
		t.Fatal("address rules")
	}
}

func TestSquadRoutesNeedSignIn(t *testing.T) {
	n := newSquadNode(t)
	mux := http.NewServeMux()
	n.w.s.squadRoutes(mux)
	for _, rt := range []string{
		"GET /api/v1/squad", "POST /api/v1/squad/request", "POST /api/v1/squad/requests/x/accept",
		"POST /api/v1/squad/requests/x/decline", "DELETE /api/v1/squad/links/x",
		"GET /api/v1/squad/phone-asks", "POST /api/v1/squad/phone-asks/x/accept", "POST /api/v1/squad/phone-asks/x/decline",
		"GET /api/v1/squad/peer", "DELETE /api/v1/squad/peer",
	} {
		method, target, _ := strings.Cut(rt, " ")
		r := httptest.NewRequest(method, target, nil)
		r.RemoteAddr = "192.168.1.30:5000"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d %s", rt, rec.Code, rec.Body)
		}
	}
	// Non-administrators: the server handlers sit behind s.admin; phone
	// requests only show what the person may see (TestPhoneAsk).
}

type askResp struct {
	RequestID string `json:"request_id"`
	Secret    string `json:"secret"`
	Check     string `json:"check"`
	NocapID   string `json:"nocap_id"`
}

type pollResp struct {
	State  string         `json:"state"`
	Check  string         `json:"check"`
	Device *claimResponse `json:"device"`
}

func ask(t *testing.T, s *Server, body map[string]string, remote string) (askResp, int) {
	t.Helper()
	rec := squadCall(s, s.pairAsk, "POST", "/api/v1/pair/ask", body, callOpts{remote: remote})
	var out askResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out, rec.Code
}

func poll(s *Server, id, secret string) (pollResp, int) {
	rec := squadCall(s, s.pairAskPoll, "GET", "/", nil, callOpts{path: map[string]string{"id": id}, header: map[string]string{pairSecretHeader: secret}})
	var out pollResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out, rec.Code
}

func phoneAsks(t *testing.T, s *Server, as *store.User) []phoneAskJSON {
	t.Helper()
	rec := squadCall(s, s.phoneAsksList, "GET", "/", nil, callOpts{as: as})
	var out []phoneAskJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("phone asks: %d %s", rec.Code, rec.Body)
	}
	return out
}

func TestPhoneAsk(t *testing.T) {
	n := newSquadNode(t)
	s := n.w.s
	boss, alex := n.w.users["boss"], n.w.users["alex"]
	phone := map[string]string{"device_name": "Pixel 9", "platform": "android", "model": "Pixel 9"}

	// Beyond the home network: refused.
	if _, code := ask(t, s, phone, "8.8.8.8:1"); code != http.StatusForbidden {
		t.Fatalf("internet: %d", code)
	}
	a, code := ask(t, s, phone, "192.168.1.40:1")
	if code != http.StatusAccepted || a.RequestID == "" || len(a.Secret) < 32 || len(a.Check) != 4 || a.NocapID != n.id {
		t.Fatalf("ask: %d %+v", code, a)
	}
	// Only the phone (with its secret) can check on it.
	if _, code := poll(s, a.RequestID, ""); code != http.StatusNotFound {
		t.Fatalf("no secret: %d", code)
	}
	if p, code := poll(s, a.RequestID, a.Secret); code != http.StatusOK || p.State != "pending" || p.Check != a.Check || p.Device != nil {
		t.Fatalf("pending: %d %+v", code, p)
	}
	// It names nobody: administrators see it; alex doesn't.
	if l := phoneAsks(t, s, alex); len(l) != 0 {
		t.Fatalf("alex sees %+v", l)
	}
	l := phoneAsks(t, s, boss)
	if len(l) != 1 || !l[0].CanAccept || l[0].Check != a.Check || l[0].Address != "192.168.1.40" {
		t.Fatalf("boss sees %+v", l)
	}
	if rec := squadCall(s, s.phoneAskAccept, "POST", "/", nil, callOpts{as: alex, path: map[string]string{"id": a.RequestID}}); rec.Code != http.StatusNotFound {
		t.Fatalf("alex accepts: %d", rec.Code)
	}
	if rec := squadCall(s, s.phoneAskAccept, "POST", "/", nil, callOpts{as: boss, path: map[string]string{"id": a.RequestID}}); rec.Code != http.StatusOK {
		t.Fatalf("boss accepts: %d %s", rec.Code, rec.Body)
	}
	p, code := poll(s, a.RequestID, a.Secret)
	if code != http.StatusOK || p.State != "accepted" || p.Device == nil || p.Device.Token == "" || p.Device.User["username"] != "boss" {
		t.Fatalf("accepted: %d %+v", code, p)
	}
	if d, err := n.w.st.DeviceByTokenHash(context.Background(), devices.HashToken(p.Device.Token)); err != nil || d.UserID != boss.ID || d.Name != "Pixel 9" {
		t.Fatalf("device: %+v %v", d, err)
	}
	// The token is handed over once.
	if _, code := poll(s, a.RequestID, a.Secret); code != http.StatusNotFound {
		t.Fatalf("second pickup: %d", code)
	}

	// Naming an account: only that person accepts; administrators may decline.
	named := map[string]string{"device_name": "Alex's phone", "platform": "android", "username": "alex"}
	b, code := ask(t, s, named, "192.168.1.41:1")
	if code != http.StatusAccepted {
		t.Fatalf("named ask: %d", code)
	}
	if l := phoneAsks(t, s, alex); len(l) != 1 || !l[0].CanAccept {
		t.Fatalf("alex sees %+v", l)
	}
	if l := phoneAsks(t, s, boss); len(l) != 1 || l[0].CanAccept || l[0].Note != "for_user" {
		t.Fatalf("boss sees %+v", l)
	}
	if rec := squadCall(s, s.phoneAskAccept, "POST", "/", nil, callOpts{as: boss, path: map[string]string{"id": b.RequestID}}); rec.Code != http.StatusForbidden {
		t.Fatalf("boss accepts alex's: %d", rec.Code)
	}
	if rec := squadCall(s, s.phoneAskDecline, "POST", "/", nil, callOpts{as: boss, path: map[string]string{"id": b.RequestID}}); rec.Code != http.StatusOK {
		t.Fatalf("decline: %d", rec.Code)
	}
	if p, _ := poll(s, b.RequestID, b.Secret); p.State != "declined" {
		t.Fatalf("declined: %+v", p)
	}

	// Device limit: boss may have one device, and has it.
	if err := n.w.st.SetSetting(context.Background(), deviceLimitKey, "1"); err != nil {
		t.Fatal(err)
	}
	c, _ := ask(t, s, phone, "192.168.1.42:1")
	if rec := squadCall(s, s.phoneAskAccept, "POST", "/", nil, callOpts{as: boss, path: map[string]string{"id": c.RequestID}}); rec.Code != http.StatusConflict {
		t.Fatalf("over the limit: %d %s", rec.Code, rec.Body)
	}
	if p, _ := poll(s, c.RequestID, c.Secret); p.State != "pending" {
		t.Fatalf("after limit: %+v", p)
	}

	// Expiry.
	n.skew.Store(int64(squadAskTTL + time.Second))
	if _, code := poll(s, c.RequestID, c.Secret); code != http.StatusNotFound {
		t.Fatalf("expired: %d", code)
	}
}

func TestPhoneAskLimits(t *testing.T) {
	n := newSquadNode(t)
	s := n.w.s
	phone := map[string]string{"device_name": "Pixel", "platform": "android"}
	codes := []int{}
	for i := 0; i < 4; i++ {
		_, code := ask(t, s, phone, "192.168.1.50:1")
		codes = append(codes, code)
	}
	if codes[0] != http.StatusAccepted || codes[2] != http.StatusAccepted || codes[3] != http.StatusTooManyRequests {
		t.Fatalf("codes: %v", codes)
	}
	if _, code := ask(t, s, map[string]string{"device_name": "", "platform": "android"}, "192.168.1.51:1"); code != http.StatusBadRequest {
		t.Fatalf("no name: %d", code)
	}
}
