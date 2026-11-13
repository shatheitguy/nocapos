package hostctl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// IPv4 is how an interface gets its IPv4 address.
type IPv4 struct {
	Method    string   `json:"method"` // auto (DHCP) | manual (static) | disabled | unknown
	Addresses []string `json:"addresses"`
	Gateway   string   `json:"gateway,omitempty"`
	DNS       []string `json:"dns"`
	Editable  bool     `json:"editable"` // NoCapOS can change it (NetworkManager connection)
}

// IPv4Config is a requested change.
type IPv4Config struct {
	Method  string   `json:"method"`  // auto | manual
	Address string   `json:"address"` // CIDR, e.g. 192.168.1.50/24 (manual)
	Gateway string   `json:"gateway"`
	DNS     []string `json:"dns"`
}

// WiFiNetwork is one access point name seen in a scan.
type WiFiNetwork struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"` // 0–100
	Security string `json:"security"`
	Secure   bool   `json:"secure"`
	InUse    bool   `json:"in_use"`
	Saved    bool   `json:"saved"` // a profile exists, so no password is needed
	// Enterprise is an 802.1X network (WPA/WPA2/WPA3-Enterprise): joining it
	// takes an EAP sign-in, not a shared password.
	Enterprise bool `json:"enterprise"`
}

// splitTerse splits one line of `nmcli -t` output: fields are separated by
// ':' and literal ':' / '\' inside values are escaped with '\'.
func splitTerse(line string) []string {
	var out []string
	var b strings.Builder
	esc := false
	for _, r := range line {
		switch {
		case esc:
			b.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == ':':
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	return append(out, b.String())
}

// keyValues parses `nmcli -t … show` output ("KEY:value" per line). Indexed
// keys like IP4.ADDRESS[1] are collected under their base name.
func keyValues(out string) map[string][]string {
	m := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if i := strings.IndexByte(k, '['); i > 0 {
			k = k[:i]
		}
		v = strings.TrimSpace(strings.ReplaceAll(v, `\:`, ":"))
		if v != "" && v != "--" {
			m[k] = append(m[k], v)
		}
	}
	return m
}

func splitList(vs []string) []string {
	var out []string
	for _, v := range vs {
		for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// fillIPv4NM adds the method (from the connection profile) and the live
// addresses / gateway / DNS (from the device) to each managed interface.
func fillIPv4NM(ctx context.Context, st *NetworkState) {
	for i := range st.Interfaces {
		it := &st.Interfaces[i]
		ip := &IPv4{Method: "unknown", Addresses: []string{}, DNS: []string{}}
		if out, err := runCmd(ctx, "nmcli", "-t", "-f", "IP4.ADDRESS,IP4.GATEWAY,IP4.DNS", "device", "show", it.Name); err == nil {
			kv := keyValues(out)
			ip.Addresses = append(ip.Addresses, kv["IP4.ADDRESS"]...)
			if g := kv["IP4.GATEWAY"]; len(g) > 0 {
				ip.Gateway = g[0]
			}
			ip.DNS = append(ip.DNS, kv["IP4.DNS"]...)
		}
		if it.Conn != "" {
			if out, err := runCmd(ctx, "nmcli", "-t", "-f", "ipv4.method", "connection", "show", it.Conn); err == nil {
				if m := keyValues(out)["ipv4.method"]; len(m) > 0 {
					ip.Method = m[0]
				}
				ip.Editable = true
			}
		}
		if len(ip.Addresses) == 0 && len(it.Addrs) > 0 {
			ip.Addresses = it.Addrs
		}
		it.IPv4 = ip
	}
}

// ---------- Wi-Fi ----------

func WiFiNetworks(ctx context.Context, rescan bool) ([]WiFiNetwork, error) {
	if err := canControl(); err != nil {
		return nil, err
	}
	re := "no"
	if rescan {
		re = "yes"
	}
	out, err := runCmd(ctx, "nmcli", "-t", "-f", "IN-USE,SSID,SIGNAL,SECURITY", "device", "wifi", "list", "--rescan", re)
	if err != nil {
		return nil, err
	}
	saved := savedWiFi(ctx)
	best := map[string]WiFiNetwork{}
	for _, line := range strings.Split(out, "\n") {
		f := splitTerse(line)
		if len(f) < 4 || f[1] == "" {
			continue // hidden networks have no name
		}
		sig, _ := strconv.Atoi(f[2])
		sec := strings.TrimSpace(f[3])
		n := WiFiNetwork{SSID: f[1], Signal: sig, Security: sec, Secure: sec != "" && sec != "--", InUse: f[0] == "*", Saved: saved[f[1]],
			Enterprise: strings.Contains(strings.ToUpper(sec), "802.1X")}
		if cur, ok := best[n.SSID]; !ok || n.InUse || (!cur.InUse && n.Signal > cur.Signal) {
			best[n.SSID] = n
		}
	}
	list := make([]WiFiNetwork, 0, len(best))
	for _, n := range best {
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].InUse != list[j].InUse {
			return list[i].InUse
		}
		return list[i].Signal > list[j].Signal
	})
	return list, nil
}

// savedWiFi lists Wi-Fi profiles NetworkManager already has (by name, which
// is the SSID for connections it created).
func savedWiFi(ctx context.Context) map[string]bool {
	m := map[string]bool{}
	out, err := runCmd(ctx, "nmcli", "-t", "-f", "NAME,TYPE", "connection", "show")
	if err != nil {
		return m
	}
	for _, line := range strings.Split(out, "\n") {
		f := splitTerse(line)
		if len(f) >= 2 && strings.Contains(f[1], "wireless") {
			m[f[0]] = true
		}
	}
	return m
}

// WiFiJoin is a request to join a network.
type WiFiJoin struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Hidden   bool   `json:"hidden"` // not broadcast: join it by name
	Device   string `json:"device"` // Wi-Fi interface ("" = the first one)
	// EAP, when set, joins a WPA/WPA2/WPA3-Enterprise (802.1X) network;
	// Password is then unused (the EAP password lives in EAP).
	EAP *EAPConfig `json:"eap,omitempty"`
}

