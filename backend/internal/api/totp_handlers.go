package api

import (
	"errors"
	"net/http"

	"alfaos/alfad/internal/auth"
)

func (s *Server) totpAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrTOTPBadCode):
		writeError(w, http.StatusBadRequest, "bad_code", err.Error())
	case errors.Is(err, auth.ErrTOTPNotEnrolled):
		writeError(w, http.StatusBadRequest, "not_enrolled", err.Error())
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "incorrect password")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "validation", err.Error())
	case errors.Is(err, auth.ErrNoSealer):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "secret storage is not configured")
	default:
		s.internalError(w, r, err)
	}
}

func (s *Server) totpStatus(w http.ResponseWriter, r *http.Request) {
	enabled, err := s.Auth.TOTPStatus(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.totpAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request) {
	secret, uri, err := s.Auth.BeginTOTP(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.totpAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri})
}

type codeRequest struct {
	Code string `json:"code"`
}

func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request) {
	var req codeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u := userFrom(r.Context())
	if err := s.Auth.EnableTOTP(r.Context(), u.ID, req.Code); err != nil {
		s.totpAuthError(w, r, err)
		return
	}
	s.audit(r, u.ID, "auth.totp.enable", u.Username, true, "")
	w.WriteHeader(http.StatusNoContent)
}

type passwordConfirm struct {
	Password string `json:"password"`
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	var req passwordConfirm
	if !decodeJSON(w, r, &req) {
		return
	}
	u := userFrom(r.Context())
	// Re-check the password before disabling 2FA.
	if _, err := s.Auth.Login(r.Context(), u.Username, req.Password, s.meta(r)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "incorrect password")
		return
	}
	if err := s.Auth.DisableTOTP(r.Context(), u.ID); err != nil {
		s.totpAuthError(w, r, err)
		return
	}
	s.audit(r, u.ID, "auth.totp.disable", u.Username, true, "")
	w.WriteHeader(http.StatusNoContent)
}

type changePasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u := userFrom(r.Context())
	if err := s.Auth.ChangePassword(r.Context(), u.ID, req.Current, req.New); err != nil {
		s.totpAuthError(w, r, err)
		return
	}
	s.audit(r, u.ID, "auth.password.change", u.Username, true, "")
	w.WriteHeader(http.StatusNoContent)
}

type resetRequest struct {
	Username string `json:"username"`
	Code     string `json:"code"`
	Password string `json:"password"`
}

// authReset is the lock-screen "forgot password" flow: a valid TOTP code lets
// the user set a new password. Rate-limited per IP like login.
func (s *Server) authReset(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r) {
		return
	}
	var req resetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Auth.ResetPassword(r.Context(), req.Username, req.Code, req.Password)
	switch {
	case err == nil:
		s.audit(r, "", "auth.password.reset", truncate(req.Username, 64), true, "via TOTP")
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrTOTPBadCode), errors.Is(err, auth.ErrTOTPNotEnrolled):
		s.audit(r, "", "auth.password.reset", truncate(req.Username, 64), false, "")
		// One generic message so an attacker can't tell which part failed.
		writeError(w, http.StatusUnauthorized, "reset_failed", "username or code is incorrect, or 2FA is not set up for this account")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "validation", err.Error())
	default:
		s.internalError(w, r, err)
	}
}
