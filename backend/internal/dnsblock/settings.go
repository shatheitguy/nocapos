package dnsblock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// List is one block list NoCapOS downloads.
type List struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// Record is a local DNS record: a name on your network NoCapOS answers
// itself (A, AAAA or CNAME).
type Record struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Settings is everything the DNS blocker can be told.
type Settings struct {
	Enabled bool `json:"enabled"`
	// Listen: auto (every address, or only this machine's own addresses
	// when another program holds port 53 on loopback), all, lan.
	Listen string `json:"listen"`
	Port   int    `json:"port"`
	// Upstreams are tried in order: "9.9.9.9", "tcp://1.1.1.1",
	// "tls://1.1.1.1", "https://1.1.1.1/dns-query".
	Upstreams []string `json:"upstreams"`
	// Bootstrap is the plain DNS server that finds upstreams given by name.
	Bootstrap string `json:"bootstrap"`
	// BlockMode: null (answer 0.0.0.0 / ::) or nxdomain (say it doesn't exist).
	BlockMode string `json:"block_mode"`
	CacheSize int    `json:"cache_size"`
	// CNAMEs: also block answers that lead to a blocked name.
	CheckCNAMEs  bool     `json:"check_cnames"`
	Lists        []List   `json:"lists"`
	RefreshHours int      `json:"refresh_hours"` // 0 = only when asked
	Allow        []string `json:"allow"`
	Block        []string `json:"block"`
	Records      []Record `json:"records"`
	LogEnabled   bool     `json:"log_enabled"`
	LogHours     int      `json:"log_hours"`     // how long the query log keeps entries
	LogClients   bool     `json:"log_clients"`   // false: the log doesn't say which device asked
	UseOnServer  bool     `json:"use_on_server"` // this machine asks the blocker too
}

// DefaultLists are offered on a new install; only the first is on.
var DefaultLists = []List{
	{ID: "unified", Name: "Ads and trackers (unified hosts)", URL: "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts", Enabled: true},
	{ID: "light", Name: "Ads, trackers and scams (light)", URL: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/domains/light.txt"},
	{ID: "malware", Name: "Malware sites", URL: "https://urlhaus.abuse.ch/downloads/hostfile/"},
}

// FallbackUpstreams are used when this machine has no resolvers of its own.
var FallbackUpstreams = []string{"9.9.9.9", "1.1.1.1"}

// Defaults returns the settings of a fresh install: off.
func Defaults() Settings {
	return Settings{
		Listen: "auto", Port: 53, Bootstrap: "9.9.9.9", BlockMode: "null", CacheSize: 10000,
		CheckCNAMEs: true, Lists: append([]List(nil), DefaultLists...), RefreshHours: 24,
		Allow: []string{}, Block: []string{}, Records: []Record{},
		LogEnabled: true, LogHours: 24, LogClients: true,
	}
}

// Limits on what Settings may hold.
const (
	maxLists    = 30
	maxEntries  = 5000 // allow / block entries each
	maxRecords  = 1000
	maxUpstream = 8
)

var errBad = errors.New("invalid")

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errBad, fmt.Sprintf(format, a...))
}

// IsInvalid reports whether err is a validation error (shown to the user).
func IsInvalid(err error) bool { return errors.Is(err, errBad) }

