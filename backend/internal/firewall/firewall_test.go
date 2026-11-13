package firewall

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeAccepts(t *testing.T) {
	cases := []struct {
		in   Rule
		want Rule
	}{
		{Rule{Action: "ALLOW", Port: " 22 ", Proto: "TCP"}, Rule{Action: "allow", Direction: "in", Proto: "tcp", Port: "22"}},
		{Rule{Action: "deny", Port: "8000-8100", Proto: "udp", From: "10.0.0.7/8"}, Rule{Action: "deny", Direction: "in", Proto: "udp", Port: "8000:8100", From: "10.0.0.0/8"}},
		{Rule{Action: "limit", App: "OpenSSH"}, Rule{Action: "limit", Direction: "in", Proto: "any", App: "OpenSSH"}},
		{Rule{Action: "reject", Direction: "out", Port: "25", Proto: "tcp", Comment: " no mail "}, Rule{Action: "reject", Direction: "out", Proto: "tcp", Port: "25", Comment: "no mail"}},
		{Rule{Action: "allow", From: "::ffff:192.168.1.2"}, Rule{Action: "allow", Direction: "in", Proto: "any", From: "192.168.1.2"}},
		{Rule{Action: "allow", From: "192.168.1.9/32", Interface: "eth0"}, Rule{Action: "allow", Direction: "in", Proto: "any", From: "192.168.1.9", Interface: "eth0"}},
		{Rule{Action: "allow", Port: "80, 443", Proto: "tcp", From: "0.0.0.0/0"}, Rule{Action: "allow", Direction: "in", Proto: "tcp", Port: "80,443"}},
		{Rule{Action: "allow", Port: "053"}, Rule{Action: "allow", Direction: "in", Proto: "any", Port: "53"}},
		{Rule{Action: "allow", From: "FD00::1"}, Rule{Action: "allow", Direction: "in", Proto: "any", From: "fd00::1"}},
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if err != nil {
			t.Errorf("Normalize(%+v): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Normalize(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestNormalizeRejects(t *testing.T) {
	bad := []Rule{
		{Action: "accept", Port: "22"},
		{Action: "allow", Direction: "sideways", Port: "22"},
		{Action: "allow", Port: "22; rm -rf /"},
		{Action: "allow", Port: "0"},
		{Action: "allow", Port: "65536"},
		{Action: "allow", Port: "100:50", Proto: "tcp"},
		{Action: "allow", Port: "80,443"}, // several ports need a protocol
		{Action: "allow", Port: "1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16", Proto: "tcp"},
		{Action: "allow", Port: "22", Proto: "icmp"},
		{Action: "allow", Port: "22", Interface: "eth0 to any"},
		{Action: "allow", Port: "22", Interface: "-eth0"},
		{Action: "allow", Port: "22", From: "not-an-ip"},
		{Action: "allow", Port: "22", From: "fe80::1%eth0"},
		{Action: "allow", Port: "22", From: "10.0.0.1", To: "::1"},
		{Action: "allow", Port: "22", Comment: "it's mine"},
		{Action: "allow", Port: "22", Comment: "a\nb"},
		{Action: "allow", Port: "22", Comment: "x # y"},
		{Action: "allow", Port: "22", Comment: strings.Repeat("a", MaxComment+1)},
		{Action: "allow", App: "OpenSSH", Port: "22"},
		{Action: "allow", App: "Open;SSH"},
		{Action: "allow", App: "OpenSSH", Proto: "tcp"},
		{Action: "allow"},               // nothing to match on
		{Action: "allow", Proto: "tcp"}, // protocol alone
		{Action: "allow", Port: "-22"},
	}
	for _, r := range bad {
		if got, err := Normalize(r); err == nil {
			t.Errorf("Normalize(%+v) accepted: %+v", r, got)
		} else if !errors.Is(err, ErrInvalid) {
			t.Errorf("Normalize(%+v): error %v isn't ErrInvalid", r, err)
		}
	}
}

func TestArgs(t *testing.T) {
	r, err := Normalize(Rule{Action: "deny", Interface: "eth0", Proto: "tcp", From: "10.0.0.0/8", Port: "22", Comment: "lan only"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"deny", "in", "on", "eth0", "proto", "tcp", "from", "10.0.0.0/8", "to", "any", "port", "22", "comment", "lan only"}
	if got := r.Args(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Args = %q, want %q", got, want)
	}
	app, _ := Normalize(Rule{Action: "limit", App: "OpenSSH"})
	if got := app.Args(); !reflect.DeepEqual(got, []string{"limit", "in", "from", "any", "to", "any", "app", "OpenSSH"}) {
		t.Fatalf("app Args = %q", got)
	}
}

const numbered = `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 22/tcp                     ALLOW IN    Anywhere
[ 2] 80,443/tcp                 ALLOW IN    192.168.1.0/24             # web
[ 3] 8000:8100/udp              DENY IN     10.0.0.5
[ 4] OpenSSH                    LIMIT IN    Anywhere
[ 5] Anywhere on eth0           ALLOW IN    192.168.1.10
[ 6] 53                         ALLOW OUT   Anywhere                   (out)
[ 7] 192.168.1.2 443/tcp        REJECT IN   Anywhere
[ 8] Anywhere                   ALLOW FWD   10.0.0.0/8
[ 9] 8443/tcp                   ALLOW IN    192.168.1.50               # nocap keep-me-in
[10] 22/tcp (v6)                ALLOW IN    Anywhere (v6)
[11] Anywhere (v6)              DENY IN     fd00::/8
`

func TestParseNumbered(t *testing.T) {
	active, rules := ParseNumbered(numbered)
	if !active {
		t.Fatal("not active")
	}
	if len(rules) != 11 {
		t.Fatalf("got %d rules", len(rules))
	}
	check := func(i int, want Rule, editable, v6 bool) {
		t.Helper()
		e := rules[i]
		if e.Number != i+1 {
			t.Errorf("rule %d: number %d", i+1, e.Number)
		}
		if e.Editable != editable || e.V6 != v6 {
			t.Errorf("rule %d: editable %v v6 %v", i+1, e.Editable, e.V6)
		}
		if editable && e.Rule != want {
			t.Errorf("rule %d = %+v, want %+v", i+1, e.Rule, want)
		}
	}
	check(0, Rule{Action: "allow", Direction: "in", Proto: "tcp", Port: "22"}, true, false)
	check(1, Rule{Action: "allow", Direction: "in", Proto: "tcp", Port: "80,443", From: "192.168.1.0/24", Comment: "web"}, true, false)
	check(2, Rule{Action: "deny", Direction: "in", Proto: "udp", Port: "8000:8100", From: "10.0.0.5"}, true, false)
	check(3, Rule{Action: "limit", Direction: "in", Proto: "any", App: "OpenSSH"}, true, false)
	check(4, Rule{Action: "allow", Direction: "in", Proto: "any", Interface: "eth0", From: "192.168.1.10"}, true, false)
	check(5, Rule{Action: "allow", Direction: "out", Proto: "any", Port: "53"}, true, false)
	check(6, Rule{Action: "reject", Direction: "in", Proto: "tcp", Port: "443", To: "192.168.1.2"}, true, false)
	check(7, Rule{}, false, false) // routed
	check(9, Rule{Action: "allow", Direction: "in", Proto: "tcp", Port: "22"}, true, true)
	check(10, Rule{Action: "deny", Direction: "in", Proto: "any", From: "fd00::/8"}, true, true)
	if !rules[8].Guard {
		t.Error("guard rule not marked")
	}
	if rules[1].Raw != "80,443/tcp ALLOW IN 192.168.1.0/24 # web" {
		t.Errorf("raw = %q", rules[1].Raw)
	}
}

func TestParseNumberedOddLines(t *testing.T) {
	out := "Status: active\n\n[ 1] 22/tcp ALLOW IN Anywhere (log)\n[ 2] 47/gre ALLOW IN Anywhere\ngarbage\n[x] nope\n"
	_, rules := ParseNumbered(out)
	if len(rules) != 2 || rules[0].Editable || rules[1].Editable {
		t.Fatalf("got %+v", rules)
	}
	if a, r := ParseNumbered("Status: inactive\n"); a || len(r) != 0 {
		t.Fatal("inactive misread")
	}
}

func TestParseReport(t *testing.T) {
	out := `Status: active
Logging: on (medium)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip
`
	r := ParseReport(out)
	want := Report{Active: true, Incoming: "deny", Outgoing: "allow", Routed: "disabled", Logging: "medium"}
	if r != want {
		t.Fatalf("got %+v", r)
	}
	if r := ParseReport("Status: active\nLogging: off\nDefault: reject (incoming), deny (outgoing)"); r.Logging != "off" || r.Incoming != "reject" || r.Outgoing != "deny" {
		t.Fatalf("got %+v", r)
	}
}

func TestParseAdded(t *testing.T) {
	out := `Added user rules (see 'ufw status' for running firewall):
ufw allow 22/tcp
ufw limit OpenSSH
ufw deny in on eth0 proto tcp from 10.0.0.0/8 to any port 22 comment 'no lan ssh'
ufw allow out 53
ufw route allow in on eth0 out on eth1
ufw allow 8080
ufw allow from 192.168.1.0/24 to any app Samba
`
	rules := ParseAdded(out)
	if len(rules) != 7 {
		t.Fatalf("got %d", len(rules))
	}
	want := []Rule{
		{Action: "allow", Direction: "in", Proto: "tcp", Port: "22"},
		{Action: "limit", Direction: "in", Proto: "any", App: "OpenSSH"},
		{Action: "deny", Direction: "in", Interface: "eth0", Proto: "tcp", From: "10.0.0.0/8", Port: "22", Comment: "no lan ssh"},
		{Action: "allow", Direction: "out", Proto: "any", Port: "53"},
		{},
		{Action: "allow", Direction: "in", Proto: "any", Port: "8080"},
		{Action: "allow", Direction: "in", Proto: "any", From: "192.168.1.0/24", App: "Samba"},
	}
	for i, w := range want {
		if i == 4 {
			if rules[i].Editable {
				t.Error("route rule editable")
			}
			continue
		}
		if !rules[i].Editable || rules[i].Rule != w {
			t.Errorf("rule %d = %+v (editable %v), want %+v", i, rules[i].Rule, rules[i].Editable, w)
		}
	}
}

func TestParseAppsAndListeners(t *testing.T) {
	if got := ParseAppList("Available applications:\n  OpenSSH\n  Samba\n  Bad;Name\n"); !reflect.DeepEqual(got, []string{"OpenSSH", "Samba"}) {
		t.Fatalf("apps %q", got)
	}
	ps := ParseAppProfiles("[OpenSSH]\ntitle=Secure shell server\ndescription=Remote access\nports=22/tcp\n\n[Samba]\nports=137,138/udp|139,445/tcp\n")
	if len(ps) != 2 || ps[0].Title != "Secure shell server" || ps[1].Ports != "137,138/udp, 139,445/tcp" {
		t.Fatalf("profiles %+v", ps)
	}
	ss := `udp   UNCONN 0      0          127.0.0.53%lo:53         0.0.0.0:*    users:(("systemd-resolve",pid=512,fd=13))
tcp   LISTEN 0      4096             0.0.0.0:22         0.0.0.0:*    users:(("sshd",pid=900,fd=3))
tcp   LISTEN 0      4096                [::]:22            [::]:*    users:(("sshd",pid=900,fd=4))
tcp   LISTEN 0      4096                   *:443              *:*    users:(("alfad",pid=1000,fd=8))
tcp   LISTEN 0      4096                   *:443              *:*    users:(("alfad",pid=1000,fd=9))
`
	ls := ParseListeners(ss)
	if len(ls) != 4 {
		t.Fatalf("listeners %+v", ls)
	}
	if !ls[0].Loopback() || ls[0].Process != "systemd-resolve" || ls[1].Port != 22 || ls[1].PID != 900 || ls[3].Addr != "::" || ls[3].Loopback() {
		t.Fatalf("listeners %+v", ls)
	}
}

func TestParseBlocked(t *testing.T) {
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	log := `2026-01-02T09:12:01+0000 nocap kernel: [UFW BLOCK] IN=eth0 OUT= MAC=aa SRC=203.0.113.9 DST=192.168.1.20 LEN=60 PROTO=TCP SPT=51234 DPT=23 WINDOW=1 SYN URGP=0
2026-01-02T09:12:02+0000 nocap kernel: [UFW ALLOW] IN=eth0 OUT= SRC=192.168.1.5 DST=192.168.1.20 PROTO=TCP SPT=1 DPT=22
Dec 31 23:59:59 nocap kernel: [12.3] [UFW LIMIT BLOCK] IN=eth0 OUT= SRC=198.51.100.1 DST=192.168.1.20 PROTO=UDP SPT=5 DPT=53
something else entirely
`
	bs := ParseBlocked(log, now)
	if len(bs) != 2 {
		t.Fatalf("got %+v", bs)
	}
	if bs[0].Src != "203.0.113.9" || bs[0].DstPort != 23 || bs[0].Proto != "tcp" || bs[0].Interface != "eth0" || bs[0].Time.Hour() != 9 {
		t.Errorf("first %+v", bs[0])
	}
	if bs[1].Action != "limit block" || bs[1].Time.Year() != 2025 {
		t.Errorf("second %+v", bs[1])
	}
}

func entries(t *testing.T, rs ...Rule) []Entry {
	var out []Entry
	for _, r := range rs {
		n, err := Normalize(r)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, Entry{Rule: n, Editable: true})
	}
	return out
}

func TestReachable(t *testing.T) {
	me := netip.MustParseAddr("192.168.1.50")
	cases := []struct {
		name  string
		def   string
		rules []Entry
		want  bool
	}{
		{"default deny", "deny", nil, false},
		{"default allow", "allow", nil, true},
		{"allowed port", "deny", entries(t, Rule{Action: "allow", Port: "8443", Proto: "tcp"}), true},
		{"limit counts as allow", "deny", entries(t, Rule{Action: "limit", Port: "8443", Proto: "tcp"}), true},
		{"udp only", "deny", entries(t, Rule{Action: "allow", Port: "8443", Proto: "udp"}), false},
		{"range", "deny", entries(t, Rule{Action: "allow", Port: "8000:9000", Proto: "tcp"}), true},
		{"deny first", "deny", entries(t, Rule{Action: "deny", From: "192.168.1.0/24"}, Rule{Action: "allow", Port: "8443", Proto: "tcp"}), false},
		{"allow first", "deny", entries(t, Rule{Action: "allow", Port: "8443", Proto: "tcp"}, Rule{Action: "deny", From: "192.168.1.0/24"}), true},
		{"other source", "deny", entries(t, Rule{Action: "allow", From: "10.0.0.1"}), false},
		{"my source", "deny", entries(t, Rule{Action: "allow", From: "192.168.1.50"}), true},
		{"deny elsewhere", "allow", entries(t, Rule{Action: "deny", From: "10.0.0.0/8"}), true},
		{"unsure deny counts", "allow", entries(t, Rule{Action: "deny", Interface: "eth0", Port: "8443", Proto: "tcp"}), false},
		{"unsure allow doesn't", "deny", entries(t, Rule{Action: "allow", Interface: "eth0", Port: "8443", Proto: "tcp"}), false},
		{"app deny counts", "allow", entries(t, Rule{Action: "deny", App: "Web"}), false},
		{"outgoing ignored", "deny", entries(t, Rule{Action: "allow", Direction: "out", Port: "8443", Proto: "tcp"}), false},
		{"unreadable deny", "allow", []Entry{{Rule: Rule{Action: "deny", Direction: "in"}, Raw: "x", Number: 1}}, false},
	}
	for _, c := range cases {
		if got := Reachable(c.def, true, c.rules, me, 8443); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
	if !Reachable("deny", true, nil, netip.MustParseAddr("127.0.0.1"), 8443) {
		t.Error("loopback cut off")
	}
	// Numbered IPv4-only rules don't cover an IPv6 client…
	v6 := netip.MustParseAddr("fd00::5")
	r := entries(t, Rule{Action: "allow", Port: "8443", Proto: "tcp"})
	r[0].Number = 1
	if Reachable("deny", true, r, v6, 8443) {
		t.Error("IPv4 rule let IPv6 in")
	}
	// …but a rule as written (unnumbered) covers both, and with IPv6
	// filtering off nothing stops it.
	r[0].Number = 0
	if !Reachable("deny", true, r, v6, 8443) || !Reachable("deny", false, nil, v6, 8443) {
		t.Error("IPv6 client cut off")
	}
}

// fakeUFW plays ufw: it remembers whether it's running and the rules
// added, and writes ufw.conf the way ufw does.
type fakeUFW struct {
	mu     sync.Mutex
	etc    string
	active bool
	rules  [][]string
	calls  [][]string
}

func (f *fakeUFW) run(_ context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name != "ufw" {
		return "", errors.New("not here")
	}
	f.calls = append(f.calls, args)
	j := strings.Join(args, " ")
	switch {
	case j == "status verbose":
		if !f.active {
			return "Status: inactive", nil
		}
		return "Status: active\nLogging: on (low)\nDefault: deny (incoming), allow (outgoing), disabled (routed)", nil
	case j == "show added":
		var b strings.Builder
		b.WriteString("Added user rules (see 'ufw status' for running firewall):\n")
		if len(f.rules) == 0 {
			b.WriteString("(None)\n")
		}
		for _, r := range f.rules {
			b.WriteString("ufw " + quoteComment(r) + "\n")
		}
		return b.String(), nil
	case j == "app list":
		return "Available applications:\n  OpenSSH", nil
	case j == "--force enable":
		f.active = true
		return "Firewall is active", os.WriteFile(filepath.Join(f.etc, "ufw", "ufw.conf"), []byte("ENABLED=yes\nLOGLEVEL=low\n"), 0o644)
	case j == "--force disable" || j == "disable":
		f.active = false
		return "Firewall stopped", os.WriteFile(filepath.Join(f.etc, "ufw", "ufw.conf"), []byte("ENABLED=no\nLOGLEVEL=low\n"), 0o644)
	case j == "reload":
		return "Firewall reloaded", nil
	case args[0] == "allow" || args[0] == "deny" || args[0] == "limit" || args[0] == "reject":
		f.rules = append(f.rules, args)
		return "Rule added", nil
	case args[0] == "insert":
		f.rules = append([][]string{args[2:]}, f.rules...)
		return "Rule inserted", nil
	}
	return "", nil
}

func quoteComment(r []string) string {
	out := append([]string(nil), r...)
	for i := range out {
		if i > 0 && out[i-1] == "comment" {
			out[i] = "'" + out[i] + "'"
		}
	}
	return strings.Join(out, " ")
}

func newFake(t *testing.T) (*Engine, *fakeUFW) {
	t.Helper()
	etc := t.TempDir()
	for _, d := range []string{"ufw", "default"} {
		if err := os.MkdirAll(filepath.Join(etc, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must := func(p, s string) {
		if err := os.WriteFile(filepath.Join(etc, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("ufw/ufw.conf", "ENABLED=no\nLOGLEVEL=low\n")
	must("default/ufw", "IPV6=yes\nDEFAULT_INPUT_POLICY=\"DROP\"\nDEFAULT_OUTPUT_POLICY=\"ACCEPT\"\n")
	must("ufw/user.rules", "*filter\nCOMMIT\n")
	f := &fakeUFW{etc: etc}
	return NewWith(f.run, etc), f
}

func TestEnableKeepsClientIn(t *testing.T) {
	e, f := newFake(t)
	k := KeepFor("192.168.1.50", 8443, 22)
	st, err := e.Enable(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	if !f.active || st.Pending == nil {
		t.Fatalf("active %v pending %v", f.active, st.Pending)
	}
	if len(f.rules) != 2 {
		t.Fatalf("guard rules: %q", f.rules)
	}
	for _, r := range f.rules {
		g := strings.Join(r, " ")
		if !strings.Contains(g, "allow in proto tcp from 192.168.1.50 to any port") || !strings.HasSuffix(g, "comment "+GuardComment) {
			t.Errorf("guard %q", g)
		}
	}
	// Whatever the rules, the enable comes after the guards.
	last := f.calls[len(f.calls)-1]
	for i, c := range f.calls {
		if strings.Join(c, " ") == "--force enable" {
			last = f.calls[i-1]
		}
	}
	if last[0] != "allow" && last[0] != "insert" {
		t.Errorf("enable ran before the guards: %q", f.calls)
	}

	// Undo returns to off and puts the files back.
	if err := e.Undo(context.Background(), st.Pending.ID); err != nil {
		t.Fatal(err)
	}
	if f.active {
		t.Error("still active after undo")
	}
	b, _ := os.ReadFile(filepath.Join(f.etc, "ufw", "ufw.conf"))
	if !strings.Contains(string(b), "ENABLED=no") {
		t.Errorf("ufw.conf = %q", b)
	}
	if err := e.KeepChanges(st.Pending.ID); !errors.Is(err, ErrNoPending) {
		t.Errorf("keep after undo: %v", err)
	}
}

func TestEnableNoGuardWhenAllowed(t *testing.T) {
	e, f := newFake(t)
	f.rules = [][]string{{"allow", "in", "proto", "tcp", "from", "any", "to", "any", "port", "8443"}}
	if _, err := e.Enable(context.Background(), KeepFor("192.168.1.50", 8443)); err != nil {
		t.Fatal(err)
	}
	if len(f.rules) != 1 {
		t.Fatalf("added a guard anyway: %q", f.rules)
	}
}

func TestUnkeptChangeReverts(t *testing.T) {
	e, f := newFake(t)
	reverted := make(chan string, 1)
	e.OnRevert = func(what string, err error) { reverted <- what }
	st, err := e.Enable(context.Background(), KeepFor("127.0.0.1", 8443))
	if err != nil {
		t.Fatal(err)
	}
	e.expire(st.Pending.ID) // what the timer does when nobody keeps it
	select {
	case w := <-reverted:
		if w != "enable" {
			t.Errorf("reverted %q", w)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no revert")
	}
	if f.active {
		t.Error("still active")
	}
}

func TestKeepChanges(t *testing.T) {
	e, f := newFake(t)
	st, err := e.Enable(context.Background(), Keep{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.KeepChanges("wrong"); !errors.Is(err, ErrNoPending) {
		t.Fatal("kept with the wrong id")
	}
	if err := e.KeepChanges(st.Pending.ID); err != nil {
		t.Fatal(err)
	}
	e.expire(st.Pending.ID)
	if !f.active {
		t.Error("kept change was undone")
	}
}

func TestDefaultsFromFiles(t *testing.T) {
	e, _ := newFake(t)
	st, err := e.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Active || st.Incoming != "deny" || st.Outgoing != "allow" || st.Logging != "low" || !st.IPv6 || len(st.Apps) != 1 {
		t.Fatalf("state %+v", st)
	}
	if _, err := e.SetIPv6(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.Status(context.Background()); st.IPv6 {
		t.Error("IPv6 still on")
	}
}

func TestPresetRules(t *testing.T) {
	rs, err := PresetRules("smb", "192.168.1.0/24")
	if err != nil || len(rs) != 3 || rs[2].Port != "137:138" || rs[2].Proto != "udp" || rs[0].From != "192.168.1.0/24" {
		t.Fatalf("%+v %v", rs, err)
	}
	if _, err := PresetRules("nope", ""); err == nil {
		t.Fatal("unknown preset")
	}
	for _, p := range Presets {
		if _, err := PresetRules(p.ID, ""); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
	}
}

func TestReachableFromAnyone(t *testing.T) {
	st := State{Report: Report{Incoming: "deny"}, Rules: entries(t, Rule{Action: "allow", Port: "22", Proto: "tcp", From: "10.0.0.0/8"}, Rule{Action: "allow", Port: "80", Proto: "tcp"})}
	if ReachableFromAnyone(st, 22, "tcp") || !ReachableFromAnyone(st, 80, "tcp") || ReachableFromAnyone(st, 80, "udp") {
		t.Fatal("wrong")
	}
}
