package dnsblock

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ListStatus is how a block list's last download went.
type ListStatus struct {
	Domains int       `json:"domains"`
	Updated time.Time `json:"updated,omitzero"`
	Error   string    `json:"error,omitempty"`
}

// ListView is a list with its status.
type ListView struct {
	List
	ListStatus
}

// Status is what the DNS blocker pages show.
type Status struct {
	Settings     Settings   `json:"settings"`
	Running      bool       `json:"running"`
	Listening    []string   `json:"listening"`
	ListenError  string     `json:"listen_error,omitempty"`
	ListenNote   string     `json:"listen_note,omitempty"` // "lan": port 53 was busy on loopback
	PausedUntil  time.Time  `json:"paused_until,omitzero"`
	Lists        []ListView `json:"lists"`
	Domains      int        `json:"domains"` // on all lists together
	CacheEntries int        `json:"cache_entries"`
	Refreshing   bool       `json:"refreshing"`
	Port53       PortInfo   `json:"port53"`
	Addresses    []string   `json:"addresses"` // where devices can point their DNS
}

// PortInfo says who else wants port 53 on this machine.
type PortInfo struct {
	// Stub: the system's own local resolver holds port 53 on loopback.
	Stub bool `json:"stub"`
	// Freed: NoCapOS turned that resolver's port-53 listener off.
	Freed bool `json:"freed"`
	// CanManage: NoCapOS can free port 53 and point this machine at the blocker.
	CanManage bool `json:"can_manage"`
}

// Service is the DNS blocker inside alfad.
type Service struct {
	dir  string
	log  *slog.Logger
	res  *Resolver
	qlog *queryLog
	http *http.Client

	mu          sync.Mutex
	ctx         context.Context
	settings    Settings
	status      map[string]ListStatus
	srv         *listeners
	listenErr   string
	listenNote  string
	refreshing  bool
	sets        map[string]*DomainSet // loaded lists by id
	systemHooks bool                  // false in tests: never touch the host's resolver
}

// New returns the DNS blocker keeping its files under dir.
func New(dir string, log *slog.Logger) *Service {
	return &Service{
		dir: dir, log: log, res: NewResolver(0), status: map[string]ListStatus{}, sets: map[string]*DomainSet{},
		http:        &http.Client{Timeout: 3 * time.Minute},
		systemHooks: true,
	}
}

// Start loads the settings and, when the blocker is on, starts answering.
// It runs until ctx ends.
func (s *Service) Start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(s.dir, "lists"), 0o700); err != nil {
		return err
	}
	q, err := openQueryLog(s.dir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ctx = ctx
	s.qlog = q
	s.settings = loadSettings(s.dir)
	s.loadStatus()
	s.res.onQuery = q.add
	s.mu.Unlock()
	go q.run(ctx)
	s.mu.Lock()
	s.applyLocked(s.settings, true)
	s.mu.Unlock()
	go s.schedule(ctx)
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		s.stopLocked()
		s.mu.Unlock()
		q.db.Close()
	}()
	return nil
}

// Settings returns the current settings.
func (s *Service) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// Apply validates and saves new settings and puts them into effect.
func (s *Service) Apply(next Settings) (Status, error) {
	next, err := next.Validate()
	if err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	if s.qlog == nil {
		s.mu.Unlock()
		return Status{}, errors.New("the DNS blocker isn't running")
	}
	if !next.Enabled {
		next.UseOnServer = false
	}
	if next.Enabled && len(next.Upstreams) == 0 {
		next.Upstreams = defaultUpstreams()
	}
	if err := saveSettings(s.dir, next); err != nil {
		s.mu.Unlock()
		return Status{}, err
	}
	err = s.applyLocked(next, false)
	s.mu.Unlock()
	return s.Status(), err
}

func defaultUpstreams() []string {
	var out []string
	for _, a := range systemUpstreams() {
		if ip, err := netip.ParseAddr(a); err == nil && !ip.IsLoopback() && !ip.IsUnspecified() {
			out = append(out, ip.String())
		}
	}
	if len(out) == 0 {
		out = append(out, FallbackUpstreams...)
	}
	if len(out) > maxUpstream {
		out = out[:maxUpstream]
	}
	return out
}

