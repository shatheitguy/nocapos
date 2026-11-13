package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"alfaos/alfad/internal/store"
)

// maxRestoreUpload caps an uploaded backup (the database holds settings,
// conversations and memory, not files).
const maxRestoreUpload = 4 << 30

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

type restoreStatus struct {
	// Pending: a checked backup is waiting and is put in place the next
	// time NoCapOS starts (the machine restarts).
	Pending bool `json:"pending"`
}

func (s *Server) systemRestoreStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, restoreStatus{Pending: store.HasStagedRestore(s.Config.DataDir)})
}

// systemRestore takes a backup made by systemBackup (multipart: "password",
// then "file"), checks it and stages it for the next start. The caller's
// password is required: the backup replaces every account and setting.
func (s *Server) systemRestore(w http.ResponseWriter, r *http.Request) {
	if s.sensitiveLimited(w, r) {
		return
	}
	u := userFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Send the backup file as a form upload.")
		return
	}
	password, checked := "", false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "The upload didn't finish. Try again.")
			return
		}
		switch part.FormName() {
		case "password":
			b, _ := io.ReadAll(io.LimitReader(part, 1024))
			password = string(b)
		case "file":
			if !checked {
				if _, _, ok := s.Auth.CheckPassword(r.Context(), u.Username, password); !ok {
					s.audit(r, u.ID, "system.restore", "", false, "incorrect password")
					writeError(w, http.StatusUnauthorized, "invalid_credentials", "That password isn't right.")
					return
				}
				checked = true
			}
			err := store.StageRestore(r.Context(), s.Config.DataDir, part)
			if errors.Is(err, store.ErrBadBackup) {
				s.audit(r, u.ID, "system.restore", part.FileName(), false, err.Error())
				writeError(w, http.StatusBadRequest, "bad_backup", "This file can't be restored: "+trimBad(err))
				return
			}
			if err != nil {
				s.internalError(w, r, err)
				return
			}
			s.audit(r, u.ID, "system.restore", part.FileName(), true, "applied at the next start")
			writeJSON(w, http.StatusOK, restoreStatus{Pending: true})
			return
		}
		part.Close()
	}
	writeError(w, http.StatusBadRequest, "bad_request", "Choose a backup file to restore.")
}

func (s *Server) systemRestoreCancel(w http.ResponseWriter, r *http.Request) {
	if err := store.CancelStagedRestore(s.Config.DataDir); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "system.restore", "", true, "canceled")
	writeJSON(w, http.StatusOK, restoreStatus{Pending: false})
}

// trimBad keeps the human part of an ErrBadBackup ("bad backup: <why>").
func trimBad(err error) string {
	msg := err.Error()
	if p := store.ErrBadBackup.Error() + ": "; len(msg) > len(p) && msg[:len(p)] == p {
		return msg[len(p):]
	}
	return msg
}
