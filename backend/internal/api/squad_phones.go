package api

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/devices"
	"alfaos/alfad/internal/store"
)

// Phones asking to link without a code (see squad_links.go). The phone gets
// a request ID, a secret only it knows (to pick up the answer) and four
// check digits; whoever accepts sees the same digits.

const pairSecretHeader = "X-Pair-Secret"

// pairAsk: a phone asks this server to link it (no sign-in).
func (s *Server) pairAsk(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.Config.TrustedProxies)
	q := s.squadInit()
	if !q.asks.Allow("phone:" + ip) {
		w.Header().Set("Retry-After", "20")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many link requests. Wait a little and try again.")
		return
	}
	if !peerAddrOK(ip, s.remoteAllowed(r.Context())) {
		writeError(w, http.StatusForbidden, "not_home", "Ask to link from the same network as the server, or use a pairing code.")
		return
	}
	var req struct {
		claimRequest
		Username string `json:"username"`
	}
	if !decodeJSONLimit(w, r, &req, 8<<10) {
		return
	}
	if !checkNewDevice(w, &req.claimRequest) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) > 64 {
		writeError(w, http.StatusBadRequest, "validation", "That account name is too long.")
		return
	}
	nocapID, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if s.setupNew(r.Context()) {
		writeError(w, http.StatusConflict, "setup_new", "This server isn't set up yet. Set it up first.")
		return
	}
	a := &phoneAsk{
		ID: randomHex(8), DeviceName: req.DeviceName, Platform: req.Platform, Model: req.Model, OSVersion: req.OSVersion,
		Username: req.Username, Address: ip, Check: checkDigits(), state: "pending", secret: randomHex(24),
	}
	if a.Username != "" {
		// Unknown names aren't told apart: such a request can only be declined.
		if u, err := s.Store.UserByUsername(r.Context(), a.Username); err == nil {
			a.userID = u.ID
		}
	}
	q.mu.Lock()
	q.prune()
	mine := 0
	for _, o := range q.phones {
		if o.Address == ip && o.state == "pending" {
			mine++
		}
	}
	if len(q.phones) >= squadMaxPhoneAsks || mine >= squadAsksPerIP {
		q.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "busy", "Too many link requests are waiting. Try again in a few minutes.")
		return
	}
	now := q.now()
	a.CreatedAt, a.ExpiresAt = now, now.Add(squadAskTTL)
	q.phones[a.ID] = a
	out := *a
	q.mu.Unlock()
	s.audit(r, a.userID, "devices.ask", truncate(a.DeviceName, 64), true, "from "+ip+", check "+a.Check)
	who := ""
	if a.Username != "" {
		who = " for " + a.Username
	}
	s.squadNotify("phone-ask:"+a.ID, a.DeviceName+" wants to link",
		"A phone at "+ip+" asked to link"+who+". Check it shows "+a.Check+", then accept or decline in Settings → Your Squad.",
		a.DeviceName+" wants in",
		"Phone at "+ip+" wants to link"+who+". Make sure it shows "+a.Check+", then accept or pass in Settings → Your Squad.")
	writeJSON(w, http.StatusAccepted, map[string]any{
		"request_id": out.ID, "secret": a.secret, "check": out.Check, "expires_at": out.ExpiresAt,
		"nocap_id": nocapID, "server_name": s.serverName(),
	})
}

// askFor finds the phone's request when it shows the right secret.
func (s *Server) askFor(r *http.Request) *phoneAsk {
	q := s.squadInit()
	a := q.phones[r.PathValue("id")]
	if a == nil || !sameSecret(a.secret, r.Header.Get(pairSecretHeader)) {
		return nil
	}
	return a
}

