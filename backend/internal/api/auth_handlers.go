package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/store"
)

const (
	refreshCookie = "alfa_refresh"
	cookiePath    = "/api/v1/auth" // the refresh token is never sent to other endpoints
)

type sessionResponse struct {
	AccessToken string      `json:"access_token"`
	TokenType   string      `json:"token_type"`
	ExpiresAt   time.Time   `json:"expires_at"`
	User        *store.User `json:"user"`
}

func (s *Server) meta(r *http.Request) auth.ClientMeta {
	return auth.ClientMeta{IP: clientIP(r, s.Config.TrustedProxies), UserAgent: r.UserAgent()}
}

func (s *Server) writeSession(w http.ResponseWriter, status int, sess *auth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookie,
		Value:    sess.RefreshToken,
		Path:     cookiePath,
		Expires:  sess.RefreshExpires,
		MaxAge:   int(time.Until(sess.RefreshExpires).Seconds()),
		HttpOnly: true,
		Secure:   s.Config.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, status, sessionResponse{
		AccessToken: sess.AccessToken,
		TokenType:   "Bearer",
		ExpiresAt:   sess.AccessExpires.UTC(),
		User:        sess.User,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: "", Path: cookiePath, MaxAge: -1,
		HttpOnly: true, Secure: s.Config.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) limited(w http.ResponseWriter, r *http.Request) bool {
	if s.loginLimiter.Allow(clientIP(r, s.Config.TrustedProxies)) {
		return false
	}
	w.Header().Set("Retry-After", "12")
	writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
	return true
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	required, err := s.Auth.SetupRequired(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	mode := "app"
	if s.Auth.SystemAccountsEnabled() {
		mode = "system"
	}
	writeJSON(w, http.StatusOK, map[string]any{"setup_required": required, "accounts": mode})
}

type setupRequest struct {
	SetupToken string `json:"setup_token"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

func (s *Server) authSetup(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var req setupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sess, err := s.Auth.Setup(r.Context(), strings.TrimSpace(req.SetupToken), req.Username, req.Password, s.meta(r))
	switch {
	case err == nil:
		s.audit(r, sess.User.ID, "auth.setup", sess.User.Username, true, "")
		s.Log.Info("administrator account created", "username", sess.User.Username)
		s.writeSession(w, http.StatusCreated, sess)
	case errors.Is(err, auth.ErrSetupDone):
		writeError(w, http.StatusConflict, "setup_done", err.Error())
	case errors.Is(err, auth.ErrSetupToken):
		s.audit(r, "", "auth.setup", req.Username, false, "bad setup token")
		writeError(w, http.StatusForbidden, "invalid_setup_token", err.Error())
	case errors.Is(err, auth.ErrInvalidUsername), errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "validation", err.Error())
	default:
		s.internalError(w, r, err)
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// mfaResponse is what a right password gets on an account with 2FA: no
// session yet, just a short-lived token to send back with the code.
type mfaResponse struct {
	MFARequired bool      `json:"mfa_required"`
	MFAToken    string    `json:"mfa_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sess, err := s.Auth.Login(r.Context(), req.Username, req.Password, s.meta(r))
	var challenge *auth.MFAChallenge
	switch {
	case err == nil:
		s.audit(r, sess.User.ID, "auth.login", sess.User.Username, true, "")
		s.keepSharingPassword(sess.User.Username, req.Password)
		s.writeSession(w, http.StatusOK, sess)
	case errors.As(err, &challenge):
		// The password was right; the session waits for the 2FA code.
		s.audit(r, "", "auth.login", truncate(req.Username, 64), true, "password ok, waiting for 2FA code")
		s.keepSharingPassword(req.Username, req.Password)
		writeJSON(w, http.StatusOK, mfaResponse{MFARequired: true, MFAToken: challenge.Token, ExpiresAt: challenge.ExpiresAt.UTC()})
	case errors.Is(err, auth.ErrFilesOnly):
		// The password was right: keep their network sign-in working.
		s.audit(r, "", "auth.login", truncate(req.Username, 64), false, "file sharing only")
		s.keepSharingPassword(req.Username, req.Password)
		writeError(w, http.StatusForbidden, "files_only", err.Error())
	case errors.Is(err, auth.ErrLocked):
		s.audit(r, "", "auth.login", truncate(req.Username, 64), false, "account locked out")
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "locked", err.Error())
	case errors.Is(err, auth.ErrInvalidCredentials):
		s.audit(r, "", "auth.login", truncate(req.Username, 64), false, "invalid credentials")
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
	default:
		s.internalError(w, r, err)
	}
}

type loginTOTPRequest struct {
	MFAToken string `json:"mfa_token"`
	Code     string `json:"code"`
}

// authLoginTOTP finishes a sign-in on an account with 2FA.
func (s *Server) authLoginTOTP(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var req loginTOTPRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sess, err := s.Auth.LoginTOTP(r.Context(), req.MFAToken, req.Code, s.meta(r))
	switch {
	case err == nil:
		s.audit(r, sess.User.ID, "auth.login", sess.User.Username, true, "with 2FA code")
		s.writeSession(w, http.StatusOK, sess)
	case errors.Is(err, auth.ErrTOTPBadCode):
		s.audit(r, "", "auth.login.totp", "", false, "wrong or reused code")
		writeError(w, http.StatusUnauthorized, "bad_code", err.Error())
	case errors.Is(err, auth.ErrMFAExpired):
		writeError(w, http.StatusUnauthorized, "mfa_expired", err.Error())
	case errors.Is(err, auth.ErrLocked):
		s.audit(r, "", "auth.login.totp", "", false, "account locked out")
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "locked", err.Error())
	case errors.Is(err, auth.ErrFilesOnly):
		writeError(w, http.StatusForbidden, "files_only", err.Error())
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
	default:
		s.internalError(w, r, err)
	}
}

