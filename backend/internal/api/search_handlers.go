package api

import (
	"net/http"
	"strconv"
)

// filesSearch searches file and folder names across the storage locations
// (universal search). Same access as the rest of Files: admin only.
func (s *Server) filesSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if len(q) > 200 {
		q = q[:200]
	}
	count, building, capped := s.FileIndex.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"results":  s.FileIndex.Search(q, limit),
		"indexed":  count,
		"building": building,
		"capped":   capped,
	})
}

// touchIndex refreshes the search index after a successful file change.
func (s *Server) touchIndex(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h(rec, r)
		if rec.status < 400 && s.FileIndex != nil {
			s.FileIndex.Touch()
		}
	}
}
