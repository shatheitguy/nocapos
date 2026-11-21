package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/devices"
	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/store"
)

// Your Squad: linking NoCap servers to each other, and phones asking to
// link without a code.
//
// Servers: an administrator on A finds B on the network and sends a link
// request. A calls B over HTTPS and remembers B's certificate; B calls A
// back at the address the request came from and remembers A's. Both sides
// show both fingerprints so the administrators can compare them. When an
// administrator on B accepts, B calls A (pinned to A's certificate) with a
// token for A, and A answers with a token for B. From then on each side
// presents its token to the other ("Authorization: NoCapServer <token>")
// and only talks to the certificate it pinned. Requests last ten minutes,
// are rate-limited, and only go over the home network unless this server
// has a remote address set up. Linked servers show up in each other's
// squad; sharing things between them comes later.
//
// Phones: the NoCap OS app finds a server and asks to link (no code). The
// server's administrators (or the person the phone names) see the request
// with a 4-digit check also shown on the phone, and on accept the phone is
// linked exactly as with a pairing code, within the account's device limit.

const (
	squadAskTTL       = 10 * time.Minute // link requests (servers and phones)
	squadPickupTTL    = 2 * time.Minute  // an accepted phone's token waits this long to be picked up
	squadMaxIncoming  = 20               // pending requests from other servers
	squadMaxPhoneAsks = 20               // pending requests from phones
	squadAsksPerIP    = 3                // pending phone requests from one address
	squadPeerTimeout  = 8 * time.Second
	squadPingTimeout  = 3 * time.Second
	squadStatusKeep   = 15 * time.Second
	squadMaxBody      = 64 << 10
	peerAuthPrefix    = "NoCapServer "
	tlsCertName       = "tls.crt" // tlsutil's self-signed certificate, in <data>/tls
)

