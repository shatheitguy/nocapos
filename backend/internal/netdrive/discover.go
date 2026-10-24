package netdrive

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Finding SMB servers on the local network for "Connect to Server": mDNS
// (_smb._tcp through avahi-browse), NetBIOS (nmblookup), then each server's
// shares (smbclient -L, as a guest). Everything is bounded: a scan answers
// within discoverBudget whatever the network does, and missing tools only
// mean fewer results plus a hint.

const (
	discoverBudget = 5 * time.Second
	sharesBudget   = 5 * time.Second
	maxServers     = 64
)

// FoundServer is a file server seen on the network.
type FoundServer struct {
	Name      string   `json:"name"`             // NetBIOS or mDNS name ("" if unknown)
	Host      string   `json:"host"`             // IP address to connect to
	Hostname  string   `json:"hostname"`         // mDNS host name, e.g. nas.local
	Shares    []string `json:"shares,omitempty"` // when it lists them to guests
	Browsable bool     `json:"browsable"`        // its share list was readable
	Sources   []string `json:"sources"`          // mdns | netbios
}

// DiscoverTools says which helpers are installed.
type DiscoverTools struct {
	Avahi     bool `json:"avahi"`     // avahi-browse
	NMBLookup bool `json:"nmblookup"` // nmblookup
	SMBClient bool `json:"smbclient"` // smbclient
}

type Discovery struct {
	Servers []FoundServer `json:"servers"`
	Tools   DiscoverTools `json:"tools"`
	Hint    string        `json:"hint,omitempty"`
	TookMS  int64         `json:"took_ms"`
}

func (m *Manager) bin(name string) bool {
	_, err := m.lookPath(name)
	return err == nil
}

// Discover scans the local network for SMB servers.
func (m *Manager) Discover(ctx context.Context) Discovery {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, discoverBudget)
	defer cancel()
	out := Discovery{Servers: []FoundServer{}, Tools: DiscoverTools{Avahi: m.bin("avahi-browse"), NMBLookup: m.bin("nmblookup"), SMBClient: m.bin("smbclient")}}
	switch {
	case !out.Tools.Avahi && !out.Tools.NMBLookup:
		out.Hint = "Install Samba client tools to find servers on your network. You can still type a server's name or address."
	case !out.Tools.SMBClient:
		out.Hint = "Install Samba client tools to see each server's shared folders."
	}

	// Names first, in parallel, leaving time for share lists.
	findCtx, findCancel := context.WithTimeout(ctx, discoverBudget*3/5)
	var mu sync.Mutex
	byHost := map[string]*FoundServer{}
	add := func(s FoundServer, source string) {
		mu.Lock()
		defer mu.Unlock()
		cur := byHost[s.Host]
		if cur == nil {
			if len(byHost) >= maxServers {
				return
			}
			cur = &FoundServer{Host: s.Host}
			byHost[s.Host] = cur
		}
		if cur.Name == "" || source == "netbios" {
			if s.Name != "" {
				cur.Name = s.Name
			}
		}
		if s.Hostname != "" {
			cur.Hostname = s.Hostname
		}
		for _, have := range cur.Sources {
			if have == source {
				return
			}
		}
		cur.Sources = append(cur.Sources, source)
	}
	var wg sync.WaitGroup
	if out.Tools.Avahi {
		wg.Add(1)
		go func() {
			defer wg.Done()
			text, _ := m.run(findCtx, "", "avahi-browse", "-rtpk", "_smb._tcp")
			for _, s := range parseAvahi(text) {
				add(s, "mdns")
			}
		}()
	}
	if out.Tools.NMBLookup {
		wg.Add(1)
		go func() {
			defer wg.Done()
			text, _ := m.run(findCtx, "", "nmblookup", "*")
			ips := parseNmbWildcard(text)
			var inner sync.WaitGroup
			sem := make(chan struct{}, 16)
			for _, ip := range ips {
				inner.Add(1)
				go func(ip string) {
					defer inner.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					status, _ := m.run(findCtx, "", "nmblookup", "-A", ip)
					add(FoundServer{Host: ip, Name: parseNmbStatus(status)}, "netbios")
				}(ip)
			}
			inner.Wait()
		}()
	}
	wg.Wait()
	findCancel()

	list := make([]*FoundServer, 0, len(byHost))
	for _, s := range byHost {
		list = append(list, s)
	}
	if out.Tools.SMBClient {
		var lw sync.WaitGroup
		sem := make(chan struct{}, 16)
		for _, s := range list {
			lw.Add(1)
			go func(s *FoundServer) {
				defer lw.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if shares, err := m.listShares(ctx, s.Host); err == nil {
					s.Shares, s.Browsable = shares, true
				}
			}(s)
		}
		lw.Wait()
	}
	for _, s := range list {
		out.Servers = append(out.Servers, *s)
	}
	sortServers(out.Servers)
	out.TookMS = time.Since(start).Milliseconds()
	return out
}

