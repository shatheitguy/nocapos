package dnsblock

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestParseList(t *testing.T) {
	text := `# hosts file
127.0.0.1 localhost
::1 ip6-localhost
0.0.0.0 0.0.0.0
0.0.0.0 ads.example.com tracker.example.net # two on a line
127.0.0.1	Pixel.Example.ORG.
! adblock-style comment
||metrics.example.io^
||cdn.example.io/path^
||opt.example.io^$third-party
*.wild.example.com
plain.example.co.uk
not a domain line
-bad-.example.com
single
1.2.3.4
`
	var got []string
	n, err := ParseList(strings.NewReader(text), 100, func(d string) { got = append(got, d) })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ads.example.com", "tracker.example.net", "pixel.example.org", "metrics.example.io", "wild.example.com", "plain.example.co.uk"}
	if !reflect.DeepEqual(got, want) || n != len(want) {
		t.Fatalf("got %q", got)
	}
	got = nil
	if n, _ := ParseList(strings.NewReader(text), 2, func(d string) { got = append(got, d) }); n != 2 || len(got) != 2 {
		t.Fatalf("limit: %d %q", n, got)
	}
}

func TestDomainSet(t *testing.T) {
	s := NewDomainSet([]string{"ads.example.com", "tracker.net", "ads.example.com", "a.b.c.d.e"})
	if s.Len() != 3 {
		t.Fatalf("len %d", s.Len())
	}
	cases := map[string]string{
		"ads.example.com":      "ads.example.com",
		"x.y.ads.example.com":  "ads.example.com",
		"tracker.net":          "tracker.net",
		"cdn.tracker.net":      "tracker.net",
		"example.com":          "",
		"notads.example.com":   "",
		"tracker.network":      "",
		"b.c.d.e":              "",
		"z.a.b.c.d.e":          "a.b.c.d.e",
		"net":                  "",
		"ads.example.com.evil": "",
	}
	for name, want := range cases {
		got, ok := s.Match(name)
		if got != want || ok != (want != "") {
			t.Errorf("Match(%q) = %q %v", name, got, ok)
		}
	}
	var empty *DomainSet
	if _, ok := empty.Match("x.com"); ok || empty.Len() != 0 {
		t.Error("nil set matched")
	}
}

func TestSettingsValidate(t *testing.T) {
	s := Defaults()
	s.Allow = []string{"Good.Example.com.", "", "good.example.com"}
	s.Records = []Record{{Name: "NAS", Type: "a", Value: "192.168.1.10"}, {Name: "media.home", Type: "CNAME", Value: "nas.home"}}
	s.Upstreams = []string{"9.9.9.9", "tls://1.1.1.1", "https://1.1.1.1/dns-query", " "}
	v, err := s.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Allow, []string{"good.example.com"}) || v.Records[0].Name != "nas" || v.Records[0].Type != "A" || len(v.Upstreams) != 3 {
		t.Fatalf("%+v", v)
	}
	bad := []func(*Settings){
		func(s *Settings) { s.Listen = "everywhere" },
		func(s *Settings) { s.Port = 70000 },
		func(s *Settings) { s.Upstreams = []string{"dns.example.com"} }, // plain DNS needs an IP
		func(s *Settings) { s.Upstreams = []string{"ftp://1.1.1.1"} },
		func(s *Settings) { s.Upstreams = []string{"https://1.1.1.1/dns-query?x=1"} },
		func(s *Settings) { s.Upstreams = []string{"9.9.9.9; rm"} },
		func(s *Settings) { s.Bootstrap = "127.0.0.1" },
		func(s *Settings) { s.BlockMode = "sinkhole" },
		func(s *Settings) { s.Lists = append(s.Lists, List{Name: "x", URL: "file:///etc/passwd"}) },
		func(s *Settings) { s.Lists = append(s.Lists, List{Name: "x", URL: "https://user:pw@example.com/l"}) },
		func(s *Settings) { s.Lists = append(s.Lists, List{Name: "", URL: "https://example.com/l"}) },
		func(s *Settings) {
			s.Lists = append(s.Lists, List{ID: "Bad ID", Name: "x", URL: "https://example.com/l"})
		},
		func(s *Settings) { s.Block = []string{"not a domain"} },
		func(s *Settings) { s.Records = []Record{{Name: "x.home", Type: "A", Value: "::1"}} },
		func(s *Settings) { s.Records = []Record{{Name: "x.home", Type: "MX", Value: "y.home"}} },
		func(s *Settings) { s.Records = []Record{{Name: "x.home", Type: "CNAME", Value: "x.home"}} },
		func(s *Settings) { s.Records = []Record{{Name: "*.home", Type: "A", Value: "1.2.3.4"}} },
		func(s *Settings) { s.RefreshHours = 5 },
		func(s *Settings) { s.LogHours = 3 },
		func(s *Settings) { s.CacheSize = -1 },
	}
	for i, f := range bad {
		s := Defaults()
		f(&s)
		if _, err := s.Validate(); err == nil || !IsInvalid(err) {
			t.Errorf("case %d accepted (%v)", i, err)
		}
	}
}

