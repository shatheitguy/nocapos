//go:build !linux

package appdata

import "context"

// SystemCopy is the plain copy where there are no Linux copy tools.
func SystemCopy(ctx context.Context, src, dst string) error { return GoCopy(ctx, src, dst) }