// applyLocked puts settings into effect (s.mu held).
func (s *Service) applyLocked(next Settings, boot bool) error {
	prev := s.settings
	s.settings = next
	s.qlog.configure(next.LogEnabled, next.LogClients, next.LogHours)
	s.rebuildLocked()
	if !next.Enabled {
		s.stopLocked()
		if s.systemHooks && (prev.UseOnServer || boot) {
			_ = useOnServer(s.dir, false)
		}
		return nil
	}
	restart := boot || s.srv == nil || prev.Listen != next.Listen || prev.Port != next.Port
	if restart {
		s.stopLocked()
		srv, note, err := listen(next.Listen, next.Port)
		s.listenNote = note
		if err != nil {
			s.listenErr = err.Error()
			if s.systemHooks && next.UseOnServer {
				// Never leave this machine pointing at a blocker that isn't there.
				_ = useOnServer(s.dir, false)
			}
			return fmt.Errorf("couldn't listen on port %d: %w", next.Port, err)
		}
		s.listenErr = ""
		s.srv = srv
		srv.serve(s.ctx, s.res)
	}
	if s.systemHooks && (boot || prev.UseOnServer != next.UseOnServer) {
		if err := useOnServer(s.dir, next.UseOnServer); err != nil {
			return err
		}
	}
	if s.needsDownloadLocked() {
		go s.Refresh(false)
	}
	return nil
}

func (s *Service) stopLocked() {
	if s.srv != nil {
		s.srv.close()
		s.srv = nil
	}
}

// rebuildLocked loads the enabled lists from disk and swaps the rules in.
func (s *Service) rebuildLocked() {
	st := s.settings
	r := &rules{blockMode: st.BlockMode, checkCNAMEs: st.CheckCNAMEs, records: map[string][]Record{}}
	r.allow = NewDomainSet(append([]string(nil), st.Allow...))
	r.block = NewDomainSet(append([]string(nil), st.Block...))
	for _, rec := range st.Records {
		r.records[rec.Name] = append(r.records[rec.Name], rec)
	}
	budget := MaxDomains
	for _, l := range st.Lists {
		if !l.Enabled || !st.Enabled {
			continue // lists only take memory while the blocker is on
		}
		set, ok := s.sets[l.ID]
		if !ok {
			var loaded bool
			set, loaded = s.loadList(l.ID, budget)
			if loaded {
				s.sets[l.ID] = set
			}
		}
		budget -= set.Len()
		r.lists = append(r.lists, namedSet{name: l.Name, set: set})
	}
	// Forget lists that were switched off or removed (their memory too).
	for id := range s.sets {
		if !st.Enabled || !enabledList(st, id) {
			delete(s.sets, id)
		}
	}
	for _, u := range st.Upstreams {
		if up, err := ParseUpstream(u, st.Bootstrap); err == nil {
			r.ups = append(r.ups, up)
		}
	}
	s.res.setRules(r, st.CacheSize)
}

func enabledList(st Settings, id string) bool {
	for _, l := range st.Lists {
		if l.ID == id && l.Enabled {
			return true
		}
	}
	return false
}

func (s *Service) listPath(id string) string { return filepath.Join(s.dir, "lists", id+".txt") }

// loadList reads a downloaded list; loaded is false when there's no file yet.
func (s *Service) loadList(id string, limit int) (set *DomainSet, loaded bool) {
	f, err := os.Open(s.listPath(id))
	if err != nil {
		return NewDomainSet(nil), false
	}
	defer f.Close()
	var names []string
	_, _ = ParseList(f, max(limit, 0), func(d string) { names = append(names, d) })
	return NewDomainSet(names), true
}

func (s *Service) needsDownloadLocked() bool {
	for _, l := range s.settings.Lists {
		if l.Enabled {
			if _, err := os.Stat(s.listPath(l.ID)); err != nil && s.status[l.ID].Error == "" {
				return true
			}
		}
	}
	return false
}