// fakeUp answers every A question with 93.184.216.34, optionally through a
// CNAME, and counts the questions it got.
type fakeUp struct {
	mu    sync.Mutex
	n     int
	cname string
	fail  bool
	ttl   uint32
}

func (f *fakeUp) String() string { return "fake" }

func (f *fakeUp) Exchange(_ context.Context, msg []byte) ([]byte, error) {
	f.mu.Lock()
	f.n++
	f.mu.Unlock()
	if f.fail {
		return nil, errors.New("down")
	}
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		return nil, err
	}
	q, err := p.Question()
	if err != nil {
		return nil, err
	}
	ttl := f.ttl
	if ttl == 0 {
		ttl = 300
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, RecursionDesired: true, RecursionAvailable: true})
	_ = b.StartQuestions()
	_ = b.Question(q)
	_ = b.StartAnswers()
	owner := q.Name
	if f.cname != "" {
		target := dnsmessage.MustNewName(f.cname + ".")
		_ = b.CNAMEResource(dnsmessage.ResourceHeader{Name: owner, Class: dnsmessage.ClassINET, TTL: ttl}, dnsmessage.CNAMEResource{CNAME: target})
		owner = target
	}
	if q.Type == dnsmessage.TypeA {
		_ = b.AResource(dnsmessage.ResourceHeader{Name: owner, Class: dnsmessage.ClassINET, TTL: ttl}, dnsmessage.AResource{A: [4]byte{93, 184, 216, 34}})
	}
	return b.Finish()
}

func (f *fakeUp) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func query(t *testing.T, id uint16, name string, typ dnsmessage.Type) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	_ = b.StartQuestions()
	if err := b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name + "."), Type: typ, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type answer struct {
	h     dnsmessage.Header
	addrs []string
	ttls  []uint32
	cname []string
}

func parse(t *testing.T, resp []byte) answer {
	t.Helper()
	var m dnsmessage.Message
	if err := m.Unpack(resp); err != nil {
		t.Fatalf("unpack: %v", err)
	}
	a := answer{h: m.Header}
	for _, r := range m.Answers {
		switch b := r.Body.(type) {
		case *dnsmessage.AResource:
			a.addrs = append(a.addrs, netip.AddrFrom4(b.A).String())
		case *dnsmessage.AAAAResource:
			a.addrs = append(a.addrs, netip.AddrFrom16(b.AAAA).String())
		case *dnsmessage.CNAMEResource:
			a.cname = append(a.cname, b.CNAME.String())
		}
		a.ttls = append(a.ttls, r.Header.TTL)
	}
	return a
}

