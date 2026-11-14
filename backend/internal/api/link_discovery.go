package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/beacon"
)

// Finding NoCap servers. Every server announces itself on the home network
// (mDNS, _nocap._tcp) with its NoCap ID, scheme, version and whether it's
// still waiting for its first setup (setup=new|done); no secrets. It can
// also advertise over Bluetooth LE for phones nearby (internal/beacon). The
// phone app lists both; NoCap OS lists the other servers it finds on mDNS.
// Pairing and setup always go over HTTPS on the home network.

// discoveryState wakes the announcers when what they say changes (the first
// administrator was created, the name changed, Bluetooth was switched).
type discoveryState struct {
	mu      sync.Mutex
	changed chan struct{}
	ble     bleStatus
}

type bleStatus struct {
	Advertising bool   `json:"advertising"`
	Error       string `json:"error,omitempty"`
}

// wait returns a channel closed at the next change.
func (d *discoveryState) wait() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.changed == nil {
		d.changed = make(chan struct{})
	}
	return d.changed
}

// bump tells every announcer to start over with fresh details.
func (d *discoveryState) bump() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.changed != nil {
		close(d.changed)
	}
	d.changed = make(chan struct{})
}

func (d *discoveryState) setBLE(st bleStatus) {
	d.mu.Lock()
	d.ble = st
	d.mu.Unlock()
}

func (d *discoveryState) bleNow() bleStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ble
}

// setupNew reports whether this server still waits for its first administrator.
func (s *Server) setupNew(ctx context.Context) bool {
	if s.Auth == nil {
		return false
	}
	req, err := s.Auth.SetupRequired(ctx)
	return err == nil && req
}

func setupWord(isNew bool) string {
	if isNew {
		return "new"
	}
	return "done"
}

// linkTXT is the mDNS TXT record: who this is and how to reach it. The
// phone still checks the NoCap ID and the certificate before trusting it.
func linkTXT(id, scheme, version string, setupNew bool) []string {
	out := []string{"nocap_id=" + id, "scheme=" + scheme, "setup=" + setupWord(setupNew)}
	if v := strings.TrimSpace(version); v != "" && len(v) <= 32 {
		out = append(out, "version="+v)
	}
	return out
}

// ---------- Bluetooth ----------

// settingLinkBLE: "on", "off", or "" (the default: on only while the server
// still waits for its first setup, so a new server is easy to find).
const settingLinkBLE = "link.ble"

func bleWanted(mode string, setupNew bool) bool {
	switch mode {
	case "on":
		return true
	case "off":
		return false
	}
	return setupNew
}

// bleRetry: how long to wait before trying the adapter again.
const bleRetry = time.Minute

// advertiseBLE keeps the Bluetooth advertisement in step with the setting
// and the setup state, while there's an adapter.
func (s *Server) advertiseBLE(ctx context.Context) {
	if !beacon.Supported || inContainer() {
		return
	}
	for ctx.Err() == nil {
		changed := s.discovery.wait()
		mode, _ := s.Store.Setting(ctx, settingLinkBLE)
		isNew := s.setupNew(ctx)
		if !bleWanted(mode, isNew) {
			s.discovery.setBLE(bleStatus{})
			select {
			case <-ctx.Done():
				return
			case <-changed:
			}
			continue
		}
		run, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- beacon.Run(run, beacon.Ad{Name: s.serverName(), SetupNew: isNew}) }()
		s.discovery.setBLE(bleStatus{Advertising: true})
		select {
		case <-ctx.Done():
			stop()
			<-done
			return
		case <-changed:
			stop()
			<-done
		case err := <-done:
			stop()
			st := bleStatus{}
			if err != nil && !errors.Is(err, beacon.ErrUnavailable) {
				st.Error = err.Error()
				s.Log.Warn("bluetooth advertisement stopped", "err", err)
			}
			s.discovery.setBLE(st)
			select {
			case <-ctx.Done():
				return
			case <-changed:
			case <-time.After(bleRetry):
			}
		}
	}
}

type bluetoothJSON struct {
	Mode        string `json:"mode"`    // on | off | auto
	Enabled     bool   `json:"enabled"` // what the switch shows
	Supported   bool   `json:"supported"`
	Adapter     bool   `json:"adapter"`
	Advertising bool   `json:"advertising"`
	SetupNew    bool   `json:"setup_new"`
	Error       string `json:"error,omitempty"`
}

func (s *Server) bluetoothState(ctx context.Context) (bluetoothJSON, error) {
	mode, err := s.Store.Setting(ctx, settingLinkBLE)
	if err != nil {
		return bluetoothJSON{}, err
	}
	isNew := s.setupNew(ctx)
	st := s.discovery.bleNow()
	out := bluetoothJSON{
		Mode: mode, Enabled: bleWanted(mode, isNew), Supported: beacon.Supported,
		Advertising: st.Advertising, SetupNew: isNew, Error: st.Error,
	}
	if out.Mode == "" {
		out.Mode = "auto"
	}
	if beacon.Supported {
		out.Adapter = beacon.Available(ctx)
	}
	return out, nil
}