// schedule refreshes the lists every RefreshHours while the blocker is on.
func (s *Service) schedule(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		st := s.settings
		due := false
		if st.Enabled && st.RefreshHours > 0 {
			for _, l := range st.Lists {
				if l.Enabled && time.Since(s.status[l.ID].Updated) > time.Duration(st.RefreshHours)*time.Hour {
					due = true
				}
			}
		}
		s.mu.Unlock()
		if due {
			s.Refresh(true)
		}
	}
}

// Refresh downloads the enabled lists (all, or only missing ones) and
// reloads them.
func (s *Service) Refresh(all bool) {
	s.mu.Lock()
	if s.refreshing || s.ctx == nil {
		s.mu.Unlock()
		return
	}
	s.refreshing = true
	ctx := s.ctx
	lists := append([]List(nil), s.settings.Lists...)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.refreshing = false
		s.mu.Unlock()
	}()
	for _, l := range lists {
		if !l.Enabled {
			continue
		}
		if _, err := os.Stat(s.listPath(l.ID)); err == nil && !all {
			continue
		}
		n, err := s.download(ctx, l)
		s.mu.Lock()
		ls := s.status[l.ID]
		if err != nil {
			ls.Error = err.Error()
			s.log.Warn("dns block list download failed", "list", l.Name, "err", err)
		} else {
			ls = ListStatus{Domains: n, Updated: time.Now()}
			delete(s.sets, l.ID) // reload from the new file
		}
		s.status[l.ID] = ls
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.saveStatus()
	s.rebuildLocked()
	s.mu.Unlock()
}

const maxListBytes = 64 << 20

// download fetches one list to disk and returns how many domains it has.
func (s *Service) download(ctx context.Context, l List) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.URL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "NoCapOS")
	res, err := s.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("the list's server answered %s", res.Status)
	}
	tmp := s.listPath(l.ID) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	w := bufio.NewWriter(f)
	n, perr := ParseList(io.LimitReader(res.Body, maxListBytes), MaxDomains, func(d string) {
		w.WriteString(d)
		w.WriteByte('\n')
	})
	if err := w.Flush(); err != nil && perr == nil {
		perr = err
	}
	f.Close()
	if perr != nil {
		os.Remove(tmp)
		return 0, perr
	}
	if n == 0 {
		os.Remove(tmp)
		return 0, errors.New("no domains found in that list")
	}
	return n, os.Rename(tmp, s.listPath(l.ID))
}

func (s *Service) statusPath() string { return filepath.Join(s.dir, "lists", "status.json") }

func (s *Service) loadStatus() {
	b, err := os.ReadFile(s.statusPath())
	if err == nil {
		_ = jsonUnmarshal(b, &s.status)
	}
	if s.status == nil {
		s.status = map[string]ListStatus{}
	}
}

func (s *Service) saveStatus() {
	if b, err := jsonMarshal(s.status); err == nil {
		_ = os.WriteFile(s.statusPath(), b, 0o600)
	}
}

// Status reports the blocker's state.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Settings: s.settings, ListenError: s.listenErr, ListenNote: s.listenNote, Refreshing: s.refreshing, Listening: []string{}, Lists: []ListView{}}
	if s.srv != nil {
		st.Running = true
		st.Listening = s.srv.addrs
	}
	st.PausedUntil = s.res.PausedUntil()
	for _, l := range s.settings.Lists {
		v := ListView{List: l, ListStatus: s.status[l.ID]}
		if set, ok := s.sets[l.ID]; ok {
			v.Domains = set.Len()
			st.Domains += set.Len()
		}
		st.Lists = append(st.Lists, v)
	}
	st.CacheEntries = s.res.cache.Len()
	if s.systemHooks {
		st.Port53 = systemPort53(s.dir)
	}
	st.Addresses = lanAddresses()
	return st
}

// Pause stops blocking for d (0 resumes it).
func (s *Service) Pause(d time.Duration) {
	if d <= 0 {
		s.res.Pause(time.Time{})
		return
	}
	s.res.Pause(time.Now().Add(d))
}

