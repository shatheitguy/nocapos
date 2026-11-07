package netdrive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"alfaos/alfad/internal/spaces"
)

// scripted is a fake host whose answers are set per command line prefix.
type scripted struct {
	mu      sync.Mutex
	answers map[string]string // "cmd args" prefix → output; a "!" prefix makes it fail
	calls   []string
	stdin   []string
}

func (s *scripted) set(prefix, out string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers[prefix] = out
}

func (s *scripted) run(_ context.Context, stdin, name string, args ...string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	s.calls = append(s.calls, line)
	s.stdin = append(s.stdin, stdin)
	best := ""
	for p := range s.answers {
		if strings.HasPrefix(line, p) && len(p) > len(best) {
			best = p
		}
	}
	if best == "" {
		if name == "id" {
			return "", errors.New("no such user")
		}
		return "", nil
	}
	out := s.answers[best]
	if strings.HasPrefix(out, "!") {
		return out[1:], errors.New("exit status 1")
	}
	return out, nil
}

func (s *scripted) called(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func item(t *testing.T, h SMBHealth, id string) HealthItem {
	t.Helper()
	for _, it := range h.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no %s check in %+v", id, h.Items)
	return HealthItem{}
}

// healthySetup: SMB on with one shared folder alex can use, and a host
// where everything checks out.
func healthySetup(t *testing.T) (*Manager, *scripted) {
	t.Helper()
	ctx := context.Background()
	m, _, _ := setup(t)
	s := &scripted{answers: map[string]string{
		"systemctl list-unit-files smbd.service": "smbd.service enabled enabled",
		"systemctl list-unit-files nmbd.service": "nmbd.service enabled enabled",
		"systemctl list-unit-files wsdd.service": "wsdd.service enabled enabled",
		"systemctl list-unit-files":              "",
		"systemctl is-active smbd":               "active",
		"systemctl is-active nmbd":               "active",
		"systemctl is-active wsdd":               "active",
		"systemctl is-active samba-ad-dc":        "!inactive",
		"pdbedit -L -w":                          "admin:1000:XX:AB:[U          ]:LCT-1:\nalex:1001:XX:CD:[U          ]:LCT-1:\nsam:1002:XX:EF:[U          ]:LCT-1:\n",
		"ufw status":                             "Status: inactive",
		"firewall-cmd --state":                   "!not running",
	}}
	m.run = s.run
	_ = os.MkdirAll(m.avahiDir, 0o755)
	on := true
	if err := m.SetProtocols(ctx, &on, nil); err != nil {
		t.Fatal(err)
	}
	f, err := m.AddShare(ctx, "Media", "drive", "/Media", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.spaces.SetPermission(ctx, f.ID, "alex", spaces.ReadWrite); err != nil {
		t.Fatal(err)
	}
	return m, s
}

func TestHealthAllGood(t *testing.T) {
	m, _ := healthySetup(t)
	h := m.Health(context.Background())
	if !h.SMB || !h.Live {
		t.Fatalf("not live: %+v", h.Items)
	}
	for _, it := range h.Items {
		if it.State != StateOK {
			t.Errorf("%s: %s (%s)", it.ID, it.State, it.Detail)
		}
	}
	if h.Host != "nocap" || len(h.Addresses) != 1 {
		t.Errorf("host %q %v", h.Host, h.Addresses)
	}
	// admin and alex are ready; sam has a home (Linux homes off, homes
	// folder on the drive) and hasn't turned SMB on.
	ready := map[string]bool{}
	for _, a := range h.Accounts {
		ready[a.Name] = a.Ready
	}
	if !ready["admin"] || !ready["alex"] {
		t.Errorf("accounts: %+v", h.Accounts)
	}
	if _, ok := ready["gone"]; ok {
		t.Errorf("disabled person listed: %+v", h.Accounts)
	}
	if _, err := os.Stat(filepath.Join(m.avahiDir, avahiSMB)); err != nil {
		t.Error("SMB not announced over Avahi")
	}
}

func TestHealthProblems(t *testing.T) {
	ctx := context.Background()
	m, s := healthySetup(t)
	s.set("systemctl is-active smbd", "!inactive")
	s.set("testparm -s", "!Unknown parameter encountered: \"bogus\"\nIgnoring unknown parameter \"bogus\"")
	s.set("pdbedit -L -w", "admin:1000:XX:AB:[UD         ]:LCT-1:\n")
	s.set("ufw status", "Status: active\n\nTo Action From\n22/tcp ALLOW Anywhere\n")
	s.set("systemctl is-active wsdd", "!inactive")
	h := m.Health(ctx)
	if h.Live {
		t.Error("live with problems")
	}
	for id, want := range map[string]string{CheckService: StateBad, CheckConfig: StateBad, CheckAccounts: StateBad, CheckFirewall: StateBad, CheckDiscovery: StateWarn} {
		if it := item(t, h, id); it.State != want {
			t.Errorf("%s: %s, want %s (%s)", id, it.State, want, it.Detail)
		}
	}
	if it := item(t, h, CheckConfig); !strings.Contains(it.Detail, "Unknown parameter") || it.Fix == "" {
		t.Errorf("config: %+v", it)
	}
	if it := item(t, h, CheckFirewall); it.Fix == "" {
		t.Errorf("firewall has no fix: %+v", it)
	}

	// Fixes run the right commands.
	if err := m.Fix(ctx, CheckFirewall); err != nil {
		t.Fatal(err)
	}
	if !s.called("ufw allow proto tcp to any port 139,445") {
		t.Errorf("ports not opened: %v", s.calls)
	}
	if err := m.Fix(ctx, CheckService); err != nil {
		t.Fatal(err)
	}
	if !s.called("systemctl enable --now smbd") || !s.called("systemctl enable --now nmbd") {
		t.Errorf("Samba not started: %v", s.calls)
	}
	if err := m.Fix(ctx, CheckDiscovery); err != nil {
		t.Fatal(err)
	}
	if !s.called("systemctl enable --now wsdd") {
		t.Errorf("wsdd not started: %v", s.calls)
	}
	if err := m.Fix(ctx, "nope"); err == nil {
		t.Error("unknown fix accepted")
	}
	// A config Samba rejects is rolled back, not left in place.
	before := readConf(t, m)
	if err := m.Fix(ctx, CheckConfig); err == nil {
		t.Error("rejected config reported fine")
	}
	if readConf(t, m) != before {
		t.Error("rejected config left in place")
	}

	// Samba missing: one item, with nothing more checked.
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	h = m.Health(ctx)
	if len(h.Items) != 1 || h.Items[0].ID != CheckInstalled || h.Items[0].State != StateBad {
		t.Errorf("missing samba: %+v", h.Items)
	}
	m.native = func() bool { return false }
	if h = m.Health(ctx); h.Items[0].Fix != "" {
		t.Errorf("fix offered off Linux: %+v", h.Items)
	}
}

func TestHealthFirewalldAndOwnWsdd(t *testing.T) {
	ctx := context.Background()
	m, s := healthySetup(t)
	s.set("systemctl list-unit-files wsdd.service", "")
	s.set("ufw status", "!not found")
	s.set("firewall-cmd --state", "running")
	s.set("firewall-cmd --query-service=samba", "!no")
	h := m.Health(ctx)
	if it := item(t, h, CheckFirewall); it.State != StateBad || !strings.Contains(it.Detail, "firewalld") {
		t.Errorf("firewalld: %+v", it)
	}
	if err := m.openFirewall(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.called("firewall-cmd --permanent --add-service=samba") || !s.called("firewall-cmd --reload") {
		t.Errorf("firewalld not opened: %v", s.calls)
	}
	// Only the wsdd program: NoCapOS runs it with its own unit.
	if err := m.setDiscovery(ctx, true); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(filepath.Join(m.unitDir, ownWsddUnit+".service"))
	if err != nil || !strings.Contains(string(unit), "DynamicUser=yes") {
		t.Fatalf("own wsdd unit: %s %v", unit, err)
	}
	if !s.called("systemctl enable --now " + ownWsddUnit) {
		t.Errorf("own wsdd not started: %v", s.calls)
	}
	if err := m.setDiscovery(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !s.called("systemctl disable --now "+ownWsddUnit) || fileExists(filepath.Join(m.avahiDir, avahiSMB)) {
		t.Error("discovery not stopped")
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestEnableMySMB(t *testing.T) {
	ctx := context.Background()
	m, s := healthySetup(t)
	s.set("pdbedit -L -w", "")
	if st := m.MySMBStatus(ctx, "sam"); !st.SMB || st.Ready {
		t.Errorf("status before: %+v", st)
	}
	if err := m.EnableMySMB(ctx, "sam", "wrong-pass"); !errors.Is(err, ErrBadPassword) {
		t.Errorf("wrong password: %v", err)
	}
	if err := m.EnableMySMB(ctx, "root", "whatever"); err == nil {
		t.Error("root turned SMB on")
	}
	if err := m.EnableMySMB(ctx, "admin", "admin-pass-1"); err != nil {
		t.Fatal(err)
	}
	var viaStdin bool
	for i, c := range s.calls {
		if strings.Contains(c, "admin-pass-1") {
			t.Fatalf("password in arguments: %q", c)
		}
		if c == "smbpasswd -a -s admin" && s.stdin[i] == "admin-pass-1\nadmin-pass-1\n" {
			viaStdin = true
		}
	}
	if !viaStdin {
		t.Errorf("password not set through stdin: %v", s.calls)
	}
	s.set("pdbedit -L -w", "admin:1000:XX:AB:[U          ]:LCT-1:\n")
	if st := m.MySMBStatus(ctx, "admin"); !st.Ready {
		t.Errorf("status after: %+v", st)
	}
	// Five wrong tries, then a pause even for the right password.
	for i := 0; i < 5; i++ {
		_ = m.EnableMySMB(ctx, "alex", "nope")
	}
	if err := m.EnableMySMB(ctx, "alex", "alex-pass-1"); !errors.Is(err, ErrTooManyTries) {
		t.Errorf("no pause after wrong tries: %v", err)
	}
	off := false
	if err := m.SetProtocols(ctx, &off, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.EnableMySMB(ctx, "sam", "sam-pass-1"); err == nil {
		t.Error("turned on with SMB off")
	}
}

func TestSmbpasswdSet(t *testing.T) {
	stdin, args, err := smbpasswdSet("alex", "p@ss word")
	if err != nil || stdin != "p@ss word\np@ss word\n" || strings.Join(args, " ") != "-a -s alex" {
		t.Errorf("got %q %v %v", stdin, args, err)
	}
	for _, bad := range [][2]string{{"alex", "two\nlines"}, {"alex", ""}, {"-x", "pass-1234"}, {"a b", "pass-1234"}, {"", "pass-1234"}} {
		if _, _, err := smbpasswdSet(bad[0], bad[1]); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestParsers(t *testing.T) {
	got := parsePdbedit("Alex:1001:XX:CD:[U          ]:LCT-1:\nbob:1002:XX:CD:[DU         ]:LCT-1:\njunk\n")
	if !got["alex"] || got["bob"] || len(got) != 2 {
		t.Errorf("pdbedit: %v", got)
	}
	if !ufwAllowsSMB("Status: active\n445/tcp ALLOW Anywhere\n") || !ufwAllowsSMB("Samba ALLOW Anywhere") || ufwAllowsSMB("22/tcp ALLOW Anywhere\n4450/tcp ALLOW x\n445/tcp DENY x") || !ufwAllowsSMB("139,445/tcp ALLOW Anywhere") {
		t.Error("ufw parsing")
	}
	if !hasInclude("[global]\n  "+includeLine+"  \n") || hasInclude("# "+includeLine) {
		t.Error("include detection")
	}
	if e := testparmError("Load smb config files from x\nERROR: bad thing\n"); e != "ERROR: bad thing" {
		t.Errorf("testparm error %q", e)
	}
}
