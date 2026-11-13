package dnsblock

import (
	"context"
	"encoding/binary"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Query is one question a device asked, as the log and the dashboard see it.
type Query struct {
	Time     time.Time `json:"time"`
	Client   string    `json:"client,omitempty"`
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Status   string    `json:"status"`           // blocked | forwarded | cached | local | error
	Reason   string    `json:"reason,omitempty"` // which list or rule blocked it
	Upstream string    `json:"upstream,omitempty"`
	Ms       int       `json:"ms"`
	RCode    string    `json:"rcode,omitempty"`
}

// namedSet is one block list's domains.
type namedSet struct {
	name string
	set  *DomainSet
}

// rules is what the resolver answers by; it's swapped as a whole.
type rules struct {
	blockMode   string
	checkCNAMEs bool
	lists       []namedSet
	block       *DomainSet
	allow       *DomainSet
	records     map[string][]Record
	ups         []Upstream
}

// Resolver answers DNS messages: local records, then blocking, then the
// cache, then the upstreams.
type Resolver struct {
	mu          sync.RWMutex
	r           *rules
	pausedUntil time.Time
	cache       *cache
	stats       *counters
	onQuery     func(Query)
	now         func() time.Time
}

// NewResolver returns a resolver with no rules and no upstreams.
func NewResolver(cacheSize int) *Resolver {
	return &Resolver{r: &rules{blockMode: "null", records: map[string][]Record{}}, cache: newCache(cacheSize), stats: newCounters(), now: time.Now}
}

func (rs *Resolver) setRules(r *rules, cacheSize int) {
	rs.mu.Lock()
	rs.r = r
	rs.mu.Unlock()
	rs.cache.reset(cacheSize)
}

// Pause turns blocking off until t (the zero time resumes it).
func (rs *Resolver) Pause(until time.Time) {
	rs.mu.Lock()
	rs.pausedUntil = until
	rs.mu.Unlock()
}

// PausedUntil is when blocking resumes (zero when it isn't paused).
func (rs *Resolver) PausedUntil() time.Time {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if rs.now().After(rs.pausedUntil) {
		return time.Time{}
	}
	return rs.pausedUntil
}

func (rs *Resolver) current() (*rules, bool) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.r, rs.now().Before(rs.pausedUntil)
}

// blockedBy reports which list or rule blocks name ("" = not blocked).
func (r *rules) blockedBy(name string) string {
	if _, ok := r.allow.Match(name); ok {
		return ""
	}
	if _, ok := r.block.Match(name); ok {
		return "Your block list"
	}
	for _, l := range r.lists {
		if _, ok := l.set.Match(name); ok {
			return l.name
		}
	}
	return ""
}

// Handle answers one DNS message from client. maxSize is the largest
// answer the transport takes (0: no limit, as over TCP). It returns nil
// for messages it won't answer (garbage, answers).
func (rs *Resolver) Handle(ctx context.Context, req []byte, client netip.Addr, maxSize int) []byte {
	start := rs.now()
	var p dnsmessage.Parser
	h, err := p.Start(req)
	if err != nil || h.Response {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return reply(h, nil, dnsmessage.RCodeFormatError)
	}
	if h.OpCode != 0 {
		return reply(h, &q, dnsmessage.RCodeNotImplemented)
	}
	if maxSize > 0 {
		maxSize = max(maxSize, ednsSize(&p))
	}
	name := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
	qtype := typeName(q.Type)
	r, paused := rs.current()
	log := Query{Time: start, Client: client.Unmap().String(), Name: name, Type: qtype}
	finish := func(resp []byte, status, reason, upstream string) []byte {
		log.Status, log.Reason, log.Upstream = status, reason, upstream
		log.Ms = int(rs.now().Sub(start) / time.Millisecond)
		log.RCode = rcodeOf(resp)
		rs.stats.add(log.Time, status == "blocked")
		if rs.onQuery != nil {
			rs.onQuery(log)
		}
		if maxSize > 0 && len(resp) > maxSize {
			return truncated(h, q)
		}
		return resp
	}

	if recs, ok := r.records[name]; ok {
		return finish(localAnswer(h, q, recs, r.records), "local", "", "")
	}
	if !paused {
		if reason := r.blockedBy(name); reason != "" {
			return finish(blockedAnswer(h, q, r.blockMode), "blocked", reason, "")
		}
	}
	key := cacheKey(name, q.Type, q.Class)
	if resp, ok := rs.cache.get(key, h.ID, rs.now()); ok {
		return finish(resp, "cached", "", "")
	}
	if len(r.ups) == 0 {
		return finish(reply(h, &q, dnsmessage.RCodeServerFailure), "error", "", "")
	}
	out := append([]byte(nil), req...)
	id := randomID()
	binary.BigEndian.PutUint16(out, id)
	for _, up := range r.ups {
		cctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
		resp, err := up.Exchange(cctx, out)
		cancel()
		if err != nil || !answers(resp, id, q) {
			continue
		}
		if r.checkCNAMEs && !paused {
			if target := blockedCNAME(resp, r); target != "" {
				return finish(blockedAnswer(h, q, r.blockMode), "blocked", "CNAME "+target, up.String())
			}
		}
		binary.BigEndian.PutUint16(resp, h.ID)
		if ttl, ok := cacheTTL(resp); ok {
			rs.cache.put(key, resp, rs.now(), ttl)
		}
		return finish(resp, "forwarded", "", up.String())
	}
	return finish(reply(h, &q, dnsmessage.RCodeServerFailure), "error", "", "")
}

