package firewall

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Entry is a rule as ufw reports it.
type Entry struct {
	// Number is the rule's position in "ufw status numbered" (0 while the
	// firewall is off: ufw only numbers running rules).
	Number int `json:"number"`
	Rule
	V6 bool `json:"v6,omitempty"`
	// Raw is ufw's own text for the rule. It names the rule in requests, so
	// a change made somewhere else in the meantime is noticed.
	Raw string `json:"raw"`
	// Editable is false for rules NoCapOS shows but can't rebuild (routed
	// traffic, protocols it doesn't offer): those can only be deleted.
	Editable bool `json:"editable"`
	// Guard marks the rule that keeps the person who turned the firewall on
	// connected.
	Guard bool `json:"guard,omitempty"`
	// spec holds the words after "ufw" for rules read from "ufw show added".
	spec []string
}

// Report is what "ufw status verbose" says besides the rules.
type Report struct {
	Active   bool   `json:"active"`
	Incoming string `json:"incoming"` // allow | deny | reject
	Outgoing string `json:"outgoing"`
	Routed   string `json:"routed,omitempty"` // allow | deny | reject | disabled
	Logging  string `json:"logging"`          // off | low | medium | high | full
}

var (
	defaultRe = regexp.MustCompile(`(allow|deny|reject|disabled) \((incoming|outgoing|routed)\)`)
	loggingRe = regexp.MustCompile(`^Logging:\s+(on|off)(?:\s+\((\w+)\))?`)
	rowRe     = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.*?)\s+(ALLOW|DENY|REJECT|LIMIT)(?:\s+(IN|OUT|FWD))?\s+(.*)$`)
	portRe    = regexp.MustCompile(`^[0-9]+(?::[0-9]+)?(?:,[0-9]+(?::[0-9]+)?)*$`)
)

// ParseReport reads "ufw status verbose".
func ParseReport(out string) Report {
	r := Report{Logging: "off"}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Status:"):
			r.Active = strings.TrimSpace(strings.TrimPrefix(line, "Status:")) == "active"
		case strings.HasPrefix(line, "Logging:"):
			if m := loggingRe.FindStringSubmatch(line); m != nil && m[1] == "on" {
				r.Logging = "low"
				if m[2] != "" {
					r.Logging = m[2]
				}
			}
		case strings.HasPrefix(line, "Default:"):
			for _, m := range defaultRe.FindAllStringSubmatch(line, -1) {
				switch m[2] {
				case "incoming":
					r.Incoming = m[1]
				case "outgoing":
					r.Outgoing = m[1]
				case "routed":
					r.Routed = m[1]
				}
			}
		}
	}
	return r
}

// ParseNumbered reads "ufw status numbered": whether the firewall is on,
// and its running rules in order.
func ParseNumbered(out string) (active bool, rules []Entry) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if s, ok := strings.CutPrefix(strings.TrimSpace(line), "Status:"); ok {
			active = strings.TrimSpace(s) == "active"
			continue
		}
		m := rowRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		e := Entry{Number: n, Raw: strings.Join(strings.Fields(strings.TrimSpace(line[strings.Index(line, "]")+1:])), " ")}
		e.Action = strings.ToLower(m[3])
		e.Direction = "in"
		ok := true
		switch m[4] {
		case "OUT":
			e.Direction = "out"
		case "FWD":
			ok = false // routed traffic: shown, not editable
		}
		rest := m[5]
		if i := strings.Index(rest, "#"); i >= 0 {
			e.Comment = strings.TrimSpace(rest[i+1:])
			rest = rest[:i]
		}
		rest = strings.TrimSpace(rest)
		// Markers ufw adds after the source column.
		for _, mark := range []string{"(out)", "(log-all)", "(log)"} {
			if strings.HasSuffix(rest, mark) {
				if mark != "(out)" {
					ok = false // logging rules can't be rebuilt from here
				}
				rest = strings.TrimSpace(strings.TrimSuffix(rest, mark))
			}
		}
		to, okTo := parseLocation(m[2])
		from, okFrom := parseLocation(rest)
		ok = ok && okTo && okFrom
		e.V6 = to.v6 || from.v6
		e.To, e.Port, e.App, e.Proto = to.addr, to.port, to.app, to.proto
		e.From = from.addr
		if from.port != "" || from.app != "" {
			ok = false // source ports: rare, and not offered
		}
		if from.proto != "" {
			if e.Proto != "" && e.Proto != from.proto {
				ok = false
			}
			e.Proto = from.proto
		}
		if e.Proto == "" {
			e.Proto = "any"
		}
		switch {
		case to.iface != "" && from.iface != "":
			ok = false
		case to.iface != "":
			e.Interface = to.iface
		case from.iface != "":
			e.Interface = from.iface
		}
		if ok {
			// What comes back from Normalize is what would be sent to ufw
			// again, so only rules that survive it are editable.
			if norm, err := Normalize(e.Rule); err == nil {
				e.Rule = norm
			} else {
				ok = false
			}
		}
		e.Editable = ok
		e.Guard = e.Comment == GuardComment
		rules = append(rules, e)
	}
	return active, rules
}

type location struct {
	addr, port, app, proto, iface string
	v6                            bool
}

// parseLocation reads one "To" or "From" cell of ufw's status table:
// "Anywhere", "22/tcp", "80,443/tcp (v6)", "192.168.1.5 22/tcp",
// "10.0.0.0/8", "OpenSSH", "Anywhere on eth0", "192.168.1.5/udp", …
func parseLocation(s string) (location, bool) {
	var l location
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, " on "); i >= 0 {
		l.iface = strings.TrimSpace(s[i+4:])
		s = strings.TrimSpace(s[:i])
		if !ifaceRe.MatchString(l.iface) {
			return l, false
		}
	}
	if strings.HasSuffix(s, "(v6)") {
		l.v6 = true
		s = strings.TrimSpace(strings.TrimSuffix(s, "(v6)"))
	}
	if s == "" {
		return l, false
	}
	fields := strings.Fields(s)
	first := fields[0]
	// "Anywhere" or an address, each optionally with "/tcp" or "/udp".
	base, proto := first, ""
	for _, p := range []string{"tcp", "udp"} {
		if b, ok := strings.CutSuffix(first, "/"+p); ok {
			base, proto = b, p
		}
	}
	switch {
	case base == "Anywhere":
		l.proto = proto
		fields = fields[1:]
	case isAddress(base):
		l.addr, l.proto = base, proto
		if strings.Contains(base, ":") {
			l.v6 = true
		}
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return l, true
	}
	rest := strings.Join(fields, " ")
	// What's left is ports ("22", "80,443/tcp", "8000:8100/udp") or an app.
	ports, pproto := rest, ""
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		ports, pproto = rest[:i], rest[i+1:]
	}
	if portRe.MatchString(ports) {
		if pproto != "" && pproto != "tcp" && pproto != "udp" {
			l.port = ports
			return l, false // a protocol NoCapOS doesn't offer
		}
		l.port = ports
		if pproto != "" {
			l.proto = pproto
		}
		return l, true
	}
	if l.proto != "" {
		return l, false
	}
	l.app = rest
	return l, appRe.MatchString(rest)
}

func isAddress(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	_, err := netip.ParsePrefix(s)
	return err == nil
}

// ParseAdded reads "ufw show added": the rules as the commands that made
// them. It's the only listing ufw gives while the firewall is off.
func ParseAdded(out string) []Entry {
	var rules []Entry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		words, ok := splitWords(line)
		if !ok || len(words) < 2 || words[0] != "ufw" {
			continue
		}
		e := Entry{Raw: line, spec: words[1:]}
		if r, ok := parseSpec(words[1:]); ok {
			if norm, err := Normalize(r); err == nil {
				e.Rule, e.Editable = norm, true
			}
		}
		if !e.Editable {
			e.Action = words[1]
			if e.Action == "route" && len(words) > 2 {
				e.Action = words[2]
			}
			e.Direction, e.Proto = "in", "any"
		}
		e.Guard = e.Comment == GuardComment
		rules = append(rules, e)
	}
	return rules
}

// splitWords splits a command line the way ufw prints it: words separated
// by spaces, with the comment in single quotes.
func splitWords(s string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	in, quoted := false, false
	for _, c := range s {
		switch {
		case c == '\'':
			quoted = !quoted
			in = true
		case (c == ' ' || c == '\t') && !quoted:
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(c)
			in = true
		}
	}
	if quoted {
		return nil, false
	}
	if in {
		words = append(words, cur.String())
	}
	return words, true
}

// parseSpec reads ufw's rule syntax, simple ("allow 22/tcp", "limit
// OpenSSH") and full ("deny in on eth0 from 10.0.0.0/8 to any port 22 proto
// tcp comment 'x'").
func parseSpec(w []string) (Rule, bool) {
	var r Rule
	if len(w) < 2 {
		return r, false
	}
	switch w[0] {
	case "allow", "deny", "reject", "limit":
		r.Action = w[0]
	default:
		return r, false // "route …" and anything newer
	}
	w = w[1:]
	r.Direction, r.Proto = "in", "any"
	if w[0] == "in" || w[0] == "out" {
		r.Direction = w[0]
		w = w[1:]
	}
	full := false
	for i := 0; i < len(w); i++ {
		next := func() (string, bool) {
			if i+1 >= len(w) {
				return "", false
			}
			i++
			return w[i], true
		}
		var v string
		var ok bool
		switch w[i] {
		case "on":
			if v, ok = next(); !ok {
				return r, false
			}
			r.Interface, full = v, true
		case "proto":
			if v, ok = next(); !ok {
				return r, false
			}
			r.Proto, full = v, true
		case "from", "to":
			key := w[i]
			if v, ok = next(); !ok {
				return r, false
			}
			full = true
			if v == "any" {
				v = ""
			}
			if key == "from" {
				r.From = v
			} else {
				r.To = v
			}
			// "port N" or "app NAME" may follow either address.
			if i+2 < len(w) && (w[i+1] == "port" || w[i+1] == "app") {
				if key == "from" {
					return r, false // source ports aren't offered
				}
				if w[i+1] == "port" {
					r.Port = w[i+2]
				} else {
					r.App = w[i+2]
				}
				i += 2
			}
		case "comment":
			if v, ok = next(); !ok {
				return r, false
			}
			r.Comment = v
		case "log", "log-all":
			return r, false
		default:
			if full || i != 0 {
				return r, false
			}
			// Simple syntax: one word, "22", "22/tcp", "80,443/tcp" or an app.
			ports, proto, hasProto := strings.Cut(w[i], "/")
			if portRe.MatchString(ports) {
				r.Port = ports
				if hasProto {
					r.Proto = proto
				}
			} else {
				r.App = w[i]
			}
		}
	}
	return r, true
}

// ParseAppList reads "ufw app list".
func ParseAppList(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			continue // the heading
		}
		if name := strings.TrimSpace(line); name != "" && appRe.MatchString(name) {
			names = append(names, name)
		}
	}
	return names
}

// AppProfile is one ufw application profile.
type AppProfile struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Ports       string `json:"ports,omitempty"`
}

// ParseAppProfiles reads one file from /etc/ufw/applications.d.
func ParseAppProfiles(text string) []AppProfile {
	var out []AppProfile
	var cur *AppProfile
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' && line[len(line)-1] == ']' {
			out = append(out, AppProfile{Name: strings.TrimSpace(line[1 : len(line)-1])})
			cur = &out[len(out)-1]
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || cur == nil {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "title":
			cur.Title = v
		case "description":
			cur.Description = v
		case "ports":
			cur.Ports = strings.ReplaceAll(v, "|", ", ")
		}
	}
	return out
}

// Listener is one listening socket, from "ss -H -tulpn".
type Listener struct {
	Proto   string `json:"proto"` // tcp | udp
	Addr    string `json:"addr"`  // "0.0.0.0", "::", "127.0.0.1", …
	Port    int    `json:"port"`
	Process string `json:"process,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

