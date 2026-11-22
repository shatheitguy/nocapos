package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/store"
)

// Failover pairs: two linked NoCap servers kept in sync.
//
// Turning it on: an administrator on A picks a linked server B (Settings →
// Your Squad → Make this my failover) and which of the two is primary for
// the first sync. A asks B over the link; an administrator on B accepts.
// Then the server that isn't primary takes the primary's shared data
// (accounts, sharing, apps, backups, settings, notes, linked devices; see
// store.SyncTables), tells the primary where both logs stand, and from
// then on each server reads the other's change log every few seconds and
// applies it: last writer wins, and records both changed before they
// synced are kept as conflicts for the administrators.
//
// Everything goes over the link: HTTPS to the pinned certificate, with the
// link's token. Secrets are opened by the sender, travel inside that TLS,
// and are sealed again with the receiver's own key.

const (
	failoverEvery     = 5 * time.Second  // how often each server reads the other's log
	failoverTimeout   = 60 * time.Second // one call to the other server
	failoverPage      = 500              // records per page of the log
	failoverPageBytes = 8 << 20          // about this much of values per page
	failoverMaxBody   = 48 << 20         // the most a page's answer may be
	failoverMaxPages  = 400              // pages per sync (more waits for the next one)
	failoverFailAfter = 3                // failed syncs in a row before the administrators hear about it
)

type failoverState struct {
	once sync.Once
	run  sync.Mutex    // one sync at a time
	kick chan struct{} // "Sync now"
	now  func() time.Time
	// sysAt: when the Linux accounts were last compared (failover_accounts.go).
	sysAt time.Time
}

func (s *Server) failoverInit() *failoverState {
	f := &s.failover
	f.once.Do(func() {
		f.kick = make(chan struct{}, 1)
		if f.now == nil {
			f.now = time.Now
		}
	})
	return f
}

// theFailover is this server's failover pair (nil when there is none).
func (s *Server) theFailover(ctx context.Context) (*store.FailoverPair, error) {
	pairs, err := s.Store.FailoverPairs(ctx)
	if err != nil || len(pairs) == 0 {
		return nil, err
	}
	return pairs[0], nil
}

// failoverClient talks to the other server of the pair: its pinned
// certificate, longer calls than a ping.
func (s *Server) failoverClient(ctx context.Context, l *store.ServerLink) (*http.Client, string, error) {
	tok, err := s.openPeerToken(l)
	if err != nil {
		return nil, "", err
	}
	c := peerClient(l.Fingerprint, nil, s.remoteAllowed(ctx))
	c.Timeout = failoverTimeout
	return c, tok, nil
}

func (s *Server) failoverCall(ctx context.Context, l *store.ServerLink, method, path string, body, out any) error {
	c, tok, err := s.failoverClient(ctx, l)
	if err != nil {
		return err
	}
	return peerCallMax(ctx, c, method, l.URL, path, tok, body, out, failoverMaxBody)
}

func (s *Server) sealSyncSecret(plain string) []byte {
	if s.Secrets != nil {
		return s.Secrets.Seal(plain)
	}
	return []byte(plain)
}

func (s *Server) openSyncSecret(b []byte) (string, error) {
	if s.Secrets != nil {
		return s.Secrets.Open(b)
	}
	return string(b), nil
}

// ---------- the sync ----------

// failoverLoop syncs the pair every few seconds (and at once on "Sync now").
func (s *Server) failoverLoop(ctx context.Context) {
	f := s.failoverInit()
	t := time.NewTicker(failoverEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-f.kick:
		}
		s.failoverSyncOnce(ctx, false)
		s.failoverTakeoverTick(ctx) // is the other server there; take over or stand back (failover_takeover.go)
	}
}

