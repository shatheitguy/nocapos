package dnsblock

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Personal lists: someone's own allow and block lists, for all their
// devices ("user") or one linked device ("device"). They only apply to
// questions their devices ask over DNS-over-HTTPS, and live in their own
// file so the server's settings page never overwrites them.

// PersonalLists is one scope's lists.
type PersonalLists struct {
	Allow []string `json:"allow"`
	Block []string `json:"block"`
}

// Limits on personal lists.
const (
	maxPersonalEntries = 1000 // per list
	maxPersonalScopes  = 4000 // people and devices together
)

// ErrTooMany: the server holds as many personal lists as it takes.
var ErrTooMany = errors.New("too many personal lists on this server")

type personalStore struct {
	path string

	mu       sync.Mutex
	scopes   map[string]PersonalLists // "user:<id>" / "device:<id>"
	compiled map[string][2]*DomainSet // allow, block
}

func openPersonal(dir string) *personalStore {
	p := &personalStore{path: filepath.Join(dir, "personal.json"), scopes: map[string]PersonalLists{}, compiled: map[string][2]*DomainSet{}}
	if b, err := os.ReadFile(p.path); err == nil {
		var in map[string]PersonalLists
		if json.Unmarshal(b, &in) == nil {
			for k, v := range in {
				if len(p.scopes) >= maxPersonalScopes {
					break
				}
				if v, err := v.clean(); err == nil {
					p.scopes[k] = v
				}
			}
		}
	}
	return p
}

func (l PersonalLists) clean() (PersonalLists, error) {
	if len(l.Allow) > maxPersonalEntries || len(l.Block) > maxPersonalEntries {
		return l, bad("at most %d entries in each list", maxPersonalEntries)
	}
	var err error
	if l.Allow, err = cleanDomains(l.Allow, "allow list"); err != nil {
		return l, err
	}
	if l.Block, err = cleanDomains(l.Block, "block list"); err != nil {
		return l, err
	}
	return l, nil
}

func (p *personalStore) get(scope string) PersonalLists {
	p.mu.Lock()
	defer p.mu.Unlock()
	l := p.scopes[scope]
	if l.Allow == nil {
		l.Allow = []string{}
	}
	if l.Block == nil {
		l.Block = []string{}
	}
	return PersonalLists{Allow: append([]string{}, l.Allow...), Block: append([]string{}, l.Block...)}
}

// sets returns scope's compiled allow and block sets (nil when empty).
func (p *personalStore) sets(scope string) (allow, block *DomainSet) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.compiled[scope]; ok {
		return c[0], c[1]
	}
	l, ok := p.scopes[scope]
	if !ok {
		return nil, nil
	}
	c := [2]*DomainSet{NewDomainSet(append([]string(nil), l.Allow...)), NewDomainSet(append([]string(nil), l.Block...))}
	p.compiled[scope] = c
	return c[0], c[1]
}

func (p *personalStore) set(scope string, l PersonalLists) (PersonalLists, error) {
	l, err := l.clean()
	if err != nil {
		return l, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, had := p.scopes[scope]
	empty := len(l.Allow) == 0 && len(l.Block) == 0
	if !had && !empty && len(p.scopes) >= maxPersonalScopes {
		return l, ErrTooMany
	}
	prev, prevOK := p.scopes[scope]
	if empty {
		delete(p.scopes, scope)
	} else {
		p.scopes[scope] = l
	}
	delete(p.compiled, scope)
	if err := p.saveLocked(); err != nil {
		if prevOK {
			p.scopes[scope] = prev
		} else {
			delete(p.scopes, scope)
		}
		return l, err
	}
	return l, nil
}

func (p *personalStore) forget(scope string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.scopes[scope]; !ok {
		return
	}
	delete(p.scopes, scope)
	delete(p.compiled, scope)
	_ = p.saveLocked()
}

func (p *personalStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(p.scopes)
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

func userScope(id string) string   { return "user:" + id }
func deviceScope(id string) string { return "device:" + id }

func (s *Service) personalStore() *personalStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.personal == nil {
		s.personal = openPersonal(s.dir)
	}
	return s.personal
}

// PersonalFor returns the lists of a person (scope "user") or one of their
// devices (scope "device").
func (s *Service) PersonalFor(userID, deviceID string) (user, device PersonalLists) {
	p := s.personalStore()
	return p.get(userScope(userID)), p.get(deviceScope(deviceID))
}

// SetPersonal replaces a person's or a device's lists.
func (s *Service) SetPersonal(scope, id string, l PersonalLists) (PersonalLists, error) {
	key := userScope(id)
	switch scope {
	case "user":
	case "device":
		key = deviceScope(id)
	default:
		return l, bad("scope must be user or device")
	}
	return s.personalStore().set(key, l)
}

// ForgetDevice drops an unlinked device's lists.
func (s *Service) ForgetDevice(deviceID string) { s.personalStore().forget(deviceScope(deviceID)) }

// ForgetUser drops a removed person's lists.
func (s *Service) ForgetUser(userID string) { s.personalStore().forget(userScope(userID)) }

// Asker says who asks a question over DNS-over-HTTPS.
type Asker struct {
	UserID   string
	DeviceID string
	Label    string // the query log's "client" (the device's name)
	Addr     netip.Addr
}

// ErrOff: the blocker is switched off on this server.
var ErrOff = errors.New("the DNS blocker is off on this server")

// Ask answers a DNS message asked over DNS-over-HTTPS by a linked device,
// with that person's and device's own lists first. The answer is never
// truncated (HTTPS carries any size).
func (s *Service) Ask(ctx context.Context, msg []byte, a Asker) (Result, error) {
	s.mu.Lock()
	on := s.settings.Enabled && s.qlog != nil
	s.mu.Unlock()
	if !on {
		return Result{}, ErrOff
	}
	p := s.personalStore()
	per := &Personal{}
	per.UserAllow, per.UserBlock = p.sets(userScope(a.UserID))
	per.DeviceAllow, per.DeviceBlock = p.sets(deviceScope(a.DeviceID))
	ctx, cancel := context.WithTimeout(ctx, 2*upstreamTimeout+time.Second)
	defer cancel()
	return s.res.HandleWith(ctx, msg, a.Addr, Opts{Client: a.Label, Device: a.DeviceID, Personal: per}), nil
}

// DeviceToday counts a device's questions since midnight and its most
// recently blocked names (from the query log; empty when it's off).
func (s *Service) DeviceToday(ctx context.Context, deviceID string, recent int) (total, blocked int, last []Query) {
	s.mu.Lock()
	q := s.qlog
	s.mu.Unlock()
	last = []Query{}
	if q == nil || deviceID == "" {
		return
	}
	if on, _, _ := q.settings(); !on {
		return
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var b int64
	_ = q.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(status = 'blocked'), 0) FROM queries WHERE device = ? AND t >= ?`, deviceID, midnight.UnixMilli()).Scan(&total, &b)
	blocked = int(b)
	if l, err := q.list(ctx, LogFilter{Device: deviceID, Status: "blocked", Limit: recent}); err == nil {
		last = l
	}
	return
}