// answers reports whether resp answers q under id.
func answers(resp []byte, id uint16, q dnsmessage.Question) bool {
	var p dnsmessage.Parser
	h, err := p.Start(resp)
	if err != nil || !h.Response || h.ID != id {
		return false
	}
	rq, err := p.Question()
	return err == nil && rq.Type == q.Type && strings.EqualFold(rq.Name.String(), q.Name.String())
}

// ednsSize is the UDP size the client said it takes (512 without EDNS).
func ednsSize(p *dnsmessage.Parser) int {
	if p.SkipAllQuestions() != nil || p.SkipAllAnswers() != nil || p.SkipAllAuthorities() != nil {
		return 512
	}
	for {
		h, err := p.AdditionalHeader()
		if err != nil {
			return 512
		}
		if h.Type == dnsmessage.TypeOPT {
			return min(max(int(h.Class), 512), 4096)
		}
		if p.SkipAdditional() != nil {
			return 512
		}
	}
}

func baseHeader(h dnsmessage.Header, rc dnsmessage.RCode) dnsmessage.Header {
	return dnsmessage.Header{ID: h.ID, Response: true, OpCode: h.OpCode, RecursionDesired: h.RecursionDesired, RecursionAvailable: true, RCode: rc}
}

// reply is an answer with no records.
func reply(h dnsmessage.Header, q *dnsmessage.Question, rc dnsmessage.RCode) []byte {
	b := dnsmessage.NewBuilder(nil, baseHeader(h, rc))
	if q != nil {
		_ = b.StartQuestions()
		_ = b.Question(*q)
	}
	out, _ := b.Finish()
	return out
}

func truncated(h dnsmessage.Header, q dnsmessage.Question) []byte {
	hd := baseHeader(h, dnsmessage.RCodeSuccess)
	hd.Truncated = true
	b := dnsmessage.NewBuilder(nil, hd)
	_ = b.StartQuestions()
	_ = b.Question(q)
	out, _ := b.Finish()
	return out
}

const blockedTTL = 10

func blockedAnswer(h dnsmessage.Header, q dnsmessage.Question, mode string) []byte {
	if mode == "nxdomain" {
		return reply(h, &q, dnsmessage.RCodeNameError)
	}
	b := dnsmessage.NewBuilder(nil, baseHeader(h, dnsmessage.RCodeSuccess))
	_ = b.StartQuestions()
	_ = b.Question(q)
	_ = b.StartAnswers()
	rh := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: blockedTTL}
	switch q.Type {
	case dnsmessage.TypeA:
		_ = b.AResource(rh, dnsmessage.AResource{})
	case dnsmessage.TypeAAAA:
		_ = b.AAAAResource(rh, dnsmessage.AAAAResource{})
	}
	out, _ := b.Finish()
	return out
}