func (s *Server) linkBluetoothGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.bluetoothState(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) linkBluetoothPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	mode := "off"
	if req.Enabled {
		mode = "on"
	}
	if err := s.Store.SetSetting(r.Context(), settingLinkBLE, mode); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "link.bluetooth", mode, true, "")
	s.discovery.bump()
	// Give the advertiser a moment to (re)start before answering.
	time.Sleep(300 * time.Millisecond)
	s.linkBluetoothGet(w, r)
}

// ---------- other NoCap servers on the network ----------

type foundNoCap struct {
	NocapID   string   `json:"nocap_id"`
	Name      string   `json:"name"`
	Hostname  string   `json:"hostname"`
	Addresses []string `json:"addresses"`
	URL       string   `json:"url"`
	Version   string   `json:"version"`
	Setup     string   `json:"setup"` // new | done | "" (an older server)
}

// browseTimeout bounds one look around the network.
const browseTimeout = 6 * time.Second

func (s *Server) linkDiscover(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), browseTimeout)
	defer cancel()
	text, ok := browseNoCap(ctx)
	self, _ := s.nocapID()
	servers := parseNoCapBrowse(text, self)
	writeJSON(w, http.StatusOK, map[string]any{"available": ok, "servers": servers})
}

// parseNoCapBrowse reads `avahi-browse -rtpk _nocap._tcp`: resolved lines
// are "=;iface;proto;name;type;domain;host;address;port;txt". One entry per
// NoCap ID (a server is seen once per interface and protocol), leaving out
// selfID.
func parseNoCapBrowse(text, selfID string) []foundNoCap {
	byID := map[string]*foundNoCap{}
	var order []string
	ports := map[string]string{}
	schemes := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\r"), ";", 10)
		if len(f) < 10 || f[0] != "=" || f[4] != linkService {
			continue
		}
		txt := parseTXT(f[9])
		id := txt["nocap_id"]
		if !validNocapID(id) || strings.EqualFold(id, selfID) {
			continue
		}
		addr := strings.TrimSpace(f[7])
		ip := net.ParseIP(strings.SplitN(addr, "%", 2)[0])
		port, err := strconv.Atoi(f[8])
		if ip == nil || err != nil || port < 1 || port > 65535 {
			continue
		}
		e := byID[id]
		if e == nil {
			name := strings.TrimPrefix(avahiText(f[3]), "NoCap ")
			e = &foundNoCap{NocapID: id, Name: clip(name, 64), Hostname: clip(f[6], 255), Addresses: []string{}}
			e.Version = clip(printable(txt["version"]), 32)
			switch txt["setup"] {
			case "new", "done":
				e.Setup = txt["setup"]
			}
			byID[id] = e
			order = append(order, id)
		}
		// Link-local IPv6 needs an interface to be reachable: not a link.
		if ip.To4() == nil && ip.IsLinkLocalUnicast() {
			continue
		}
		a := ip.String()
		if ip.To4() == nil {
			a = "[" + a + "]"
		}
		dup := false
		for _, x := range e.Addresses {
			dup = dup || x == a
		}
		if !dup {
			e.Addresses = append(e.Addresses, a)
		}
		ports[id] = strconv.Itoa(port)
		if sc := txt["scheme"]; sc == "http" || sc == "https" {
			schemes[id] = sc
		}
	}
	out := make([]foundNoCap, 0, len(order))
	for _, id := range order {
		e := byID[id]
		// IPv4 first: it's what people recognise and every browser reaches.
		sort.SliceStable(e.Addresses, func(i, j int) bool {
			return !strings.HasPrefix(e.Addresses[i], "[") && strings.HasPrefix(e.Addresses[j], "[")
		})
		if len(e.Addresses) > 0 {
			scheme := schemes[id]
			if scheme == "" {
				scheme = "https"
			}
			e.URL = scheme + "://" + e.Addresses[0] + ":" + ports[id]
		}
		out = append(out, *e)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// validNocapID: "NC-" and a short run of letters, digits and dashes.
func validNocapID(id string) bool {
	if !strings.HasPrefix(id, "NC-") || len(id) > 40 {
		return false
	}
	for _, r := range id[3:] {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return len(id) > 3
}

// parseTXT reads avahi's quoted TXT strings: "k=v" "k2=v2" (with \" and \\
// escapes inside).
func parseTXT(s string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(s); i++ {
		if s[i] != '"' {
			continue
		}
		j := i + 1
		for ; j < len(s) && s[j] != '"'; j++ {
			if s[j] == '\\' {
				j++ // an escaped character, maybe a quote
			}
		}
		if j > len(s) {
			j = len(s)
		}
		if k, v, ok := strings.Cut(avahiText(s[i+1:j]), "="); ok {
			if _, seen := out[k]; !seen {
				out[k] = v
			}
		}
		i = j
	}
	return out
}

// avahiText decodes avahi's escapes: \DDD (decimal, e.g. "NoCap\032box")
// and a backslash before any other character (\" \\ \.).
func avahiText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		if i+4 <= len(s) {
			if n, err := strconv.Atoi(s[i+1 : i+4]); err == nil && n >= 0 && n < 256 && isDigits(s[i+1:i+4]) {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		i++
		b.WriteByte(s[i])
	}
	return b.String()
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func clip(s string, n int) string {
	s = printable(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
