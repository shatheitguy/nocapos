// Package webapps streams native GUI applications into NoCapOS windows:
// each runs on its own virtual display, and x11vnc + noVNC/websockify turn
// that display into a web stream alfad reverse-proxies to the desktop,
// same-origin.
package webapps

// Phase is where a stream is in its lifecycle.
type Phase string

const (
	PhaseIdle     Phase = "idle"
	PhaseStarting Phase = "starting"
	PhaseReady    Phase = "ready"
	PhaseError    Phase = "error"
)
