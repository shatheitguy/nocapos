package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/store"
)

// linkNodes links a and b the way administrators do (request, accept).
func linkNodes(t *testing.T, a, b *squadNode) {
	t.Helper()
	if rec := a.send(t, b); rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	in := b.list(t).Incoming
	if len(in) != 1 {
		t.Fatalf("incoming: %+v", in)
	}
	rec := squadCall(b.w.s, b.w.s.squadAccept, "POST", "/", nil, callOpts{as: b.w.users["boss"], path: map[string]string{"id": in[0].ID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
}

type failoverResp struct {
	Pair       *failoverPairJSON `json:"pair"`
	Categories []string          `json:"categories"`
	DBBytes    int64             `json:"db_bytes"`
}

func (n *squadNode) failover(t *testing.T, as *store.User) failoverResp {
	t.Helper()
	rec := squadCall(n.w.s, n.w.s.failoverGet, "GET", "/api/v1/failover", nil, callOpts{as: as})
	var out failoverResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("failover get: %d %s", rec.Code, rec.Body)
	}
	return out
}

func (n *squadNode) syncOnce(t *testing.T) {
	t.Helper()
	if err := n.w.s.failoverSyncOnce(context.Background(), false); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

func addUser(t *testing.T, st *store.Store, name string) *store.User {
	t.Helper()
	u := &store.User{ID: store.NewID(), Username: name, PasswordHash: "x", Role: store.RoleUser, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

// pairNodes links a and b and makes b a's failover with a primary; returns
// a's administrator (the account both servers share after the first sync).
func pairNodes(t *testing.T, a, b *squadNode) *store.User {
	t.Helper()
	ctx := context.Background()
	linkNodes(t, a, b)
	boss := a.w.users["boss"]
	rec := squadCall(a.w.s, a.w.s.failoverRequest, "POST", "/", map[string]string{"nocap_id": b.id, "primary": "self"}, callOpts{as: boss})
	if rec.Code != http.StatusOK {
		t.Fatalf("request: %d %s", rec.Code, rec.Body)
	}
	if p := b.failover(t, b.w.users["boss"]).Pair; p == nil || p.State != store.FailoverAsking || p.Primary != "peer" || p.AskedBy != "boss" {
		t.Fatalf("B's pair: %+v", p)
	}
	rec = squadCall(b.w.s, b.w.s.failoverAccept, "POST", "/", nil, callOpts{as: b.w.users["boss"]})
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	// B takes A's data; A hears it's done.
	b.syncOnce(t)
	for _, n := range []*squadNode{a, b} {
		p, err := n.w.st.FailoverPair(ctx, map[*squadNode]string{a: b.id, b: a.id}[n])
		if err != nil || p.State != store.FailoverActive {
			t.Fatalf("pair not active: %+v %v", p, err)
		}
		l, _ := n.w.st.ServerLink(ctx, p.NocapID)
		if l.Role != store.RoleLinkFailover {
			t.Fatalf("link role %q", l.Role)
		}
	}
	return boss
}

// TestFailoverPairEndToEnd: two servers on localhost over their pinned
// HTTPS link: request, accept, first sync, changes both ways, conflicts,
// turning it off.
func TestFailoverPairEndToEnd(t *testing.T) {
	ctx := context.Background()
	a, b := newSquadNode(t), newSquadNode(t)
	onlyA := addUser(t, a.w.st, "only-a")
	onlyB := addUser(t, b.w.st, "only-b")
	boss := pairNodes(t, a, b)

	// The first sync: B has A's accounts (same IDs), not its own.
	if u, err := b.w.st.UserByUsername(ctx, "only-a"); err != nil || u.ID != onlyA.ID {
		t.Fatalf("only-a on B: %+v %v", u, err)
	}
	if _, err := b.w.st.UserByUsername(ctx, "only-b"); err != store.ErrNotFound {
		t.Fatalf("only-b still on B: %v (%s)", err, onlyB.ID)
	}
	if u, err := b.w.st.UserByUsername(ctx, "boss"); err != nil || u.ID != boss.ID {
		t.Fatalf("boss on B: %+v %v", u, err)
	}

	// A change on A reaches B, and one on B reaches A.
	addUser(t, a.w.st, "new-on-a")
	b.syncOnce(t)
	if _, err := b.w.st.UserByUsername(ctx, "new-on-a"); err != nil {
		t.Fatalf("new-on-a on B: %v", err)
	}
	if err := b.w.st.SetSetting(ctx, "general.server_name_test", "from-b"); err != nil {
		t.Fatal(err)
	}
	addUser(t, b.w.st, "new-on-b")
	a.syncOnce(t)
	if _, err := a.w.st.UserByUsername(ctx, "new-on-b"); err != nil {
		t.Fatalf("new-on-b on A: %v", err)
	}
	if v, _ := a.w.st.Setting(ctx, "general.server_name_test"); v != "from-b" {
		t.Fatalf("setting on A: %q", v)
	}
	// Settings each server keeps for itself don't travel.
	if err := a.w.st.SetSetting(ctx, linkRemoteKey, "https://a.example"); err != nil {
		t.Fatal(err)
	}
	b.syncOnce(t)
	if v, _ := b.w.st.Setting(ctx, linkRemoteKey); v != "" {
		t.Fatalf("local setting travelled: %q", v)
	}
	// Nothing bounces back: A reading B now finds none of its own changes.
	a.syncOnce(t)
	b.syncOnce(t)
	if p := a.failover(t, boss).Pair; p.Queued != 0 || p.Conflicts != 0 || p.State != store.FailoverActive || !p.Online {
		t.Fatalf("A after the round trip: %+v", p)
	}

	// Both change the same account before syncing: the newer wins on both,
	// and each side keeps a conflict to settle.
	u, _ := a.w.st.UserByUsername(ctx, "new-on-a")
	if err := a.w.st.SetRole(ctx, u.ID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	// B changes it twice: its clock is ahead, so B's version wins.
	for range 2 {
		if err := b.w.st.SetDisabled(ctx, u.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	a.syncOnce(t)
	b.syncOnce(t)
	ua, _ := a.w.st.UserByUsername(ctx, "new-on-a")
	ub, _ := b.w.st.UserByUsername(ctx, "new-on-a")
	if ua.Role != ub.Role || ua.Disabled != ub.Disabled {
		t.Fatalf("diverged: A %s/%v B %s/%v", ua.Role, ua.Disabled, ub.Role, ub.Disabled)
	}
	rec := squadCall(a.w.s, a.w.s.failoverConflicts, "GET", "/", nil, callOpts{as: boss})
	var conflicts []conflictJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &conflicts); err != nil || len(conflicts) != 1 || conflicts[0].Table != "users" {
		t.Fatalf("conflicts on A: %s", rec.Body)
	}
	if _, ok := conflicts[0].Mine["password_hash"]; ok {
		t.Fatal("a conflict shows a password hash")
	}
	// Keep A's own version: it becomes a new change B takes too.
	rec = squadCall(a.w.s, a.w.s.failoverResolve, "POST", "/", map[string]string{"keep": "mine"}, callOpts{as: boss, path: map[string]string{"id": jsonNum(conflicts[0].ID)}})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
	}
	b.syncOnce(t)
	ub, _ = b.w.st.UserByUsername(ctx, "new-on-a")
	if ub.Role != store.RoleAdmin || ub.Disabled {
		t.Fatalf("kept version not on B: %s/%v", ub.Role, ub.Disabled)
	}

	// Pause: no sync; Sync now syncs anyway.
	rec = squadCall(b.w.s, b.w.s.failoverPause, "POST", "/", map[string]bool{"paused": true}, callOpts{as: boss})
	if rec.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", rec.Code, rec.Body)
	}
	addUser(t, a.w.st, "while-paused")
	b.syncOnce(t)
	if _, err := b.w.st.UserByUsername(ctx, "while-paused"); err != store.ErrNotFound {
		t.Fatal("synced while paused")
	}
	rec = squadCall(b.w.s, b.w.s.failoverSyncNow, "POST", "/", nil, callOpts{as: boss})
	if rec.Code != http.StatusOK {
		t.Fatalf("sync now: %d %s", rec.Code, rec.Body)
	}
	if _, err := b.w.st.UserByUsername(ctx, "while-paused"); err != nil {
		t.Fatal("Sync now didn't sync")
	}

	// Turn it off on B: both sides stop, the links stay.
	rec = squadCall(b.w.s, b.w.s.failoverOff, "DELETE", "/", nil, callOpts{as: boss})
	if rec.Code != http.StatusOK {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}
	for _, c := range []struct {
		n  *squadNode
		id string
	}{{a, b.id}, {b, a.id}} {
		if _, err := c.n.w.st.FailoverPair(ctx, c.id); err != store.ErrNotFound {
			t.Fatalf("pair still there: %v", err)
		}
		if l, err := c.n.w.st.ServerLink(ctx, c.id); err != nil || l.Role != store.RoleLinkPeer {
			t.Fatalf("link after off: %+v %v", l, err)
		}
	}
	// Audit trail: every enable/disable step.
	for _, c := range []struct {
		n       *squadNode
		actions []string
		who     string
	}{{a, []string{"failover.request", "failover.resolve"}, boss.ID}, {b, []string{"failover.accept"}, b.w.users["boss"].ID}, {b, []string{"failover.pause", "failover.off"}, boss.ID}} {
		for _, want := range c.actions {
			if n, err := c.n.w.st.CountAudit(ctx, c.who, want); err != nil || n == 0 {
				t.Errorf("audit missing %s (%v)", want, err)
			}
		}
	}
}

func jsonNum(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// TestFailoverPeerAuth: only a linked server's token reaches the peer
// routes, only a paired one reads the change log, and only administrators
// reach the rest.
func TestFailoverPeerAuth(t *testing.T) {
	a, b := newSquadNode(t), newSquadNode(t)
	linkNodes(t, a, b)
	mux := http.NewServeMux()
	failoverPeerRoutes(b.w.s, mux)

	call := func(token, method, path string) int {
		r := httptest.NewRequest(method, path, nil)
		if token != "" {
			r.Header.Set("Authorization", peerAuthPrefix+token)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec.Code
	}
	if c := call("", "GET", "/api/v1/failover/peer/changes?since=0"); c != http.StatusUnauthorized {
		t.Fatalf("no token: %d", c)
	}
	if c := call(strings.Repeat("y", 43), "GET", "/api/v1/failover/peer/changes?since=0"); c != http.StatusUnauthorized {
		t.Fatalf("stranger: %d", c)
	}
	la, _ := a.w.st.ServerLink(context.Background(), b.id)
	tok, err := a.w.s.openPeerToken(la)
	if err != nil {
		t.Fatal(err)
	}
	// Linked, but not a failover pair: no change log, no "ready".
	if c := call(tok, "GET", "/api/v1/failover/peer/changes?since=0"); c != http.StatusForbidden {
		t.Fatalf("unpaired changes: %d", c)
	}
	if c := call(tok, "POST", "/api/v1/failover/peer/accept"); c != http.StatusNotFound && c != http.StatusBadRequest && c != http.StatusUnsupportedMediaType {
		t.Fatalf("accept nothing asked: %d", c)
	}

	// The administrator routes refuse anyone else.
	full := http.NewServeMux()
	b.w.s.failoverRoutes(full)
	for _, path := range []string{"/api/v1/failover", "/api/v1/failover/conflicts"} {
		r := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		full.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s signed out: %d", path, rec.Code)
		}
	}
}

func TestPlanSystemAccounts(t *testing.T) {
	e := func(name, hash string, changed int64) accounts.SystemEntry {
		return accounts.SystemEntry{Username: name, Role: store.RoleUser, Hash: hash, Changed: changed}
	}
	fp := func(x accounts.SystemEntry) string { return x.Fingerprint() }
	names := func(l []accounts.SystemEntry) string {
		var out []string
		for _, x := range l {
			out = append(out, x.Username+"="+x.Hash)
		}
		return strings.Join(out, ",")
	}
	last := map[string]string{
		"same": fp(e("same", "$1", 1)), "theirs": fp(e("theirs", "$1", 1)), "ours": fp(e("ours", "$1", 1)),
		"both": fp(e("both", "$1", 1)), "gonehere": fp(e("gonehere", "$1", 1)), "gonethere": fp(e("gonethere", "$1", 1)),
	}
	local := []accounts.SystemEntry{e("same", "$1", 1), e("theirs", "$1", 1), e("ours", "$2", 5), e("both", "$2", 5), e("gonethere", "$1", 1)}
	remote := []accounts.SystemEntry{e("same", "$1", 1), e("theirs", "$9", 4), e("ours", "$1", 1), e("both", "$3", 6), e("gonehere", "$1", 1), e("new", "$5", 2)}
	take, agreed, conflicts := planSystemAccounts(local, remote, last, false, false)
	if got := names(take); got != "theirs=$9,both=$3,new=$5" {
		t.Fatalf("take: %s", got)
	}
	if conflicts != 1 {
		t.Fatalf("conflicts: %d", conflicts)
	}
	// Removed on one side: not brought back, still remembered.
	if agreed["gonehere"] != last["gonehere"] || agreed["gonethere"] != last["gonethere"] || agreed["ours"] != last["ours"] {
		t.Fatalf("agreed: %+v", agreed)
	}
	// The other server's view of the same accounts takes ours (it's symmetric).
	take2, _, _ := planSystemAccounts(remote, local, last, true, false)
	if got := names(take2); got != "ours=$2" {
		t.Fatalf("other side takes: %s", got)
	}
	// Same day on both: the higher NoCap ID wins.
	tie := []accounts.SystemEntry{e("x", "$a", 3)}
	tieR := []accounts.SystemEntry{e("x", "$b", 3)}
	if take, _, _ := planSystemAccounts(tie, tieR, nil, true, false); len(take) != 1 {
		t.Fatal("tie: the higher ID should win")
	}
	if take, _, _ := planSystemAccounts(tie, tieR, nil, false, false); len(take) != 0 {
		t.Fatal("tie: the lower ID should lose")
	}
	// The first sync: everything of the primary's.
	if take, _, _ := planSystemAccounts(local, remote, nil, false, true); len(take) != len(remote) {
		t.Fatalf("force took %d", len(take))
	}
}
