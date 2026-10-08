package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// systemBackup streams a consistent snapshot of the NoCapOS database (accounts,
// settings, AI providers and conversations, memory) as a download. Admin only.
func (s *Server) systemBackup(w http.ResponseWriter, r *http.Request) {
	tmp := filepath.Join(s.Config.DataDir, fmt.Sprintf(".backup-%d.db", time.Now().UnixNano()))
	if err := s.Store.BackupTo(r.Context(), tmp); err != nil {
		s.internalError(w, r, err)
		return
	}
	defer os.Remove(tmp)

	f, err := os.Open(tmp)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	name := fmt.Sprintf("nocapos-backup-%s.db", time.Now().Format("2006-01-02-1504"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, info.ModTime(), f)

	s.audit(r, userFrom(r.Context()).ID, "system.backup", name, true, "")
}