// Stats is the dashboard.
type Stats struct {
	LogEnabled  bool         `json:"log_enabled"`
	Today       int          `json:"today"`
	Blocked     int          `json:"blocked_today"`
	TopBlocked  []Count      `json:"top_blocked"`
	TopAllowed  []Count      `json:"top_allowed"`
	TopClients  []Count      `json:"top_clients"`
	LastHour    []Point      `json:"last_hour"` // per minute
	LastDay     []Point      `json:"last_day"`  // per hour
	Clients     []ClientStat `json:"clients"`
	PausedUntil time.Time    `json:"paused_until,omitzero"`
}

// Stats builds the dashboard. Top lists need the query log.
func (s *Service) Stats(ctx context.Context) Stats {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	out := Stats{TopBlocked: []Count{}, TopAllowed: []Count{}, TopClients: []Count{}, Clients: []ClientStat{}}
	out.LastHour = s.res.stats.series(now, 60, 1)
	out.LastDay = s.res.stats.series(now, 24, 60)
	out.PausedUntil = s.res.PausedUntil()
	out.Today, out.Blocked = s.res.stats.since(midnight, now)
	s.mu.Lock()
	q := s.qlog
	s.mu.Unlock()
	if q == nil {
		return out
	}
	on, _, _ := q.settings()
	out.LogEnabled = on
	if !on {
		return out
	}
	if t, b, err := q.totals(ctx, midnight); err == nil && t >= out.Today {
		out.Today, out.Blocked = t, b // the log also remembers before a restart
	}
	if v, err := q.top(ctx, "name", midnight, true, 10); err == nil {
		out.TopBlocked = v
	}
	if v, err := q.top(ctx, "name", midnight, false, 10); err == nil {
		out.TopAllowed = v
	}
	if v, err := q.top(ctx, "client", midnight, false, 10); err == nil {
		out.TopClients = v
	}
	if v, err := q.clients(ctx, now.Add(-24*time.Hour)); err == nil {
		out.Clients = v
	}
	return out
}

// Log lists query log entries.
func (s *Service) Log(ctx context.Context, f LogFilter) ([]Query, error) {
	s.mu.Lock()
	q := s.qlog
	s.mu.Unlock()
	if q == nil {
		return []Query{}, nil
	}
	return q.list(ctx, f)
}

// ClearLog deletes the whole query log.
func (s *Service) ClearLog() error {
	s.mu.Lock()
	q := s.qlog
	s.mu.Unlock()
	if q == nil {
		return nil
	}
	return q.clear()
}

// FreePort53 turns off the system resolver's loopback listener so the
// blocker can listen everywhere, and restarts the blocker's listeners.
func (s *Service) FreePort53() (Status, error) {
	if err := freePort53(s.dir); err != nil {
		return s.Status(), err
	}
	s.mu.Lock()
	var err error
	if s.settings.Enabled {
		s.stopLocked()
		err = s.applyLocked(s.settings, true)
	}
	s.mu.Unlock()
	return s.Status(), err
}

// RestorePort53 gives port 53 back to the system resolver.
func (s *Service) RestorePort53() (Status, error) {
	s.mu.Lock()
	s.stopLocked()
	s.mu.Unlock()
	err := restorePort53(s.dir)
	s.mu.Lock()
	if s.settings.Enabled {
		if e := s.applyLocked(s.settings, true); err == nil {
			err = e
		}
	}
	s.mu.Unlock()
	return s.Status(), err
}

// ---------- listeners ----------

type listeners struct {
	pcs   []net.PacketConn
	lns   []net.Listener
	addrs []string
	wg    sync.WaitGroup
}

func (l *listeners) close() {
	for _, pc := range l.pcs {
		pc.Close()
	}
	for _, ln := range l.lns {
		ln.Close()
	}
	l.wg.Wait()
}

// listen opens UDP and TCP port on every address ("all"), on each of this
// machine's own addresses ("lan"), or the first that works ("auto").
func listen(mode string, port int) (*listeners, string, error) {
	if testHosts != nil {
		l, err := listenOn(testHosts, port)
		return l, "", err
	}
	if mode == "all" || mode == "auto" {
		l, err := listenOn([]string{""}, port)
		if err == nil || mode == "all" {
			return l, "", err
		}
		if !errors.Is(err, syscall.EADDRINUSE) && !isAddrInUse(err) {
			return nil, "", err
		}
	}
	l, err := listenOn(localAddrs(), port)
	if err != nil {
		return nil, "", err
	}
	note := ""
	if mode == "auto" {
		note = "lan"
	}
	return l, note, nil
}