// Loopback reports whether only this machine itself can reach the socket.
func (l Listener) Loopback() bool {
	a, err := netip.ParseAddr(l.Addr)
	return err == nil && a.IsLoopback()
}

var ssProcRe = regexp.MustCompile(`\(\("([^"]+)",pid=(\d+)`)

// ParseListeners reads "ss -H -tulpn".
func ParseListeners(out string) []Listener {
	var ls []Listener
	seen := map[Listener]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || (f[0] != "tcp" && f[0] != "udp") {
			continue
		}
		local := f[4]
		i := strings.LastIndex(local, ":")
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(local[i+1:])
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		addr := strings.Trim(local[:i], "[]")
		if j := strings.Index(addr, "%"); j >= 0 {
			addr = addr[:j] // "127.0.0.53%lo"
		}
		if addr == "*" {
			addr = "::" // both families
		}
		l := Listener{Proto: f[0], Addr: addr, Port: port}
		if m := ssProcRe.FindStringSubmatch(line); m != nil {
			l.Process = m[1]
			l.PID, _ = strconv.Atoi(m[2])
		}
		key := l
		key.PID = 0
		if seen[key] {
			continue
		}
		seen[key] = true
		ls = append(ls, l)
	}
	return ls
}

// Blocked is one connection the firewall turned away, from the kernel log.
type Blocked struct {
	Time      time.Time `json:"time"`
	Action    string    `json:"action"` // block | limit | audit …
	Interface string    `json:"interface,omitempty"`
	Out       bool      `json:"out,omitempty"`
	Src       string    `json:"src"`
	Dst       string    `json:"dst"`
	Proto     string    `json:"proto"`
	SrcPort   int       `json:"src_port,omitempty"`
	DstPort   int       `json:"dst_port,omitempty"`
}