// serverAsk is a link request between this server and another one.
type serverAsk struct {
	ID          string    `json:"id"`
	NocapID     string    `json:"nocap_id"` // the other server
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Address     string    `json:"address"`
	Version     string    `json:"version"`
	Fingerprint string    `json:"fingerprint"` // the other server's certificate, as this one saw it
	State       string    `json:"state"`       // pending | declined
	By          string    `json:"by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	token       string    // proves an answer comes from the server that was asked
	busy        bool
}

// phoneAsk is a phone asking to link without a code.
type phoneAsk struct {
	ID         string    `json:"id"`
	DeviceName string    `json:"device_name"`
	Platform   string    `json:"platform"`
	Model      string    `json:"model,omitempty"`
	OSVersion  string    `json:"os_version,omitempty"`
	Username   string    `json:"username,omitempty"` // the account it asks for ("" = whichever administrator accepts)
	Address    string    `json:"address"`
	Check      string    `json:"check"` // also shown on the phone
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	state      string    // pending | accepted | declined
	userID     string    // the account named by Username, "" when none or unknown
	secret     string
	result     *claimResponse
	busy       bool
}

type peerStatus struct {
	Online bool
	At     time.Time
}

type squadState struct {
	once    sync.Once
	mu      sync.Mutex
	in      map[string]*serverAsk
	out     map[string]*serverAsk
	phones  map[string]*phoneAsk
	status  map[string]peerStatus
	asks    *auth.Limiter // new requests, per address
	answers *auth.Limiter // answers to our requests, per address
	now     func() time.Time
}

func (s *Server) squadInit() *squadState {
	q := &s.squad
	q.once.Do(func() {
		q.in, q.out, q.phones, q.status = map[string]*serverAsk{}, map[string]*serverAsk{}, map[string]*phoneAsk{}, map[string]peerStatus{}
		if q.asks == nil {
			q.asks = auth.NewLimiter(20*time.Second, 3)
		}
		if q.answers == nil {
			q.answers = auth.NewLimiter(2*time.Second, 10)
		}
		if q.now == nil {
			q.now = time.Now
		}
	})
	return q
}

// prune drops requests past their time. Call with mu held.
func (q *squadState) prune() {
	now := q.now()
	for id, a := range q.in {
		if !now.Before(a.ExpiresAt) {
			delete(q.in, id)
		}
	}
	for id, a := range q.out {
		if !now.Before(a.ExpiresAt) {
			delete(q.out, id)
		}
	}
	for id, a := range q.phones {
		if !now.Before(a.ExpiresAt) {
			delete(q.phones, id)
		}
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// checkDigits: four digits both screens show for the same request.
func checkDigits() string {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%04d", (int(b[0])<<8|int(b[1]))%10000)
}

func sameSecret(a, b string) bool {
	return a != "" && len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ---------- reaching other servers ----------

// certFingerprint is the SHA-256 of a certificate (DER), lowercase hex:
// what the NoCap OS app pins too.
func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// ownFingerprint is this server's own certificate, "" without HTTPS.
func (s *Server) ownFingerprint() string {
	path := s.Config.TLSCert
	if path == "" && s.Config.TLSAuto {
		path = filepath.Join(s.Config.DataDir, "tls", tlsCertName)
	}
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return ""
	}
	return certFingerprint(block.Bytes)
}

// remoteAllowed: other servers beyond the home network only once this
// server has a remote address set up (Your Squad → addresses).
func (s *Server) remoteAllowed(ctx context.Context) bool {
	v, _ := s.Store.Setting(ctx, linkRemoteKey)
	return cleanOrigin(v) != ""
}

// peerAddrOK: the home network (or this machine), or anywhere when remote is allowed.
func peerAddrOK(ip string, remote bool) bool {
	switch netOf(ip) {
	case "local", "lan":
		return true
	case "internet":
		return remote
	}
	return false
}

var errNotHome = errors.New("that address isn't on your home network")

type peerError struct {
	Status  int
	Code    string
	Message string
}

func (e *peerError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "the other server answered " + strconv.Itoa(e.Status)
}

// peerClient talks HTTPS to another NoCap server. With pin, only that
// certificate is accepted; without, any is, and the one seen goes to *seen
// (shown to the administrators, and pinned from then on). Every address it
// dials must pass peerAddrOK.
func peerClient(pin string, seen *string, remote bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil || !peerAddrOK(host, remote) {
				return errNotHome
			}
			return nil
		},
	}
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		DisableKeepAlives:   true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// Self-signed certificates rarely name a LAN address: the pin
			// below is the check.
			InsecureSkipVerify: true, //nolint:gosec
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("the other server showed no certificate")
				}
				fp := certFingerprint(cs.PeerCertificates[0].Raw)
				if seen != nil {
					*seen = fp
				}
				if pin != "" && !strings.EqualFold(fp, pin) {
					return errors.New("the other server's certificate changed since it was pinned")
				}
				return nil
			},
		},
	}
	return &http.Client{
		Transport:     tr,
		Timeout:       squadPeerTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// peerCall sends body (JSON) to base+path and reads the JSON answer into out.
func peerCall(ctx context.Context, c *http.Client, method, base, path, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", peerAuthPrefix+token)
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, squadMaxBody))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		pe := &peerError{Status: res.StatusCode}
		var eb struct {
			Error errorBody `json:"error"`
		}
		if json.Unmarshal(data, &eb) == nil {
			pe.Code, pe.Message = eb.Error.Code, eb.Error.Message
		}
		return pe
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// peerBase checks an address another server can be reached at: https, a
// host and port, nothing else.
func peerBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return "", errors.New("Use the other server's https:// address.")
	}
	return "https://" + u.Host, nil
}

func (s *Server) sealPeerToken(tok string) ([]byte, bool) {
	if s.Secrets != nil {
		return s.Secrets.Seal(tok), true
	}
	return []byte(tok), false
}

func (s *Server) openPeerToken(l *store.ServerLink) (string, error) {
	if !l.Sealed {
		return string(l.TheirToken), nil
	}
	if s.Secrets == nil {
		return "", errors.New("no secret box to open the token")
	}
	return s.Secrets.Open(l.TheirToken)
}

// validPeerToken: a token another server handed over (base64url, sane size).
func validPeerToken(t string) bool {
	if len(t) < 20 || len(t) > 100 {
		return false
	}
	for _, r := range t {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func validFingerprint(fp string) bool {
	if len(fp) != 64 {
		return false
	}
	_, err := hex.DecodeString(fp)
	return err == nil
}

// ownPort is the port other servers reach this one on; "" when this server
// doesn't serve HTTPS itself (linking needs it).
func (s *Server) ownPort() string {
	if !s.Config.TLSEnabled() {
		return ""
	}
	port, _ := listenPort(s.Config.ListenAddr)
	return port
}

func (s *Server) squadNotify(key, title, body, gzTitle, gzBody string) {
	if s.notify == nil {
		return
	}
	_, _ = s.notify.Add(context.Background(), notify.Notification{
		Key: key, Kind: notify.KindSecurity, Level: "info", Title: title, Body: body,
		GenZ: &notify.Variant{Title: gzTitle, Body: gzBody},
		Icon: "settings", Action: &notify.Action{App: "settings", Props: map[string]any{"section": "devices"}},
		CreatedAt: time.Now(),
	})
}

// ---------- administrators: the squad ----------

type squadSelf struct {
	NocapID     string `json:"nocap_id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
	CanLink     bool   `json:"can_link"` // serves HTTPS itself
}