func testResolver(up Upstream, mode string) *Resolver {
	rs := NewResolver(100)
	rs.setRules(&rules{
		blockMode:   mode,
		checkCNAMEs: true,
		lists:       []namedSet{{name: "Ads", set: NewDomainSet([]string{"ads.example.com", "tracker.example.net"})}},
		block:       NewDomainSet([]string{"custom.example.org"}),
		allow:       NewDomainSet([]string{"ok.ads.example.com"}),
		records: map[string][]Record{
			"nas":        {{Name: "nas", Type: "A", Value: "192.168.1.10"}, {Name: "nas", Type: "AAAA", Value: "fd00::10"}},
			"media.home": {{Name: "media.home", Type: "CNAME", Value: "nas"}},
		},
		ups: []Upstream{up},
	}, 100)
	return rs
}

var me = netip.MustParseAddr("192.168.1.50")

func TestHandleBlocked(t *testing.T) {
	up := &fakeUp{}
	rs := testResolver(up, "null")
	var logged []Query
	rs.onQuery = func(q Query) { logged = append(logged, q) }
	ctx := context.Background()

	a := parse(t, rs.Handle(ctx, query(t, 7, "x.ads.example.com", dnsmessage.TypeA), me, 512))
	if a.h.ID != 7 || !a.h.Response || a.h.RCode != dnsmessage.RCodeSuccess || !reflect.DeepEqual(a.addrs, []string{"0.0.0.0"}) {
		t.Fatalf("A: %+v", a)
	}
	a = parse(t, rs.Handle(ctx, query(t, 8, "tracker.example.net", dnsmessage.TypeAAAA), me, 512))
	if !reflect.DeepEqual(a.addrs, []string{"::"}) {
		t.Fatalf("AAAA: %+v", a)
	}
	a = parse(t, rs.Handle(ctx, query(t, 9, "custom.example.org", dnsmessage.TypeMX), me, 512))
	if len(a.addrs) != 0 || a.h.RCode != dnsmessage.RCodeSuccess {
		t.Fatalf("MX: %+v", a)
	}
	if up.count() != 0 {
		t.Fatal("blocked names went upstream")
	}
	if len(logged) != 3 || logged[0].Status != "blocked" || logged[0].Reason != "Ads" || logged[2].Reason != "Your block list" || logged[0].Client != "192.168.1.50" {
		t.Fatalf("log %+v", logged)
	}

	nx := testResolver(up, "nxdomain")
	if a := parse(t, nx.Handle(ctx, query(t, 1, "ads.example.com", dnsmessage.TypeA), me, 512)); a.h.RCode != dnsmessage.RCodeNameError || len(a.addrs) != 0 {
		t.Fatalf("nxdomain: %+v", a)
	}
}

func TestHandleAllowedGoesUpstream(t *testing.T) {
	up := &fakeUp{}
	rs := testResolver(up, "null")
	ctx := context.Background()
	for _, name := range []string{"example.com", "ok.ads.example.com", "sub.ok.ads.example.com"} {
		a := parse(t, rs.Handle(ctx, query(t, 42, name, dnsmessage.TypeA), me, 512))
		if a.h.ID != 42 || !reflect.DeepEqual(a.addrs, []string{"93.184.216.34"}) {
			t.Fatalf("%s: %+v", name, a)
		}
	}
	if up.count() != 3 {
		t.Fatalf("upstream asked %d times", up.count())
	}
}

func TestPause(t *testing.T) {
	up := &fakeUp{}
	rs := testResolver(up, "null")
	rs.Pause(time.Now().Add(time.Minute))
	if rs.PausedUntil().IsZero() {
		t.Fatal("not paused")
	}
	a := parse(t, rs.Handle(context.Background(), query(t, 1, "ads.example.com", dnsmessage.TypeA), me, 512))
	if !reflect.DeepEqual(a.addrs, []string{"93.184.216.34"}) {
		t.Fatalf("paused but blocked: %+v", a)
	}
	rs.Pause(time.Time{})
	a = parse(t, rs.Handle(context.Background(), query(t, 1, "ads.example.com", dnsmessage.TypeA), me, 512))
	if !reflect.DeepEqual(a.addrs, []string{"0.0.0.0"}) {
		t.Fatalf("resumed but not blocked: %+v", a)
	}
}

