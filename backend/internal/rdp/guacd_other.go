//go:build !linux

package rdp

import "context"

// guacd has no native Windows/macOS build; use the container instead.
func (g *Guacd) native(context.Context) (string, error) { return "", errNoNative }