// localAnswer answers from the local records (following a CNAME to
// another local name once).
func localAnswer(h dnsmessage.Header, q dnsmessage.Question, recs []Record, all map[string][]Record) []byte {
	hd := baseHeader(h, dnsmessage.RCodeSuccess)
	hd.Authoritative = true
	b := dnsmessage.NewBuilder(nil, hd)
	b.EnableCompression()
	_ = b.StartQuestions()
	_ = b.Question(q)
	_ = b.StartAnswers()
	add := func(owner dnsmessage.Name, rs []Record, follow bool) {
		for _, r := range rs {
			rh := dnsmessage.ResourceHeader{Name: owner, Class: dnsmessage.ClassINET, TTL: 60}
			switch {
			case r.Type == "A" && (q.Type == dnsmessage.TypeA || q.Type == dnsmessage.TypeALL):
				a := netip.MustParseAddr(r.Value).As4()
				_ = b.AResource(rh, dnsmessage.AResource{A: a})
			case r.Type == "AAAA" && (q.Type == dnsmessage.TypeAAAA || q.Type == dnsmessage.TypeALL):
				a := netip.MustParseAddr(r.Value).As16()
				_ = b.AAAAResource(rh, dnsmessage.AAAAResource{AAAA: a})
			case r.Type == "CNAME":
				target, err := dnsmessage.NewName(r.Value + ".")
				if err != nil {
					continue
				}
				_ = b.CNAMEResource(rh, dnsmessage.CNAMEResource{CNAME: target})
				if next, ok := all[r.Value]; ok && follow && q.Type != dnsmessage.TypeCNAME {
					// Only the address records of the target.
					var addrs []Record
					for _, n := range next {
						if n.Type != "CNAME" {
							addrs = append(addrs, n)
						}
					}
					for _, n := range addrs {
						nh := dnsmessage.ResourceHeader{Name: target, Class: dnsmessage.ClassINET, TTL: 60}
						if n.Type == "A" && q.Type == dnsmessage.TypeA {
							_ = b.AResource(nh, dnsmessage.AResource{A: netip.MustParseAddr(n.Value).As4()})
						}
						if n.Type == "AAAA" && q.Type == dnsmessage.TypeAAAA {
							_ = b.AAAAResource(nh, dnsmessage.AAAAResource{AAAA: netip.MustParseAddr(n.Value).As16()})
						}
					}
				}
			}
		}
	}
	add(q.Name, recs, true)
	out, err := b.Finish()
	if err != nil {
		return reply(h, &q, dnsmessage.RCodeServerFailure)
	}
	return out
}

// blockedCNAME returns the first CNAME target in resp that's blocked.
func blockedCNAME(resp []byte, r *rules) string {
	var p dnsmessage.Parser
	if _, err := p.Start(resp); err != nil || p.SkipAllQuestions() != nil {
		return ""
	}
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			return ""
		}
		if h.Type != dnsmessage.TypeCNAME {
			if p.SkipAnswer() != nil {
				return ""
			}
			continue
		}
		c, err := p.CNAMEResource()
		if err != nil {
			return ""
		}
		t := strings.ToLower(strings.TrimSuffix(c.CNAME.String(), "."))
		if r.blockedBy(t) != "" {
			return t
		}
	}
}

func rcodeOf(resp []byte) string {
	if len(resp) < 4 {
		return ""
	}
	switch resp[3] & 0x0f {
	case 0:
		return "NOERROR"
	case 1:
		return "FORMERR"
	case 2:
		return "SERVFAIL"
	case 3:
		return "NXDOMAIN"
	case 4:
		return "NOTIMP"
	case 5:
		return "REFUSED"
	}
	return "RCODE" + string(rune('0'+resp[3]&0x0f))
}

func typeName(t dnsmessage.Type) string {
	s := t.String()
	return strings.TrimPrefix(s, "Type")
}

// ---------- cache ----------

type cacheEntry struct {
	msg     []byte
	stored  time.Time
	expires time.Time
}

type cache struct {
	mu   sync.Mutex
	max  int
	data map[string]*cacheEntry
}

func newCache(n int) *cache { return &cache{max: n, data: map[string]*cacheEntry{}} }

func (c *cache) reset(n int) {
	c.mu.Lock()
	c.max, c.data = n, map[string]*cacheEntry{}
	c.mu.Unlock()
}

// Len is how many answers are cached.
func (c *cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.data)
}

func cacheKey(name string, t dnsmessage.Type, c dnsmessage.Class) string {
	return name + "|" + t.String() + "|" + c.String()
}

