//go:build !linux

package sshd

// lchown does nothing off Linux: remote login only runs there.
func lchown(string, int, int) error { return nil }
