// Package firewall is NoCapOS's side of the host firewall. The engine is
// Linux's own ufw: this package validates what the Shield app asks for,
// turns it into ufw arguments (always an argument array, never a shell
// string), reads ufw's answers back, and keeps whoever is making a change
// from locking themselves out.
package firewall

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Rule is one firewall rule the way the Shield app shows and edits it.
type Rule struct {
	Action    string `json:"action"`              // allow | deny | reject | limit
	Direction string `json:"direction"`           // in | out
	Interface string `json:"interface,omitempty"` // "" = every interface
	Proto     string `json:"proto"`               // any | tcp | udp
	Port      string `json:"port,omitempty"`      // "", "22", "80,443", "8000:8100"
	From      string `json:"from,omitempty"`      // "" = anywhere, else an IP or CIDR
	To        string `json:"to,omitempty"`        // "" = any address on this machine
	App       string `json:"app,omitempty"`       // a ufw app profile, instead of Port
	Comment   string `json:"comment,omitempty"`
}

// ErrInvalid marks a rule (or a setting) NoCapOS refuses to hand to ufw.
var ErrInvalid = errors.New("invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

var (
	ifaceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,14}$`)
	appRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._+-]{0,63}$`)
)

// MaxPorts is how many ports one rule may list (a range counts as two): the
// kernel's multiport match takes fifteen.
const MaxPorts = 15

// MaxComment is the longest comment NoCapOS accepts.
const MaxComment = 80

// Normalize validates r and returns it in canonical form (lower-case,
// addresses in their shortest spelling, defaults filled in). Every field is
// checked: nothing reaches ufw that this function didn't accept.
func Normalize(r Rule) (Rule, error) {
	r.Action = strings.ToLower(strings.TrimSpace(r.Action))
	switch r.Action {
	case "allow", "deny", "reject", "limit":
	default:
		return r, invalid("action must be allow, deny, reject or limit")
	}
	r.Direction = strings.ToLower(strings.TrimSpace(r.Direction))
	switch r.Direction {
	case "":
		r.Direction = "in"
	case "in", "out":
	default:
		return r, invalid("direction must be in or out")
	}
	r.Interface = strings.TrimSpace(r.Interface)
	if r.Interface != "" && !ifaceRe.MatchString(r.Interface) {
		return r, invalid("that isn't a network interface name")
	}
	r.Proto = strings.ToLower(strings.TrimSpace(r.Proto))
	switch r.Proto {
	case "", "any":
		r.Proto = "any"
	case "tcp", "udp":
	default:
		return r, invalid("protocol must be any, tcp or udp")
	}
	var err error
	if r.From, err = normalizeAddr(r.From); err != nil {
		return r, invalid("source: %v", err)
	}
	if r.To, err = normalizeAddr(r.To); err != nil {
		return r, invalid("destination: %v", err)
	}
	if r.From != "" && r.To != "" && isV6(r.From) != isV6(r.To) {
		return r, invalid("source and destination must both be IPv4 or both IPv6")
	}
	r.App = strings.TrimSpace(r.App)
	if r.App != "" {
		if !appRe.MatchString(r.App) {
			return r, invalid("that isn't an app profile name")
		}
		if strings.TrimSpace(r.Port) != "" {
			return r, invalid("pick an app profile or ports, not both")
		}
		if r.Proto != "any" {
			return r, invalid("an app profile brings its own protocol")
		}
	}
	if r.Port, err = normalizePorts(r.Port); err != nil {
		return r, err
	}
	if strings.ContainsAny(r.Port, ",:") && r.Proto == "any" {
		return r, invalid("several ports or a range need tcp or udp")
	}
	if r.Port == "" && r.App == "" && r.Proto != "any" && r.From == "" && r.To == "" {
		// ufw has no way to say "all of tcp from anywhere to anywhere".
		return r, invalid("a protocol on its own needs a port or an address")
	}
	if r.Port == "" && r.App == "" && r.From == "" && r.To == "" && r.Interface == "" {
		return r, invalid("the rule needs a port, an app, an address or an interface")
	}
	r.Comment = strings.TrimSpace(r.Comment)
	if len(r.Comment) > MaxComment {
		return r, invalid("the comment is too long (%d characters at most)", MaxComment)
	}
	for _, c := range r.Comment {
		if c < 0x20 || c == 0x7f || c == '\'' || c == '"' || c == '\\' || c == '#' {
			return r, invalid("the comment can't contain quotes, #, \\ or control characters")
		}
	}
	return r, nil
}

