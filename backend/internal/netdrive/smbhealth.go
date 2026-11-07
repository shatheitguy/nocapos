package netdrive

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/syspkg"
)

// SMB health: everything that has to be right before another computer can
// open a shared folder over SMB, checked one by one, with a fix where
// NoCapOS can make one itself. Also the parts of "being reachable" that
// aren't smb.conf: the firewall, and announcing the server so it shows up in
// network browsers (WS-Discovery for Windows, Avahi's _smb._tcp for macOS
// and Linux).

// Health check ids, also the fixes' names.
const (
	CheckInstalled = "installed"
	CheckService   = "service"
	CheckConfig    = "config"
	CheckAccounts  = "accounts"
	CheckFirewall  = "firewall"
	CheckDiscovery = "discovery"
)

// Health states.
const (
	StateOK   = "ok"
	StateWarn = "warn" // works, with something missing
	StateBad  = "bad"  // SMB won't work until this is sorted
)

// HealthItem is one check.
type HealthItem struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"` // the fix button's label ("" = nothing to press)
}

// SMBAccount is whether one person can sign in over SMB yet.
type SMBAccount struct {
	Name  string `json:"name"`
	Admin bool   `json:"admin"`
	Ready bool   `json:"ready"`
}

// SMBHealth is the whole picture.
type SMBHealth struct {
	SMB       bool         `json:"smb"`  // SMB sharing is switched on
	Live      bool         `json:"live"` // on, and nothing is in the way
	Host      string       `json:"host"`
	Addresses []string     `json:"addresses"`
	Items     []HealthItem `json:"items"`
	Accounts  []SMBAccount `json:"accounts"`
}

// Health checks SMB sharing end to end.
func (m *Manager) Health(ctx context.Context) SMBHealth {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out := SMBHealth{SMB: m.flag(ctx, keySMB), Addresses: []string{}, Items: []HealthItem{}, Accounts: []SMBAccount{}}
	if h, err := m.hostname(); err == nil {
		out.Host = strings.TrimSpace(h)
	}
	out.Addresses = append(out.Addresses, m.addrs()...)
	add := func(it HealthItem) { out.Items = append(out.Items, it) }

	switch {
	case !m.native():
		add(HealthItem{ID: CheckInstalled, Label: "Samba", State: StateBad, Detail: "SMB needs NoCapOS installed straight on Linux. This copy runs in Docker, on Windows or in development, so use WebDAV instead."})
		return out
	case !m.has("samba"):
		it := HealthItem{ID: CheckInstalled, Label: "Samba", State: StateBad, Detail: "Samba isn’t installed, so there’s nothing to answer SMB."}
		if m.canInstall() {
			it.Fix = "Install Samba"
		} else {
			it.Detail += " Install the samba package, then come back."
		}
		add(it)
		return out
	}
	add(HealthItem{ID: CheckInstalled, Label: "Samba", State: StateOK, Detail: "Installed."})
	if !out.SMB {
		return out
	}

	// Running.
	unit := m.sambaUnit(ctx)
	svc := HealthItem{ID: CheckService, Label: "Samba service", State: StateOK, Detail: unit + " is running."}
	switch {
	case m.unitActive(ctx, "samba-ad-dc"):
		svc.State, svc.Detail = StateBad, "samba-ad-dc (the domain controller) is running and holds the SMB port. Stop and mask it, then start "+unit+"."
	case !m.unitActive(ctx, unit):
		svc.State, svc.Detail, svc.Fix = StateBad, unit+" isn’t running.", "Start Samba"
	default:
		if nmb := m.nmbUnit(ctx); nmb != "" && !m.unitActive(ctx, nmb) {
			svc.State, svc.Detail, svc.Fix = StateWarn, unit+" is running, but "+nmb+" (NetBIOS names for older Windows) isn’t.", "Start it"
		}
	}
	add(svc)

	// Config.
	conf := HealthItem{ID: CheckConfig, Label: "Configuration", State: StateOK, Detail: "smb.conf includes NoCapOS’s shares and Samba accepts it."}
	main, err := os.ReadFile(filepath.Join(m.sambaDir, "smb.conf"))
	_, ownErr := os.Stat(filepath.Join(m.sambaDir, confName))
	switch {
	case err != nil || ownErr != nil || !hasInclude(string(main)):
		conf.State, conf.Detail, conf.Fix = StateBad, "smb.conf doesn’t load NoCapOS’s shares, so Samba has nothing to offer.", "Rewrite it"
	default:
		if terr := m.testparm(ctx); terr != nil {
			conf.State, conf.Detail, conf.Fix = StateBad, "Samba rejects its settings: "+terr.Error(), "Rewrite it"
		}
	}
	add(conf)

	// Accounts.
	out.Accounts = m.smbAccounts(ctx)
	add(m.accountsItem(ctx, out.Accounts))

	// Firewall.
	fw := HealthItem{ID: CheckFirewall, Label: "Firewall", State: StateOK, Detail: "No firewall in the way."}
	if kind, active, open := m.firewall(ctx); active {
		if open {
			fw.Detail = kind + " lets SMB in (ports 445 and 139)."
		} else {
			fw.State, fw.Detail, fw.Fix = StateBad, kind+" is blocking SMB (ports 445 and 139).", "Open the ports"
		}
	}
	add(fw)

	// Discovery.
	add(m.discoveryItem(ctx))

	out.Live = true
	for _, it := range out.Items {
		if it.State == StateBad {
			out.Live = false
		}
	}
	return out
}

