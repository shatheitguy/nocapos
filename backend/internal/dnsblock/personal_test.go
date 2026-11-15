package dnsblock

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// TestPersonalRulesOrder: the device's lists, then the person's, then the
// server's.
func TestPersonalRulesOrder(t *testing.T) {
	up := &fakeUp{}
	rs := testResolver(up, "null")
	ctx := context.Background()
	per := &Personal{
		DeviceAllow: NewDomainSet([]string{"tracker.example.net"}),
		DeviceBlock: NewDomainSet([]string{"ok.ads.example.com", "games.example"}),
		UserAllow:   NewDomainSet([]string{"games.example", "custom.example.org"}),
		UserBlock:   NewDomainSet([]string{"social.example"}),
	}
	cases := []struct {
		name, status, reason string
	}{
		{"tracker.example.net", "forwarded", ""},             // server-blocked, device-allowed
		{"ok.ads.example.com", "blocked", ReasonDeviceBlock}, // server-allowed, device-blocked
		{"games.example", "blocked", ReasonDeviceBlock},      // device block wins over the person's allow
		{"custom.example.org", "forwarded", ""},              // person allows the server's block
		{"m.social.example", "blocked", ReasonUserBlock},     // subdomains too
		{"x.ads.example.com", "blocked", "Ads"},              // the server's lists still apply
		{"example.com", "forwarded", ""},
	}
	for i, c := range cases {
		res := rs.HandleWith(ctx, query(t, uint16(i+1), c.name, dnsmessage.TypeA), me, Opts{Personal: per, Device: "dev1", Client: "Pixel"})
		if res.Status != c.status || res.Reason != c.reason {
			t.Errorf("%s: %s %q, want %s %q", c.name, res.Status, res.Reason, c.status, c.reason)
		}
		if a := parse(t, res.Msg); a.h.ID != uint16(i+1) {
			t.Errorf("%s: id %d", c.name, a.h.ID)
		}
	}
	// Without personal lists nothing changes for everyone else.
	if res := rs.HandleWith(ctx, query(t, 50, "tracker.example.net", dnsmessage.TypeA), me, Opts{}); res.Status != "blocked" {
		t.Fatalf("server list: %+v", res)
	}
}

func TestPersonalBlocksCNAMETarget(t *testing.T) {
	rs := testResolver(&fakeUp{cname: "cdn.social.example"}, "null")
	per := &Personal{UserBlock: NewDomainSet([]string{"social.example"})}
	res := rs.HandleWith(context.Background(), query(t, 3, "innocent.example", dnsmessage.TypeA), me, Opts{Personal: per})
	if res.Status != "blocked" || !strings.HasPrefix(res.Reason, "CNAME ") {
		t.Fatalf("%+v", res)
	}
}

func TestHandleWithLogsDevice(t *testing.T) {
	rs := testResolver(&fakeUp{}, "null")
	var got Query
	rs.onQuery = func(q Query) { got = q }
	rs.HandleWith(context.Background(), query(t, 9, "x.ads.example.com", dnsmessage.TypeA), me, Opts{Client: "Pixel 9", Device: "d1"})
	if got.Client != "Pixel 9" || got.Device != "d1" || got.Status != "blocked" {
		t.Fatalf("%+v", got)
	}
}

func TestPersonalStore(t *testing.T) {
	dir := t.TempDir()
	p := openPersonal(dir)
	if _, err := p.set("user:a", PersonalLists{Block: []string{"Ads.Example.com", "ads.example.com", ""}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.set("user:a", PersonalLists{Block: []string{"not a domain"}}); err == nil || !IsInvalid(err) {
		t.Fatalf("bad domain: %v", err)
	}
	big := make([]string, maxPersonalEntries+1)
	for i := range big {
		big[i] = "x" + strings.Repeat("a", i%50) + ".example"
	}
	if _, err := p.set("user:a", PersonalLists{Allow: big}); err == nil {
		t.Fatal("too many entries accepted")
	}
	got := openPersonal(dir).get("user:a")
	if !reflect.DeepEqual(got.Block, []string{"ads.example.com"}) || len(got.Allow) != 0 {
		t.Fatalf("saved: %+v", got)
	}
	if _, b := p.sets("user:a"); !setHas(b, "www.ads.example.com") {
		t.Fatal("compiled set")
	}
	p.forget("user:a")
	if a, b := p.sets("user:a"); a != nil || b != nil {
		t.Fatal("forgotten lists still apply")
	}
	if l := openPersonal(dir).get("user:a"); len(l.Block) != 0 {
		t.Fatal("forget wasn't saved")
	}
}

// TestQueryLogAddsDeviceColumn: a log from before devices could ask keeps
// working.
func TestQueryLogAddsDeviceColumn(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "querylog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE queries (t INTEGER NOT NULL, client TEXT NOT NULL, name TEXT NOT NULL, qtype TEXT NOT NULL,
		status TEXT NOT NULL, reason TEXT NOT NULL, upstream TEXT NOT NULL, ms INTEGER NOT NULL, rcode TEXT NOT NULL);
		INSERT INTO queries VALUES (1, '', 'old.example', 'A', 'blocked', 'x', '', 1, 'NOERROR')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	l, err := openQueryLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.db.Close()
	if !hasColumn(l.db, "queries", "device") {
		t.Fatal("no device column")
	}
	rows, err := l.list(context.Background(), LogFilter{})
	if err != nil || len(rows) != 1 || rows[0].Name != "old.example" {
		t.Fatalf("%v %+v", err, rows)
	}
}

// TestAskNeedsBlockerOn and counts per device.
func TestAskAndDeviceToday(t *testing.T) {
	testHosts = []string{"127.0.0.1"}
	defer func() { testHosts = nil }()
	upAddr, _ := udpUpstream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.systemHooks = false
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Ask(ctx, query(t, 1, "example.com", dnsmessage.TypeA), Asker{UserID: "u", DeviceID: "d"}); err != ErrOff {
		t.Fatalf("off: %v", err)
	}
	s := Defaults()
	s.Enabled, s.Port, s.Lists, s.Upstreams = true, freePort(t), []List{}, []string{upAddr}
	if _, err := svc.Apply(s); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPersonal("device", "d", PersonalLists{Block: []string{"mine.example"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPersonal("everyone", "d", PersonalLists{}); err == nil {
		t.Fatal("bad scope accepted")
	}
	res, err := svc.Ask(ctx, query(t, 2, "a.mine.example", dnsmessage.TypeA), Asker{UserID: "u", DeviceID: "d", Label: "Pixel"})
	if err != nil || res.Status != "blocked" {
		t.Fatalf("%v %+v", err, res)
	}
	// Another device of someone else isn't affected.
	if res, _ := svc.Ask(ctx, query(t, 3, "a.mine.example", dnsmessage.TypeA), Asker{UserID: "v", DeviceID: "e"}); res.Status != "forwarded" {
		t.Fatalf("other device: %+v", res)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		total, blocked, last := svc.DeviceToday(ctx, "d", 10)
		if total == 1 && blocked == 1 && len(last) == 1 && last[0].Name == "a.mine.example" && last[0].Client == "Pixel" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("today: %d %d %+v", total, blocked, last)
		}
		time.Sleep(100 * time.Millisecond)
	}
	svc.ForgetDevice("d")
	if u, d := svc.PersonalFor("u", "d"); len(d.Block) != 0 || len(u.Block) != 0 {
		t.Fatal("forgotten")
	}
}