type linkedServerJSON struct {
	NocapID     string     `json:"nocap_id"`
	Name        string     `json:"name"`
	URL         string     `json:"url"`
	Fingerprint string     `json:"fingerprint"`
	Version     string     `json:"version"`
	Online      bool       `json:"online"`
	LastSeen    *time.Time `json:"last_seen,omitempty"`
	LinkedBy    string     `json:"linked_by"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (s *Server) squadSelf() squadSelf {
	id, _ := s.nocapID()
	return squadSelf{NocapID: id, Name: s.serverName(), Version: s.Version, Fingerprint: s.ownFingerprint(), CanLink: s.ownPort() != ""}
}

// squadList: this server, its linked servers (with whether they answer
// now) and the link requests both ways.
func (s *Server) squadList(w http.ResponseWriter, r *http.Request) {
	links, err := s.Store.ListServerLinks(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.refreshPeers(r.Context(), links)
	q := s.squadInit()
	q.mu.Lock()
	q.prune()
	linked := make([]linkedServerJSON, 0, len(links))
	for _, l := range links {
		j := linkedServerJSON{NocapID: l.NocapID, Name: l.Name, URL: l.URL, Fingerprint: l.Fingerprint, Version: l.Version,
			Online: q.status[l.NocapID].Online, LinkedBy: l.LinkedBy, CreatedAt: l.CreatedAt}
		if !l.LastSeen.IsZero() {
			t := l.LastSeen
			j.LastSeen = &t
		}
		linked = append(linked, j)
	}
	in, out := asksSorted(q.in), asksSorted(q.out)
	q.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"self": s.squadSelf(), "linked": linked, "incoming": in, "outgoing": out})
}

func asksSorted(m map[string]*serverAsk) []serverAsk {
	out := make([]serverAsk, 0, len(m))
	for _, a := range m {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// refreshPeers asks linked servers that weren't checked lately whether
// they're there (all at once, briefly).
func (s *Server) refreshPeers(ctx context.Context, links []*store.ServerLink) {
	q := s.squadInit()
	q.mu.Lock()
	now := q.now()
	var due []*store.ServerLink
	for _, l := range links {
		if st, ok := q.status[l.NocapID]; !ok || now.Sub(st.At) > squadStatusKeep {
			due = append(due, l)
		}
	}
	q.mu.Unlock()
	if len(due) == 0 {
		return
	}
	remote := s.remoteAllowed(ctx)
	var wg sync.WaitGroup
	for _, l := range due {
		wg.Add(1)
		go func(l *store.ServerLink) {
			defer wg.Done()
			online := s.pingPeer(ctx, l, remote)
			q.mu.Lock()
			q.status[l.NocapID] = peerStatus{Online: online, At: q.now()}
			q.mu.Unlock()
		}(l)
	}
	wg.Wait()
}

type peerHello struct {
	NocapID string `json:"nocap_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (s *Server) pingPeer(ctx context.Context, l *store.ServerLink, remote bool) bool {
	tok, err := s.openPeerToken(l)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, squadPingTimeout)
	defer cancel()
	var out peerHello
	if err := peerCall(ctx, peerClient(l.Fingerprint, nil, remote), http.MethodGet, l.URL, "/api/v1/squad/peer", tok, nil, &out); err != nil || out.NocapID != l.NocapID {
		return false
	}
	name, ver := clip(out.Name, 64), clip(out.Version, 32)
	if name == "" {
		name = l.Name
	}
	_ = s.Store.TouchServerLink(context.Background(), l.NocapID, name, ver, time.Now())
	l.Name, l.Version, l.LastSeen = name, ver, time.Now()
	return true
}