func isV6(s string) bool { return strings.Contains(s, ":") }

// normalizeAddr accepts "", "any", an IP address or a CIDR prefix.
func normalizeAddr(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "any" || s == "anywhere" {
		return "", nil
	}
	bad := errors.New("not an IP address or a network like 192.168.1.0/24")
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil || p.Addr().Zone() != "" {
			return "", bad
		}
		if p.Bits() == p.Addr().BitLen() {
			return p.Addr().String(), nil
		}
		if p.Bits() == 0 {
			return "", nil // 0.0.0.0/0 and ::/0 are "anywhere"
		}
		return p.Masked().String(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return "", bad
	}
	return a.Unmap().String(), nil
}

// normalizePorts accepts "22", "80,443", "8000:8100" and mixes of them.
func normalizePorts(s string) (string, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	s = strings.ReplaceAll(s, "-", ":")
	if s == "" {
		return "", nil
	}
	parts := strings.Split(s, ",")
	count := 0
	for i, p := range parts {
		lo, hi, isRange := strings.Cut(p, ":")
		a, err := parsePort(lo)
		if err != nil {
			return "", err
		}
		count++
		parts[i] = strconv.Itoa(a)
		if isRange {
			b, err := parsePort(hi)
			if err != nil {
				return "", err
			}
			if b <= a {
				return "", invalid("a port range goes from low to high, like 8000:8100")
			}
			count++
			parts[i] += ":" + strconv.Itoa(b)
		}
	}
	if count > MaxPorts {
		return "", invalid("too many ports in one rule (%d at most; a range counts as two)", MaxPorts)
	}
	return strings.Join(parts, ","), nil
}

func parsePort(s string) (int, error) {
	bad := invalid("ports are numbers from 1 to 65535")
	if s == "" || len(s) > 5 {
		return 0, bad
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, bad
		}
	}
	n, _ := strconv.Atoi(s)
	if n < 1 || n > 65535 {
		return 0, bad
	}
	return n, nil
}

// Args returns the ufw arguments that add r (after "ufw" and any
// "insert N"). r must come from Normalize. The full syntax is always used,
// so nothing in a field can be read as another keyword.
func (r Rule) Args() []string {
	a := []string{r.Action, r.Direction}
	if r.Interface != "" {
		a = append(a, "on", r.Interface)
	}
	if r.Proto != "any" {
		a = append(a, "proto", r.Proto)
	}
	a = append(a, "from", orAny(r.From), "to", orAny(r.To))
	switch {
	case r.App != "":
		a = append(a, "app", r.App)
	case r.Port != "":
		a = append(a, "port", r.Port)
	}
	if r.Comment != "" {
		a = append(a, "comment", r.Comment)
	}
	return a
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}

// ports lists the port ranges r covers; all is true when it covers every
// port (or the ports can't be known, as with an app profile).
func (r Rule) ports() (ranges [][2]int, all bool) {
	if r.Port == "" {
		return nil, true
	}
	for _, p := range strings.Split(r.Port, ",") {
		lo, hi, isRange := strings.Cut(p, ":")
		a, _ := strconv.Atoi(lo)
		b := a
		if isRange {
			b, _ = strconv.Atoi(hi)
		}
		ranges = append(ranges, [2]int{a, b})
	}
	return ranges, false
}

// CoversPort reports whether r applies to port/proto ("tcp" or "udp").
func (r Rule) CoversPort(port int, proto string) bool {
	if r.Proto != "any" && r.Proto != proto {
		return false
	}
	ranges, all := r.ports()
	if all {
		return true
	}
	for _, x := range ranges {
		if port >= x[0] && port <= x[1] {
			return true
		}
	}
	return false
}

// CoversSource reports whether r applies to traffic from ip. The zero Addr
// stands for "anyone": only rules without a source cover it.
func (r Rule) CoversSource(ip netip.Addr) bool {
	if r.From == "" {
		return true
	}
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	if p, err := netip.ParsePrefix(r.From); err == nil {
		return p.Contains(ip)
	}
	a, err := netip.ParseAddr(r.From)
	return err == nil && a == ip
}

// OnlyPort reports whether r is exactly "this one port, from anywhere, on
// every interface": the kind of rule the per-service switch adds and removes.
func (r Rule) OnlyPort(port int, proto string) bool {
	return r.Direction == "in" && r.Interface == "" && r.From == "" && r.To == "" && r.App == "" &&
		r.Port == strconv.Itoa(port) && (r.Proto == proto || r.Proto == "any")
}