func (m *Manager) accountsItem(ctx context.Context, accounts []SMBAccount) HealthItem {
	it := HealthItem{ID: CheckAccounts, Label: "Sign-ins", State: StateOK}
	guests := false
	if m.spaces != nil {
		for _, f := range m.spaces.NetFolders(nil) {
			if guestMode(f.SharedFolder) != "" {
				guests = true
			}
		}
	}
	var ready, waiting []string
	for _, a := range accounts {
		if a.Ready {
			ready = append(ready, a.Name)
		} else {
			waiting = append(waiting, a.Name)
		}
	}
	if m.legacy(ctx) {
		ready = append(ready, ShareUser)
	}
	switch {
	case len(ready) == 0 && len(waiting) == 0 && guests:
		it.Detail = "Guest folders open without a password. Nobody else has access yet."
	case len(ready) == 0 && len(waiting) == 0:
		it.State, it.Detail = StateWarn, "Nobody has access to a shared folder yet."
	case len(ready) == 0 && !guests:
		it.State, it.Detail = StateBad, "Nobody can sign in over SMB yet: "+strings.Join(waiting, ", ")+" still need to turn SMB on for their account (one password, once)."
	case len(waiting) > 0:
		it.State, it.Detail = StateWarn, strings.Join(waiting, ", ")+" can’t sign in over SMB yet. They turn it on under File Sharing (or by signing in to NoCapOS again)."
	default:
		it.Detail = "Everyone with access can sign in with their NoCapOS password."
	}
	return it
}