// wifiConnectTimeout bounds association plus DHCP.
const wifiConnectTimeout = 50 * time.Second

// Validate checks a join request.
func (j WiFiJoin) Validate() error {
	if j.SSID == "" || len(j.SSID) > 32 || strings.ContainsAny(j.SSID, "\x00\n\r") {
		return errors.New("enter the network name (up to 32 characters)")
	}
	if strings.ContainsAny(j.Password, "\x00\n\r") {
		return errors.New("the password can't contain line breaks")
	}
	if len(j.Password) > 64 {
		return errors.New("Wi-Fi passwords are at most 63 characters (or a 64-digit hex key)")
	}
	if j.Device != "" && !validDevice(j.Device) {
		return errors.New("invalid device")
	}
	if j.EAP != nil {
		return j.EAP.Validate(nil)
	}
	return nil
}

func validDevice(d string) bool {
	return d != "" && len(d) <= 15 && !strings.ContainsAny(d, " /\x00\n:")
}

// wifiConnectArgs builds `nmcli device wifi connect` for an open network, or
// to rejoin one NetworkManager already has secrets for. NetworkManager works
// out WPA/WPA2/WPA3 (SAE) or WEP from the scan; the SSID is one argument, so
// spaces and quotes in it need no escaping.
func wifiConnectArgs(j WiFiJoin) []string {
	args := []string{"--wait", strconv.Itoa(int(wifiConnectTimeout/time.Second) - 5), "device", "wifi", "connect", j.SSID}
	if j.Device != "" {
		args = append(args, "ifname", j.Device)
	}
	if j.Hidden {
		args = append(args, "hidden", "yes")
	}
	return args
}

// NetworkManager's own folder for connection profiles; a profile placed here
// (owner root, mode 0600, which NM insists on) is picked up by `connection
// reload` the same as one nmcli itself would have written. A var, not a
// const, so tests can point it at a temp dir instead of needing root.
var nmConnectionsDir = "/etc/NetworkManager/system-connections"

