//go:build !linux

package rdp

import "context"

// guacd has no native Windows/macOS build; use the container instead.
func (g *Guacd) native(context.Context) (string, error) { return "", errNoNative }

// waitPreload: only the Linux OS images preload container images.
func (g *Guacd) waitPreload(context.Context) bool { return false }