// squadSend: an administrator asks another NoCap server to link.
func (s *Server) squadSend(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req struct {
		URL     string `json:"url"`
		NocapID string `json:"nocap_id"`
	}
	if !decodeJSONLimit(w, r, &req, 8<<10) {
		return
	}
	base, err := peerBase(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	if !validNocapID(req.NocapID) {
		writeError(w, http.StatusBadRequest, "validation", "That isn't a NoCap ID.")
		return
	}
	self := s.squadSelf()
	if strings.EqualFold(req.NocapID, self.NocapID) {
		writeError(w, http.StatusBadRequest, "validation", "That's this server.")
		return
	}
	port := s.ownPort()
	if port == "" {
		writeError(w, http.StatusConflict, "needs_https", "Linking servers needs this server to serve HTTPS itself.")
		return
	}
	q := s.squadInit()
	if !q.asks.Allow("send:" + u.ID) {
		w.Header().Set("Retry-After", "20")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many link requests. Wait a little and try again.")
		return
	}
	token := randomHex(24)
	var seen string
	remote := s.remoteAllowed(r.Context())
	c := peerClient("", &seen, remote)
	var resp struct {
		RequestID   string    `json:"request_id"`
		NocapID     string    `json:"nocap_id"`
		Name        string    `json:"name"`
		Version     string    `json:"version"`
		Fingerprint string    `json:"fingerprint"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	body := map[string]any{
		"nocap_id": self.NocapID, "name": self.Name, "version": self.Version, "port": port,
		"token": token, "fingerprint": self.Fingerprint,
	}
	err = peerCall(r.Context(), c, http.MethodPost, base, "/api/v1/squad/hello", "", body, &resp)
	if err != nil {
		s.audit(r, u.ID, "squad.request", req.NocapID, false, err.Error())
		msg := "Couldn't reach that server: " + err.Error()
		var pe *peerError
		if errors.As(err, &pe) && pe.Message != "" {
			msg = pe.Message
		}
		writeError(w, http.StatusBadGateway, "unreachable", msg)
		return
	}
	if resp.NocapID != req.NocapID {
		s.audit(r, u.ID, "squad.request", req.NocapID, false, "a different server answered")
		writeError(w, http.StatusBadGateway, "wrong_server", "A different server answered at that address.")
		return
	}
	if resp.Fingerprint != "" && !strings.EqualFold(resp.Fingerprint, seen) {
		s.audit(r, u.ID, "squad.request", req.NocapID, false, "certificate mismatch")
		writeError(w, http.StatusBadGateway, "certificate", "The certificate that server reports isn't the one it showed. Something between you may be listening in.")
		return
	}
	host, _ := url.Parse(base)
	now := q.now()
	a := &serverAsk{
		ID: randomHex(8), NocapID: resp.NocapID, Name: clip(resp.Name, 64), URL: base, Address: host.Hostname(),
		Version: clip(resp.Version, 32), Fingerprint: seen, State: "pending", By: u.Username,
		CreatedAt: now, ExpiresAt: now.Add(squadAskTTL), token: token,
	}
	q.mu.Lock()
	q.prune()
	for id, o := range q.out {
		if o.NocapID == a.NocapID {
			delete(q.out, id)
		}
	}
	q.out[a.ID] = a
	out := *a
	q.mu.Unlock()
	s.audit(r, u.ID, "squad.request", a.Name+" ("+a.NocapID+")", true, "fingerprint "+seen)
	writeJSON(w, http.StatusOK, out)
}

// ---------- another server asks to link (no sign-in) ----------

func (s *Server) squadHello(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.Config.TrustedProxies)
	q := s.squadInit()
	if !q.asks.Allow("hello:" + ip) {
		w.Header().Set("Retry-After", "20")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many link requests from that address. Wait a little and try again.")
		return
	}
	if !peerAddrOK(ip, s.remoteAllowed(r.Context())) {
		writeError(w, http.StatusForbidden, "not_home", "This server only takes link requests from its home network.")
		return
	}
	var req struct {
		NocapID     string `json:"nocap_id"`
		Name        string `json:"name"`
		Version     string `json:"version"`
		Port        string `json:"port"`
		Token       string `json:"token"`
		Fingerprint string `json:"fingerprint"`
	}
	if !decodeJSONLimit(w, r, &req, 8<<10) {
		return
	}
	port, perr := strconv.Atoi(req.Port)
	if !validNocapID(req.NocapID) || perr != nil || port < 1 || port > 65535 || len(req.Token) < 32 || len(req.Token) > 100 ||
		(req.Fingerprint != "" && !validFingerprint(req.Fingerprint)) {
		writeError(w, http.StatusBadRequest, "validation", "That link request isn't complete.")
		return
	}
	self := s.squadSelf()
	if strings.EqualFold(req.NocapID, self.NocapID) {
		writeError(w, http.StatusBadRequest, "validation", "A server can't link to itself.")
		return
	}
	if !self.CanLink {
		writeError(w, http.StatusConflict, "needs_https", "That server doesn't serve HTTPS itself, so it can't link.")
		return
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation", "Couldn't tell where that request came from.")
		return
	}
	// Call back where the request came from (never an address it names),
	// and remember the certificate it shows.
	back := originFor("https", addr.Unmap(), strconv.Itoa(port))
	var seen string
	var info pairInfo
	ctx, cancel := context.WithTimeout(r.Context(), squadPeerTimeout)
	defer cancel()
	if err := peerCall(ctx, peerClient("", &seen, s.remoteAllowed(r.Context())), http.MethodGet, back, "/api/v1/pair/info", "", nil, &info); err != nil {
		s.audit(r, "", "squad.request_in", req.NocapID, false, "couldn't call back: "+err.Error())
		writeError(w, http.StatusBadRequest, "no_callback", "This server couldn't reach yours back over HTTPS.")
		return
	}
	if info.NocapID != req.NocapID || (req.Fingerprint != "" && !strings.EqualFold(req.Fingerprint, seen)) {
		s.audit(r, "", "squad.request_in", req.NocapID, false, "callback didn't match")
		writeError(w, http.StatusBadRequest, "mismatch", "The server that answered back isn't the one asking (or its certificate differs).")
		return
	}
	name := clip(strings.TrimSpace(req.Name), 64)
	if name == "" {
		name = clip(info.Name, 64)
	}
	now := q.now()
	a := &serverAsk{
		ID: randomHex(8), NocapID: req.NocapID, Name: name, URL: back, Address: addr.Unmap().String(),
		Version: clip(req.Version, 32), Fingerprint: seen, State: "pending",
		CreatedAt: now, ExpiresAt: now.Add(squadAskTTL), token: req.Token,
	}
	q.mu.Lock()
	q.prune()
	for id, o := range q.in {
		if o.NocapID == a.NocapID {
			delete(q.in, id)
		}
	}
	if len(q.in) >= squadMaxIncoming {
		q.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "busy", "This server has too many link requests waiting. Try again later.")
		return
	}
	q.in[a.ID] = a
	q.mu.Unlock()
	s.audit(r, "", "squad.request_in", name+" ("+a.NocapID+")", true, "from "+a.Address+", fingerprint "+seen)
	s.squadNotify("squad:"+a.ID, name+" wants to link",
		"A NoCap server at "+a.Address+" ("+a.NocapID+") asked to link with this one. Accept or decline it in Settings → Your Squad.",
		name+" wants to link up",
		"NoCap server at "+a.Address+" ("+a.NocapID+") wants in on your squad. Accept or pass in Settings → Your Squad.")
	writeJSON(w, http.StatusAccepted, map[string]any{
		"request_id": a.ID, "nocap_id": self.NocapID, "name": self.Name, "version": self.Version,
		"fingerprint": self.Fingerprint, "expires_at": a.ExpiresAt,
	})
}

// takeAsk marks a pending request busy so two clicks can't both act on it.
func (q *squadState) takeAsk(m map[string]*serverAsk, id string) (*serverAsk, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.prune()
	a := m[id]
	if a == nil || a.busy || a.State != "pending" {
		return nil, false
	}
	a.busy = true
	return a, true
}

func (q *squadState) release(a *serverAsk) {
	q.mu.Lock()
	a.busy = false
	q.mu.Unlock()
}

// squadAccept: an administrator accepts another server's request.
func (s *Server) squadAccept(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	q := s.squadInit()
	a, ok := q.takeAsk(q.in, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That link request is gone (it may have expired).")
		return
	}
	defer q.release(a)
	self := s.squadSelf()
	mine, mineHash := devices.NewToken()
	var resp struct {
		NocapID string `json:"nocap_id"`
		Token   string `json:"token"`
	}
	body := map[string]any{"request_token": a.token, "nocap_id": self.NocapID, "accept": true, "token": mine, "name": self.Name, "version": self.Version}
	err := peerCall(r.Context(), peerClient(a.Fingerprint, nil, s.remoteAllowed(r.Context())), http.MethodPost, a.URL, "/api/v1/squad/answer", "", body, &resp)
	if err == nil && (resp.NocapID != a.NocapID || !validPeerToken(resp.Token)) {
		err = errors.New("the other server's answer didn't make sense")
	}
	if err != nil {
		s.audit(r, u.ID, "squad.accept", a.Name+" ("+a.NocapID+")", false, err.Error())
		writeError(w, http.StatusBadGateway, "unreachable", "Couldn't finish linking with "+a.Name+": "+err.Error())
		return
	}
	sealed, isSealed := s.sealPeerToken(resp.Token)
	l := &store.ServerLink{
		NocapID: a.NocapID, Name: a.Name, URL: a.URL, Fingerprint: a.Fingerprint, TheirToken: sealed, Sealed: isSealed,
		OurTokenHash: mineHash, Version: a.Version, LinkedBy: u.Username, CreatedAt: time.Now(), LastSeen: time.Now(),
	}
	if err := s.Store.SaveServerLink(r.Context(), l); err != nil {
		s.internalError(w, r, err)
		return
	}
	q.mu.Lock()
	delete(q.in, a.ID)
	q.status[a.NocapID] = peerStatus{Online: true, At: q.now()}
	q.mu.Unlock()
	s.audit(r, u.ID, "squad.accept", a.Name+" ("+a.NocapID+")", true, "fingerprint "+a.Fingerprint)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// squadDecline: an administrator declines another server's request (and
// tells it), or drops one of this server's own.
func (s *Server) squadDecline(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	q := s.squadInit()
	id := r.PathValue("id")
	q.mu.Lock()
	q.prune()
	if o := q.out[id]; o != nil {
		delete(q.out, id)
		q.mu.Unlock()
		s.audit(r, u.ID, "squad.cancel", o.Name+" ("+o.NocapID+")", true, "")
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	a := q.in[id]
	if a == nil || a.busy {
		q.mu.Unlock()
		writeError(w, http.StatusNotFound, "not_found", "That link request is gone (it may have expired).")
		return
	}
	delete(q.in, id)
	q.mu.Unlock()
	// Best effort: the other side's request expires anyway.
	self := s.squadSelf()
	ctx, cancel := context.WithTimeout(r.Context(), squadPingTimeout)
	defer cancel()
	_ = peerCall(ctx, peerClient(a.Fingerprint, nil, s.remoteAllowed(r.Context())), http.MethodPost, a.URL, "/api/v1/squad/answer", "",
		map[string]any{"request_token": a.token, "nocap_id": self.NocapID, "accept": false}, nil)
	s.audit(r, u.ID, "squad.decline", a.Name+" ("+a.NocapID+")", true, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// squadAnswer: the server this one asked accepts or declines (no sign-in;
// the request's token proves who it is).
func (s *Server) squadAnswer(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.Config.TrustedProxies)
	q := s.squadInit()
	if !q.answers.Allow(ip) {
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many answers. Wait a little.")
		return
	}
	var req struct {
		RequestToken string `json:"request_token"`
		NocapID      string `json:"nocap_id"`
		Accept       bool   `json:"accept"`
		Token        string `json:"token"`
		Name         string `json:"name"`
		Version      string `json:"version"`
	}
	if !decodeJSONLimit(w, r, &req, 8<<10) {
		return
	}
	q.mu.Lock()
	q.prune()
	var a *serverAsk
	for _, o := range q.out {
		if o.NocapID == req.NocapID && o.State == "pending" && !o.busy && sameSecret(o.token, req.RequestToken) {
			a = o
		}
	}
	if a == nil {
		q.mu.Unlock()
		s.audit(r, "", "squad.answer", truncate(req.NocapID, 40), false, "no such request")
		writeError(w, http.StatusNotFound, "not_found", "There's no link request waiting for that answer.")
		return
	}
	if !req.Accept {
		a.State = "declined"
		q.mu.Unlock()
		s.audit(r, "", "squad.declined", a.Name+" ("+a.NocapID+")", true, "")
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if !validPeerToken(req.Token) {
		q.mu.Unlock()
		writeError(w, http.StatusBadRequest, "validation", "The answer has no usable token.")
		return
	}
	a.busy = true
	q.mu.Unlock()
	defer q.release(a)

	theirs, theirsHash := devices.NewToken()
	sealed, isSealed := s.sealPeerToken(req.Token)
	name := clip(strings.TrimSpace(req.Name), 64)
	if name == "" {
		name = a.Name
	}
	ver := clip(req.Version, 32)
	if ver == "" {
		ver = a.Version
	}
	l := &store.ServerLink{
		NocapID: a.NocapID, Name: name, URL: a.URL, Fingerprint: a.Fingerprint, TheirToken: sealed, Sealed: isSealed,
		OurTokenHash: theirsHash, Version: ver, LinkedBy: a.By, CreatedAt: time.Now(), LastSeen: time.Now(),
	}
	if err := s.Store.SaveServerLink(r.Context(), l); err != nil {
		s.internalError(w, r, err)
		return
	}
	q.mu.Lock()
	delete(q.out, a.ID)
	q.status[a.NocapID] = peerStatus{Online: true, At: q.now()}
	q.mu.Unlock()
	s.audit(r, "", "squad.accepted", name+" ("+a.NocapID+")", true, "fingerprint "+a.Fingerprint)
	s.squadNotify("squad-linked:"+a.NocapID, name+" linked with this server",
		name+" accepted the link request. It's in Settings → Your Squad → Linked servers.",
		name+" is in your squad now",
		name+" said yes. Find it in Settings → Your Squad → Linked servers.")
	self := s.squadSelf()
	writeJSON(w, http.StatusOK, map[string]string{"nocap_id": self.NocapID, "token": theirs})
}

// squadUnlink: an administrator unlinks another server (and tells it).
func (s *Server) squadUnlink(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	l, err := s.Store.ServerLink(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That server isn't linked (anymore).")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if tok, err := s.openPeerToken(l); err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), squadPingTimeout)
		_ = peerCall(ctx, peerClient(l.Fingerprint, nil, s.remoteAllowed(r.Context())), http.MethodDelete, l.URL, "/api/v1/squad/peer", tok, nil, nil)
		cancel()
	}
	if err := s.Store.DeleteServerLink(r.Context(), l.NocapID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	q := s.squadInit()
	q.mu.Lock()
	delete(q.status, l.NocapID)
	q.mu.Unlock()
	s.audit(r, u.ID, "squad.unlink", l.Name+" ("+l.NocapID+")", true, "")
	w.WriteHeader(http.StatusNoContent)
}

// ---------- linked servers calling ----------

type squadPeerKey struct{}

// peer requires "Authorization: NoCapServer <token>" from a linked server.
func (s *Server) peer(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), peerAuthPrefix)
		tok = strings.TrimSpace(tok)
		if !ok || !validPeerToken(tok) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "not a linked server")
			return
		}
		l, err := s.Store.ServerLinkByToken(r.Context(), devices.HashToken(tok))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.internalError(w, r, err)
				return
			}
			if s.limited(w, r) {
				return
			}
			writeError(w, http.StatusUnauthorized, "unauthorized", "not a linked server")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), squadPeerKey{}, l)))
	})
}

func peerFrom(ctx context.Context) *store.ServerLink {
	l, _ := ctx.Value(squadPeerKey{}).(*store.ServerLink)
	return l
}

// squadPeerHello: a linked server checks this one is there.
func (s *Server) squadPeerHello(w http.ResponseWriter, r *http.Request) {
	l := peerFrom(r.Context())
	q := s.squadInit()
	q.mu.Lock()
	q.status[l.NocapID] = peerStatus{Online: true, At: q.now()}
	q.mu.Unlock()
	_ = s.Store.TouchServerLink(r.Context(), l.NocapID, l.Name, l.Version, time.Now())
	self := s.squadSelf()
	writeJSON(w, http.StatusOK, peerHello{NocapID: self.NocapID, Name: self.Name, Version: self.Version})
}

// squadPeerUnlink: a linked server unlinked this one.
func (s *Server) squadPeerUnlink(w http.ResponseWriter, r *http.Request) {
	l := peerFrom(r.Context())
	if err := s.Store.DeleteServerLink(r.Context(), l.NocapID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	q := s.squadInit()
	q.mu.Lock()
	delete(q.status, l.NocapID)
	q.mu.Unlock()
	s.audit(r, "", "squad.unlinked", l.Name+" ("+l.NocapID+")", true, "by the other server")
	s.squadNotify("squad-unlinked:"+l.NocapID, l.Name+" unlinked from this server",
		l.Name+" ended the link. Send a new link request in Settings → Your Squad to link again.",
		l.Name+" left the squad",
		l.Name+" unlinked. Send a fresh link request in Settings → Your Squad to link back up.")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) squadRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/squad", s.admin(s.squadList))
	mux.Handle("POST /api/v1/squad/request", s.admin(s.squadSend))
	mux.Handle("POST /api/v1/squad/requests/{id}/accept", s.admin(s.squadAccept))
	mux.Handle("POST /api/v1/squad/requests/{id}/decline", s.admin(s.squadDecline))
	mux.Handle("DELETE /api/v1/squad/links/{id}", s.admin(s.squadUnlink))
	// Other servers (no sign-in; rate-limited, home network only).
	mux.HandleFunc("POST /api/v1/squad/hello", s.squadHello)
	mux.HandleFunc("POST /api/v1/squad/answer", s.squadAnswer)
	mux.Handle("GET /api/v1/squad/peer", s.peer(s.squadPeerHello))
	mux.Handle("DELETE /api/v1/squad/peer", s.peer(s.squadPeerUnlink))
	// Phones asking to link without a code.
	mux.HandleFunc("POST /api/v1/pair/ask", s.pairAsk)
	mux.HandleFunc("GET /api/v1/pair/ask/{id}", s.pairAskPoll)
	mux.HandleFunc("DELETE /api/v1/pair/ask/{id}", s.pairAskCancel)
	mux.Handle("GET /api/v1/squad/phone-asks", s.authed(s.phoneAsksList))
	mux.Handle("POST /api/v1/squad/phone-asks/{id}/accept", s.authed(s.phoneAskAccept))
	mux.Handle("POST /api/v1/squad/phone-asks/{id}/decline", s.authed(s.phoneAskDecline))
}
