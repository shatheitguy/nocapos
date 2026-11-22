package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/store"
)

// Failover pairs on native Linux installs: the people's Linux accounts
// (what they sign in with there) are kept the same on both servers as well,
// carefully: only accounts of people (never root, never system accounts),
// only the password hash, administrator group, lock and display name, and
// never a deletion (an account removed on one server stays on the other
// until an administrator removes it there too). Each server remembers what
// both had the last time they agreed (failover.sysacct), so it can tell
// which side changed an account; when both did, the newer change wins.

const (
	failoverSysEvery = time.Minute
	failoverSysKey   = "failover.sysacct" // username → fingerprint both had last
)

// systemSync is this server's Linux account directory, when it uses one.
func (s *Server) systemSync() accounts.SystemSync {
	if s.Accounts == nil || s.Accounts.Mode() != accounts.ModeSystem {
		return nil
	}
	ss, _ := s.Accounts.(accounts.SystemSync)
	return ss
}

// planSystemAccounts decides which of the other server's accounts to take.
// last: what both had when they last agreed; peerWinsTie: both changed an
// account on the same day and the other server has the higher NoCap ID.
// Returns the accounts to take and what both will have agreed on.
func planSystemAccounts(local, remote []accounts.SystemEntry, last map[string]string, peerWinsTie, force bool) ([]accounts.SystemEntry, map[string]string, int) {
	mine := map[string]accounts.SystemEntry{}
	for _, e := range local {
		mine[e.Username] = e
	}
	agreed := map[string]string{}
	var take []accounts.SystemEntry
	conflicts := 0
	for _, r := range remote {
		l, here := mine[r.Username]
		before, had := last[r.Username]
		rf := r.Fingerprint()
		switch {
		case force:
			take, agreed[r.Username] = append(take, r), rf
		case !here && had:
			// Removed here after both had it: not brought back, and not
			// removed there either.
			agreed[r.Username] = before
		case !here:
			take, agreed[r.Username] = append(take, r), rf
		case l.Fingerprint() == rf:
			agreed[r.Username] = rf
		case had && l.Fingerprint() == before: // only the other server changed it
			take, agreed[r.Username] = append(take, r), rf
		case had && rf == before: // only this one did: the other takes ours
			agreed[r.Username] = before
		default: // both changed it: the newer change wins
			conflicts++
			if r.Changed > l.Changed || (r.Changed == l.Changed && peerWinsTie) {
				take = append(take, r)
			}
			agreed[r.Username] = rf
		}
	}
	for name, fp := range last {
		if _, ok := agreed[name]; !ok {
			if _, here := mine[name]; here {
				agreed[name] = fp // removed there: kept here, as above
			}
		}
	}
	return take, agreed, conflicts
}

type sysAccountsJSON struct {
	System  bool                   `json:"system"`
	Entries []accounts.SystemEntry `json:"entries"`
}

// failoverPeerAccounts: the other server of an active pair reads this one's
// Linux accounts (password hashes only, inside the link's TLS).
func (s *Server) failoverPeerAccounts(w http.ResponseWriter, r *http.Request) {
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
	out := sysAccountsJSON{Entries: []accounts.SystemEntry{}}
	if ss := s.systemSync(); ss != nil {
		list, err := ss.ExportAccounts(r.Context())
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		out.System, out.Entries = true, list
	}
	writeJSON(w, http.StatusOK, out)
}

// failoverSystemAccounts keeps the Linux accounts in step with the other
// server (both must use Linux accounts). force: the first sync, where the
// primary's accounts win.
func (s *Server) failoverSystemAccounts(ctx context.Context, l *store.ServerLink, self string, force bool) error {
	ss := s.systemSync()
	if ss == nil {
		return nil
	}
	f := s.failoverInit()
	if !force && f.now().Sub(f.sysAt) < failoverSysEvery {
		return nil
	}
	f.sysAt = f.now()
	var theirs sysAccountsJSON
	if err := s.failoverCall(ctx, l, http.MethodGet, "/api/v1/failover/peer/system-accounts", nil, &theirs); err != nil {
		return err
	}
	if !theirs.System {
		return nil // the other server keeps NoCapOS's own accounts
	}
	mine, err := ss.ExportAccounts(ctx)
	if err != nil {
		return err
	}
	last := map[string]string{}
	if v, _ := s.Store.Setting(ctx, failoverSysKey); v != "" {
		_ = json.Unmarshal([]byte(v), &last)
	}
	take, agreed, conflicts := planSystemAccounts(mine, theirs.Entries, last, l.NocapID > self, force)
	for _, e := range take {
		if err := ss.ImportAccount(ctx, e); err != nil {
			s.auditCtx(ctx, "", "failover.system_account", e.Username, "", false, err.Error())
			delete(agreed, e.Username) // try again next time
			continue
		}
		s.auditCtx(ctx, "", "failover.system_account", e.Username, "", true, "from "+l.Name+" ("+l.NocapID+")")
	}
	if conflicts > 0 {
		s.auditCtx(ctx, "", "failover.conflict", l.Name+" ("+l.NocapID+")", "", true, "Linux accounts changed on both servers; the newer change was kept")
	}
	b, _ := json.Marshal(agreed)
	return s.Store.SetSetting(ctx, failoverSysKey, string(b))
}
