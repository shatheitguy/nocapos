//go:build !linux

package beacon

import "context"

// Supported: only Linux servers advertise over Bluetooth.
const Supported = false

// Available: no adapter to use here.
func Available(ctx context.Context) bool { return false }

// Run: nothing to advertise with here.
func Run(ctx context.Context, ad Ad) error { return ErrUnavailable }