func TestCacheCountsDown(t *testing.T) {
	up := &fakeUp{ttl: 100}
	rs := testResolver(up, "null")
	now := time.Unix(1_700_000_000, 0)
	rs.now = func() time.Time { return now }
	ctx := context.Background()
	var statuses []string
	rs.onQuery = func(q Query) { statuses = append(statuses, q.Status) }
	rs.Handle(ctx, query(t, 1, "example.com", dnsmessage.TypeA), me, 512)
	now = now.Add(30 * time.Second)
	a := parse(t, rs.Handle(ctx, query(t, 2, "EXAMPLE.com", dnsmessage.TypeA), me, 512))
	if up.count() != 1 || a.h.ID != 2 || a.ttls[0] != 70 {
		t.Fatalf("cache: upstream %d, %+v", up.count(), a)
	}
	now = now.Add(80 * time.Second) // past the TTL
	rs.Handle(ctx, query(t, 3, "example.com", dnsmessage.TypeA), me, 512)
	if up.count() != 2 {
		t.Fatal("expired answer served")
	}
	if !reflect.DeepEqual(statuses, []string{"forwarded", "cached", "forwarded"}) {
		t.Fatalf("statuses %q", statuses)
	}
}

func TestCNAMEToBlockedName(t *testing.T) {
	up := &fakeUp{cname: "edge.tracker.example.net"}
	rs := testResolver(up, "null")
	var last Query
	rs.onQuery = func(q Query) { last = q }
	a := parse(t, rs.Handle(context.Background(), query(t, 5, "innocent.example.com", dnsmessage.TypeA), me, 512))
	if !reflect.DeepEqual(a.addrs, []string{"0.0.0.0"}) || last.Status != "blocked" || !strings.Contains(last.Reason, "edge.tracker.example.net") {
		t.Fatalf("%+v %+v", a, last)
	}
}

func TestLocalRecords(t *testing.T) {
	up := &fakeUp{}
	rs := testResolver(up, "null")
	ctx := context.Background()
	a := parse(t, rs.Handle(ctx, query(t, 1, "nas", dnsmessage.TypeA), me, 512))
	if !a.h.Authoritative || !reflect.DeepEqual(a.addrs, []string{"192.168.1.10"}) {
		t.Fatalf("A %+v", a)
	}
	a = parse(t, rs.Handle(ctx, query(t, 1, "nas", dnsmessage.TypeAAAA), me, 512))
	if !reflect.DeepEqual(a.addrs, []string{"fd00::10"}) {
		t.Fatalf("AAAA %+v", a)
	}
	a = parse(t, rs.Handle(ctx, query(t, 1, "media.home", dnsmessage.TypeA), me, 512))
	if !reflect.DeepEqual(a.cname, []string{"nas."}) || !reflect.DeepEqual(a.addrs, []string{"192.168.1.10"}) {
		t.Fatalf("CNAME %+v", a)
	}
	a = parse(t, rs.Handle(ctx, query(t, 1, "nas", dnsmessage.TypeMX), me, 512))
	if len(a.addrs) != 0 || a.h.RCode != dnsmessage.RCodeSuccess {
		t.Fatalf("MX %+v", a)
	}
	if up.count() != 0 {
		t.Fatal("local name went upstream")
	}
}

func TestUpstreamFailures(t *testing.T) {
	rs := testResolver(&fakeUp{fail: true}, "null")
	a := parse(t, rs.Handle(context.Background(), query(t, 1, "example.com", dnsmessage.TypeA), me, 512))
	if a.h.RCode != dnsmessage.RCodeServerFailure {
		t.Fatalf("%+v", a)
	}
	// The next upstream answers when the first fails.
	good := &fakeUp{}
	rs.r.ups = []Upstream{&fakeUp{fail: true}, good}
	a = parse(t, rs.Handle(context.Background(), query(t, 1, "example.com", dnsmessage.TypeA), me, 512))
	if len(a.addrs) != 1 || good.count() != 1 {
		t.Fatalf("failover %+v", a)
	}
}

