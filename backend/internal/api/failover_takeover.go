package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/store"
)

// Failover pairs: taking over. The pair's primary (the server whose data
// won the first sync) runs the apps; the other one stands by with the same
// settings and files. Each checks on the other every few seconds. When the
// primary hasn't answered for the time the administrators chose (5 minutes
// by default) and automatic take-over is on, the standby takes over: it
// starts the apps (deploying any it doesn't have yet, on their synced
// data), says so on the network (its mDNS record names the server it
// stands in for), and, if set up, takes the pair's shared address (a
// virtual IP on the LAN, Linux only). When the primary comes back it sees
// that and stands back (stops its apps, leaves the shared address) so only
// one runs them; both keep syncing. An administrator then hands back: the
// standby stops its apps, sends the last changes, and the primary syncs
// them in and starts its apps again.
//
// A network split (both up, neither reaching the other) makes both run the
// apps until it heals; changes on both sides then meet as conflicts. Turn
// automatic take-over off where that isn't acceptable.

const (
	takeoverDefaultMin = 5
	takeoverPeerUp     = 30 * time.Second   // the other server answered within this: it's up
	keyFailoverActive  = "failover.active"  // "1": this standby took over
	keyFailoverYielded = "failover.yielded" // "1": this primary stood back
)

// takeoverIn is what the decision looks at.
type takeoverIn struct {
	Primary    bool          // this server is the pair's primary
	Active     bool          // this standby took over
	Yielded    bool          // this primary stood back
	PeerUp     bool          // the other server answers
	PeerDown   time.Duration // how long it hasn't
	PeerActive bool          // the other server says it took over
	Auto       bool
	After      time.Duration
}

type takeoverAct string

const (
	actNone     takeoverAct = ""
	actTakeOver takeoverAct = "take_over" // standby: the primary is gone
	actYield    takeoverAct = "yield"     // primary: the standby took over meanwhile
	actBack     takeoverAct = "back"      // standby: the primary is back (hand back when ready)
)

// decideTakeover is the pair's state machine (one step).
func decideTakeover(in takeoverIn) takeoverAct {
	if in.Primary {
		if in.PeerUp && in.PeerActive && !in.Yielded {
			return actYield
		}
		return actNone
	}
	switch {
	case !in.Active && !in.PeerUp && in.Auto && in.After > 0 && in.PeerDown >= in.After:
		return actTakeOver
	case in.Active && in.PeerUp:
		return actBack
	}
	return actNone
}

type takeoverState struct {
	mu          sync.Mutex
	lastContact time.Time // the other server last answered
	since       time.Time // when this server started watching
	peerActive  bool
	peerRunning bool
	peerHost    string
	backNoted   bool // "the primary is back" was said
	busy        bool // a take-over or hand-back runs
}

func (s *Server) takeover() *takeoverState { return &s.failoverTake }

func (s *Server) flag(ctx context.Context, key string) bool {
	v, _ := s.Store.Setting(ctx, key)
	return v == "1"
}

func (s *Server) setFlag(ctx context.Context, key string, on bool) error {
	v := ""
	if on {
		v = "1"
	}
	return s.Store.SetSetting(ctx, key, v)
}

func (o failoverOptions) auto() bool { return o.TakeoverAuto == nil || *o.TakeoverAuto }

func (o failoverOptions) after() time.Duration {
	m := o.TakeoverMin
	if m <= 0 {
		m = takeoverDefaultMin
	}
	return time.Duration(m) * time.Minute
}

// runsApps: does this server run the pair's apps now.
func (s *Server) runsApps(ctx context.Context, p *store.FailoverPair) bool {
	self, _ := s.nocapID()
	if p.PrimaryID == self {
		return !s.flag(ctx, keyFailoverYielded)
	}
	return s.flag(ctx, keyFailoverActive)
}