// failoverKick asks the loop to sync now.
func (s *Server) failoverKick() {
	f := s.failoverInit()
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// failoverSyncOnce runs one sync of the pair: the first sync on the server
// that isn't primary, or reading the other's log once it's active. force
// syncs even while paused ("Sync now").
func (s *Server) failoverSyncOnce(ctx context.Context, force bool) error {
	f := s.failoverInit()
	f.run.Lock()
	defer f.run.Unlock()
	p, err := s.theFailover(ctx)
	if err != nil || p == nil {
		return err
	}
	self, err := s.nocapID()
	if err != nil {
		return err
	}
	switch {
	case p.State == store.FailoverStarting && p.PrimaryID != self:
		err = s.failoverFirstSync(ctx, p, self)
	case p.State == store.FailoverActive && (!p.Paused || force):
		err = s.failoverPull(ctx, p, self)
	default:
		return nil
	}
	if err != nil {
		s.failoverFailed(ctx, p, err)
	}
	return err
}

type failoverReadResult struct {
	changes []store.SyncChange
	next    int64
	seen    int64
	more    bool
}

// failoverRead reads the other server's log after since, page by page.
func (s *Server) failoverRead(ctx context.Context, l *store.ServerLink, since int64) (*failoverReadResult, error) {
	schema, err := s.Store.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	out := &failoverReadResult{next: since}
	for page := 0; page < failoverMaxPages; page++ {
		var feed store.SyncFeed
		if err := s.failoverCall(ctx, l, http.MethodGet, "/api/v1/failover/peer/changes?since="+strconv.FormatInt(out.next, 10), nil, &feed); err != nil {
			return nil, err
		}
		if feed.Schema != schema {
			return nil, fmt.Errorf("%s runs a different version of NoCapOS (database %d, this one %d). Update both servers to the same version", l.Name, feed.Schema, schema)
		}
		if feed.Next < out.next {
			return nil, errors.New("the other server's change log went backwards")
		}
		out.changes = append(out.changes, feed.Changes...)
		out.next, out.seen, out.more = feed.Next, feed.Seen, feed.More
		if !feed.More {
			break
		}
	}
	return out, nil
}

// failoverFirstSync: this server takes the primary's shared data, then
// tells it where both logs stand.
func (s *Server) failoverFirstSync(ctx context.Context, p *store.FailoverPair, self string) error {
	l, err := s.Store.ServerLink(ctx, p.NocapID)
	if err != nil {
		return err
	}
	read, err := s.failoverRead(ctx, l, 0)
	if err != nil {
		return err
	}
	if read.more {
		return errors.New("the first sync is larger than one pass; it continues on the next try")
	}
	res, err := s.Store.ApplySyncChanges(ctx, read.changes, store.SyncApply{Self: self, Peer: p.NocapID, Force: true, Seal: s.sealSyncSecret})
	if err != nil {
		return err
	}
	seq, err := s.Store.SyncSeq(ctx)
	if err != nil {
		return err
	}
	if err := s.failoverCall(ctx, l, http.MethodPost, "/api/v1/failover/peer/ready", map[string]int64{"cursor": read.next, "seq": seq}, nil); err != nil {
		return err
	}
	now := s.failoverInit().now()
	p.State, p.TheirCursor, p.Acked, p.ActiveAt = store.FailoverActive, read.next, seq, now
	p.LastPull, p.LastOK, p.LastError, p.Failures = now, now, "", 0
	if err := s.Store.SaveFailoverPair(ctx, p); err != nil {
		return err
	}
	s.failoverApplied(res)
	if err := s.failoverSystemAccounts(ctx, l, self, true); err != nil {
		s.Log.Warn("failover: Linux accounts", "err", err)
	}
	s.auditCtx(ctx, "", "failover.first_sync", l.Name+" ("+l.NocapID+")", "", true, fmt.Sprintf("%d records from the primary", res.Applied))
	s.squadNotify("failover-ready:"+l.NocapID+":"+strconv.FormatInt(now.Unix(), 10), "Failover with "+l.Name+" is on",
		"This server now has "+l.Name+"'s accounts and settings, and both stay in sync. See Settings → Your Squad → Failover.",
		"Failover with "+l.Name+" is live",
		"Synced up with "+l.Name+": accounts and settings match now and stay that way. Peek at Settings → Your Squad → Failover.")
	return nil
}

// failoverPull reads and applies what changed on the other server.
func (s *Server) failoverPull(ctx context.Context, p *store.FailoverPair, self string) error {
	l, err := s.Store.ServerLink(ctx, p.NocapID)
	if err != nil {
		return err
	}
	read, err := s.failoverRead(ctx, l, p.TheirCursor)
	if err != nil {
		return err
	}
	if read.seen > 0 {
		_ = s.Store.SetFailoverAcked(ctx, p.NocapID, read.seen)
	}
	// Fresh: the other server may have read this one's log meanwhile.
	if cur, err := s.Store.FailoverPair(ctx, p.NocapID); err == nil {
		p = cur
	}
	res := &store.SyncResult{}
	if len(read.changes) > 0 {
		res, err = s.Store.ApplySyncChanges(ctx, read.changes, store.SyncApply{Self: self, Peer: p.NocapID, Acked: p.Acked, Seal: s.sealSyncSecret})
		if err != nil {
			return err
		}
	}
	now := s.failoverInit().now()
	if err := s.Store.NoteFailoverSync(ctx, p.NocapID, true, read.next, "", now); err != nil {
		return err
	}
	if p.Failures >= failoverFailAfter {
		s.auditCtx(ctx, "", "failover.sync_back", l.Name+" ("+l.NocapID+")", "", true, "")
	}
	s.failoverApplied(res)
	if err := s.failoverSystemAccounts(ctx, l, self, false); err != nil {
		s.Log.Warn("failover: Linux accounts", "err", err)
	}
	if res.Conflicts > 0 {
		s.auditCtx(ctx, "", "failover.conflict", l.Name+" ("+l.NocapID+")", "", true, fmt.Sprintf("%d records changed on both servers", res.Conflicts))
		s.squadNotify("failover-conflict:"+l.NocapID+":"+strconv.FormatInt(now.UnixNano(), 10), "Sync conflict with "+l.Name,
			"The same thing was changed on both servers before they synced. The newest change was kept; choose which one stays in Settings → Your Squad → Failover.",
			"Sync clash with "+l.Name,
			"Both servers changed the same thing at once. Newest one won for now; pick the keeper in Settings → Your Squad → Failover.")
	}
	return nil
}

func (s *Server) failoverFailed(ctx context.Context, p *store.FailoverPair, err error) {
	msg := err.Error()
	var pe *peerError
	if errors.As(err, &pe) && pe.Message != "" {
		msg = pe.Message
	}
	_ = s.Store.NoteFailoverSync(ctx, p.NocapID, false, 0, truncate(msg, 300), s.failoverInit().now())
	if p.Failures+1 == failoverFailAfter {
		name := p.NocapID
		if l, err := s.Store.ServerLink(ctx, p.NocapID); err == nil && l.Name != "" {
			name = l.Name
		}
		s.auditCtx(ctx, "", "failover.sync_failing", name+" ("+p.NocapID+")", "", false, truncate(msg, 300))
		s.squadNotify("failover-fail:"+p.NocapID+":"+strconv.FormatInt(time.Now().Unix(), 10), "Failover sync with "+name+" isn't working",
			"This server couldn't sync with "+name+" for a while: "+truncate(msg, 160)+". See Settings → Your Squad → Failover.",
			"Failover sync with "+name+" is stuck",
			"Can't sync with "+name+" right now: "+truncate(msg, 160)+". Check Settings → Your Squad → Failover.")
	}
}

// failoverApplied puts what changed to work: network shares follow the
// shared folders and accounts.
func (s *Server) failoverApplied(res *store.SyncResult) {
	if res == nil || len(res.Tables) == 0 {
		return
	}
	if s.NetDrives != nil && (res.Tables["shared_folders"] || res.Tables["share_permissions"] || res.Tables["users"] || res.Tables["server_settings"]) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := s.NetDrives.ApplySamba(ctx); err != nil {
				s.Log.Warn("failover: update network shares", "err", err)
			}
		}()
	}
}

