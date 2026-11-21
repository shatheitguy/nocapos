package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"alfaos/alfad/internal/hostaccess"
	"alfaos/alfad/internal/syspkg"
)

// The earlier desktop's packages (syspkg.OldDesktopPackages): once NoCap
// Desktop is confirmed working here (hostaccess.DesktopConfirm), Settings →
// This Screen shows a one-time notice: remove the ones NoCapOS installed,
// or keep them. The choice is remembered in the data folder.

// oldDesktopChoiceFile is in the data folder: "keep", or "removed" and
// the packages.
const oldDesktopChoiceFile = "old-desktop"

type oldDesktopState struct {
	// Notice: show the one-time notice (confirmed, packages to remove, no
	// choice made yet).
	Notice    bool                      `json:"notice"`
	Confirmed bool                      `json:"confirmed"`
	Progress  hostaccess.DesktopConfirm `json:"progress"`
	Remove    []string                  `json:"remove"`
	Keep      []string                  `json:"keep"`
	// Choice: "", "keep" or "removed".
	Choice  string   `json:"choice,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

func (s *Server) oldDesktopChoicePath() string {
	dir := s.desktop.DataDir
	if dir == "" {
		dir = "/var/lib/nocapos"
	}
	return filepath.Join(dir, oldDesktopChoiceFile)
}

func (s *Server) oldDesktopChoice() (string, []string) {
	b, err := os.ReadFile(s.oldDesktopChoicePath())
	if err != nil {
		return "", nil
	}
	f := strings.Fields(string(b))
	if len(f) == 0 || (f[0] != "keep" && f[0] != "removed") {
		return "", nil
	}
	return f[0], f[1:]
}

func (s *Server) oldDesktopState(ctx context.Context) oldDesktopState {
	st := oldDesktopState{Remove: []string{}, Keep: []string{}}
	if !s.desktop.Native || !syspkg.CanInstall() {
		return st
	}
	st.Progress = s.desktop.Confirmation()
	st.Confirmed = st.Progress.IsConfirmed()
	st.Choice, st.Removed = s.oldDesktopChoice()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if plan, err := s.pkgAdmin.OldDesktop(ctx); err == nil {
		if plan.Remove != nil {
			st.Remove = plan.Remove
		}
		if plan.Keep != nil {
			st.Keep = plan.Keep
		}
	}
	st.Notice = st.Confirmed && st.Choice == "" && len(st.Remove) > 0
	return st
}

// GET /api/v1/system/desktop/old-desktop
func (s *Server) oldDesktopGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.oldDesktopState(r.Context()))
}

// POST /api/v1/system/desktop/old-desktop {"action": "remove" | "keep"}:
// remove runs as a package job (GET /api/v1/packages/jobs/{id}).
func (s *Server) oldDesktopSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	uid := userFrom(r.Context()).ID
	switch req.Action {
	case "keep":
		err := os.WriteFile(s.oldDesktopChoicePath(), []byte("keep\n"), 0o644)
		s.audit(r, uid, "system.old_desktop", "keep", err == nil, errText(err))
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, s.oldDesktopState(r.Context()))
	case "remove":
		st := s.oldDesktopState(r.Context())
		if !st.Confirmed {
			writeError(w, http.StatusConflict, "not_confirmed", "NoCap Desktop hasn't been confirmed working here yet; the earlier desktop stays until it has.")
			return
		}
		if len(st.Remove) == 0 {
			writeError(w, http.StatusConflict, "nothing", "There's nothing of the earlier desktop that NoCapOS installed left to remove.")
			return
		}
		// Never while the earlier desktop is on the screen.
		if h := s.desktop.Health(r.Context()); h.Has(hostaccess.ProblemOtherDM) {
			writeError(w, http.StatusConflict, "in_use", "The earlier desktop is still running; restart the server first.")
			return
		}
		path := s.oldDesktopChoicePath()
		steps, err := syspkg.OldDesktopSteps(st.Remove, syspkg.RunLong, func(removed []string) {
			if removed != nil {
				_ = os.WriteFile(path, []byte("removed "+strings.Join(removed, " ")+"\n"), 0o644)
			}
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		s.startPkgJob(w, r, "remove-old-desktop", strings.Join(st.Remove, " "), steps, nil)
	default:
		writeError(w, http.StatusBadRequest, "bad_request", errors.New("action must be remove or keep").Error())
	}
}