func (c *cache) put(key string, msg []byte, now time.Time, ttl uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.max <= 0 || ttl == 0 {
		return
	}
	if len(c.data) >= c.max {
		for k, e := range c.data {
			if now.After(e.expires) {
				delete(c.data, k)
			}
		}
		// Still full: drop about a tenth (map order is random enough).
		for k := range c.data {
			if len(c.data) < c.max*9/10 {
				break
			}
			delete(c.data, k)
		}
	}
	c.data[key] = &cacheEntry{msg: append([]byte(nil), msg...), stored: now, expires: now.Add(time.Duration(ttl) * time.Second)}
}

// get returns the cached answer for key with id and its TTLs counted down.
func (c *cache) get(key string, id uint16, now time.Time) ([]byte, bool) {
	c.mu.Lock()
	e, ok := c.data[key]
	if ok && now.After(e.expires) {
		delete(c.data, key)
		ok = false
	}
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	var m dnsmessage.Message
	if err := m.Unpack(e.msg); err != nil {
		return nil, false
	}
	age := uint32(now.Sub(e.stored) / time.Second)
	for _, sec := range [][]dnsmessage.Resource{m.Answers, m.Authorities, m.Additionals} {
		for i := range sec {
			if sec[i].Header.Type == dnsmessage.TypeOPT {
				continue
			}
			if sec[i].Header.TTL > age {
				sec[i].Header.TTL -= age
			} else {
				sec[i].Header.TTL = 0
			}
		}
	}
	m.Header.ID = id
	out, err := m.Pack()
	if err != nil {
		return nil, false
	}
	return out, true
}

// cacheTTL is how long resp may be cached: its shortest record TTL, or the
// negative-answer TTL from the SOA, at most an hour.
func cacheTTL(resp []byte) (uint32, bool) {
	var p dnsmessage.Parser
	h, err := p.Start(resp)
	if err != nil || h.Truncated || (h.RCode != dnsmessage.RCodeSuccess && h.RCode != dnsmessage.RCodeNameError) {
		return 0, false
	}
	if p.SkipAllQuestions() != nil {
		return 0, false
	}
	ttl := uint32(3600)
	n := 0
	for {
		rh, err := p.AnswerHeader()
		if err != nil {
			break
		}
		ttl = min(ttl, rh.TTL)
		n++
		if p.SkipAnswer() != nil {
			return 0, false
		}
	}
	if n == 0 {
		ttl = 60
		for {
			rh, err := p.AuthorityHeader()
			if err != nil {
				break
			}
			if rh.Type == dnsmessage.TypeSOA {
				soa, err := p.SOAResource()
				if err != nil {
					break
				}
				ttl = min(rh.TTL, soa.MinTTL, 3600)
				break
			}
			if p.SkipAuthority() != nil {
				break
			}
		}
	}
	return ttl, ttl > 0
}

// ---------- live counters ----------

// counters keeps a count per minute for the last day, in memory only (the
// dashboard's live graph, even with the query log off).
type counters struct {
	mu      sync.Mutex
	minutes [1440]struct {
		at             int64 // unix minute
		total, blocked int
	}
}

func newCounters() *counters { return &counters{} }

func (c *counters) add(t time.Time, blocked bool) {
	m := t.Unix() / 60
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &c.minutes[m%1440]
	if s.at != m {
		s.at, s.total, s.blocked = m, 0, 0
	}
	s.total++
	if blocked {
		s.blocked++
	}
}

// Point is one bar of the live graph.
type Point struct {
	Time    time.Time `json:"time"`
	Total   int       `json:"total"`
	Blocked int       `json:"blocked"`
}

// series returns n buckets of step minutes ending now.
func (c *counters) series(now time.Time, n, step int) []Point {
	c.mu.Lock()
	defer c.mu.Unlock()
	end := now.Unix() / 60
	out := make([]Point, n)
	for i := 0; i < n; i++ {
		first := end - int64((n-i)*step) + 1
		p := Point{Time: time.Unix(first*60, 0)}
		for m := first; m < first+int64(step); m++ {
			s := c.minutes[((m%1440)+1440)%1440]
			if s.at == m {
				p.Total += s.total
				p.Blocked += s.blocked
			}
		}
		out[i] = p
	}
	return out
}

// since sums the counts from t until now.
func (c *counters) since(t, now time.Time) (total, blocked int) {
	for _, p := range c.series(now, 1440, 1) {
		if !p.Time.Before(t.Truncate(time.Minute)) {
			total += p.Total
			blocked += p.Blocked
		}
	}
	return
}
