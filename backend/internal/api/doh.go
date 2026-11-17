package api

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/devices"
	"alfaos/alfad/internal/dnsblock"
	"alfaos/alfad/internal/store"

	"golang.org/x/net/dns/dnsmessage"
)

// Shield on the phone: linked devices ask the server's DNS blocker over
// DNS-over-HTTPS (RFC 8484) at /dns-query, at home and away. Only linked
// devices (their device token) get answers: the server is never an open
// resolver. Each device has its own rate limit; questions go to the
// server's normal query log under the device's name.

const (
	dohMaxMsg      = 4096 // a DNS question is far smaller
	dohContentType = "application/dns-message"
	dohAuthTTL     = 30 * time.Second // a revoked device stops within this (unlinking clears it at once)
	dohAuthMax     = 512
)

// dohState: who was let in recently (so every question isn't a database
// lookup) and the rate limits.
type dohState struct {
	once    sync.Once
	perDev  *auth.Limiter // questions per device
	badAuth *auth.Limiter // wrong tokens per address

	mu   sync.Mutex
	seen map[string]dohSeen // token hash → device
}

type dohSeen struct {
	d  *store.Device
	u  *store.User
	at time.Time
}

func (s *Server) doh() *dohState {
	s.dohS.once.Do(func() {
		// 40 a second, bursts of 400 (opening an app asks a lot at once).
		s.dohS.perDev = auth.NewLimiter(25*time.Millisecond, 400)
		s.dohS.badAuth = auth.NewLimiter(time.Second, 20)
		s.dohS.seen = map[string]dohSeen{}
	})
	return &s.dohS
}

func (s *Server) dohRoutes(mux *http.ServeMux) {
	mux.Handle("GET /dns-query", s.dohAuth(s.withShield(s.dnsQuery)))
	mux.Handle("POST /dns-query", s.dohAuth(s.withShield(s.dnsQuery)))
	mux.Handle("GET /api/v1/device/shield", s.device(s.withShield(s.deviceShieldGet)))
	mux.Handle("POST /api/v1/device/shield/rules", s.device(s.withShield(s.deviceShieldRule)))
}

// withShield answers 503 where the DNS blocker couldn't start.
func (s *Server) withShield(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.shield == nil || s.shield.dns == nil {
			writeError(w, http.StatusServiceUnavailable, "shield_off", "Shield isn't available on this server.")
			return
		}
		h(w, r)
	}
}

