// Package webapps manages GUI applications that NoCapOS streams into a window.
//
// Brave is installed natively on the Linux host (from Brave's official repo) and
// runs on a virtual display; x11vnc + noVNC/websockify turn that display into a
// web stream that alfad reverse-proxies to the desktop, same-origin.
package webapps

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"
)

const braveWebPort = 6080 // noVNC bridge, loopback only

type Phase string

const (
	PhaseIdle       Phase = "idle"
	PhaseInstalling Phase = "installing"
	PhaseStarting   Phase = "starting"
	PhaseReady      Phase = "ready"
	PhaseError      Phase = "error"
)

// Status is a snapshot of Brave's lifecycle.
type Status struct {
	Phase   Phase  `json:"phase"`
	Message string `json:"message"`
	Ready   bool   `json:"ready"`
}

// Brave supervises the native Brave stream.
type Brave struct {
	log     *slog.Logger
	dataDir string

	mu      sync.Mutex
	phase   Phase
	message string
	port    int
	working bool
	proc    *os.Process
}

func NewBrave(log *slog.Logger, dataDir string) *Brave {
	return &Brave{log: log, dataDir: dataDir, phase: PhaseIdle}
}

func (b *Brave) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Status{Phase: b.phase, Message: b.message, Ready: b.phase == PhaseReady && b.port > 0}
}

// Target is where the proxy should send traffic: a plain-HTTP noVNC bridge on
// loopback. Port 0 means not ready.
func (b *Brave) Target() (port int, scheme string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.phase != PhaseReady {
		return 0, "http"
	}
	return b.port, "http"
}

func (b *Brave) set(phase Phase, msg string) {
	b.mu.Lock()
	b.phase, b.message = phase, msg
	b.mu.Unlock()
}

// Ensure starts Brave if it isn't running and returns immediately; callers poll
// Status(). Installing on first use can take a few minutes.
func (b *Brave) Ensure() {
	b.mu.Lock()
	if (b.phase == PhaseReady && b.port > 0) || b.working {
		b.mu.Unlock()
		return
	}
	b.working = true
	b.phase, b.message = PhaseStarting, "Preparing Brave…"
	b.mu.Unlock()

	go func() {
		defer func() {
			b.mu.Lock()
			b.working = false
			b.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		port, err := b.provision(ctx)
		if err != nil {
			b.set(PhaseError, err.Error())
			b.log.Error("brave", "err", err)
			return
		}
		b.mu.Lock()
		b.port, b.phase, b.message = port, PhaseReady, "Ready"
		b.mu.Unlock()
		b.log.Info("brave ready", "port", port)
	}()
}

// exited is called when the stream process dies, so the next Ensure restarts it.
func (b *Brave) exited(p *os.Process) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.proc != p {
		return
	}
	b.proc, b.port = nil, 0
	if b.phase == PhaseReady {
		b.phase, b.message = PhaseIdle, "Brave stopped"
	}
}

// Close stops the stream (on alfad shutdown).
func (b *Brave) Close() {
	b.mu.Lock()
	p := b.proc
	b.proc = nil
	b.mu.Unlock()
	if p != nil {
		stopProcess(p)
	}
}
