package api

import (
	"net/http"

	"alfaos/alfad/internal/files"
)

// maxLocalPaths is how many files one pick may hand over.
const maxLocalPaths = 256

type localPathsRequest struct {
	Root  string   `json:"root"`
	Paths []string `json:"paths"`
}

// filesLocal tells NoCap Desktop's file picker where picked files are on
// this machine, so the app that asked reads them in place. Only the
// loopback desktop listener answers (the picker runs on this machine's own
// screen). Each path is "" when it isn't a regular file there.
func (s *Server) filesLocal(w http.ResponseWriter, r *http.Request) {
	if !onLocalDesktop(r) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req localPathsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Paths) == 0 || len(req.Paths) > maxLocalPaths {
		writeError(w, http.StatusBadRequest, "bad_request", "pick between 1 and 256 files")
		return
	}
	rels := make([]string, len(req.Paths))
	for i, p := range req.Paths {
		rel, err := files.Clean(p)
		if err != nil {
			s.fileError(w, r, err)
			return
		}
		rels[i] = rel
	}
	if !s.canUse(w, r, req.Root, false, rels...) {
		return
	}
	out := make([]string, len(rels))
	for i, rel := range rels {
		p, err := s.Files.LocalPath(req.Root, rel)
		if err != nil {
			s.fileError(w, r, err)
			return
		}
		out[i] = p
	}
	writeJSON(w, http.StatusOK, map[string]any{"paths": out})
}