func TestHandleGarbage(t *testing.T) {
	rs := testResolver(&fakeUp{}, "null")
	if rs.Handle(context.Background(), []byte{1, 2, 3}, me, 512) != nil {
		t.Fatal("answered garbage")
	}
	// An answer isn't a question.
	resp := rs.Handle(context.Background(), query(t, 1, "ads.example.com", dnsmessage.TypeA), me, 512)
	if rs.Handle(context.Background(), resp, me, 512) != nil {
		t.Fatal("answered an answer")
	}
	// A header without the question it promises.
	hdr := []byte{0, 9, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	a := parse(t, rs.Handle(context.Background(), hdr, me, 512))
	if a.h.RCode != dnsmessage.RCodeFormatError || a.h.ID != 9 {
		t.Fatalf("%+v", a)
	}
}

func TestTruncatesBigUDPAnswers(t *testing.T) {
	big := &bigUp{}
	rs := testResolver(big, "null")
	a := parse(t, rs.Handle(context.Background(), query(t, 1, "big.example.com", dnsmessage.TypeTXT), me, 512))
	if !a.h.Truncated {
		t.Fatal("not truncated")
	}
	full := rs.Handle(context.Background(), query(t, 1, "big.example.com", dnsmessage.TypeTXT), me, 0)
	if len(full) <= 512 {
		t.Fatal("TCP answer cut")
	}
}

type bigUp struct{}

func (bigUp) String() string { return "big" }
func (bigUp) Exchange(_ context.Context, msg []byte) ([]byte, error) {
	var p dnsmessage.Parser
	h, _ := p.Start(msg)
	q, _ := p.Question()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true})
	_ = b.StartQuestions()
	_ = b.Question(q)
	_ = b.StartAnswers()
	for i := 0; i < 10; i++ {
		_ = b.TXTResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}, dnsmessage.TXTResource{TXT: []string{strings.Repeat("x", 200)}})
	}
	return b.Finish()
}

func TestCounters(t *testing.T) {
	c := newCounters()
	now := time.Unix(1_700_000_000, 0)
	c.add(now.Add(-2*time.Minute), true)
	c.add(now.Add(-2*time.Minute), false)
	c.add(now, false)
	c.add(now.Add(-25*time.Hour), true) // too old: overwritten slot or ignored
	s := c.series(now, 60, 1)
	if len(s) != 60 || s[59].Total != 1 || s[57].Total != 2 || s[57].Blocked != 1 {
		t.Fatalf("series %+v", s[55:])
	}
	if total, blocked := c.since(now.Add(-time.Hour), now); total != 3 || blocked != 1 {
		t.Fatalf("since %d %d", total, blocked)
	}
}

// ---------- end to end: the service on loopback, with a fake upstream ----------

func freePort(t *testing.T) int {
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

// udpUpstream serves fakeUp over real UDP.
func udpUpstream(t *testing.T) (string, *fakeUp) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	f := &fakeUp{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			resp, err := f.Exchange(context.Background(), buf[:n])
			if err == nil {
				_, _ = pc.WriteTo(resp, from)
			}
		}
	}()
	return "udp://" + pc.LocalAddr().String(), f
}