// failoverTakeoverTick checks on the other server and acts (from the sync loop).
func (s *Server) failoverTakeoverTick(ctx context.Context) {
	p, err := s.theFailover(ctx)
	if err != nil || p == nil || p.State != store.FailoverActive {
		return
	}
	self, err := s.nocapID()
	if err != nil {
		return
	}
	l, err := s.Store.ServerLink(ctx, p.NocapID)
	if err != nil {
		return
	}
	t := s.takeover()
	now := s.failoverInit().now()
	var st peerFailoverStatus
	cctx, cancel := context.WithTimeout(ctx, squadPeerTimeout)
	err = s.failoverCall(cctx, l, http.MethodGet, "/api/v1/failover/peer/status", nil, &st)
	cancel()
	t.mu.Lock()
	if t.since.IsZero() {
		t.since = now
	}
	if err == nil {
		t.lastContact, t.peerActive, t.peerRunning = now, st.Active, st.Running
		if st.Hostname != "" {
			t.peerHost = st.Hostname
		}
	}
	last := t.lastContact
	if last.IsZero() {
		last = t.since
	}
	peerActive, busy := t.peerActive, t.busy
	t.mu.Unlock()
	if busy {
		return
	}
	o := parseFailoverOptions(p.Options)
	in := takeoverIn{
		Primary: p.PrimaryID == self, Active: s.flag(ctx, keyFailoverActive), Yielded: s.flag(ctx, keyFailoverYielded),
		PeerUp: now.Sub(last) < takeoverPeerUp, PeerDown: now.Sub(last), PeerActive: peerActive,
		Auto: o.auto(), After: o.after(),
	}
	s.applyVIP(ctx, o, (in.Primary && !in.Yielded) || (!in.Primary && in.Active))
	switch decideTakeover(in) {
	case actTakeOver:
		s.takeOver(ctx, l, fmt.Sprintf("%s hasn't answered for %s", l.Name, in.PeerDown.Round(time.Second)), "")
	case actYield:
		s.standBack(ctx, l)
	case actBack:
		t.mu.Lock()
		noted := t.backNoted
		t.backNoted = true
		t.mu.Unlock()
		if !noted {
			s.auditCtx(ctx, "", "failover.primary_back", l.Name+" ("+l.NocapID+")", "", true, "")
			s.squadNotify("failover-back:"+l.NocapID+":"+strconv.FormatInt(now.Unix(), 10), l.Name+" is back",
				l.Name+" answers again and has stood back. This server still runs the apps; hand them back in Settings → Your Squad → Failover when you're ready.",
				l.Name+" is back online",
				l.Name+" is back and chilling. This server still runs the apps; hand them back in Settings → Your Squad → Failover when you're ready.")
		}
	}
}

// takeOver: this standby starts running the apps.
func (s *Server) takeOver(ctx context.Context, l *store.ServerLink, reason, by string) {
	t := s.takeover()
	t.mu.Lock()
	if t.busy {
		t.mu.Unlock()
		return
	}
	t.busy, t.backNoted = true, false
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.busy = false
		t.mu.Unlock()
	}()
	if err := s.setFlag(ctx, keyFailoverActive, true); err != nil {
		s.Log.Warn("failover: take over", "err", err)
		return
	}
	s.discovery.bump() // the mDNS record now names the server this one stands in for
	started, failed := s.runApps(ctx, true)
	detail := reason + "; apps started: " + strings.Join(started, ", ")
	if len(failed) > 0 {
		detail += "; didn't start: " + strings.Join(failed, ", ")
	}
	s.auditCtx(ctx, by, "failover.take_over", l.Name+" ("+l.NocapID+")", "", len(failed) == 0, detail)
	s.squadNotify("failover-takeover:"+l.NocapID+":"+strconv.FormatInt(time.Now().Unix(), 10), "This server took over from "+l.Name,
		reason+". This server now runs the apps with the synced data. See Settings → Your Squad → Failover.",
		"Took over from "+l.Name,
		reason+". This server's running the apps now, on the synced data. Peek at Settings → Your Squad → Failover.")
}

// standBack: this primary stops its apps while the standby runs them.
func (s *Server) standBack(ctx context.Context, l *store.ServerLink) {
	if err := s.setFlag(ctx, keyFailoverYielded, true); err != nil {
		return
	}
	stopped, _ := s.runApps(ctx, false)
	s.applyVIP(ctx, failoverOptions{}, false)
	s.auditCtx(ctx, "", "failover.stand_back", l.Name+" ("+l.NocapID+")", "", true, "apps stopped: "+strings.Join(stopped, ", "))
	s.squadNotify("failover-standback:"+l.NocapID+":"+strconv.FormatInt(time.Now().Unix(), 10), l.Name+" is running the apps",
		l.Name+" took over while this server was away, so this server stopped its apps. Hand back on "+l.Name+" when ready.",
		l.Name+" has the apps now",
		l.Name+" took over while this server was out, so the apps here are stopped. Hand back on "+l.Name+" when ready.")
}