// dohAuth is s.device with a short memory of good tokens.
func (s *Server) dohAuth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := s.doh()
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Device ")
		tok = strings.TrimSpace(tok)
		if !ok || tok == "" {
			w.Header().Set("WWW-Authenticate", `Device realm="nocap"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing device token")
			return
		}
		hash := devices.HashToken(tok)
		now := time.Now()
		st.mu.Lock()
		e, hit := st.seen[hash]
		if hit && now.Sub(e.at) > dohAuthTTL {
			delete(st.seen, hash)
			hit = false
		}
		st.mu.Unlock()
		if hit {
			ctx := context.WithValue(context.WithValue(r.Context(), deviceKey, e.d), userKey, e.u)
			h(w, r.WithContext(ctx))
			return
		}
		if !st.badAuth.Allow(clientIP(r, s.Config.TrustedProxies)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many tries. Slow down a little.")
			return
		}
		s.device(func(w http.ResponseWriter, r *http.Request) {
			d, u := deviceFrom(r.Context()), userFrom(r.Context())
			st.mu.Lock()
			if len(st.seen) >= dohAuthMax {
				for k, v := range st.seen {
					if now.Sub(v.at) > dohAuthTTL || len(st.seen) >= dohAuthMax {
						delete(st.seen, k)
					}
				}
			}
			st.seen[hash] = dohSeen{d: d, u: u, at: now}
			st.mu.Unlock()
			h(w, r)
		}).ServeHTTP(w, r)
	})
}

// dohForget stops a device at once (unlinked) and drops its own lists.
func (s *Server) dohForget(deviceID string) {
	st := s.doh()
	st.mu.Lock()
	for k, v := range st.seen {
		if v.d.ID == deviceID {
			delete(st.seen, k)
		}
	}
	st.mu.Unlock()
	if s.shield != nil {
		s.shield.dns.ForgetDevice(deviceID)
	}
}

// dohMessage reads the DNS message of a GET (?dns=, base64url) or POST.
func dohMessage(w http.ResponseWriter, r *http.Request) ([]byte, int, string) {
	var msg []byte
	if r.Method == http.MethodGet {
		q := r.URL.Query().Get("dns")
		if q == "" {
			return nil, http.StatusBadRequest, "missing dns parameter"
		}
		if len(q) > base64.RawURLEncoding.EncodedLen(dohMaxMsg) {
			return nil, http.StatusRequestURITooLong, "question too long"
		}
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(q, "="))
		if err != nil {
			return nil, http.StatusBadRequest, "dns parameter isn't base64url"
		}
		msg = b
	} else {
		if ct := r.Header.Get("Content-Type"); !strings.EqualFold(strings.TrimSpace(strings.Split(ct, ";")[0]), dohContentType) {
			return nil, http.StatusUnsupportedMediaType, "send " + dohContentType
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, dohMaxMsg))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return nil, http.StatusRequestEntityTooLarge, "question too long"
			}
			return nil, http.StatusBadRequest, "couldn't read the question"
		}
		msg = b
	}
	if len(msg) < 12 {
		return nil, http.StatusBadRequest, "not a DNS message"
	}
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil || h.Response {
		return nil, http.StatusBadRequest, "not a DNS question"
	}
	if _, err := p.Question(); err != nil {
		return nil, http.StatusBadRequest, "not a DNS question"
	}
	if _, err := p.Question(); err == nil {
		return nil, http.StatusBadRequest, "one question at a time"
	}
	return msg, 0, ""
}

func (s *Server) dnsQuery(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	if !s.doh().perDev.Allow(d.ID) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many questions at once.")
		return
	}
	msg, code, why := dohMessage(w, r)
	if code != 0 {
		writeError(w, code, "bad_request", why)
		return
	}
	addr, _ := netip.ParseAddr(clientIP(r, s.Config.TrustedProxies))
	res, err := s.shield.dns.Ask(r.Context(), msg, dnsblock.Asker{UserID: u.ID, DeviceID: d.ID, Label: d.Name, Addr: addr})
	if errors.Is(err, dnsblock.ErrOff) {
		writeError(w, http.StatusServiceUnavailable, "shield_off", "The DNS blocker is off on your server. Turn it on in Shield.")
		return
	}
	if err != nil || res.Msg == nil {
		writeError(w, http.StatusBadGateway, "dns_failed", "No answer.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", dohContentType)
	h.Set("Cache-Control", "private, max-age="+strconv.Itoa(int(minTTL(res.Msg))))
	if res.Status == "blocked" {
		// The phone keeps its own "blocked today" from this (it never asks the log).
		h.Set("X-NoCap-Blocked", "1")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res.Msg)
}

// minTTL is the shortest TTL in an answer (for Cache-Control), at most an hour.
func minTTL(msg []byte) uint32 {
	var p dnsmessage.Parser
	if _, err := p.Start(msg); err != nil || p.SkipAllQuestions() != nil {
		return 0
	}
	ttl, n := uint32(3600), 0
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			break
		}
		ttl = min(ttl, h.TTL)
		n++
		if p.SkipAnswer() != nil {
			break
		}
	}
	if n == 0 {
		return 0
	}
	return ttl
}

type deviceShieldView struct {
	Available   bool                   `json:"available"` // the blocker is on
	PausedUntil time.Time              `json:"paused_until,omitzero"`
	User        dnsblock.PersonalLists `json:"user"`
	Device      dnsblock.PersonalLists `json:"device"`
	Today       int                    `json:"today"`
	Blocked     int                    `json:"blocked_today"`
	Recent      []dnsblock.Query       `json:"recent_blocked"`
}

func (s *Server) deviceShieldView(r *http.Request) deviceShieldView {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	st := s.shield.dns.Status()
	v := deviceShieldView{Available: st.Settings.Enabled && st.Running, PausedUntil: st.PausedUntil}
	v.User, v.Device = s.shield.dns.PersonalFor(u.ID, d.ID)
	v.Today, v.Blocked, v.Recent = s.shield.dns.DeviceToday(r.Context(), d.ID, 30)
	return v
}

// deviceShieldGet: the phone's Shield page (its own lists, today's count
// from the server's log when that's on).
func (s *Server) deviceShieldGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.deviceShieldView(r))
}

// deviceShieldRule allows or blocks one domain for this device or for all
// of the person's devices ("clear" takes it off both lists).
func (s *Server) deviceShieldRule(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	var req struct {
		Domain string `json:"domain"`
		Action string `json:"action"` // allow | block | clear
		Scope  string `json:"scope"`  // device | user
	}
	if !decodeJSONLimit(w, r, &req, 4096) {
		return
	}
	name, ok := dnsblock.NormalizeDomain(req.Domain)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "That isn't a domain name.")
		return
	}
	id := d.ID
	switch req.Scope {
	case "", "device":
		req.Scope = "device"
	case "user":
		id = u.ID
	default:
		writeError(w, http.StatusBadRequest, "invalid", "scope must be device or user")
		return
	}
	userL, devL := s.shield.dns.PersonalFor(u.ID, d.ID)
	l := devL
	if req.Scope == "user" {
		l = userL
	}
	l.Allow, l.Block = without(l.Allow, name), without(l.Block, name)
	switch req.Action {
	case "allow":
		l.Allow = append(l.Allow, name)
	case "block":
		l.Block = append(l.Block, name)
	case "clear":
	default:
		writeError(w, http.StatusBadRequest, "invalid", "action must be allow, block or clear")
		return
	}
	_, err := s.shield.dns.SetPersonal(req.Scope, id, l)
	s.audit(r, u.ID, "dns.personal."+req.Action, name, err == nil, req.Scope+" "+d.Name)
	switch {
	case err == nil:
	case dnsblock.IsInvalid(err):
		writeError(w, http.StatusBadRequest, "invalid", strings.TrimPrefix(err.Error(), "invalid: "))
		return
	case errors.Is(err, dnsblock.ErrTooMany):
		writeError(w, http.StatusConflict, "too_many", err.Error())
		return
	default:
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.deviceShieldView(r))
}

func without(list []string, name string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != name {
			out = append(out, x)
		}
	}
	return out
}