var ufwLogRe = regexp.MustCompile(`\[UFW ([A-Z ]+)\]\s+(.*)$`)

// ParseBlocked reads kernel log lines ("… [UFW BLOCK] IN=eth0 OUT= SRC=…")
// from the journal (ISO timestamps) or a syslog file. Only lines about
// refused traffic are kept; now gives the year for syslog's short dates.
func ParseBlocked(out string, now time.Time) []Blocked {
	var list []Blocked
	for _, line := range strings.Split(out, "\n") {
		m := ufwLogRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(m[1]))
		if action == "allow" || strings.HasPrefix(action, "audit") {
			continue
		}
		b := Blocked{Action: action, Time: logTime(line, now)}
		for _, kv := range strings.Fields(m[2]) {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			switch k {
			case "IN":
				if v != "" {
					b.Interface = v
				}
			case "OUT":
				if v != "" && b.Interface == "" {
					b.Interface, b.Out = v, true
				}
			case "SRC":
				b.Src = v
			case "DST":
				b.Dst = v
			case "PROTO":
				b.Proto = strings.ToLower(v)
			case "SPT":
				b.SrcPort, _ = strconv.Atoi(v)
			case "DPT":
				b.DstPort, _ = strconv.Atoi(v)
			}
		}
		if b.Src == "" || b.Dst == "" {
			continue
		}
		list = append(list, b)
	}
	return list
}

// logTime reads the timestamp a log line starts with.
func logTime(line string, now time.Time) time.Time {
	first, _, _ := strings.Cut(line, " ")
	for _, layout := range []string{"2006-01-02T15:04:05-0700", "2006-01-02T15:04:05-07:00", time.RFC3339Nano, "2006-01-02T15:04:05.000000-07:00"} {
		if t, err := time.Parse(layout, first); err == nil {
			return t
		}
	}
	if len(line) >= 15 {
		if t, err := time.ParseInLocation("Jan _2 15:04:05", line[:15], now.Location()); err == nil {
			t = t.AddDate(now.Year(), 0, 0)
			if t.After(now.Add(24 * time.Hour)) {
				t = t.AddDate(-1, 0, 0) // December's lines read in January
			}
			return t
		}
	}
	return time.Time{}
}