// runApps starts (or stops) the installed apps; missing ones are deployed
// on start (the standby never ran them). Returns which did and didn't.
func (s *Server) runApps(ctx context.Context, start bool) (done, failed []string) {
	if s.AppStore == nil {
		return nil, nil
	}
	status, err := s.AppStore.Status(ctx)
	if err != nil {
		return nil, []string{err.Error()}
	}
	ids := make([]string, 0, len(status))
	for id := range status {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st := status[id]
		var err error
		switch {
		case start && st.Status == "missing":
			_, err = s.AppStore.Update(context.WithoutCancel(ctx), id, nil) // deploys it; runs in the background
		case start && st.Status != "running":
			err = s.AppStore.Control(ctx, id, "start")
		case !start && st.Status != "stopped" && st.Status != "missing":
			err = s.AppStore.Control(ctx, id, "stop")
		default:
			continue
		}
		if err != nil {
			failed = append(failed, id)
			s.Log.Warn("failover: app", "app", id, "start", start, "err", err)
			continue
		}
		done = append(done, id)
	}
	return done, failed
}

// ---------- the shared address ----------

var vipHeld struct {
	sync.Mutex
	cidr, iface string
}

// applyVIP holds (or leaves) the pair's shared address, when one is set up.
func (s *Server) applyVIP(ctx context.Context, o failoverOptions, hold bool) {
	vipHeld.Lock()
	defer vipHeld.Unlock()
	if !hold || o.VIP == "" {
		if vipHeld.cidr != "" {
			if err := vipRelease(ctx, vipHeld.cidr, vipHeld.iface); err != nil {
				s.Log.Warn("failover: leave the shared address", "err", err)
			}
			s.auditCtx(ctx, "", "failover.vip_release", vipHeld.cidr, "", true, "")
			vipHeld.cidr, vipHeld.iface = "", ""
		}
		return
	}
	if vipHeld.cidr == o.VIP && vipHeld.iface == o.VIPIface {
		return
	}
	if err := vipTake(ctx, o.VIP, o.VIPIface); err != nil {
		s.Log.Warn("failover: take the shared address", "err", err)
		return
	}
	vipHeld.cidr, vipHeld.iface = o.VIP, o.VIPIface
	s.auditCtx(ctx, "", "failover.vip_take", o.VIP+" on "+o.VIPIface, "", true, "")
}

// validVIP: an address with its network size on a LAN, and an interface name.
func validVIP(cidr, iface string) error {
	if cidr == "" && iface == "" {
		return nil
	}
	pfx, err := netip.ParsePrefix(cidr)
	if err != nil || !pfx.Addr().IsPrivate() || pfx.Bits() < 8 {
		return errors.New("The shared address is a private address with its network size, like 192.168.1.50/24.")
	}
	if iface == "" || len(iface) > 15 || strings.ContainsAny(iface, "/ \t\n") {
		return errors.New("Choose the network interface for the shared address, like eth0.")
	}
	return nil
}

// ---------- mDNS ----------

// failoverTXT: while this standby runs the apps, its mDNS record names the
// server it stands in for (phones looking for that one find this one).
func (s *Server) failoverTXT(ctx context.Context) []string {
	if !s.flag(ctx, keyFailoverActive) {
		return nil
	}
	p, err := s.theFailover(ctx)
	if err != nil || p == nil {
		return nil
	}
	return []string{"stands_in_for=" + p.NocapID}
}

// ---------- administrators ----------

type takeoverJSON struct {
	Role        string     `json:"role"`    // primary | standby
	Running     bool       `json:"running"` // this server runs the apps now
	PeerRunning bool       `json:"peer_running"`
	PeerUp      bool       `json:"peer_up"`
	LastContact *time.Time `json:"last_contact,omitempty"`
	Auto        bool       `json:"auto"`
	AfterMin    int        `json:"after_min"`
	VIP         string     `json:"vip"`
	VIPIface    string     `json:"vip_iface"`
	VIPHeld     bool       `json:"vip_held"`
	CanHandBack bool       `json:"can_hand_back"`
	Busy        bool       `json:"busy"`
}

