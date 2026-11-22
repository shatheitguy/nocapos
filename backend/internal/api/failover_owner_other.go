//go:build !linux

package api

// homeOwner: home folders only sync on Linux.
func homeOwner(string) func(string) (int, int, bool) { return nil }
