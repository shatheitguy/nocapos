package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/store"
)

// NoCap Link web sign-in: the NoCap OS app asks for a one-time ticket with
// its device token and opens the web desktop at /?link=<ticket>; the page
// trades the ticket for a normal session (POST /api/v1/auth/link).

type deviceSessionResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
	URL       string    `json:"url"`
}

// deviceSession hands a linked device a one-time, 60-second ticket that
// signs its owner in to the web desktop.
func (s *Server) deviceSession(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	tok, exp := s.linkTickets().IssueSession(u.ID, d.ID)
	s.audit(r, u.ID, "devices.web_session", d.Name, true, "ticket issued")
	writeJSON(w, http.StatusOK, deviceSessionResponse{
		Ticket:    tok,
		ExpiresAt: exp.UTC().Truncate(time.Second),
		URL:       requestOrigin(r) + "/?link=" + url.QueryEscape(tok),
	})
}

type linkRequest struct {
	Ticket string `json:"ticket"`
}

// authLink trades a device's web sign-in ticket for a session, the same as
// signing in with a password. Each ticket works once; the device must still
// be linked and its owner still allowed on the desktop.
func (s *Server) authLink(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var req linkRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, deviceID, ok := s.linkTickets().RedeemSession(strings.TrimSpace(req.Ticket))
	if !ok {
		s.audit(r, "", "devices.web_session", "", false, "unknown, used or expired ticket")
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "That sign-in link expired or was already used. Tap Open my NoCap desktop again.")
		return
	}
	d, err := s.Store.DeviceByID(r.Context(), deviceID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.UserID != userID) {
		s.audit(r, userID, "devices.web_session", deviceID, false, "device unlinked")
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "This device isn't linked anymore. Link it again from Settings → Devices.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sess, err := s.Auth.LinkedSession(r.Context(), userID, s.meta(r))
	if errors.Is(err, auth.ErrInvalidToken) || errors.Is(err, auth.ErrFilesOnly) {
		s.audit(r, userID, "devices.web_session", d.Name, false, "account can't sign in")
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "The account this device belongs to can't sign in right now.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, userID, "devices.web_session", d.Name, true, "signed in from linked device")
	s.signedIn(r, sess, d)
	s.writeSession(w, r, http.StatusOK, sess)
}