// Validate checks every field and returns s cleaned up.
func (s Settings) Validate() (Settings, error) {
	switch s.Listen {
	case "":
		s.Listen = "auto"
	case "auto", "all", "lan":
	default:
		return s, bad("listen must be auto, all or lan")
	}
	if s.Port == 0 {
		s.Port = 53
	}
	if s.Port < 1 || s.Port > 65535 {
		return s, bad("port must be 1 to 65535")
	}
	if len(s.Upstreams) > maxUpstream {
		return s, bad("at most %d upstream resolvers", maxUpstream)
	}
	ups := []string{}
	for _, u := range s.Upstreams {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if _, err := ParseUpstream(u, ""); err != nil {
			return s, err
		}
		ups = append(ups, u)
	}
	s.Upstreams = ups
	if s.Bootstrap == "" {
		s.Bootstrap = "9.9.9.9"
	}
	if a, err := netip.ParseAddr(s.Bootstrap); err != nil || a.IsLoopback() || a.IsUnspecified() {
		return s, bad("the bootstrap resolver must be an IP address of another machine")
	}
	switch s.BlockMode {
	case "":
		s.BlockMode = "null"
	case "null", "nxdomain":
	default:
		return s, bad("block mode must be null or nxdomain")
	}
	if s.CacheSize < 0 || s.CacheSize > 200000 {
		return s, bad("cache size must be 0 to 200000")
	}
	if len(s.Lists) > maxLists {
		return s, bad("at most %d block lists", maxLists)
	}
	seen := map[string]bool{}
	for i, l := range s.Lists {
		l.Name = strings.TrimSpace(l.Name)
		l.URL = strings.TrimSpace(l.URL)
		if l.ID == "" {
			l.ID = newID()
		}
		if !validID(l.ID) || seen[l.ID] {
			return s, bad("bad list id")
		}
		seen[l.ID] = true
		if l.Name == "" || len(l.Name) > 80 || strings.ContainsAny(l.Name, "\r\n\t") {
			return s, bad("each list needs a name (80 characters at most)")
		}
		if err := checkListURL(l.URL); err != nil {
			return s, err
		}
		s.Lists[i] = l
	}
	switch s.RefreshHours {
	case 0, 6, 12, 24, 72, 168:
	default:
		return s, bad("refresh every 6, 12, 24, 72 or 168 hours, or 0 for never")
	}
	var err error
	if s.Allow, err = cleanDomains(s.Allow, "allow list"); err != nil {
		return s, err
	}
	if s.Block, err = cleanDomains(s.Block, "block list"); err != nil {
		return s, err
	}
	if len(s.Records) > maxRecords {
		return s, bad("at most %d local records", maxRecords)
	}
	recs := make([]Record, 0, len(s.Records))
	for _, r := range s.Records {
		r, err := r.validate()
		if err != nil {
			return s, err
		}
		recs = append(recs, r)
	}
	s.Records = recs
	switch s.LogHours {
	case 0:
		s.LogHours = 24
	case 1, 24, 168, 720:
	default:
		return s, bad("keep the query log 1, 24, 168 or 720 hours")
	}
	return s, nil
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 32 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "c" + hex.EncodeToString(b)
}

func checkListURL(raw string) error {
	if len(raw) > 2048 {
		return bad("that list address is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return bad("a list address starts with https:// (or http://)")
	}
	return nil
}

func cleanDomains(in []string, what string) ([]string, error) {
	if len(in) > maxEntries {
		return nil, bad("at most %d entries in the %s", maxEntries, what)
	}
	out := []string{}
	seen := map[string]bool{}
	for _, d := range in {
		if strings.TrimSpace(d) == "" {
			continue
		}
		n, ok := NormalizeDomain(d)
		if !ok {
			return nil, bad("%q isn't a domain name (%s)", d, what)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

func (r Record) validate() (Record, error) {
	name, ok := NormalizeDomain(r.Name)
	if !ok {
		// Local names may be a single label ("nas").
		n := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.Name), "."))
		if d, ok2 := NormalizeDomain(n + ".x"); ok2 && !strings.Contains(n, ".") {
			name, ok = strings.TrimSuffix(d, ".x"), true
		}
	}
	if !ok || strings.HasPrefix(strings.TrimSpace(r.Name), "*") {
		return r, bad("%q isn't a name a device can look up", r.Name)
	}
	r.Name = name
	r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
	r.Value = strings.TrimSpace(r.Value)
	switch r.Type {
	case "A", "AAAA":
		a, err := netip.ParseAddr(r.Value)
		if err != nil || a.Zone() != "" || (r.Type == "A") != a.Is4() {
			return r, bad("%s needs an IPv%s address", r.Type, map[bool]string{true: "4", false: "6"}[r.Type == "A"])
		}
		r.Value = a.String()
	case "CNAME":
		v, ok := NormalizeDomain(r.Value)
		if !ok {
			return r, bad("a CNAME points at another domain name")
		}
		if v == r.Name {
			return r, bad("a CNAME can't point at itself")
		}
		r.Value = v
	default:
		return r, bad("record type must be A, AAAA or CNAME")
	}
	return r, nil
}

// loadSettings reads the saved settings (defaults when there are none).
func loadSettings(dir string) Settings {
	s := Defaults()
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return s
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Defaults()
	}
	if v, err := s.Validate(); err == nil {
		return v
	}
	return Defaults()
}

func saveSettings(dir string, s Settings) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "settings.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "settings.json"))
}
