package rdp

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/docker"
)

const (
	guacdPort      = 4822
	guacdContainer = "nocap-guacd"
)

// guacdRef pins the guacd container image (name:tag@digest). The OS image
// build reads the same file to preload that image, so the two never drift.
//
//go:embed guacd.ref
var guacdRef string

// guacdImage and guacdTag name the pinned image: the one the OS images
// preload (docker load keeps the tag) and the one pulled otherwise.
var guacdImage, guacdTag = parseGuacdRef(guacdRef)

// parseGuacdRef reads the first non-comment line of guacd.ref.
func parseGuacdRef(s string) (image, tag string) {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line, _, _ = strings.Cut(line, "@")
		if i := strings.LastIndex(line, ":"); i > 0 && !strings.Contains(line[i:], "/") {
			return line[:i], line[i+1:]
		}
		return line, "latest"
	}
	panic("rdp: guacd.ref has no image reference")
}

type Phase string

const (
	PhaseIdle      Phase = "idle"
	PhasePreparing Phase = "preparing"
	PhaseReady     Phase = "ready"
	PhaseError     Phase = "error"
)

type Status struct {
	Phase   Phase  `json:"phase"`
	Message string `json:"message"`
	Ready   bool   `json:"ready"`
}

// Guacd finds or provisions an RDP engine (guacd) for alfad to talk to.
//
// Order: an explicit address (ALFA_GUACD_ADDR) → a guacd already listening on
// 127.0.0.1:4822 → a native install on Linux → the official guacd container.
type Guacd struct {
	log      *slog.Logger
	dc       *docker.Client
	override string

	mu      sync.Mutex
	phase   Phase
	message string
	addr    string
	working bool
}

func NewGuacd(log *slog.Logger, dc *docker.Client, override string) *Guacd {
	return &Guacd{log: log, dc: dc, override: override, phase: PhaseIdle}
}

func (g *Guacd) Status() Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	return Status{Phase: g.phase, Message: g.message, Ready: g.phase == PhaseReady}
}

// Addr returns guacd's address once ready.
func (g *Guacd) Addr() (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addr, g.phase == PhaseReady
}

func (g *Guacd) set(p Phase, msg string) {
	g.mu.Lock()
	g.phase, g.message = p, msg
	g.mu.Unlock()
}

// Ensure starts provisioning in the background if guacd isn't ready (or has
// gone away), and returns immediately.
func (g *Guacd) Ensure() {
	g.mu.Lock()
	if g.working {
		g.mu.Unlock()
		return
	}
	if g.phase == PhaseReady && reachable(g.addr) {
		g.mu.Unlock()
		return
	}
	g.working = true
	g.phase, g.message = PhasePreparing, "Preparing the remote desktop engine…"
	g.mu.Unlock()

	go func() {
		defer func() {
			// A bug here must not crash alfad: that would restart the whole
			// process (under systemd) and look, from the Remote Desktop
			// window, like it never leaves "Preparing" at all.
			if r := recover(); r != nil {
				g.set(PhaseError, "The remote desktop engine failed to start (internal error).")
				g.log.Error("guacd panic", "recover", r, "stack", string(debug.Stack()))
			}
			g.mu.Lock()
			g.working = false
			g.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		addr, err := g.provision(ctx)
		if err != nil {
			g.set(PhaseError, err.Error())
			g.log.Error("guacd", "err", err)
			return
		}
		g.mu.Lock()
		g.addr, g.phase, g.message = addr, PhaseReady, "Ready"
		g.mu.Unlock()
		g.log.Info("guacd ready", "addr", addr)
	}()
}

func (g *Guacd) provision(ctx context.Context) (string, error) {
	if g.override != "" {
		if !reachable(g.override) {
			return "", errors.New("guacd is not reachable at " + g.override)
		}
		return g.override, nil
	}
	local := "127.0.0.1:" + strconv.Itoa(guacdPort)
	if reachable(local) {
		return local, nil
	}
	if addr, err := g.native(ctx); err == nil {
		return addr, nil
	} else if err != errNoNative {
		g.log.Warn("native guacd unavailable, falling back to container", "err", err)
	}
	return g.container(ctx)
}

var errNoNative = errors.New("no native guacd on this platform")

// container runs the official guacd image, published on loopback only.
func (g *Guacd) container(ctx context.Context) (string, error) {
	if g.dc == nil {
		return "", errors.New("no RDP engine available: install guacd or Docker")
	}
	const private = "4822/tcp"
	id, running, ok, err := g.dc.FindContainer(ctx, guacdContainer)
	if err != nil {
		return "", errors.New("no RDP engine available: guacd isn't installed and Docker isn't reachable")
	}
	if ok {
		if !running {
			if err := g.dc.StartContainer(ctx, id); err != nil {
				return "", err
			}
		}
		if port, _ := g.dc.HostPort(ctx, id, private); port > 0 {
			return waitReachable("127.0.0.1:" + strconv.Itoa(port))
		}
		_ = g.dc.RemoveContainer(ctx, id)
	}
	ref := guacdImage + ":" + guacdTag
	exists, _ := g.dc.ImageExists(ctx, ref)
	if !exists && g.waitPreload(ctx) {
		// The OS image's first boot was still loading its bundled copy.
		exists, _ = g.dc.ImageExists(ctx, ref)
	}
	if !exists {
		g.set(PhasePreparing, "Downloading the remote desktop engine (guacd)…")
		if err := g.dc.PullImage(ctx, guacdImage, guacdTag, nil); err != nil {
			return "", errors.New("could not download guacd: " + err.Error())
		}
	}
	g.set(PhasePreparing, "Starting the remote desktop engine…")
	newID, err := g.dc.CreateContainer(ctx, guacdContainer, docker.CreateConfig{
		Image:        ref,
		Labels:       map[string]string{docker.LabelSystem: "guacd"},
		ExposedPorts: map[string]struct{}{private: {}},
		HostConfig: docker.HostConfig{
			PortBindings:  map[string][]docker.PortBinding{private: {{HostIP: "127.0.0.1", HostPort: "0"}}},
			RestartPolicy: &docker.RestartPolicy{Name: "unless-stopped"},
		},
	})
	if err != nil {
		return "", errors.New("could not create guacd: " + err.Error())
	}
	if err := g.dc.StartContainer(ctx, newID); err != nil {
		return "", errors.New("could not start guacd: " + err.Error())
	}
	port, err := g.dc.HostPort(ctx, newID, private)
	if err != nil || port == 0 {
		return "", errors.New("guacd started but no port was published")
	}
	return waitReachable("127.0.0.1:" + strconv.Itoa(port))
}

func reachable(addr string) bool {
	if addr == "" {
		return false
	}
	c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func waitReachable(addr string) (string, error) {
	for i := 0; i < 60; i++ {
		if reachable(addr) {
			return addr, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return "", errors.New("guacd did not start listening on " + addr)
}