// writeWifiProfile saves a WPA/WPA2/WPA3 personal or enterprise profile as
// a NetworkManager keyfile. This is how secrets reach NetworkManager instead
// of a command-line argument (`nmcli device wifi connect ... password ...`,
// where any other local user could read it from `ps` or /proc/<pid>/cmdline
// for as long as the connect attempt runs): they only ever live in this one
// root-only file, which NetworkManager itself reads. Enterprise certificates
// go in a root-only folder of their own (see writeCerts).
func writeWifiProfile(j WiFiJoin) (path string, cleanup func(), err error) {
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return "", nil, err
	}
	uuid, err := newUUID()
	if err != nil {
		return "", nil, err
	}
	var certs certPaths
	if j.EAP != nil {
		if certs, err = writeCerts(uuid, *j.EAP, certPaths{}); err != nil {
			removeCerts(uuid)
			return "", nil, err
		}
	}
	path = filepath.Join(nmConnectionsDir, "nocap-"+hex.EncodeToString(id)+".nmconnection")
	if err := writeFileAtomic(path, []byte(wifiKeyfile(j, uuid, certs))); err != nil {
		removeCerts(uuid)
		return "", nil, err
	}
	return path, func() { os.Remove(path); removeCerts(uuid) }, nil
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// wifiKeyfile is the keyfile text for a new Wi-Fi profile.
func wifiKeyfile(j WiFiJoin, uuid string, certs certPaths) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[connection]\nid=%s\nuuid=%s\ntype=wifi\n", keyfileEscape(j.SSID), uuid)
	if j.Device != "" {
		fmt.Fprintf(&b, "interface-name=%s\n", j.Device)
	}
	b.WriteString("\n[wifi]\nmode=infrastructure\n")
	fmt.Fprintf(&b, "ssid=%s\n", keyfileEscape(j.SSID))
	if j.Hidden {
		b.WriteString("hidden=true\n")
	}
	if j.EAP != nil {
		b.WriteString("\n[wifi-security]\nkey-mgmt=wpa-eap\n\n[802-1x]\n")
		for _, kv := range eapKeys(*j.EAP, certs, true) {
			if kv.v != nil {
				fmt.Fprintf(&b, "%s=%s\n", kv.k, *kv.v)
			}
		}
	} else {
		b.WriteString("\n[wifi-security]\nkey-mgmt=wpa-psk\n")
		fmt.Fprintf(&b, "psk=%s\n", keyfileEscape(j.Password))
	}
	b.WriteString("\n[ipv4]\nmethod=auto\n\n[ipv6]\nmethod=auto\n")
	return b.String()
}

