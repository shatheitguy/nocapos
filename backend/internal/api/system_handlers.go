package api

import (
	"context"
	"net/http"
	"time"

	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/hardware"
)

type systemInfoResponse struct {
	Version      string            `json:"version"`
	Host         hardware.HostInfo `json:"host"`
	Docker       *docker.Info      `json:"docker,omitempty"`
	DockerError  string            `json:"docker_error,omitempty"`
	Accelerators []hardware.GPU    `json:"accelerators"`
}

func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	resp := systemInfoResponse{
		Version:      s.Version,
		Host:         s.Sampler.HostInfo(),
		Accelerators: s.Sampler.Accelerators(),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if info, err := s.Docker.Info(ctx); err == nil {
		resp.Docker = info
	} else {
		s.Log.Warn("docker info failed", "err", err)
		resp.DockerError = "docker engine unavailable"
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) systemMetrics(w http.ResponseWriter, r *http.Request) {
	snap := s.Sampler.Latest()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "metrics not collected yet")
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