// ---------- administrators ----------

type failoverCategoryJSON struct {
	ID         string     `json:"id"`
	Queued     int        `json:"queued"`
	LastChange *time.Time `json:"last_change,omitempty"`
}

type failoverPairJSON struct {
	NocapID    string                 `json:"nocap_id"`
	Name       string                 `json:"name"`
	URL        string                 `json:"url"`
	Online     bool                   `json:"online"`
	State      string                 `json:"state"`
	Primary    string                 `json:"primary"` // self | peer: whose data the first sync keeps
	Paused     bool                   `json:"paused"`
	AskedBy    string                 `json:"asked_by,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	ActiveAt   *time.Time             `json:"active_at,omitempty"`
	LastPull   *time.Time             `json:"last_pull,omitempty"`
	LastOK     *time.Time             `json:"last_ok,omitempty"`
	LastError  string                 `json:"last_error,omitempty"`
	Failures   int                    `json:"failures"`
	Queued     int                    `json:"queued"` // this server's changes the other hasn't read yet
	Conflicts  int                    `json:"conflicts"`
	Categories []failoverCategoryJSON `json:"categories"`
	Files      *failoverFilesJSON     `json:"files,omitempty"`
	Takeover   *takeoverJSON          `json:"takeover,omitempty"`
}

// failoverGet: the pair (if any), how each category stands, and what
// turning it on would take.
func (s *Server) failoverGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbSize, _ := s.Store.DatabaseSize(ctx)
	out := map[string]any{"pair": nil, "categories": store.SyncCategories, "db_bytes": dbSize}
	p, err := s.theFailover(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if p != nil {
		j, err := s.failoverPairJSON(ctx, p)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		out["pair"] = j
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) failoverPairJSON(ctx context.Context, p *store.FailoverPair) (*failoverPairJSON, error) {
	self, _ := s.nocapID()
	j := &failoverPairJSON{NocapID: p.NocapID, State: p.State, Paused: p.Paused, AskedBy: p.AskedBy, CreatedAt: p.CreatedAt,
		ActiveAt: timePtr(p.ActiveAt), LastPull: timePtr(p.LastPull), LastOK: timePtr(p.LastOK), LastError: p.LastError, Failures: p.Failures,
		Primary: "peer", Categories: []failoverCategoryJSON{}}
	if p.PrimaryID == self {
		j.Primary = "self"
	}
	if l, err := s.Store.ServerLink(ctx, p.NocapID); err == nil {
		j.Name, j.URL = l.Name, l.URL
		s.refreshPeers(ctx, []*store.ServerLink{l})
		q := s.squadInit()
		q.mu.Lock()
		j.Online = q.status[l.NocapID].Online
		q.mu.Unlock()
	}
	if p.State == store.FailoverActive {
		cats, err := s.Store.SyncStatus(ctx, p.Acked)
		if err != nil {
			return nil, err
		}
		for _, c := range cats {
			j.Queued += c.Queued
			j.Categories = append(j.Categories, failoverCategoryJSON{ID: c.ID, Queued: c.Queued, LastChange: timePtr(c.LastChange)})
		}
		n, err := s.Store.CountSyncConflicts(ctx)
		if err != nil {
			return nil, err
		}
		j.Conflicts = n
		if j.Files, err = s.failoverFilesJSON(ctx, p); err != nil {
			return nil, err
		}
		j.Takeover = s.takeoverJSON(ctx, p)
	}
	return j, nil
}

func (s *Server) linkedOr404(w http.ResponseWriter, r *http.Request, id string) *store.ServerLink {
	l, err := s.Store.ServerLink(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That server isn't linked (anymore).")
		return nil
	}
	if err != nil {
		s.internalError(w, r, err)
		return nil
	}
	return l
}

// failoverRequest: an administrator asks a linked server to be this one's
// failover, saying which of the two is primary for the first sync.
func (s *Server) failoverRequest(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req struct {
		NocapID string `json:"nocap_id"`
		Primary string `json:"primary"` // self | peer
	}
	if !decodeJSONLimit(w, r, &req, 4<<10) {
		return
	}
	if req.Primary != "self" && req.Primary != "peer" {
		writeError(w, http.StatusBadRequest, "validation", "Choose which server is primary for the first sync.")
		return
	}
	l := s.linkedOr404(w, r, req.NocapID)
	if l == nil {
		return
	}
	if p, err := s.theFailover(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	} else if p != nil {
		writeError(w, http.StatusConflict, "has_failover", "This server already has a failover pair. Turn it off first.")
		return
	}
	self, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	primary := self
	if req.Primary == "peer" {
		primary = l.NocapID
	}
	err = s.failoverCall(r.Context(), l, http.MethodPost, "/api/v1/failover/peer/ask", map[string]string{"primary_id": primary, "by": u.Username}, nil)
	if err != nil {
		s.audit(r, u.ID, "failover.request", l.Name+" ("+l.NocapID+")", false, err.Error())
		msg := "Couldn't reach " + l.Name + ": " + err.Error()
		var pe *peerError
		if errors.As(err, &pe) && pe.Message != "" {
			msg = pe.Message
		}
		writeError(w, http.StatusBadGateway, "unreachable", msg)
		return
	}
	p := &store.FailoverPair{NocapID: l.NocapID, State: store.FailoverAsked, PrimaryID: primary, AskedBy: u.Username, CreatedAt: time.Now()}
	if err := s.Store.SaveFailoverPair(r.Context(), p); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "failover.request", l.Name+" ("+l.NocapID+")", true, "primary for the first sync: "+primaryName(primary, self, l))
	s.failoverGet(w, r)
}

func primaryName(primary, self string, l *store.ServerLink) string {
	if primary == self {
		return "this server"
	}
	return l.Name + " (" + l.NocapID + ")"
}

// failoverAccept: an administrator accepts the other server's request.
func (s *Server) failoverAccept(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	p, err := s.theFailover(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if p == nil || p.State != store.FailoverAsking {
		writeError(w, http.StatusNotFound, "not_found", "There's no failover request waiting.")
		return
	}
	l := s.linkedOr404(w, r, p.NocapID)
	if l == nil {
		return
	}
	if err := s.failoverCall(r.Context(), l, http.MethodPost, "/api/v1/failover/peer/accept", map[string]string{"by": u.Username}, nil); err != nil {
		s.audit(r, u.ID, "failover.accept", l.Name+" ("+l.NocapID+")", false, err.Error())
		writeError(w, http.StatusBadGateway, "unreachable", "Couldn't reach "+l.Name+": "+err.Error())
		return
	}
	if err := s.failoverStart(r.Context(), p); err != nil {
		s.internalError(w, r, err)
		return
	}
	self, _ := s.nocapID()
	s.audit(r, u.ID, "failover.accept", l.Name+" ("+l.NocapID+")", true, "primary for the first sync: "+primaryName(p.PrimaryID, self, l))
	s.failoverKick()
	s.failoverGet(w, r)
}

// failoverStart: both administrators said yes; the first sync begins.
func (s *Server) failoverStart(ctx context.Context, p *store.FailoverPair) error {
	p.State = store.FailoverStarting
	if err := s.Store.SaveFailoverPair(ctx, p); err != nil {
		return err
	}
	return s.Store.SetServerLinkRole(ctx, p.NocapID, store.RoleLinkFailover)
}

// failoverOff: an administrator declines a request, cancels one, or turns
// failover off (the other server is told; it stops too).
func (s *Server) failoverOff(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	p, err := s.theFailover(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, "not_found", "This server has no failover pair.")
		return
	}
	name := p.NocapID
	if l, err := s.Store.ServerLink(r.Context(), p.NocapID); err == nil {
		name = l.Name + " (" + l.NocapID + ")"
		ctx, cancel := context.WithTimeout(r.Context(), squadPeerTimeout)
		_ = s.failoverCall(ctx, l, http.MethodDelete, "/api/v1/failover/peer", nil, nil) // best effort: it may be down
		cancel()
	}
	if err := s.failoverEnd(r.Context(), p.NocapID); err != nil {
		s.internalError(w, r, err)
		return
	}
	action := "failover.off"
	switch p.State {
	case store.FailoverAsking:
		action = "failover.decline"
	case store.FailoverAsked:
		action = "failover.cancel"
	}
	s.audit(r, u.ID, action, name, true, "")
	s.failoverGet(w, r)
}

func (s *Server) failoverEnd(ctx context.Context, nocapID string) error {
	f := s.failoverInit()
	f.run.Lock() // not in the middle of a sync
	defer f.run.Unlock()
	if err := s.Store.DeleteFailoverPair(ctx, nocapID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := s.Store.FileIndex().ClearFileIndex(ctx); err != nil {
		return err
	}
	// Each server goes back to running its own apps as it is now; the shared
	// address is left.
	_ = s.setFlag(ctx, keyFailoverActive, false)
	_ = s.setFlag(ctx, keyFailoverYielded, false)
	s.applyVIP(ctx, failoverOptions{}, false)
	s.discovery.bump()
	return s.Store.SetServerLinkRole(ctx, nocapID, store.RoleLinkPeer)
}

// failoverPause stops or restarts syncing on this server.
func (s *Server) failoverPause(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req struct {
		Paused bool `json:"paused"`
	}
	if !decodeJSONLimit(w, r, &req, 1<<10) {
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
	p.Paused = req.Paused
	if err := s.Store.SaveFailoverPair(r.Context(), p); err != nil {
		s.internalError(w, r, err)
		return
	}
	action := "failover.resume"
	if req.Paused {
		action = "failover.pause"
	} else {
		s.failoverKick()
	}
	s.audit(r, u.ID, action, p.NocapID, true, "")
	s.failoverGet(w, r)
}

// failoverSyncNow syncs at once (even while paused) and says how it went.
func (s *Server) failoverSyncNow(w http.ResponseWriter, r *http.Request) {
	p, err := s.theFailover(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if p == nil || (p.State != store.FailoverActive && p.State != store.FailoverStarting) {
		writeError(w, http.StatusNotFound, "not_found", "Failover isn't on.")
		return
	}
	go func() { _ = s.failoverFilesOnce(context.WithoutCancel(r.Context()), true) }() // files take their time
	if err := s.failoverSyncOnce(r.Context(), true); err != nil {
		msg := err.Error()
		var pe *peerError
		if errors.As(err, &pe) && pe.Message != "" {
			msg = pe.Message
		}
		writeError(w, http.StatusBadGateway, "sync_failed", "Couldn't sync: "+msg)
		return
	}
	s.failoverGet(w, r)
}

// conflictJSON shows a conflict without secrets: only plain values, and
// none from columns that hold passwords, keys or tokens.
type conflictJSON struct {
	ID        int64             `json:"id"`
	Table     string            `json:"table"`
	Category  string            `json:"category"`
	Key       string            `json:"key"`
	Mine      map[string]string `json:"mine"`   // nil: deleted here
	Theirs    map[string]string `json:"theirs"` // nil: deleted there
	Winner    string            `json:"winner"`
	Note      string            `json:"note,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

func hiddenColumn(c string) bool {
	for _, w := range []string{"hash", "secret", "password", "token", "_enc", "key", "config", "data", "body"} {
		if strings.Contains(c, w) {
			return true
		}
	}
	return false
}

func plainRow(row store.SyncRow) map[string]string {
	if row == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range row {
		if hiddenColumn(k) || (v.T != "s" && v.T != "i" && v.T != "f") {
			continue
		}
		out[k] = clip(v.V, 120)
	}
	return out
}

func (s *Server) failoverConflicts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.SyncConflicts(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]conflictJSON, 0, len(list))
	for _, c := range list {
		key := strings.Trim(c.PK, "[]")
		out = append(out, conflictJSON{ID: c.ID, Table: c.Table, Category: c.Category, Key: clip(key, 80),
			Mine: plainRow(c.Mine), Theirs: plainRow(c.Theirs), Winner: c.Winner, Note: c.Note, CreatedAt: c.CreatedAt})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	writeJSON(w, http.StatusOK, out)
}