// keyfileEscape writes a string the way NetworkManager's keyfile reader
// (GKeyFile) reads it back literally: backslashes and control characters
// escaped, and leading or trailing spaces kept with \s.
func keyfileEscape(v string) string {
	var b strings.Builder
	trail := len(strings.TrimRight(v, " "))
	for i, r := range v {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == ' ' && (i == 0 || i >= trail):
			b.WriteString(`\s`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wifiProfiles returns the UUIDs of saved Wi-Fi profiles for an SSID: named
// after it, or "SSID 1", "SSID 2"… as NetworkManager names repeats.
func wifiProfiles(ctx context.Context, ssid string) []string {
	out, err := runCmd(ctx, "nmcli", "-t", "-f", "NAME,UUID,TYPE", "connection", "show")
	if err != nil {
		return nil
	}
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		f := splitTerse(line)
		if len(f) < 3 || !strings.Contains(f[2], "wireless") {
			continue
		}
		name := f[0]
		if rest, ok := strings.CutPrefix(name, ssid); ok && (rest == "" || isRepeatSuffix(rest)) {
			ids = append(ids, f[1])
		}
	}
	return ids
}

func isRepeatSuffix(s string) bool {
	n, ok := strings.CutPrefix(s, " ")
	if !ok || n == "" {
		return false
	}
	_, err := strconv.Atoi(n)
	return err == nil
}

func deleteProfiles(ctx context.Context, uuids []string) {
	for _, id := range uuids {
		_, _ = runCmd(ctx, "nmcli", "connection", "delete", "uuid", id)
		removeCerts(id)
	}
}

// firstWiFiDevice is the first Wi-Fi interface NetworkManager manages.
func firstWiFiDevice(ctx context.Context) string {
	out, err := runCmd(ctx, "nmcli", "-t", "-f", "DEVICE,TYPE", "device")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if f := splitTerse(line); len(f) >= 2 && f[1] == "wifi" {
			return f[0]
		}
	}
	return ""
}

// WiFiConnect joins a network. With a password, any saved profile for that
// network is removed first: a stale one with an old key would otherwise be
// reused and fail with "secrets were required". If joining fails, the
// profile the attempt left behind is removed too, so a typo isn't saved.
func WiFiConnect(ctx context.Context, j WiFiJoin) error {
	if err := canControl(); err != nil {
		return err
	}
	if err := j.Validate(); err != nil {
		return err
	}
	if j.Device == "" {
		j.Device = firstWiFiDevice(ctx)
	}
	secrets := j.Password != "" || j.EAP != nil
	if secrets || j.Hidden {
		deleteProfiles(ctx, wifiProfiles(ctx, j.SSID))
	}
	cctx, cancel := context.WithTimeout(ctx, wifiConnectTimeout)
	defer cancel()

	var out string
	var err error
	var profileCleanup func()
	if secrets {
		path, cleanup, werr := writeWifiProfile(j)
		if werr != nil {
			return werr
		}
		profileCleanup = cleanup
		if _, err = runCmd(cctx, "nmcli", "connection", "reload", path); err != nil {
			cleanup()
			return fmt.Errorf("couldn't load the connection: %w", err)
		}
		out, err = runCmd(cctx, "nmcli", "--wait", strconv.Itoa(int(wifiConnectTimeout/time.Second)-5), "connection", "up", "id", j.SSID)
	} else {
		out, err = runCmd(cctx, "nmcli", wifiConnectArgs(j)...)
	}
	if err == nil {
		// Success: leave the profile in place so NetworkManager can
		// reconnect on its own (e.g. after a reboot or coming back in range).
		return nil
	}
	if profileCleanup != nil {
		profileCleanup()
	}
	if secrets || j.Hidden {
		deleteProfiles(context.Background(), wifiProfiles(context.Background(), j.SSID))
	}
	return wifiError(j, out, err)
}

// wifiError explains nmcli's failure and keeps its own words for details.
func wifiError(j WiFiJoin, out string, err error) error {
	raw := strings.TrimSpace(out)
	if raw == "" {
		raw = err.Error()
	}
	raw = strings.TrimPrefix(raw, "Error: ")
	low := strings.ToLower(raw)
	var why string
	switch {
	case strings.Contains(low, "secrets were required") || strings.Contains(low, "no secrets") ||
		strings.Contains(low, "802-1x supplicant") || strings.Contains(low, "4-way handshake") || strings.Contains(low, "psk: property is invalid"):
		if j.EAP != nil {
			why = j.SSID + " turned down that sign-in. Check the user name, password and certificate, then try again."
		} else if j.Password == "" {
			why = "This network needs a password. Pop it in and try again."
		} else {
			why = "That password's not it for " + j.SSID + ". Try again?"
		}
	case strings.Contains(low, "no network with ssid") || strings.Contains(low, "not found"):
		why = "Lowkey can't find " + j.SSID + " — it's out of range (for a hidden network, choose Other network and tick Hidden)."
	case strings.Contains(low, "timeout") || strings.Contains(low, "timed out") || strings.Contains(low, "killed") || strings.Contains(low, "deadline"):
		why = j.SSID + " is taking forever to answer. Move closer to the router and try again."
	case strings.Contains(low, "ip configuration could not be reserved") || strings.Contains(low, "dhcp"):
		why = "Joined " + j.SSID + ", but its router never handed over an address."
	case j.EAP == nil && (strings.Contains(low, "key-mgmt") || strings.Contains(low, "enterprise") || strings.Contains(low, "802-1x")):
		why = j.SSID + " is a WPA Enterprise network — sign in to it with a user name (Enterprise sign-in) instead of a password."
	}
	if why == "" {
		return fmt.Errorf("Wi-Fi's being sus — couldn't join %s: %s", j.SSID, raw)
	}
	// A recognized failure gets a plain, friendly message only — nmcli's own
	// text is for the unclassified case above, not for something this common.
	return errors.New(why)
}

func WiFiDisconnect(ctx context.Context, device string) error {
	if err := canControl(); err != nil {
		return err
	}
	if !validDevice(device) {
		return errors.New("invalid device")
	}
	_, err := runCmd(ctx, "nmcli", "device", "disconnect", device)
	return err
}

// ---------- IPv4: DHCP / static, with automatic revert ----------

// Validate checks a requested IPv4 configuration.
func (c IPv4Config) Validate() error {
	switch c.Method {
	case "auto":
	case "manual":
		p, err := netip.ParsePrefix(strings.TrimSpace(c.Address))
		if err != nil || !p.Addr().Is4() || p.Bits() < 1 || p.Bits() > 32 {
			return errors.New("enter the address with its prefix, e.g. 192.168.1.50/24")
		}
		if c.Gateway != "" {
			g, err := netip.ParseAddr(strings.TrimSpace(c.Gateway))
			if err != nil || !g.Is4() {
				return errors.New("the gateway must be an IPv4 address")
			}
			if !p.Masked().Contains(g) {
				return fmt.Errorf("the gateway %s isn't on the %s network", g, p.Masked())
			}
		}
	default:
		return errors.New("method must be auto (DHCP) or manual (static)")
	}
	for _, d := range c.DNS {
		if a, err := netip.ParseAddr(strings.TrimSpace(d)); err != nil || !a.Is4() {
			return fmt.Errorf("%q isn't an IPv4 DNS server", d)
		}
	}
	return nil
}

// modifyArgs builds the `nmcli connection modify` arguments for a config.
func modifyArgs(conn string, c IPv4Config) []string {
	args := []string{"connection", "modify", conn}
	dns := strings.Join(c.DNS, " ")
	if c.Method == "manual" {
		args = append(args, "ipv4.method", "manual", "ipv4.addresses", strings.TrimSpace(c.Address), "ipv4.gateway", strings.TrimSpace(c.Gateway))
	} else {
		args = append(args, "ipv4.method", "auto", "ipv4.addresses", "", "ipv4.gateway", "")
	}
	args = append(args, "ipv4.dns", dns)
	// With DHCP and custom DNS, use only the servers given.
	if c.Method == "auto" && dns != "" {
		args = append(args, "ipv4.ignore-auto-dns", "yes")
	} else {
		args = append(args, "ipv4.ignore-auto-dns", "no")
	}
	return args
}

// connIPv4 reads a connection profile's configured IPv4 settings (for revert).
func connIPv4(ctx context.Context, conn string) (IPv4Config, error) {
	out, err := runCmd(ctx, "nmcli", "-t", "-f", "ipv4.method,ipv4.addresses,ipv4.gateway,ipv4.dns", "connection", "show", conn)
	if err != nil {
		return IPv4Config{}, err
	}
	kv := keyValues(out)
	c := IPv4Config{Method: "auto", DNS: splitList(kv["ipv4.dns"])}
	if m := kv["ipv4.method"]; len(m) > 0 {
		c.Method = m[0]
	}
	if a := splitList(kv["ipv4.addresses"]); len(a) > 0 {
		c.Address = a[0]
	}
	if g := kv["ipv4.gateway"]; len(g) > 0 {
		c.Gateway = g[0]
	}
	if c.Method != "manual" {
		c.Method = "auto"
	}
	return c, nil
}

func applyIPv4(ctx context.Context, conn string, c IPv4Config) error {
	if _, err := runCmd(ctx, "nmcli", modifyArgs(conn, c)...); err != nil {
		return err
	}
	_, err := runCmd(ctx, "nmcli", "connection", "up", conn)
	return err
}

type pendingChange struct {
	conn  string
	old   IPv4Config
	old6  *IPv6Config // set when the change touches IPv6 too
	timer *time.Timer
	// applied: the new settings are live, so reaching the server now proves
	// they work (ConfirmIP only counts after this).
	applied bool
}

var (
	pendingMu sync.Mutex
	pending   = map[string]*pendingChange{}
)

// ChangeIPv4 applies a new IPv4 config shortly after returning and reverts it
// after `keep` unless KeepIPv4 is called — so a change that cuts off the
// browser undoes itself.
func ChangeIPv4(ctx context.Context, conn string, c IPv4Config, keep time.Duration) (string, error) {
	if err := canControl(); err != nil {
		return "", err
	}
	if conn == "" || strings.ContainsAny(conn, "\x00\n") {
		return "", errors.New("invalid connection")
	}
	if err := c.Validate(); err != nil {
		return "", err
	}
	old, err := connIPv4(ctx, conn)
	if err != nil {
		return "", fmt.Errorf("couldn't read the current settings: %w", err)
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	pc := &pendingChange{conn: conn, old: old}
	pendingMu.Lock()
	pending[token] = pc
	pc.timer = time.AfterFunc(keep, func() {
		pendingMu.Lock()
		_, still := pending[token]
		delete(pending, token)
		pendingMu.Unlock()
		if still {
			_ = applyIPv4(context.Background(), conn, old) // nobody confirmed: put it back
		}
	})
	pendingMu.Unlock()
	go func() {
		time.Sleep(800 * time.Millisecond) // let the HTTP response get out first
		_ = applyIPv4(context.Background(), conn, c)
	}()
	return token, nil
}

// RevertIPv4 undoes a pending change right away.
func RevertIPv4(token string) bool {
	pendingMu.Lock()
	pc, ok := pending[token]
	if ok {
		pc.timer.Stop()
		delete(pending, token)
	}
	pendingMu.Unlock()
	if ok {
		go func() { _ = pc.revert(context.Background()) }()
	}
	return ok
}

func (pc *pendingChange) revert(ctx context.Context) error {
	if pc.old6 == nil {
		return applyIPv4(ctx, pc.conn, pc.old)
	}
	return applyIP(ctx, pc.conn, pc.old, *pc.old6)
}

// KeepIPv4 confirms a change made by ChangeIPv4.
func KeepIPv4(token string) bool {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	pc, ok := pending[token]
	if !ok {
		return false
	}
	pc.timer.Stop()
	delete(pending, token)
	return true
}

// ---------- IPv4 + IPv6 together, confirmed by reaching the server ----------

// IPv6Config is a requested IPv6 setup.
type IPv6Config struct {
	Method  string   `json:"method"`  // auto (router advertisements / DHCPv6) | manual | disabled
	Address string   `json:"address"` // CIDR, e.g. fd00::50/64 (manual)
	Gateway string   `json:"gateway"`
	DNS     []string `json:"dns"`
}

// Validate checks a requested IPv6 configuration.
func (c IPv6Config) Validate() error {
	switch c.Method {
	case "auto", "disabled":
	case "manual":
		p, err := netip.ParsePrefix(strings.TrimSpace(c.Address))
		if err != nil || !p.Addr().Is6() || p.Addr().Is4In6() || p.Bits() < 1 || p.Bits() > 128 {
			return errors.New("enter the IPv6 address with its prefix, e.g. fd00::50/64")
		}
		if c.Gateway != "" {
			g, err := netip.ParseAddr(strings.TrimSpace(c.Gateway))
			if err != nil || !g.Is6() || g.Is4In6() {
				return errors.New("the IPv6 gateway must be an IPv6 address")
			}
			// Routers often advertise themselves by their link-local address.
			if !g.IsLinkLocalUnicast() && !p.Masked().Contains(g) {
				return fmt.Errorf("the IPv6 gateway %s isn't on the %s network", g, p.Masked())
			}
		}
	default:
		return errors.New("IPv6 method must be auto, manual (static) or disabled")
	}
	for _, d := range c.DNS {
		if a, err := netip.ParseAddr(strings.TrimSpace(d)); err != nil || !a.Is6() || a.Is4In6() {
			return fmt.Errorf("%q isn't an IPv6 DNS server", d)
		}
	}
	return nil
}

// modifyArgs6 is the IPv6 part of `nmcli connection modify`. method is passed
// through as is, so reverting can restore whatever the profile had before.
func modifyArgs6(c IPv6Config) []string {
	dns := strings.Join(c.DNS, " ")
	var args []string
	if c.Method == "manual" {
		args = []string{"ipv6.method", "manual", "ipv6.addresses", strings.TrimSpace(c.Address), "ipv6.gateway", strings.TrimSpace(c.Gateway)}
	} else {
		args = []string{"ipv6.method", c.Method, "ipv6.addresses", "", "ipv6.gateway", ""}
	}
	args = append(args, "ipv6.dns", dns)
	if c.Method == "auto" && dns != "" {
		args = append(args, "ipv6.ignore-auto-dns", "yes")
	} else {
		args = append(args, "ipv6.ignore-auto-dns", "no")
	}
	return args
}

// connIPv6 reads a profile's configured IPv6 settings (for revert).
func connIPv6(ctx context.Context, conn string) (IPv6Config, error) {
	out, err := runCmd(ctx, "nmcli", "-t", "-f", "ipv6.method,ipv6.addresses,ipv6.gateway,ipv6.dns", "connection", "show", conn)
	if err != nil {
		return IPv6Config{}, err
	}
	kv := keyValues(out)
	c := IPv6Config{Method: "auto", DNS: splitList(kv["ipv6.dns"])}
	if m := kv["ipv6.method"]; len(m) > 0 && validNMMethod6(m[0]) {
		c.Method = m[0]
	}
	if a := splitList(kv["ipv6.addresses"]); len(a) > 0 {
		c.Address = a[0]
	}
	if g := kv["ipv6.gateway"]; len(g) > 0 {
		c.Gateway = g[0]
	}
	return c, nil
}

func validNMMethod6(m string) bool {
	switch m {
	case "auto", "dhcp", "link-local", "manual", "ignore", "disabled", "shared":
		return true
	}
	return false
}

func applyIP(ctx context.Context, conn string, v4 IPv4Config, v6 IPv6Config) error {
	args := append(modifyArgs(conn, v4), modifyArgs6(v6)...)
	if _, err := runCmd(ctx, "nmcli", args...); err != nil {
		return err
	}
	_, err := runCmd(ctx, "nmcli", "connection", "up", conn)
	return err
}

// IPChange is a new address setup for one connection. IPv6 is optional
// (nil leaves it as it is).
type IPChange struct {
	IPv4 IPv4Config  `json:"ipv4"`
	IPv6 *IPv6Config `json:"ipv6,omitempty"`
}

// applyDelay lets the HTTP response get out before the address changes.
var applyDelay = 800 * time.Millisecond

// ChangeIP applies a new IPv4 (and optionally IPv6) setup shortly after
// returning, and puts the old one back after keep unless the change is
// confirmed: by ConfirmIP once the browser reaches the server on its new
// address, or by KeepIPv4. A change that cuts the browser off undoes itself.
func ChangeIP(ctx context.Context, conn string, c IPChange, keep time.Duration) (string, error) {
	if err := canControl(); err != nil {
		return "", err
	}
	if conn == "" || strings.ContainsAny(conn, "\x00\n") {
		return "", errors.New("invalid connection")
	}
	if err := c.IPv4.Validate(); err != nil {
		return "", err
	}
	if c.IPv6 != nil {
		if err := c.IPv6.Validate(); err != nil {
			return "", err
		}
	}
	old, err := connIPv4(ctx, conn)
	if err != nil {
		return "", fmt.Errorf("couldn't read the current settings: %w", err)
	}
	pc := &pendingChange{conn: conn, old: old}
	if c.IPv6 != nil {
		old6, err := connIPv6(ctx, conn)
		if err != nil {
			return "", fmt.Errorf("couldn't read the current IPv6 settings: %w", err)
		}
		pc.old6 = &old6
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	pendingMu.Lock()
	pending[token] = pc
	pc.timer = time.AfterFunc(keep, func() {
		pendingMu.Lock()
		_, still := pending[token]
		delete(pending, token)
		pendingMu.Unlock()
		if still {
			_ = pc.revert(context.Background()) // nobody confirmed: put it back
		}
	})
	pendingMu.Unlock()
	go func() {
		time.Sleep(applyDelay)
		var err error
		if c.IPv6 != nil {
			err = applyIP(context.Background(), conn, c.IPv4, *c.IPv6)
		} else {
			err = applyIPv4(context.Background(), conn, c.IPv4)
		}
		pendingMu.Lock()
		if _, still := pending[token]; still && err == nil {
			pc.applied = true
		}
		pendingMu.Unlock()
	}()
	return token, nil
}

// Confirm results.
const (
	ConfirmKept    = "kept"    // confirmed: the new settings stay
	ConfirmPending = "pending" // not live yet: ask again in a moment
	ConfirmGone    = "gone"    // unknown token, or already reverted
)

// ConfirmIP keeps a change made by ChangeIP, but only once it is live: the
// browser reaching the server after that is the proof the new address works.
func ConfirmIP(token string) string {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	pc, ok := pending[token]
	if !ok {
		return ConfirmGone
	}
	if !pc.applied {
		return ConfirmPending
	}
	pc.timer.Stop()
	delete(pending, token)
	return ConfirmKept
}

// ---------- Windows (read-only) ----------

// fillIPv4Windows reads `netsh interface ipv4 show config` (English output).
func fillIPv4Windows(ctx context.Context, st *NetworkState) {
	out, err := runCmd(ctx, "netsh", "interface", "ipv4", "show", "config")
	if err != nil {
		return
	}
	blocks := map[string]*IPv4{}
	var cur *IPv4
	var inDNS bool
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "Configuration for interface") {
			name := strings.Trim(strings.TrimPrefix(line, "Configuration for interface"), ` "`)
			cur = &IPv4{Method: "unknown", Addresses: []string{}, DNS: []string{}}
			blocks[name] = cur
			inDNS = false
			continue
		}
		if cur == nil || line == "" {
			continue
		}
		k, v, hasColon := strings.Cut(line, ":")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case hasColon && k == "DHCP enabled":
			if strings.EqualFold(v, "yes") {
				cur.Method = "auto"
			} else {
				cur.Method = "manual"
			}
			inDNS = false
		case hasColon && k == "IP Address":
			cur.Addresses = append(cur.Addresses, v)
			inDNS = false
		case hasColon && k == "Subnet Prefix":
			if p := strings.Fields(v); len(p) > 0 && len(cur.Addresses) > 0 {
				if pre, err := netip.ParsePrefix(p[0]); err == nil {
					last := len(cur.Addresses) - 1
					if !strings.Contains(cur.Addresses[last], "/") {
						cur.Addresses[last] += "/" + strconv.Itoa(pre.Bits())
					}
				}
			}
			inDNS = false
		case hasColon && k == "Default Gateway":
			if v != "" {
				cur.Gateway = v
			}
			inDNS = false
		case hasColon && strings.Contains(k, "DNS"):
			inDNS = true
			if a, err := netip.ParseAddr(v); err == nil {
				cur.DNS = append(cur.DNS, a.String())
			}
		case !hasColon && inDNS:
			if a, err := netip.ParseAddr(line); err == nil {
				cur.DNS = append(cur.DNS, a.String())
			}
		default:
			inDNS = false
		}
	}
	for i := range st.Interfaces {
		if ip, ok := blocks[st.Interfaces[i].Name]; ok {
			st.Interfaces[i].IPv4 = ip
		}
	}
}
