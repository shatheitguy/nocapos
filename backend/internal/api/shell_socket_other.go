//go:build !linux

package api

import "context"

// ServeShellSocket: NoCap Desktop only runs on Linux.
func (s *Server) ServeShellSocket(ctx context.Context, path string) error { return nil }