func isAddrInUse(err error) bool {
	var se *os.SyscallError
	if errors.As(err, &se) {
		return errors.Is(se.Err, syscall.EADDRINUSE)
	}
	return false
}

func listenOn(hosts []string, port int) (*listeners, error) {
	l := &listeners{}
	var firstErr error
	for _, h := range hosts {
		addr := net.JoinHostPort(h, strconv.Itoa(port))
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			firstErr = cmpErr(firstErr, err)
			continue
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			pc.Close()
			firstErr = cmpErr(firstErr, err)
			continue
		}
		l.pcs = append(l.pcs, pc)
		l.lns = append(l.lns, ln)
		if h == "" {
			h = "*"
		}
		l.addrs = append(l.addrs, net.JoinHostPort(h, strconv.Itoa(port)))
	}
	if len(l.pcs) == 0 {
		if firstErr == nil {
			firstErr = errors.New("no addresses to listen on")
		}
		return nil, firstErr
	}
	return l, nil
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// localAddrs are this machine's own addresses: loopback (for itself) and
// the ones other devices reach it on.
func localAddrs() []string {
	out := []string{"127.0.0.1", "::1"}
	for _, a := range interfaceAddrs() {
		out = append(out, a.String())
	}
	return out
}

func interfaceAddrs() []netip.Addr {
	var out []netip.Addr
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		ip := p.Addr()
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		out = append(out, ip)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Is4() && !out[j].Is4() })
	return out
}

// lanAddresses are the addresses to give routers and devices.
func lanAddresses() []string {
	out := []string{}
	for _, a := range interfaceAddrs() {
		if a.Is4() || a.IsPrivate() || a.IsGlobalUnicast() {
			out = append(out, a.String())
		}
	}
	return out
}

// testHosts replaces the addresses listen uses (tests: loopback only).
var testHosts []string

const maxInflight = 256

func (l *listeners) serve(ctx context.Context, res *Resolver) {
	if ctx == nil {
		ctx = context.Background()
	}
	sem := make(chan struct{}, maxInflight)
	for _, pc := range l.pcs {
		l.wg.Add(1)
		go func(pc net.PacketConn) {
			defer l.wg.Done()
			buf := make([]byte, 4096)
			for {
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						return
					}
					continue
				}
				msg := append([]byte(nil), buf[:n]...)
				select {
				case sem <- struct{}{}:
				default:
					continue // too busy: drop, the client retries
				}
				go func() {
					defer func() { <-sem }()
					client := addrOf(from)
					if resp := res.Handle(ctx, msg, client, 512); resp != nil {
						_, _ = pc.WriteTo(resp, from)
					}
				}()
			}
		}(pc)
	}
	for _, ln := range l.lns {
		l.wg.Add(1)
		go func(ln net.Listener) {
			defer l.wg.Done()
			for {
				c, err := ln.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						return
					}
					continue
				}
				go serveTCP(ctx, c, res)
			}
		}(ln)
	}
}

func addrOf(a net.Addr) netip.Addr {
	if ap, err := netip.ParseAddrPort(a.String()); err == nil {
		return ap.Addr().Unmap()
	}
	return netip.Addr{}
}

func serveTCP(ctx context.Context, c net.Conn, res *Resolver) {
	defer c.Close()
	client := addrOf(c.RemoteAddr())
	for i := 0; i < 100; i++ {
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		var l [2]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return
		}
		msg := make([]byte, binary.BigEndian.Uint16(l[:]))
		if _, err := io.ReadFull(c, msg); err != nil {
			return
		}
		resp := res.Handle(ctx, msg, client, 0)
		if resp == nil {
			return
		}
		out := make([]byte, 2+len(resp))
		binary.BigEndian.PutUint16(out, uint16(len(resp)))
		copy(out[2:], resp)
		if _, err := c.Write(out); err != nil {
			return
		}
	}
}