// smbAccounts lists the people who can reach something over SMB and whether
// Samba knows their password yet.
func (m *Manager) smbAccounts(ctx context.Context) []SMBAccount {
	out := []SMBAccount{}
	if m.spaces == nil {
		return out
	}
	people, err := m.spaces.People(ctx)
	if err != nil {
		return out
	}
	known := m.sambaAccounts(ctx)
	homes := m.spaces.LinuxHomes() || m.spaces.HomesPath() != ""
	for _, p := range people {
		if p.Disabled || strings.EqualFold(p.Name, "root") || strings.EqualFold(p.Name, ShareUser) {
			continue
		}
		if !p.Admin && !homes && len(m.spaces.PermissionsOf(p.Name)) == 0 {
			continue
		}
		out = append(out, SMBAccount{Name: p.Name, Admin: p.Admin, Ready: known[strings.ToLower(p.Name)]})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// sambaAccounts reads Samba's password database: lower-case user name →
// enabled. `pdbedit -L -w` prints smbpasswd lines, name:uid:lm:nt:[flags]:…,
// where a D in the flags means disabled.
func (m *Manager) sambaAccounts(ctx context.Context) map[string]bool {
	out, err := m.run(ctx, "", "pdbedit", "-L", "-w")
	if err != nil {
		return map[string]bool{}
	}
	return parsePdbedit(out)
}

func parsePdbedit(out string) map[string]bool {
	known := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 5 || f[0] == "" {
			continue
		}
		known[strings.ToLower(f[0])] = !strings.Contains(f[4], "D")
	}
	return known
}

// ---------- the person's own SMB sign-in ----------

// MySMB is one person's own SMB status.
type MySMB struct {
	SMB   bool `json:"smb"`   // SMB sharing is on (and Samba runs here)
	Ready bool `json:"ready"` // Samba knows their password
	Root  bool `json:"root"`  // the superuser never signs in over SMB
}

// ErrBadPassword: the password given to turn on SMB isn't the person's.
var ErrBadPassword = errors.New("that password isn’t right")

// ErrTooManyTries: too many wrong passwords in a row.
var ErrTooManyTries = errors.New("too many wrong passwords. Wait 15 minutes and try again")

// MySMBStatus reports whether a person can sign in over SMB.
func (m *Manager) MySMBStatus(ctx context.Context, username string) MySMB {
	out := MySMB{SMB: m.sambaReady() && m.flag(ctx, keySMB)}
	if !out.SMB {
		return out
	}
	if m.isRoot(ctx, username) {
		out.Root = true
		return out
	}
	out.Ready = m.sambaAccounts(ctx)[strings.ToLower(username)]
	return out
}

// tries counts wrong passwords per person for EnableMySMB.
var tries = struct {
	sync.Mutex
	fails map[string][]time.Time
}{fails: map[string][]time.Time{}}

func tooManyTries(user string, now time.Time) bool {
	tries.Lock()
	defer tries.Unlock()
	keep := tries.fails[user][:0]
	for _, t := range tries.fails[user] {
		if now.Sub(t) < 15*time.Minute {
			keep = append(keep, t)
		}
	}
	tries.fails[user] = keep
	return len(keep) >= 5
}

func failedTry(user string, now time.Time) {
	tries.Lock()
	defer tries.Unlock()
	tries.fails[user] = append(tries.fails[user], now)
}

// EnableMySMB turns SMB on for a person's own account: their password is
// checked (it's the one they sign in with, which with Linux accounts NoCapOS
// never stores) and handed to Samba. Works the same for administrators,
// who otherwise only get SMB by opting in.
func (m *Manager) EnableMySMB(ctx context.Context, username, pw string) error {
	if !m.sambaReady() || !m.flag(ctx, keySMB) {
		return errors.New("SMB sharing is off on this server")
	}
	if m.isRoot(ctx, username) {
		return errors.New("root never signs in over SMB. Use your own account")
	}
	if m.checkUser == nil {
		return errors.New("sharing isn’t ready")
	}
	key := strings.ToLower(username)
	if tooManyTries(key, time.Now()) {
		return ErrTooManyTries
	}
	name, _, ok := m.checkUser(ctx, username, pw)
	if !ok || !strings.EqualFold(name, username) {
		failedTry(key, time.Now())
		return ErrBadPassword
	}
	return m.sambaUser(ctx, name, pw, false)
}

// ---------- fixes ----------

// Fix runs the fix for one health check.
func (m *Manager) Fix(ctx context.Context, id string) error {
	switch id {
	case CheckInstalled:
		return m.Install(ctx, "samba")
	case CheckService:
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return m.startSamba(cctx, m.sambaUnit(cctx))
	case CheckConfig:
		return m.ApplySamba(ctx)
	case CheckFirewall:
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return m.openFirewall(cctx)
	case CheckDiscovery:
		if m.wsddUnit(ctx) == "" && !m.hasBin("wsdd") && !m.hasBin("wsdd2") {
			if err := m.installDiscovery(ctx); err != nil {
				return err
			}
		}
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return m.setDiscovery(cctx, true)
	}
	return fmt.Errorf("nothing to fix for %q", id)
}

// ---------- services ----------

func (m *Manager) unitExists(ctx context.Context, unit string) bool {
	out, err := m.run(ctx, "", "systemctl", "list-unit-files", unit+".service", "--no-legend")
	return err == nil && strings.Contains(out, unit+".service")
}

func (m *Manager) unitActive(ctx context.Context, unit string) bool {
	out, err := m.run(ctx, "", "systemctl", "is-active", unit)
	return err == nil && strings.TrimSpace(out) == "active"
}

// nmbUnit is NetBIOS's service: "nmbd" on Debian/Ubuntu, "nmb" elsewhere
// ("" when it isn't installed).
func (m *Manager) nmbUnit(ctx context.Context) string {
	for _, u := range []string{"nmbd", "nmb"} {
		if m.unitExists(ctx, u) {
			return u
		}
	}
	return ""
}

// ---------- firewall ----------

// firewall finds an active firewall (ufw or firewalld) and whether it lets
// SMB in. Without either, nothing is reported in the way.
func (m *Manager) firewall(ctx context.Context) (kind string, active, open bool) {
	if m.hasBin("ufw") {
		out, err := m.run(ctx, "", "ufw", "status")
		if err == nil && strings.Contains(out, "Status: active") {
			return "ufw", true, ufwAllowsSMB(out)
		}
	}
	if m.hasBin("firewall-cmd") {
		out, err := m.run(ctx, "", "firewall-cmd", "--state")
		if err == nil && strings.TrimSpace(out) == "running" {
			q, qerr := m.run(ctx, "", "firewall-cmd", "--query-service=samba")
			return "firewalld", true, qerr == nil && strings.TrimSpace(q) == "yes"
		}
	}
	return "", false, false
}

// ufwAllowsSMB reads `ufw status`: an ALLOW rule for port 445 or Samba.
func ufwAllowsSMB(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		if !strings.Contains(line, "ALLOW") {
			continue
		}
		to := strings.Fields(line)
		if len(to) == 0 {
			continue
		}
		if strings.EqualFold(to[0], "Samba") {
			return true
		}
		ports, _, _ := strings.Cut(to[0], "/")
		for _, p := range strings.Split(ports, ",") {
			if p == "445" {
				return true
			}
		}
	}
	return false
}

