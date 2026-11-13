//go:build !linux

package api

import "context"

// advertiseLink: only Linux servers announce themselves (through Avahi).
func (s *Server) advertiseLink(ctx context.Context) {}
