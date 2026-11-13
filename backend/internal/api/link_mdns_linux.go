//go:build linux

package api

import (
	"context"
	"os/exec"
	"time"
)

// advertiseLink announces this server on the home network as a
// _nocap._tcp service (through Avahi), so a phone finds it nearby without
// knowing its address. The TXT record (linkTXT) carries the NoCap ID, the
// scheme, the version and the setup state; the phone still checks the ID
// and its pinned certificate. It starts over when any of that changes.
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
	for ctx.Err() == nil {
		changed := s.discovery.wait()
		name := "NoCap " + s.serverName()
		args := append([]string{"-s", name, linkService, port}, linkTXT(id, scheme, s.Version, s.setupNew(ctx))...)
		run, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			// Runs until we stop it; restarts if Avahi does.
			_ = exec.CommandContext(run, bin, args...).Run()
			close(done)
		}()
		select {
		case <-ctx.Done():
			stop()
			<-done
			return
		case <-changed:
			stop()
			<-done
		case <-done:
			stop()
			select {
			case <-ctx.Done():
				return
			case <-changed:
			case <-time.After(30 * time.Second):
			}
		}
	}
}

// browseNoCap lists the NoCap servers Avahi sees (parseNoCapBrowse reads it).
func browseNoCap(ctx context.Context) (string, bool) {
	bin, err := exec.LookPath("avahi-browse")
	if err != nil {
		return "", false
	}
	// -r resolve, -t stop after the first full list, -p parsable, -k no lookups.
	out, _ := exec.CommandContext(ctx, bin, "-rtpk", linkService).Output()
	return string(out), true
}