// openFirewall lets SMB, NetBIOS, WS-Discovery and mDNS in, on ufw or
// firewalld when one is active.
func (m *Manager) openFirewall(ctx context.Context) error {
	kind, active, open := m.firewall(ctx)
	if !active || open {
		return nil
	}
	var steps [][]string
	switch kind {
	case "ufw":
		const note = "NoCapOS file sharing"
		steps = [][]string{
			{"ufw", "allow", "proto", "tcp", "to", "any", "port", "139,445", "comment", note},
			{"ufw", "allow", "proto", "udp", "to", "any", "port", "137,138", "comment", note},
			{"ufw", "allow", "proto", "tcp", "to", "any", "port", "5357", "comment", note},
			{"ufw", "allow", "proto", "udp", "to", "any", "port", "3702,5353", "comment", note},
		}
	case "firewalld":
		steps = [][]string{
			{"firewall-cmd", "--permanent", "--add-service=samba"},
			{"firewall-cmd", "--permanent", "--add-service=mdns"},
			{"firewall-cmd", "--permanent", "--add-port=3702/udp", "--add-port=5357/tcp"},
			{"firewall-cmd", "--reload"},
		}
	}
	for i, s := range steps {
		if out, err := m.run(ctx, "", s[0], s[1:]...); err != nil && i == 0 {
			return fmt.Errorf("open the firewall: %s", lastLine(out))
		}
	}
	return nil
}

// ---------- discovery ----------

// WS-Discovery services by package: wsdd2, Debian's newer wsdd-server, and
// wsdd. Where only the wsdd program is installed, NoCapOS runs it with its
// own unit.
var wsddUnits = []string{"wsdd2", "wsdd-server", "wsdd"}

const (
	ownWsddUnit = "nocapos-wsdd"
	avahiSMB    = "nocapos-smb.service"
)

func (m *Manager) wsddUnit(ctx context.Context) string {
	for _, u := range wsddUnits {
		if m.unitExists(ctx, u) {
			return u
		}
	}
	if _, err := os.Stat(filepath.Join(m.unitDir, ownWsddUnit+".service")); err == nil {
		return ownWsddUnit
	}
	return ""
}

// wsddUnitFile runs wsdd as an unprivileged, throwaway user.
func wsddUnitFile(bin string) string {
	return "# Managed by NoCapOS: shows this server in Windows' Network view while SMB sharing is on.\n" +
		"[Unit]\nDescription=NoCapOS network discovery (WS-Discovery)\nAfter=network-online.target smbd.service smb.service\nWants=network-online.target\n\n" +
		"[Service]\nExecStart=" + bin + " --shortlog\nDynamicUser=yes\nRestart=on-failure\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
}

