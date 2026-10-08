package api

import (
	"errors"
	"io"
	"net/http"

	"alfaos/alfad/internal/store"
)

// Profile photos. Each signed-in user manages their own; the image type is
// sniffed from the bytes (never trusted from the client) and SVG is refused,
// since it can carry script.

const maxAvatarBytes = 512 << 10

var avatarTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}

func (s *Server) avatarGet(w http.ResponseWriter, r *http.Request) {
	data, mime, err := s.Store.Avatar(r.Context(), userFrom(r.Context()).ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no profile photo")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func (s *Server) avatarPut(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxAvatarBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "could not read the image")
		return
	}
	if len(data) > maxAvatarBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "The photo must be 512 KB or smaller.")
		return
	}
	mime := http.DetectContentType(data)
	if !avatarTypes[mime] {
		writeError(w, http.StatusUnsupportedMediaType, "bad_type", "Use a PNG, JPEG, WebP or GIF image.")
		return
	}
	u := userFrom(r.Context())
	err = s.Store.SetAvatar(r.Context(), u.ID, mime, data)
	s.audit(r, u.ID, "account.avatar", u.Username, err == nil, errText(err))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) avatarDelete(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	err := s.Store.DeleteAvatar(r.Context(), u.ID)
	s.audit(r, u.ID, "account.avatar.remove", u.Username, err == nil, errText(err))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
