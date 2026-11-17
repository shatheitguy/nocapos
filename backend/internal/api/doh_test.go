package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/dnsblock"

	"golang.org/x/net/dns/dnsmessage"
)

// dohUpstream is a plain DNS server answering every A question with 1.2.3.4.
func dohUpstream(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			h, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil {
				continue
			}
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, RecursionDesired: true, RecursionAvailable: true})
			_ = b.StartQuestions()
			_ = b.Question(q)
			_ = b.StartAnswers()
			if q.Type == dnsmessage.TypeA {
				_ = b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.AResource{A: [4]byte{1, 2, 3, 4}})
			}
			out, _ := b.Finish()
			_, _ = pc.WriteTo(out, from)
		}
	}()
	return "udp://" + pc.LocalAddr().String()
}

func dohFreePort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 20; i++ {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := pc.LocalAddr().(*net.UDPAddr).Port
		pc.Close()
		if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			ln.Close()
			return port
		}
	}
	t.Fatal("no free port")
	return 0
}

// dohWorld: a server whose DNS blocker is on (block list: ads.example.com).
func dohWorld(t *testing.T) (*spacesWorld, http.Handler) {
	t.Helper()
	w := deviceWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc := dnsblock.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.TestMode()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	st := dnsblock.Defaults()
	st.Enabled, st.Port, st.Lists, st.Upstreams = true, dohFreePort(t), []dnsblock.List{}, []string{dohUpstream(t)}
	st.Block = []string{"ads.example.com"}
	if _, err := svc.Apply(st); err != nil {
		t.Fatal(err)
	}
	w.s.shield = &shieldState{dns: svc}
	mux := http.NewServeMux()
	w.s.dohRoutes(mux)
	return w, mux
}

