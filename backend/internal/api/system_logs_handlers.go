package api

import (
	"context"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type systemLogsResponse struct {
	Supported bool     `json:"supported"`
	Reason    string   `json:"reason,omitempty"`
	Lines     []string `json:"lines"`
}

// systemLogs returns NoCapOS's own recent log lines from the systemd journal
// (Settings → Troubleshoot). Other hosts get supported=false and a reason.
func (s *Server) systemLogs(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if n <= 0 {
		n = 300
	}
	n = min(n, 2000)
	resp := systemLogsResponse{Lines: []string{}}
	if runtime.GOOS != "linux" {
		resp.Reason = "Logs are read from the systemd journal, which needs a Linux host."
		writeJSON(w, http.StatusOK, resp)
		return
	}
	bin, err := exec.LookPath("journalctl")
	if err != nil {
		resp.Reason = "journalctl is not available on this host."
		writeJSON(w, http.StatusOK, resp)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-u", "nocapos", "-n", strconv.Itoa(n), "--no-pager", "-o", "short-iso").Output()
	if err != nil {
		resp.Reason = "Could not read the journal: " + errText(err)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Supported = true
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if l != "" && !strings.HasPrefix(l, "-- ") {
			resp.Lines = append(resp.Lines, l)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
