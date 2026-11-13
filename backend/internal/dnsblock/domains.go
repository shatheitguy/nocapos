// Package dnsblock is NoCapOS's own DNS server with ad and tracker
// blocking: it answers devices' DNS questions, says "nothing here" for
// domains on the block lists, and asks an upstream resolver (plain DNS,
// DNS-over-TLS or DNS-over-HTTPS) for everything else, with a cache. The
// query log stays on this machine, can be turned off, and is pruned.
package dnsblock

import (
	"bufio"
	"io"
	"net/netip"
	"sort"
	"strings"
)

// MaxDomains caps how many domains all block lists together may hold.
const MaxDomains = 3_000_000

// NormalizeDomain lower-cases d, drops a trailing dot and a leading "*.",
// and reports whether what's left is a valid domain name with at least two
// labels.
func NormalizeDomain(d string) (string, bool) {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimSuffix(d, ".")
	d = strings.TrimPrefix(d, "*.")
	if len(d) == 0 || len(d) > 253 || !strings.Contains(d, ".") {
		return "", false
	}
	for _, label := range strings.Split(d, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return "", false
			}
		}
	}
	if _, err := netip.ParseAddr(d); err == nil {
		return "", false // an address, not a name
	}
	return d, true
}

// hostsSkip are names hosts files list for the machine itself.
var hostsSkip = map[string]bool{
	"localhost": true, "localhost.localdomain": true, "local": true, "broadcasthost": true,
	"ip6-localhost": true, "ip6-loopback": true, "ip6-localnet": true, "ip6-mcastprefix": true,
	"ip6-allnodes": true, "ip6-allrouters": true, "ip6-allhosts": true, "0.0.0.0": true,
}

// ParseList reads a block list in hosts format ("0.0.0.0 ads.example"),
// as plain domains (one per line, "*.example" allowed) or as "||domain^"
// lines, calling add for each domain. Comments (#, !) and anything it
// can't read are skipped. It stops after limit domains.
func ParseList(r io.Reader, limit int, add func(string)) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n := 0
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexAny(line, "#!"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var cands []string
		switch {
		case strings.HasPrefix(line, "||"):
			d := strings.TrimPrefix(line, "||")
			end := strings.IndexAny(d, "^/$|")
			if end < 0 || d[end] != '^' || end != len(d)-1 {
				continue // a path or options: not a whole domain
			}
			cands = []string{d[:end]}
		default:
			f := strings.Fields(line)
			if _, err := netip.ParseAddr(f[0]); err == nil {
				cands = f[1:] // hosts format
			} else if len(f) == 1 {
				cands = f
			}
		}
		for _, c := range cands {
			if hostsSkip[strings.ToLower(c)] {
				continue
			}
			if d, ok := NormalizeDomain(c); ok {
				add(d)
				n++
				if n >= limit {
					return n, nil
				}
			}
		}
	}
	return n, sc.Err()
}

// DomainSet is a read-only set of domains that also matches their
// subdomains. All names live in one string with sorted offsets into it,
// which takes a fraction of the memory a map would on a small board.
type DomainSet struct {
	blob string
	offs []uint32 // start of each name in blob; names end at the next offset - 1
}

// NewDomainSet builds a set from names (already normalized).
func NewDomainSet(names []string) *DomainSet {
	sort.Strings(names)
	var b strings.Builder
	offs := make([]uint32, 0, len(names))
	prev := ""
	for i, n := range names {
		if i > 0 && n == prev {
			continue
		}
		prev = n
		offs = append(offs, uint32(b.Len()))
		b.WriteString(n)
		b.WriteByte('\n')
	}
	return &DomainSet{blob: b.String(), offs: offs}
}

// Len is how many domains the set holds.
func (s *DomainSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.offs)
}

func (s *DomainSet) at(i int) string {
	start := s.offs[i]
	end := uint32(len(s.blob))
	if i+1 < len(s.offs) {
		end = s.offs[i+1]
	}
	return s.blob[start : end-1]
}

func (s *DomainSet) has(d string) bool {
	i := sort.Search(len(s.offs), func(i int) bool { return s.at(i) >= d })
	return i < len(s.offs) && s.at(i) == d
}

// Match reports whether name or one of its parent domains is in the set,
// and which entry matched.
func (s *DomainSet) Match(name string) (string, bool) {
	if s == nil || len(s.offs) == 0 {
		return "", false
	}
	for d := name; ; {
		if s.has(d) {
			return d, true
		}
		i := strings.IndexByte(d, '.')
		if i < 0 {
			return "", false
		}
		d = d[i+1:]
	}
}
