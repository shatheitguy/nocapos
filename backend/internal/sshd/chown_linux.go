//go:build linux

package sshd

import "os"

// lchown sets the owner of path itself, never of what a link points to.
func lchown(path string, uid, gid int) error { return os.Lchown(path, uid, gid) }
