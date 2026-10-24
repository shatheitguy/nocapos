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
	case errors.Is(err, auth.ErrTOTPEnabled):
		writeError(w, http.StatusConflict, "totp_enabled", err.Error())
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "incorrect password")
	case errors.Is(err, auth.ErrLocked):
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "locked", err.Error())
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "validation", err.Error())
	case errors.Is(err, auth.ErrNoSealer):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "secret storage is not configured")
	default:
		s.internalError(w, r, err)
	}
}

// sensitiveLimited throttles password-checking endpoints for signed-in
// people (per account, on top of the account lockout).
func (s *Server) sensitiveLimited(w http.ResponseWriter, r *http.Request) bool {
	if s.sensitiveLimiter == nil || s.sensitiveLimiter.Allow(userFrom(r.Context()).ID) {
		return false
	}
	w.Header().Set("Retry-After", "12")
	writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, slow down")
	return true
}

func (s *Server) totpStatus(w http.ResponseWriter, r *http.Request) {
	enabled, err := s.Auth.TOTPStatus(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.totpAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

type passwordConfirm struct {
	Password string `json:"password"`
}

// totpSetup starts enrolment. It needs the current password and refuses
// while 2FA is on (turn it off first, with password and code).
func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request) {
	if s.sensitiveLimited(w, r) {
		return
	}
	var req passwordConfirm
	if !decodeJSON(w, r, &req) {
		return
	}
	u := userFrom(r.Context())
	secret, uri, err := s.Auth.BeginTOTP(r.Context(), u.ID, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			s.audit(r, u.ID, "auth.totp.setup", u.Username, false, "incorrect password")
		}
		s.totpAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri})
}

type codeRequest struct {
	Code string `json:"code"`
}

func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request) {
	if s.sensitiveLimited(w, r) {
		return
	}
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

type totpDisableRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// totpDisable turns 2FA off with the password and a current code. The
// password is checked directly (no new session is started).
func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	if s.sensitiveLimited(w, r) {
		return
	}
	var req totpDisableRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u := userFrom(r.Context())
	if err := s.Auth.DisableTOTP(r.Context(), u.ID, req.Password, req.Code); err != nil {
		s.audit(r, u.ID, "auth.totp.disable", u.Username, false, errText(err))
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
	if s.sensitiveLimited(w, r) {
		return
	}
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
	s.syncSharingPassword(r.Context(), u.Username, req.New)
	w.WriteHeader(http.StatusNoContent)
}

// authReset used to let a 2FA code alone set a new password. That made the
// code a second password rather than a second factor, so it's gone: an
// administrator resets passwords in Users & Groups, and a sole administrator
// runs `nocapos reset-password <user>` as root on the machine.
func (s *Server) authReset(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusGone, "reset_removed",
		"Passwords can't be reset from the sign-in screen. Ask an administrator to reset it in Users & Groups.")
}