// failoverResolve keeps one version of a conflict ("mine" or "theirs").
func (s *Server) failoverResolve(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such conflict.")
		return
	}
	var req struct {
		Keep string `json:"keep"`
	}
	if !decodeJSONLimit(w, r, &req, 1<<10) {
		return
	}
	if req.Keep != "mine" && req.Keep != "theirs" {
		writeError(w, http.StatusBadRequest, "validation", `Keep "mine" or "theirs".`)
		return
	}
	c, err := s.Store.ResolveSyncConflict(r.Context(), id, req.Keep == "mine")
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That conflict was already settled.")
		return
	}
	if err != nil {
		s.audit(r, u.ID, "failover.resolve", strconv.FormatInt(id, 10), false, err.Error())
		writeError(w, http.StatusConflict, "resolve_failed", "Couldn't keep that version: "+err.Error())
		return
	}
	s.audit(r, u.ID, "failover.resolve", c.Table+" "+clip(c.PK, 80), true, "kept "+req.Keep)
	s.failoverKick()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- the other server of the pair calling ----------

// failoverPeerAsk: a linked server asks this one to be its failover pair.
func (s *Server) failoverPeerAsk(w http.ResponseWriter, r *http.Request) {
	l := peerFrom(r.Context())
	var req struct {
		PrimaryID string `json:"primary_id"`
		By        string `json:"by"`
	}
	if !decodeJSONLimit(w, r, &req, 4<<10) {
		return
	}
	self, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if req.PrimaryID != self && req.PrimaryID != l.NocapID {
		writeError(w, http.StatusBadRequest, "validation", "The primary must be one of the two servers.")
		return
	}
	if p, err := s.theFailover(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	} else if p != nil && p.NocapID != l.NocapID {
		writeError(w, http.StatusConflict, "has_failover", s.serverName()+" already has a failover pair.")
		return
	} else if p != nil && p.State != store.FailoverAsking {
		writeError(w, http.StatusConflict, "has_failover", "These two servers already are a failover pair.")
		return
	}
	by := clip(strings.TrimSpace(req.By), 64)
	p := &store.FailoverPair{NocapID: l.NocapID, State: store.FailoverAsking, PrimaryID: req.PrimaryID, AskedBy: by, CreatedAt: time.Now()}
	if err := s.Store.SaveFailoverPair(r.Context(), p); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "", "failover.request_in", l.Name+" ("+l.NocapID+")", true, "asked by "+by+"; primary for the first sync: "+primaryName(req.PrimaryID, self, l))
	s.squadNotify("failover-ask:"+l.NocapID+":"+strconv.FormatInt(time.Now().Unix(), 10), l.Name+" asks to be a failover pair",
		l.Name+" wants this server and it to stay in sync as a failover pair. Review and accept or decline it in Settings → Your Squad → Failover.",
		l.Name+" wants to be your failover buddy",
		l.Name+" wants to sync up with this server as a failover pair. Check it out in Settings → Your Squad → Failover.")
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// failoverPeerAccept: the server this one asked said yes.
func (s *Server) failoverPeerAccept(w http.ResponseWriter, r *http.Request) {
	l := peerFrom(r.Context())
	var req struct {
		By string `json:"by"`
	}
	if !decodeJSONLimit(w, r, &req, 4<<10) {
		return
	}
	p, err := s.Store.FailoverPair(r.Context(), l.NocapID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.State != store.FailoverAsked) {
		writeError(w, http.StatusNotFound, "not_found", "This server didn't ask for that.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.failoverStart(r.Context(), p); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "", "failover.accepted", l.Name+" ("+l.NocapID+")", true, "accepted by "+clip(req.By, 64))
	s.squadNotify("failover-accepted:"+l.NocapID+":"+strconv.FormatInt(time.Now().Unix(), 10), l.Name+" accepted failover",
		"The first sync between this server and "+l.Name+" is starting. Follow it in Settings → Your Squad → Failover.",
		l.Name+" is in for failover",
		"First sync with "+l.Name+" is kicking off. Watch it in Settings → Your Squad → Failover.")
	s.failoverKick()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// failoverPeerReady: the other server finished the first sync and says
// where both logs stand: cursor is how far it read this one's, seq where
// its own is (this server starts reading there).
func (s *Server) failoverPeerReady(w http.ResponseWriter, r *http.Request) {
	l := peerFrom(r.Context())
	var req struct {
		Cursor int64 `json:"cursor"`
		Seq    int64 `json:"seq"`
	}
	if !decodeJSONLimit(w, r, &req, 1<<10) {
		return
	}
	self, _ := s.nocapID()
	p, err := s.Store.FailoverPair(r.Context(), l.NocapID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (p.State != store.FailoverStarting || p.PrimaryID != self)) {
		writeError(w, http.StatusConflict, "not_starting", "This server isn't waiting for a first sync.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	now := s.failoverInit().now()
	p.State, p.TheirCursor, p.Acked, p.ActiveAt = store.FailoverActive, req.Seq, req.Cursor, now
	p.LastOK, p.LastPull, p.LastError, p.Failures = now, now, "", 0
	if err := s.Store.SaveFailoverPair(r.Context(), p); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "", "failover.on", l.Name+" ("+l.NocapID+")", true, "first sync done")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// failoverPeerOff: the other server declined, cancelled or turned failover off.
func (s *Server) failoverPeerOff(w http.ResponseWriter, r *http.Request) {
	l := peerFrom(r.Context())
	p, err := s.Store.FailoverPair(r.Context(), l.NocapID)
	if errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.failoverEnd(r.Context(), l.NocapID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "", "failover.off_by_peer", l.Name+" ("+l.NocapID+")", true, "was "+p.State)
	title, body, gzTitle, gzBody := "Failover with "+l.Name+" is off",
		l.Name+" turned failover off. The two servers no longer sync; each keeps what it has.",
		"Failover with "+l.Name+" is off",
		l.Name+" turned off failover. No more syncing; both keep what they've got."
	if p.State == store.FailoverAsked {
		title, body, gzTitle, gzBody = l.Name+" declined failover", l.Name+" declined the failover request.", l.Name+" passed on failover", l.Name+" said no to the failover request."
	}
	s.squadNotify("failover-off:"+l.NocapID+":"+strconv.FormatInt(time.Now().Unix(), 10), title, body, gzTitle, gzBody)
	w.WriteHeader(http.StatusNoContent)
}

// failoverPeerChanges: the other server reads this one's change log.
func (s *Server) failoverPeerChanges(w http.ResponseWriter, r *http.Request) {
	l := peerFrom(r.Context())
	p, err := s.Store.FailoverPair(r.Context(), l.NocapID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.State != store.FailoverActive && p.State != store.FailoverStarting) {
		writeError(w, http.StatusForbidden, "not_paired", "These servers aren't a failover pair.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if err != nil || since < 0 {
		writeError(w, http.StatusBadRequest, "validation", "since must be a number")
		return
	}
	if p.State == store.FailoverActive && since > 0 {
		_ = s.Store.SetFailoverAcked(r.Context(), l.NocapID, since)
	}
	self, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	feed, err := s.Store.SyncChanges(r.Context(), since, self, l.NocapID, failoverPage, failoverPageBytes, s.openSyncSecret)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	feed.Seen = p.TheirCursor
	writeJSON(w, http.StatusOK, feed)
}

// failoverPeerStatus: the other server checks on this one.
func (s *Server) failoverPeerStatus(w http.ResponseWriter, r *http.Request) {
	l := peerFrom(r.Context())
	p, err := s.Store.FailoverPair(r.Context(), l.NocapID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"state": ""})
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	seq, _ := s.Store.SyncSeq(r.Context())
	host, _ := os.Hostname()
	writeJSON(w, http.StatusOK, peerFailoverStatus{State: p.State, Seq: seq, Paused: p.Paused, Active: s.flag(r.Context(), keyFailoverActive), Hostname: host,
		Running: s.runsApps(r.Context(), p)})
}

func (s *Server) failoverRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/failover", s.admin(s.failoverGet))
	mux.Handle("POST /api/v1/failover/request", s.admin(s.failoverRequest))
	mux.Handle("POST /api/v1/failover/accept", s.admin(s.failoverAccept))
	mux.Handle("DELETE /api/v1/failover", s.admin(s.failoverOff))
	mux.Handle("POST /api/v1/failover/pause", s.admin(s.failoverPause))
	mux.Handle("POST /api/v1/failover/sync", s.admin(s.failoverSyncNow))
	mux.Handle("GET /api/v1/failover/conflicts", s.admin(s.failoverConflicts))
	mux.Handle("POST /api/v1/failover/conflicts/{id}", s.admin(s.failoverResolve))
	mux.Handle("PUT /api/v1/failover/files", s.admin(s.failoverSetFiles))
	mux.Handle("PUT /api/v1/failover/takeover", s.admin(s.failoverSetTakeover))
	mux.Handle("POST /api/v1/failover/take-over", s.admin(s.failoverTakeOverNow))
	mux.Handle("POST /api/v1/failover/hand-back", s.admin(s.failoverHandBack))
	failoverPeerRoutes(s, mux)
}

// failoverPeerRoutes: what the other server of the pair calls (its link
// token, over HTTPS to the certificate it pinned).
func failoverPeerRoutes(s *Server, mux *http.ServeMux) {
	mux.Handle("POST /api/v1/failover/peer/ask", s.peer(s.failoverPeerAsk))
	mux.Handle("POST /api/v1/failover/peer/accept", s.peer(s.failoverPeerAccept))
	mux.Handle("POST /api/v1/failover/peer/ready", s.peer(s.failoverPeerReady))
	mux.Handle("DELETE /api/v1/failover/peer", s.peer(s.failoverPeerOff))
	mux.Handle("GET /api/v1/failover/peer/changes", s.peer(s.failoverPeerChanges))
	mux.Handle("GET /api/v1/failover/peer/status", s.peer(s.failoverPeerStatus))
	mux.Handle("GET /api/v1/failover/peer/system-accounts", s.peer(s.failoverPeerAccounts))
	mux.Handle("POST /api/v1/failover/peer/options", s.peer(s.failoverPeerOptions))
	mux.Handle("POST /api/v1/failover/peer/handback", s.peer(s.failoverPeerHandBack))
	mux.Handle("GET /api/v1/failover/peer/files/changes", s.peer(s.failoverPeerFileChanges))
	mux.Handle("GET /api/v1/failover/peer/files/file", s.peer(s.failoverPeerFile))
}
