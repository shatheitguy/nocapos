//go:build !linux

package api

import "context"

// advertiseLink: only Linux servers announce themselves (through Avahi).
func (s *Server) advertiseLink(ctx context.Context) {}

// browseNoCap: only Linux servers look for others (through Avahi).
func browseNoCap(ctx context.Context) (string, bool) { return "", false }