// keepSharingPassword refreshes a person's Samba password from a verified
// sign-in, so Linux users made outside NoCapOS can use SMB too. It runs in
// the background and only when SMB sharing is on.
func (s *Server) keepSharingPassword(username, password string) {
	if s.NetDrives == nil {
		return
	}
	sh, err := s.NetDrives.Sharing(context.Background())
	if err != nil || !sh.SMB || !sh.SambaReady {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := s.NetDrives.SyncUser(ctx, username, password); err != nil {
			s.Log.Warn("sharing: set Samba password", "user", username, "err", err)
		}
	}()
}

func (s *Server) authRefresh(w http.ResponseWriter, r *http.Request) {
	if !requireCSRFHeader(w, r) {
		return
	}
	c, err := r.Cookie(refreshCookie)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_token", "no session")
		return
	}
	sess, err := s.Auth.Refresh(r.Context(), c.Value, s.meta(r))
	switch {
	case err == nil:
		s.writeSession(w, http.StatusOK, sess)
	case errors.Is(err, auth.ErrTokenReuse):
		s.Log.Warn("refresh token reuse detected; session revoked", "ip", clientIP(r, s.Config.TrustedProxies))
		s.audit(r, "", "auth.refresh", "", false, "token reuse, session revoked")
		s.clearSessionCookie(w)
		writeError(w, http.StatusUnauthorized, "invalid_token", "session revoked")
	case errors.Is(err, auth.ErrInvalidToken):
		s.clearSessionCookie(w)
		writeError(w, http.StatusUnauthorized, "invalid_token", "session expired")
	default:
		s.internalError(w, r, err)
	}
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if !requireCSRFHeader(w, r) {
		return
	}
	if c, err := r.Cookie(refreshCookie); err == nil {
		userID, sid, err := s.Auth.Logout(r.Context(), c.Value)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		// Terminals, remote desktops and Brave opened from this session end too.
		s.live.closeSession(userID, sid)
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userFrom(r.Context()))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