// pairAskPoll: the phone checks on its request. An accepted request hands
// over the device token once.
func (s *Server) pairAskPoll(w http.ResponseWriter, r *http.Request) {
	q := s.squadInit()
	q.mu.Lock()
	q.prune()
	a := s.askFor(r)
	if a == nil {
		q.mu.Unlock()
		writeError(w, http.StatusNotFound, "not_found", "That link request is gone (it may have expired).")
		return
	}
	out := map[string]any{"state": a.state, "check": a.Check, "expires_at": a.ExpiresAt}
	if a.state == "accepted" && a.result != nil {
		out["device"] = *a.result
		delete(q.phones, a.ID)
	}
	q.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// pairAskCancel: the phone gives up on its request.
func (s *Server) pairAskCancel(w http.ResponseWriter, r *http.Request) {
	q := s.squadInit()
	q.mu.Lock()
	a := s.askFor(r)
	if a != nil && !a.busy {
		delete(q.phones, a.ID)
	}
	q.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

type phoneAskJSON struct {
	phoneAsk
	CanAccept bool   `json:"can_accept"`
	Note      string `json:"note,omitempty"` // unknown_user | for_user
}

// visible: administrators see every request; others the ones naming them.
func (a *phoneAsk) visibleTo(u *store.User) bool {
	return u.Role == store.RoleAdmin || (a.userID != "" && a.userID == u.ID)
}

// acceptableBy: a request naming an account is that account's to accept;
// one naming nobody is any administrator's (and links to them).
func (a *phoneAsk) acceptableBy(u *store.User) bool {
	if a.Username != "" {
		return a.userID != "" && a.userID == u.ID
	}
	return u.Role == store.RoleAdmin
}

func (s *Server) phoneAsksList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	q := s.squadInit()
	q.mu.Lock()
	q.prune()
	out := []phoneAskJSON{}
	for _, a := range q.phones {
		if a.state != "pending" || !a.visibleTo(u) {
			continue
		}
		j := phoneAskJSON{phoneAsk: *a, CanAccept: a.acceptableBy(u)}
		switch {
		case a.Username != "" && a.userID == "":
			j.Note = "unknown_user"
		case a.Username != "" && !j.CanAccept:
			j.Note = "for_user"
		}
		out = append(out, j)
	}
	q.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	writeJSON(w, http.StatusOK, out)
}

// takePhoneAsk marks a pending request busy for u, or says why not.
func (s *Server) takePhoneAsk(w http.ResponseWriter, r *http.Request, accepting bool) *phoneAsk {
	u := userFrom(r.Context())
	q := s.squadInit()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.prune()
	a := q.phones[r.PathValue("id")]
	if a == nil || a.state != "pending" || a.busy || !a.visibleTo(u) {
		writeError(w, http.StatusNotFound, "not_found", "That link request is gone (it may have expired).")
		return nil
	}
	if accepting && !a.acceptableBy(u) {
		writeError(w, http.StatusForbidden, "forbidden", "Only "+a.Username+" can accept this one (you can decline it).")
		return nil
	}
	a.busy = true
	return a
}

func (s *Server) phoneAskAccept(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	a := s.takePhoneAsk(w, r, true)
	if a == nil {
		return
	}
	q := s.squadInit()
	done := false
	defer func() {
		if !done {
			q.mu.Lock()
			a.busy = false
			q.mu.Unlock()
		}
	}()
	if err := s.Auth.DesktopAllowed(r.Context(), u); err != nil {
		if errors.Is(err, auth.ErrFilesOnly) || errors.Is(err, auth.ErrInvalidToken) {
			writeError(w, http.StatusForbidden, "forbidden", "This account can't link phones.")
			return
		}
		s.internalError(w, r, err)
		return
	}
	nocapID, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	req := claimRequest{DeviceName: a.DeviceName, Platform: a.Platform, Model: a.Model, OSVersion: a.OSVersion}
	token, hash := devices.NewToken()
	d := newDeviceFor(u, req)
	full, err := s.linkDevice(r, u, d, hash)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if full != nil {
		s.audit(r, u.ID, "devices.ask_accept", d.Name, false, "device limit reached")
		writeDeviceLimit(w, *full)
		return
	}
	s.audit(r, u.ID, "devices.ask_accept", d.Name, true, "from "+a.Address+", check "+a.Check)
	s.devicePairedFrom(r, u, d, a.Address)
	q.mu.Lock()
	a.busy, a.state = false, "accepted"
	a.result = &claimResponse{DeviceID: d.ID, Token: token, NocapID: nocapID, ServerName: s.serverName(), User: map[string]string{"username": u.Username}}
	a.ExpiresAt = q.now().Add(squadPickupTTL)
	done = true
	q.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "device_id": d.ID})
}

func (s *Server) phoneAskDecline(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	a := s.takePhoneAsk(w, r, false)
	if a == nil {
		return
	}
	q := s.squadInit()
	q.mu.Lock()
	a.busy, a.state = false, "declined"
	q.mu.Unlock()
	s.audit(r, u.ID, "devices.ask_decline", truncate(a.DeviceName, 64), true, "from "+a.Address)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
