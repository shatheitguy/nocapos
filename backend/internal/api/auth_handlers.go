package api

import (
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

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sess, err := s.Auth.Login(r.Context(), req.Username, req.Password, s.meta(r))
	switch {
	case err == nil:
		s.audit(r, sess.User.ID, "auth.login", sess.User.Username, true, "")
		s.writeSession(w, http.StatusOK, sess)
	case errors.Is(err, auth.ErrInvalidCredentials):
		s.audit(r, "", "auth.login", truncate(req.Username, 64), false, "invalid credentials")
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
	default:
		s.internalError(w, r, err)
	}
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
		if err := s.Auth.Logout(r.Context(), c.Value); err != nil {
			s.internalError(w, r, err)
			return
		}
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