func sortServers(list []FoundServer) {
	sort.Slice(list, func(i, j int) bool {
		a, b := strings.ToLower(firstNonEmpty(list[i].Name, list[i].Hostname)), strings.ToLower(firstNonEmpty(list[j].Name, list[j].Hostname))
		if (a == "") != (b == "") {
			return a != ""
		}
		if a != b {
			return a < b
		}
		return list[i].Host < list[j].Host
	})
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// ErrNoSMBClient means smbclient isn't installed.
var ErrNoSMBClient = errors.New("install Samba client tools to list a server's shared folders")

// Shares lists one server's shared folders as a guest (≤ sharesBudget).
func (m *Manager) Shares(ctx context.Context, host string) ([]string, error) {
	host = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(host), `\\`), "smb://")
	if !hostRe.MatchString(host) {
		return nil, errors.New("enter the server's name or IP address")
	}
	if !m.bin("smbclient") {
		return nil, ErrNoSMBClient
	}
	ctx, cancel := context.WithTimeout(ctx, sharesBudget)
	defer cancel()
	return m.listShares(ctx, host)
}

func (m *Manager) listShares(ctx context.Context, host string) ([]string, error) {
	text, err := m.run(ctx, "", "smbclient", "-L", "//"+host, "-N", "-g")
	shares := parseSmbclientList(text)
	if err != nil && len(shares) == 0 {
		low := strings.ToLower(text)
		switch {
		case strings.Contains(low, "access_denied") || strings.Contains(low, "logon_failure") || strings.Contains(low, "account_disabled"):
			return nil, errors.New("this server only lists its shares after signing in; type the share's name")
		case ctx.Err() != nil || strings.Contains(low, "timed out") || strings.Contains(low, "unreachable") || strings.Contains(low, "refused"):
			return nil, errors.New("the server didn't answer")
		}
		return nil, errors.New("couldn't list the shares: " + lastLine(text))
	}
	return shares, nil
}

// parseAvahi reads `avahi-browse -rtpk _smb._tcp`: resolved lines are
// "=;iface;IPv4;Name;_smb._tcp;local;host.local;192.168.1.20;445;txt".
func parseAvahi(text string) []FoundServer {
	var out []FoundServer
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Split(strings.TrimSpace(line), ";")
		if len(f) < 9 || f[0] != "=" || f[2] != "IPv4" {
			continue
		}
		addr, err := netip.ParseAddr(f[7])
		if err != nil || !addr.Is4() || addr.IsLoopback() || seen[f[7]] {
			continue
		}
		seen[f[7]] = true
		out = append(out, FoundServer{Name: avahiUnescape(f[3]), Hostname: f[6], Host: f[7]})
	}
	return out
}

// avahiUnescape decodes avahi's \DDD (decimal) escapes, e.g. "My\032NAS".
func avahiUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+4 <= len(s) && digits(s[i+1:i+4]) {
			if n, _ := strconv.Atoi(s[i+1 : i+4]); n < 256 {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		if i+1 < len(s) { // \; or \\ : the character itself
			b.WriteByte(s[i+1])
			i++
		}
	}
	return b.String()
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// parseNmbWildcard reads `nmblookup '*'`: "192.168.1.20 *<00>" per answer.
func parseNmbWildcard(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.HasPrefix(f[1], "*<") {
			continue
		}
		addr, err := netip.ParseAddr(f[0])
		if err != nil || !addr.Is4() || addr.IsLoopback() || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		out = append(out, f[0])
		if len(out) >= maxServers {
			break
		}
	}
	return out
}

// parseNmbStatus picks the machine's name from `nmblookup -A <ip>`: the
// file server (<20>) entry, else its unique workstation (<00>) name.
func parseNmbStatus(text string) string {
	var ws string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "<GROUP>") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[1] {
		case "<20>":
			return f[0]
		case "<00>":
			if ws == "" {
				ws = f[0]
			}
		}
	}
	return ws
}

// parseSmbclientList reads `smbclient -L //host -N -g` ("Disk|Name|Comment").
// Hidden ($) and printer shares are left out.
func parseSmbclientList(text string) []string {
	out := []string{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(f) < 2 || f[0] != "Disk" || f[1] == "" || strings.HasSuffix(f[1], "$") {
			continue
		}
		out = append(out, f[1])
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}
