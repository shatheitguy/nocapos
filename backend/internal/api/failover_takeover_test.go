package api

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestDecideTakeover(t *testing.T) {
	five := 5 * time.Minute
	cases := []struct {
		name string
		in   takeoverIn
		want takeoverAct
	}{
		{"standby, primary up", takeoverIn{PeerUp: true, Auto: true, After: five}, actNone},
		{"standby, primary down briefly", takeoverIn{PeerDown: 2 * time.Minute, Auto: true, After: five}, actNone},
		{"standby, primary down long enough", takeoverIn{PeerDown: five, Auto: true, After: five}, actTakeOver},
		{"standby, automatic off", takeoverIn{PeerDown: time.Hour, Auto: false, After: five}, actNone},
		{"standby already running the apps", takeoverIn{Active: true, PeerDown: time.Hour, Auto: true, After: five}, actNone},
		{"standby running, primary back", takeoverIn{Active: true, PeerUp: true, Auto: true, After: five}, actBack},
		{"primary, standby took over", takeoverIn{Primary: true, PeerUp: true, PeerActive: true}, actYield},
		{"primary already stood back", takeoverIn{Primary: true, Yielded: true, PeerUp: true, PeerActive: true}, actNone},
		{"primary, standby down", takeoverIn{Primary: true, PeerDown: time.Hour, Auto: true, After: five}, actNone},
		{"primary, standby handed back", takeoverIn{Primary: true, Yielded: true, PeerUp: true}, actNone},
	}
	for _, c := range cases {
		if got := decideTakeover(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFailoverOptionsWindowAndVIP(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 22, h, m, 0, 0, time.Local) }
	night := failoverOptions{WindowFrom: "22:00", WindowTo: "06:00"}
	if !night.inWindow(at(23, 0)) || !night.inWindow(at(5, 59)) || night.inWindow(at(12, 0)) {
		t.Fatal("a window past midnight")
	}
	day := failoverOptions{WindowFrom: "09:00", WindowTo: "17:30"}
	if !day.inWindow(at(9, 0)) || day.inWindow(at(17, 30)) || day.inWindow(at(8, 0)) {
		t.Fatal("a daytime window")
	}
	if !(failoverOptions{}).inWindow(at(3, 0)) {
		t.Fatal("no window = always")
	}
	if o := (failoverOptions{}); !o.auto() || o.after() != 5*time.Minute {
		t.Fatal("defaults")
	}
	for _, bad := range [][2]string{{"8.8.8.8/24", "eth0"}, {"192.168.1.50", "eth0"}, {"192.168.1.50/24", ""}, {"192.168.1.50/24", "eth0; rm"}} {
		if validVIP(bad[0], bad[1]) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := validVIP("192.168.1.50/24", "eth0"); err != nil {
		t.Fatal(err)
	}
}

// TestFailoverTakeoverEndToEnd: two paired servers on localhost. The
// primary stops answering, the standby takes over after the wait; the
// primary comes back and stands back; the standby hands back.
func TestFailoverTakeoverEndToEnd(t *testing.T) {
	ctx := context.Background()
	a, b := newSquadNode(t), newSquadNode(t)
	boss := pairNodes(t, a, b) // A is primary
	var skew time.Duration
	b.w.s.failover.now = func() time.Time { return time.Now().Add(skew) }

	b.w.s.failoverTakeoverTick(ctx) // A answers: nothing happens
	if b.w.s.flag(ctx, keyFailoverActive) {
		t.Fatal("took over from a server that answers")
	}
	// A goes quiet (its address stops answering).
	la, _ := b.w.st.ServerLink(ctx, a.id)
	good := la.URL
	la.URL = "https://127.0.0.1:1"
	if err := b.w.st.SaveServerLink(ctx, la); err != nil {
		t.Fatal(err)
	}
	skew = 2 * time.Minute
	b.w.s.failoverTakeoverTick(ctx)
	if b.w.s.flag(ctx, keyFailoverActive) {
		t.Fatal("took over before the wait was up")
	}
	skew = 6 * time.Minute
	b.w.s.failoverTakeoverTick(ctx)
	if !b.w.s.flag(ctx, keyFailoverActive) {
		t.Fatal("didn't take over")
	}
	if n, _ := b.w.st.CountAuditAction(ctx, "failover.take_over"); n == 0 {
		t.Error("take-over not audited")
	}
	if txt := b.w.s.failoverTXT(ctx); len(txt) != 1 || txt[0] != "stands_in_for="+a.id {
		t.Fatalf("mDNS record: %v", txt)
	}
	// A comes back, sees B runs the apps, and stands back.
	la.URL = good
	if err := b.w.st.SaveServerLink(ctx, la); err != nil {
		t.Fatal(err)
	}
	a.w.s.failoverTakeoverTick(ctx)
	if !a.w.s.flag(ctx, keyFailoverYielded) {
		t.Fatal("primary didn't stand back")
	}
	b.w.s.failoverTakeoverTick(ctx)
	j := b.failover(t, boss).Pair.Takeover
	if j == nil || j.Role != "standby" || !j.Running || !j.CanHandBack || !j.PeerUp {
		t.Fatalf("standby status: %+v", j)
	}
	// Only the standby hands back; then the primary runs the apps again.
	if rec := squadCall(a.w.s, a.w.s.failoverHandBack, "POST", "/", nil, callOpts{as: boss}); rec.Code != http.StatusConflict {
		t.Fatalf("hand back on the primary: %d", rec.Code)
	}
	rec := squadCall(b.w.s, b.w.s.failoverHandBack, "POST", "/", nil, callOpts{as: boss})
	if rec.Code != http.StatusOK {
		t.Fatalf("hand back: %d %s", rec.Code, rec.Body)
	}
	if b.w.s.flag(ctx, keyFailoverActive) || a.w.s.flag(ctx, keyFailoverYielded) {
		t.Fatal("flags after hand-back")
	}
	if n, _ := a.w.st.CountAuditAction(ctx, "failover.handed_back"); n == 0 {
		t.Error("hand-back not audited on the primary")
	}
	// Settings: automatic off, a shared address; both servers keep them.
	rec = squadCall(a.w.s, a.w.s.failoverSetTakeover, "PUT", "/", map[string]any{"auto": false, "after_min": 10, "vip": "192.168.1.50/24", "vip_iface": "eth0"}, callOpts{as: boss})
	if rec.Code != http.StatusOK {
		t.Fatalf("takeover settings: %d %s", rec.Code, rec.Body)
	}
	pb, _ := b.w.st.FailoverPair(ctx, a.id)
	if o := parseFailoverOptions(pb.Options); o.auto() || o.after() != 10*time.Minute || o.VIP != "192.168.1.50/24" {
		t.Fatalf("B's options: %s", pb.Options)
	}
}