// avahiSMBService announces SMB for macOS Finder and Linux file managers.
const avahiSMBService = `<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<!-- NoCapOS file sharing over SMB (managed by NoCapOS) -->
<service-group>
  <name replace-wildcards="yes">%h</name>
  <service>
    <type>_smb._tcp</type>
    <port>445</port>
  </service>
</service-group>
`

// setDiscovery announces the server (or stops) for network browsers.
func (m *Manager) setDiscovery(ctx context.Context, on bool) error {
	if fi, err := os.Stat(m.avahiDir); err == nil && fi.IsDir() {
		p := filepath.Join(m.avahiDir, avahiSMB)
		if on {
			if cur, _ := os.ReadFile(p); string(cur) != avahiSMBService {
				_ = writeFileAtomic(p, []byte(avahiSMBService), 0o644)
			}
		} else {
			_ = os.Remove(p)
		}
	}
	unit := m.wsddUnit(ctx)
	if !on {
		if unit == ownWsddUnit {
			_, _ = m.run(ctx, "", "systemctl", "disable", "--now", ownWsddUnit)
		}
		return nil
	}
	if unit == "" {
		bin, err := m.lookPath("wsdd")
		if err != nil {
			return errors.New("WS-Discovery (wsdd) isn’t installed, so Windows won’t list this server by itself")
		}
		if err := os.MkdirAll(m.unitDir, 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(m.unitDir, ownWsddUnit+".service"), []byte(wsddUnitFile(bin)), 0o644); err != nil {
			return err
		}
		_, _ = m.run(ctx, "", "systemctl", "daemon-reload")
		unit = ownWsddUnit
	}
	if out, err := m.run(ctx, "", "systemctl", "enable", "--now", unit); err != nil {
		return fmt.Errorf("start %s: %s", unit, lastLine(out))
	}
	return nil
}

// installDiscovery installs a WS-Discovery daemon: wsdd, else wsdd2.
func (m *Manager) installDiscovery(ctx context.Context) error {
	if !m.canInstall() {
		return errors.New("install the wsdd package on this machine, then try again")
	}
	var last error
	for _, p := range []string{"wsdd", "wsdd2"} {
		names := syspkg.Names{"apt": {p}, "dnf": {p}, "pacman": {p}, "zypper": {p}, "apk": {p}}
		if last = m.installPkgs(ctx, names); last == nil {
			return nil
		}
	}
	return fmt.Errorf("couldn’t install WS-Discovery: %w", last)
}

func (m *Manager) discoveryItem(ctx context.Context) HealthItem {
	it := HealthItem{ID: CheckDiscovery, Label: "Shows up on the network", State: StateOK}
	var parts []string
	unit := m.wsddUnit(ctx)
	switch {
	case unit != "" && m.unitActive(ctx, unit):
		parts = append(parts, "Windows lists it under Network")
	case unit != "" || m.hasBin("wsdd"):
		it.State, it.Fix = StateWarn, "Turn it on"
		parts = append(parts, "WS-Discovery is installed but off, so Windows won’t list it under Network (typing the address still works)")
	default:
		it.State = StateWarn
		if m.canInstall() {
			it.Fix = "Install it"
		}
		parts = append(parts, "Windows won’t list it under Network without WS-Discovery (typing the address still works)")
	}
	if fi, err := os.Stat(m.avahiDir); err == nil && fi.IsDir() {
		if _, err := os.Stat(filepath.Join(m.avahiDir, avahiSMB)); err == nil {
			parts = append(parts, "Macs and Linux see it too")
		} else {
			if it.Fix == "" {
				it.Fix = "Turn it on"
			}
			it.State = StateWarn
			parts = append(parts, "not announced to Macs and Linux yet")
		}
	}
	it.Detail = strings.Join(parts, "; ") + "."
	return it
}

// lanAddrs lists this machine's IPv4 addresses on real network interfaces.
func lanAddrs() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		skip := false
		for _, p := range []string{"docker", "veth", "virbr", "br-", "podman", "cni", "vnet", "lxc"} {
			if strings.HasPrefix(ifc.Name, p) {
				skip = true
			}
		}
		if skip {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out
}
