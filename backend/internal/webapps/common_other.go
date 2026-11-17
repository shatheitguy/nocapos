//go:build !linux

package webapps

import "os"

// Supported reports whether streamed apps can run on this host (Linux only).
const Supported = false

func stopProcess(p *os.Process) { _ = p.Kill() }