func dohQuestion(t *testing.T, name string) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 0, RecursionDesired: true})
	_ = b.StartQuestions()
	if err := b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name + "."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func dohGet(h http.Handler, token string, msg []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/dns-query?dns="+base64.RawURLEncoding.EncodeToString(msg), nil)
	r.RemoteAddr = "203.0.113.9:4000"
	if token != "" {
		r.Header.Set("Authorization", "Device "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func dohPost(h http.Handler, token, ctype string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/dns-query", bytes.NewReader(body))
	r.RemoteAddr = "203.0.113.9:4000"
	r.Header.Set("Content-Type", ctype)
	if token != "" {
		r.Header.Set("Authorization", "Device "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func dohAddrs(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/dns-message" {
		t.Fatalf("answer: %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	var m dnsmessage.Message
	if err := m.Unpack(rec.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, a := range m.Answers {
		if r, ok := a.Body.(*dnsmessage.AResource); ok {
			out = append(out, netip.AddrFrom4(r.A).String())
		}
	}
	return out
}

func TestDoHAuth(t *testing.T) {
	w, h := dohWorld(t)
	q := dohQuestion(t, "example.com")
	if rec := dohGet(h, "", q); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := dohGet(h, "not-a-token", q); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	tok := linkDevice(t, w.s, w.users["alex"])
	if got := dohAddrs(t, dohGet(h, tok, q)); len(got) != 1 || got[0] != "1.2.3.4" {
		t.Fatalf("answer: %v", got)
	}
	// Unlinking stops it at once, even though good tokens are remembered.
	devs, err := w.s.Store.ListDevices(context.Background(), w.users["alex"].ID)
	if err != nil || len(devs) != 1 {
		t.Fatalf("devices: %v %d", err, len(devs))
	}
	if _, err := w.s.Store.RevokeDevice(context.Background(), devs[0].ID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	w.s.dohForget(devs[0].ID)
	if rec := dohGet(h, tok, q); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after unlinking: %d", rec.Code)
	}
}

func TestDoHAnswersAndBlocks(t *testing.T) {
	w, h := dohWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	rec := dohPost(h, tok, "application/dns-message", dohQuestion(t, "www.ads.example.com"))
	if got := dohAddrs(t, rec); len(got) != 1 || got[0] != "0.0.0.0" || rec.Header().Get("X-NoCap-Blocked") != "1" {
		t.Fatalf("blocked: %v %v", got, rec.Header())
	}
	rec = dohPost(h, tok, "application/dns-message", dohQuestion(t, "example.com"))
	if got := dohAddrs(t, rec); len(got) != 1 || got[0] != "1.2.3.4" || rec.Header().Get("X-NoCap-Blocked") != "" {
		t.Fatalf("allowed: %v", got)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=120" {
		t.Fatalf("cache-control %q", cc)
	}
}

func TestDoHRejectsBadQuestions(t *testing.T) {
	w, h := dohWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	q := dohQuestion(t, "example.com")
	resp := append([]byte(nil), q...)
	resp[2] |= 0x80 // an answer, not a question
	two := func() []byte {
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
		_ = b.StartQuestions()
		for _, n := range []string{"a.example.", "b.example."} {
			_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(n), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
		}
		out, _ := b.Finish()
		return out
	}()
	for name, c := range map[string]struct {
		rec  *httptest.ResponseRecorder
		want int
	}{
		"wrong type":      {dohPost(h, tok, "application/json", q), http.StatusUnsupportedMediaType},
		"too big":         {dohPost(h, tok, "application/dns-message", make([]byte, dohMaxMsg+1)), http.StatusRequestEntityTooLarge},
		"garbage":         {dohPost(h, tok, "application/dns-message", []byte("hello there, not dns")), http.StatusBadRequest},
		"short":           {dohPost(h, tok, "application/dns-message", []byte{1, 2}), http.StatusBadRequest},
		"answer":          {dohPost(h, tok, "application/dns-message", resp), http.StatusBadRequest},
		"two questions":   {dohPost(h, tok, "application/dns-message", two), http.StatusBadRequest},
		"long GET":        {dohGet(h, tok, make([]byte, dohMaxMsg+10)), http.StatusRequestURITooLong},
		"GET not base64":  {func() *httptest.ResponseRecorder { return dohRaw(h, tok, "/dns-query?dns=***") }(), http.StatusBadRequest},
		"GET no question": {func() *httptest.ResponseRecorder { return dohRaw(h, tok, "/dns-query") }(), http.StatusBadRequest},
	} {
		if c.rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", name, c.rec.Code, c.want, c.rec.Body)
		}
	}
}

func dohRaw(h http.Handler, token, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	r.Header.Set("Authorization", "Device "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestDoHRateLimit(t *testing.T) {
	w, h := dohWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	w.s.doh().perDev = auth.NewLimiter(time.Hour, 3)
	q := dohQuestion(t, "www.ads.example.com")
	for i := 0; i < 3; i++ {
		if rec := dohGet(h, tok, q); rec.Code != http.StatusOK {
			t.Fatalf("%d: %d", i, rec.Code)
		}
	}
	if rec := dohGet(h, tok, q); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %d", rec.Code)
	}
	// Someone else's phone has its own allowance.
	other := linkDevice(t, w.s, w.users["sam"])
	if rec := dohGet(h, other, q); rec.Code != http.StatusOK {
		t.Fatalf("other device: %d", rec.Code)
	}
}

func TestDoHBlockerOff(t *testing.T) {
	w, h := dohWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	st := w.s.shield.dns.Settings()
	st.Enabled = false
	if _, err := w.s.shield.dns.Apply(st); err != nil {
		t.Fatal(err)
	}
	rec := dohGet(h, tok, dohQuestion(t, "example.com"))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "shield_off") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// TestDevicePersonalRules: a phone's own allow/block lists apply to its
// questions only (or all of that person's devices with scope "user").
func TestDevicePersonalRules(t *testing.T) {
	w, h := dohWorld(t)
	alex := linkDevice(t, w.s, w.users["alex"])
	alex2 := linkDevice(t, w.s, w.users["alex"])
	sam := linkDevice(t, w.s, w.users["sam"])
	rule := func(tok, body string) deviceShieldView {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/v1/device/shield/rules", strings.NewReader(body))
		r.Header.Set("Authorization", "Device "+tok)
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
		var v deviceShieldView
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	blocked := func(tok, name string) bool {
		t.Helper()
		got := dohAddrs(t, dohGet(h, tok, dohQuestion(t, name)))
		return len(got) == 1 && got[0] == "0.0.0.0"
	}
	v := rule(alex, `{"domain":"Social.Example","action":"block"}`)
	if !v.Available || len(v.Device.Block) != 1 || v.Device.Block[0] != "social.example" {
		t.Fatalf("%+v", v)
	}
	if !blocked(alex, "m.social.example") || blocked(alex2, "m.social.example") || blocked(sam, "m.social.example") {
		t.Fatal("a device rule must only apply to that device")
	}
	rule(alex2, `{"domain":"games.example","action":"block","scope":"user"}`)
	if !blocked(alex, "games.example") || !blocked(alex2, "games.example") || blocked(sam, "games.example") {
		t.Fatal("a person's rule applies to all their devices, nobody else's")
	}
	rule(alex, `{"domain":"ads.example.com","action":"allow"}`)
	if blocked(alex, "ads.example.com") || !blocked(alex2, "ads.example.com") {
		t.Fatal("device allow")
	}
	v = rule(alex, `{"domain":"ads.example.com","action":"clear"}`)
	if len(v.Device.Allow) != 0 || !blocked(alex, "ads.example.com") {
		t.Fatalf("clear: %+v", v)
	}
	for _, body := range []string{`{"domain":"not a domain","action":"block"}`, `{"domain":"a.example","action":"nuke"}`, `{"domain":"a.example","action":"block","scope":"everyone"}`} {
		r := httptest.NewRequest("POST", "/api/v1/device/shield/rules", strings.NewReader(body))
		r.Header.Set("Authorization", "Device "+alex)
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
}
