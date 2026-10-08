package api

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"alfaos/alfad/internal/docker"
)

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]errorBody{"error": {Code: code, Message: msg}})
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal server error")
}

// dockerError maps Engine errors onto API responses without leaking internals
// for unexpected failures.
func (s *Server) dockerError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, docker.ErrUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "docker_unavailable", "Docker engine is not reachable")
		return
	}
	var apiErr *docker.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusNotFound:
			writeError(w, http.StatusNotFound, "not_found", apiErr.Message)
			return
		case http.StatusConflict:
			writeError(w, http.StatusConflict, "conflict", apiErr.Message)
			return
		}
	}
	s.Log.Error("docker request failed", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusBadGateway, "docker_error", "docker engine request failed")
}

const maxBodyBytes = 64 << 10

// decodeJSON enforces a JSON content type (which also forces a CORS preflight
// for cross-origin callers), a size limit, and a strict schema.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeJSONLimit(w, r, v, maxBodyBytes)
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || dec.More() {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	return true
}
