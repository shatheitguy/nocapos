//go:build linux

package api

import (
	"context"
	"os/exec"
	"time"
)

// advertiseLink announces this server on the home network as a
// _nocap._tcp service (through Avahi), so a linked phone finds it nearby
// without knowing its address. The TXT record carries the NoCap ID and the
// scheme; the phone still checks the ID and its pinned certificate.
func (s *Server) advertiseLink(ctx context.Context) {
	port, open := listenPort(s.Config.ListenAddr)
	if !open || port == "" || inContainer() {
		return
	}
	bin, err := exec.LookPath("avahi-publish-service")
	if err != nil {
		return
	}
	id, err := s.nocapID()
	if err != nil {
		return
	}
	scheme := "http"
	if s.Config.TLSEnabled() {
		scheme = "https"
	}
	name := "NoCap " + s.serverName()
	for ctx.Err() == nil {
		// Runs until we stop; restarts if Avahi does.
		cmd := exec.CommandContext(ctx, bin, "-s", name, linkService, port, "nocap_id="+id, "scheme="+scheme)
		_ = cmd.Run()
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}
