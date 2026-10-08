package hostctl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
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
		n := WiFiNetwork{SSID: f[1], Signal: sig, Security: sec, Secure: sec != "" && sec != "--", InUse: f[0] == "*", Saved: saved[f[1]]}
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

// WiFiConnect joins a network. The password reaches nmcli through a private
// temporary file, never on the command line (where `ps` would show it).
func WiFiConnect(ctx context.Context, ssid, password string) error {
	if err := canControl(); err != nil {
		return err
	}
	if ssid == "" || len(ssid) > 32 || strings.ContainsAny(ssid, "\x00\n") {
		return errors.New("invalid network name")
	}
	if strings.ContainsAny(password, "\x00\n") {
		return errors.New("invalid password")
	}
	args := []string{"device", "wifi", "connect", ssid}
	if password != "" {
		f, err := os.CreateTemp("", "nocap-wifi-*")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if err := f.Chmod(0o600); err != nil {
			f.Close()
			return err
		}
		if _, err := f.WriteString("802-11-wireless-security.psk:" + password + "\n"); err != nil {
			f.Close()
			return err
		}
		f.Close()
		args = append(args, "passwd-file", f.Name())
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	_, err := runCmd(ctx, "nmcli", args...)
	return err
}

func WiFiDisconnect(ctx context.Context, device string) error {
	if err := canControl(); err != nil {
		return err
	}
	if device == "" || strings.ContainsAny(device, " /\x00") {
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
	timer *time.Timer
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
		go func() { _ = applyIPv4(context.Background(), pc.conn, pc.old) }()
	}
	return ok
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