func (s *Server) takeoverJSON(ctx context.Context, p *store.FailoverPair) *takeoverJSON {
	self, _ := s.nocapID()
	o := parseFailoverOptions(p.Options)
	t := s.takeover()
	t.mu.Lock()
	last, peerRunning, busy := t.lastContact, t.peerRunning, t.busy
	t.mu.Unlock()
	now := s.failoverInit().now()
	j := &takeoverJSON{Role: "standby", Auto: o.auto(), AfterMin: int(o.after() / time.Minute), VIP: o.VIP, VIPIface: o.VIPIface,
		LastContact: timePtr(last), PeerUp: !last.IsZero() && now.Sub(last) < takeoverPeerUp, Busy: busy}
	active, yielded := s.flag(ctx, keyFailoverActive), s.flag(ctx, keyFailoverYielded)
	if p.PrimaryID == self {
		j.Role, j.Running = "primary", !yielded
	} else {
		j.Running = active
		j.CanHandBack = active && j.PeerUp
	}
	j.PeerRunning = peerRunning && j.PeerUp
	vipHeld.Lock()
	j.VIPHeld = vipHeld.cidr != ""
	vipHeld.Unlock()
	return j
}

// failoverSetTakeover: automatic take-over, after how long, the shared address.
func (s *Server) failoverSetTakeover(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req struct {
		Auto     bool   `json:"auto"`
		AfterMin int    `json:"after_min"`
		VIP      string `json:"vip"`
		VIPIface string `json:"vip_iface"`
	}
	if !decodeJSONLimit(w, r, &req, 4<<10) {
		return
	}
	req.VIP, req.VIPIface = strings.TrimSpace(req.VIP), strings.TrimSpace(req.VIPIface)
	if req.AfterMin < 1 || req.AfterMin > 24*60 {
		writeError(w, http.StatusBadRequest, "validation", "Wait between 1 minute and 24 hours.")
		return
	}
	if err := validVIP(req.VIP, req.VIPIface); err != nil {
		writeError(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	p, err := s.theFailover(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if p == nil || p.State != store.FailoverActive {
		writeError(w, http.StatusNotFound, "not_found", "Failover isn't on.")
		return
	}
	o := parseFailoverOptions(p.Options)
	auto := req.Auto
	o.TakeoverAuto, o.TakeoverMin, o.VIP, o.VIPIface = &auto, req.AfterMin, req.VIP, req.VIPIface
	if err := s.saveFailoverOptions(r.Context(), p, o, true); err != nil {
		writeError(w, http.StatusBadGateway, "unreachable", "Couldn't tell the other server: "+err.Error())
		return
	}
	s.audit(r, u.ID, "failover.takeover_settings", p.NocapID, true, fmt.Sprintf("automatic %v after %d min; shared address %q", auto, req.AfterMin, req.VIP))
	s.failoverGet(w, r)
}

// failoverTakeOverNow: an administrator on the standby takes over by hand
// (the primary stands back when it sees it).
func (s *Server) failoverTakeOverNow(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	p, l, ok := s.standbyPair(w, r)
	if !ok {
		return
	}
	if s.flag(r.Context(), keyFailoverActive) {
		writeError(w, http.StatusConflict, "already", "This server already runs the apps.")
		return
	}
	s.takeOver(r.Context(), l, "Taken over by "+u.Username, u.ID)
	s.applyVIP(r.Context(), parseFailoverOptions(p.Options), true)
	s.failoverGet(w, r)
}

func (s *Server) standbyPair(w http.ResponseWriter, r *http.Request) (*store.FailoverPair, *store.ServerLink, bool) {
	p, err := s.theFailover(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return nil, nil, false
	}
	self, _ := s.nocapID()
	if p == nil || p.State != store.FailoverActive || p.PrimaryID == self {
		writeError(w, http.StatusConflict, "not_standby", "Only the standby server takes over and hands back.")
		return nil, nil, false
	}
	l := s.linkedOr404(w, r, p.NocapID)
	if l == nil {
		return nil, nil, false
	}
	return p, l, true
}

// failoverHandBack: an administrator on the standby hands the apps back:
// stop them here, send the last changes, the primary syncs and starts them.
func (s *Server) failoverHandBack(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	_, l, ok := s.standbyPair(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if !s.flag(ctx, keyFailoverActive) {
		writeError(w, http.StatusConflict, "not_running", "This server isn't running the apps.")
		return
	}
	t := s.takeover()
	t.mu.Lock()
	if t.busy {
		t.mu.Unlock()
		writeError(w, http.StatusConflict, "busy", "A take-over or hand-back is already running.")
		return
	}
	t.busy = true
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.busy = false
		t.mu.Unlock()
	}()
	stopped, _ := s.runApps(ctx, false)
	// The primary pulls everything (settings and files of the stopped apps)
	// before it starts its apps again.
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
	defer cancel()
	if err := s.failoverCall(hctx, l, http.MethodPost, "/api/v1/failover/peer/handback", map[string]string{"by": u.Username}, nil); err != nil {
		s.runApps(ctx, true) // still ours: keep them running
		s.audit(r, u.ID, "failover.hand_back", l.Name+" ("+l.NocapID+")", false, err.Error())
		writeError(w, http.StatusBadGateway, "unreachable", "Couldn't hand back to "+l.Name+": "+err.Error())
		return
	}
	_ = s.setFlag(ctx, keyFailoverActive, false)
	s.applyVIP(ctx, failoverOptions{}, false)
	s.discovery.bump()
	t.mu.Lock()
	t.peerActive, t.backNoted = false, false
	t.mu.Unlock()
	s.audit(r, u.ID, "failover.hand_back", l.Name+" ("+l.NocapID+")", true, "apps stopped here: "+strings.Join(stopped, ", "))
	s.failoverGet(w, r)
}

// ---------- the other server calling ----------

type peerFailoverStatus struct {
	State    string `json:"state"`
	Seq      int64  `json:"seq"`
	Paused   bool   `json:"paused"`
	Active   bool   `json:"active"`  // took over (standby) and runs the apps
	Running  bool   `json:"running"` // runs the apps (a primary that didn't stand back, or an active standby)
	Hostname string `json:"hostname,omitempty"`
}

// failoverPeerHandBack: the standby hands the apps back to this primary.
func (s *Server) failoverPeerHandBack(w http.ResponseWriter, r *http.Request) {
	l, p, ok := s.activePeerPair(w, r)
	if !ok {
		return
	}
	self, _ := s.nocapID()
	if p.PrimaryID != self {
		writeError(w, http.StatusConflict, "not_primary", "This server isn't the primary.")
		return
	}
	var req struct {
		By string `json:"by"`
	}
	if !decodeJSONLimit(w, r, &req, 1<<10) {
		return
	}
	ctx := r.Context()
	// Everything the standby changed while it ran the apps, first.
	if err := s.failoverSyncOnce(ctx, true); err != nil {
		writeError(w, http.StatusServiceUnavailable, "sync_failed", "Couldn't sync before taking the apps back: "+err.Error())
		return
	}
	if err := s.failoverFilesOnce(ctx, true); err != nil {
		writeError(w, http.StatusServiceUnavailable, "sync_failed", "Couldn't sync the files before taking the apps back: "+err.Error())
		return
	}
	_ = s.setFlag(ctx, keyFailoverYielded, false)
	t := s.takeover()
	t.mu.Lock()
	t.peerActive = false
	t.mu.Unlock()
	started, failed := s.runApps(ctx, true)
	s.applyVIP(ctx, parseFailoverOptions(p.Options), true)
	s.audit(r, "", "failover.handed_back", l.Name+" ("+l.NocapID+")", len(failed) == 0, "by "+clip(req.By, 64)+"; apps started: "+strings.Join(started, ", "))
	s.squadNotify("failover-handedback:"+l.NocapID+":"+strconv.FormatInt(time.Now().Unix(), 10), "The apps are back on this server",
		l.Name+" handed the apps back after syncing everything it changed. This server runs them again.",
		"Apps are back home",
		l.Name+" handed the apps back, all synced up. This server runs them again.")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
