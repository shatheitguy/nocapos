//go:build !linux

package api

import "context"

// ServeClipSocket: NoCap Desktop only runs on Linux.
func (s *Server) ServeClipSocket(ctx context.Context, path string) error { return nil }