func TestServiceEndToEnd(t *testing.T) {
	testHosts = []string{"127.0.0.1"}
	defer func() { testHosts = nil }()
	upAddr, up := udpUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.systemHooks = false
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if st := svc.Status(); st.Running || st.Settings.Enabled {
		t.Fatal("on by default")
	}
	port := freePort(t)
	s := Defaults()
	s.Enabled, s.Port, s.Lists = true, port, []List{}
	s.Upstreams = []string{upAddr}
	s.Block = []string{"ads.example.com"}
	st, err := svc.Apply(s)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || len(st.Listening) != 1 {
		t.Fatalf("%+v", st)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	// UDP
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ask := func(name string) answer {
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := c.Write(query(t, 77, name, dnsmessage.TypeA)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 4096)
		n, err := c.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		return parse(t, buf[:n])
	}
	if a := ask("www.ads.example.com"); !reflect.DeepEqual(a.addrs, []string{"0.0.0.0"}) {
		t.Fatalf("blocked over UDP: %+v", a)
	}
	if a := ask("example.com"); !reflect.DeepEqual(a.addrs, []string{"93.184.216.34"}) {
		t.Fatalf("allowed over UDP: %+v", a)
	}

	// TCP
	tc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	_ = tc.SetDeadline(time.Now().Add(5 * time.Second))
	q := query(t, 78, "other.example.com", dnsmessage.TypeA)
	frame := append([]byte{byte(len(q) >> 8), byte(len(q))}, q...)
	if _, err := tc.Write(frame); err != nil {
		t.Fatal(err)
	}
	var l [2]byte
	if _, err := io.ReadFull(tc, l[:]); err != nil {
		t.Fatal(err)
	}
	resp := make([]byte, binary.BigEndian.Uint16(l[:]))
	if _, err := io.ReadFull(tc, resp); err != nil {
		t.Fatal(err)
	}
	if a := parse(t, resp); !reflect.DeepEqual(a.addrs, []string{"93.184.216.34"}) || a.h.ID != 78 {
		t.Fatalf("TCP: %+v", a)
	}
	if up.count() != 2 {
		t.Fatalf("upstream asked %d times", up.count())
	}

	// The log and the dashboard see it (the log writes once a second).
	deadline := time.Now().Add(5 * time.Second)
	var logged []Query
	for time.Now().Before(deadline) {
		logged, _ = svc.Log(ctx, LogFilter{})
		if len(logged) >= 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(logged) != 3 {
		t.Fatalf("log %+v", logged)
	}
	blocked, _ := svc.Log(ctx, LogFilter{Status: "blocked"})
	if len(blocked) != 1 || blocked[0].Name != "www.ads.example.com" || blocked[0].Client != "127.0.0.1" {
		t.Fatalf("blocked log %+v", blocked)
	}
	if found, _ := svc.Log(ctx, LogFilter{Search: "other"}); len(found) != 1 {
		t.Fatalf("search %+v", found)
	}
	stats := svc.Stats(ctx)
	if stats.Today != 3 || stats.Blocked != 1 || len(stats.TopBlocked) != 1 || len(stats.Clients) != 1 || len(stats.LastHour) != 60 {
		t.Fatalf("stats %+v", stats)
	}
	if err := svc.ClearLog(); err != nil {
		t.Fatal(err)
	}
	if left, _ := svc.Log(ctx, LogFilter{}); len(left) != 0 {
		t.Fatal("log not cleared")
	}

	// Log off: nothing is written.
	s.LogEnabled = false
	if _, err := svc.Apply(s); err != nil {
		t.Fatal(err)
	}
	ask("more.example.com")
	time.Sleep(1500 * time.Millisecond)
	if left, _ := svc.Log(ctx, LogFilter{}); len(left) != 0 {
		t.Fatal("logged with the log off")
	}

	// Turning it off stops answering.
	s.Enabled = false
	if st, err := svc.Apply(s); err != nil || st.Running {
		t.Fatalf("still running: %v", err)
	}
	if svc.Settings().UseOnServer {
		t.Fatal("use on server left on")
	}
}

func TestListDownloadsUseSavedFiles(t *testing.T) {
	dir := t.TempDir()
	svc := New(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.systemHooks = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// A list already on disk is loaded without the network.
	if err := writeTestFile(svc.listPath("unified"), "ads.example.com\nb.example.com\n"); err != nil {
		t.Fatal(err)
	}
	s := Defaults()
	s.Enabled = true
	s.Lists = []List{{ID: "unified", Name: "Test", URL: "https://example.invalid/list", Enabled: true}}
	svc.mu.Lock()
	svc.settings = s
	svc.rebuildLocked()
	svc.mu.Unlock()
	st := svc.Status()
	if st.Domains != 2 {
		t.Fatalf("%+v", st.Lists)
	}
	var names []string
	for _, l := range svc.res.r.lists {
		names = append(names, l.name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Test"}) {
		t.Fatalf("lists %q", names)
	}
}

func writeTestFile(path, text string) error { return os.WriteFile(path, []byte(text), 0o600) }
