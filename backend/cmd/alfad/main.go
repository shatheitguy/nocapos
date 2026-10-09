// Command alfad is the Alfa OS system daemon: the only process with access to
// the Docker socket. It serves the REST/WebSocket API used by the Web OS.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/ai"
	"alfaos/alfad/internal/api"
	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/backup"
	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/hardware"
	"alfaos/alfad/internal/photos"
	"alfaos/alfad/internal/rdp"
	"alfaos/alfad/internal/scripts"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/terminal"
	"alfaos/alfad/internal/tlsutil"
	"alfaos/alfad/internal/webapps"
)

var version = "dev" // set via -ldflags "-X main.version=..."

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "healthcheck":
		err = healthcheck()
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "usage: alfad [serve|healthcheck|version]\n")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "alfad:", err)
		os.Exit(1)
	}
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)
	log.Info("starting alfad", "version", version, "listen", cfg.ListenAddr, "data", cfg.DataDir,
		"host_proc", cfg.HostProc, "host_sys", cfg.HostSys)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	secret := cfg.JWTSecret
	if secret == nil {
		if secret, err = auth.LoadOrCreateSecret(filepath.Join(cfg.DataDir, "secrets", "jwt.key")); err != nil {
			return fmt.Errorf("jwt secret: %w", err)
		}
	}

	setupToken := cfg.SetupToken
	users, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if users == 0 {
		if setupToken == "" {
			setupToken = randomToken()
		}
		log.Warn("first-run setup required: open the web UI and enter this setup token",
			"setup_token", setupToken)
	}
	svc := auth.NewService(st, auth.NewIssuer(secret, cfg.AccessTTL), auth.Options{
		RefreshTTL: cfg.RefreshTTL, SessionMaxAge: cfg.SessionMaxAge, SetupToken: setupToken,
	})

	dc, err := docker.New(cfg.DockerHost)
	if err != nil {
		return err
	}
	go func() {
		pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := dc.Ping(pingCtx); err != nil {
			log.Warn("docker engine not reachable; container features stay disabled until it is",
				"host", cfg.DockerHost, "err", err)
			return
		}
		log.Info("connected to docker", "host", cfg.DockerHost, "api_version", dc.APIVersion())
	}()

	sampler := hardware.NewSampler(hardware.Options{
		Proc: cfg.HostProc, Sys: cfg.HostSys, Etc: cfg.HostEtc,
		DiskPaths: cfg.DiskPaths, Interval: cfg.MetricsInterval,
	}, log)
	go sampler.Run(ctx)
	go purgeTokens(ctx, st, log)

	roots := make([]files.Root, 0, len(cfg.FileRoots))
	for _, r := range cfg.FileRoots {
		roots = append(roots, files.Root{ID: r.ID, Name: r.Name, Path: r.Path})
		log.Info("storage location", "id", r.ID, "name", r.Name, "path", r.Path)
	}
	fsvc, err := files.New(roots)
	if err != nil {
		return err
	}
	// Files must never delete, move or rename NoCapOS itself: its data, the
	// folder it runs from, and (when run from a checkout) the source tree.
	fsvc.Protect(cfg.DataDir)
	if cfg.SystemRoot {
		fsvc.Protect(files.SystemTrees...)
		fsvc.ProtectPoints(files.SystemPoints...)
	}
	if exe, err := os.Executable(); err == nil {
		fsvc.Protect(filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			fsvc.Protect(filepath.Dir(wd)) // running from backend/ in a source checkout
		}
	}

	// AI provider API keys are sealed with a key kept beside the JWT secret.
	aiKey, err := auth.LoadOrCreateSecret(filepath.Join(cfg.DataDir, "secrets", "ai.key"))
	if err != nil {
		return fmt.Errorf("ai secret: %w", err)
	}
	box, err := ai.NewSecretBox(aiKey)
	if err != nil {
		return err
	}
	aiSvc := ai.NewService(st, box, log)
	svc.SetSealer(box) // encrypt TOTP secrets with the same box

	// Who can sign in: this machine's Linux users when NoCapOS runs natively
	// as root, otherwise NoCapOS's own accounts (Docker, Windows).
	var dir accounts.Directory = accounts.NewApp(st)
	if cfg.AuthMode != "app" {
		sys, err := accounts.NewSystem()
		switch {
		case err == nil:
			svc.SetSystemAccounts(sys)
			dir = sys
			log.Info("accounts: signing in with this machine's Linux users")
		case cfg.AuthMode == "system":
			return fmt.Errorf("ALFA_AUTH=system: %w", err)
		default:
			log.Info("accounts: using NoCapOS accounts", "reason", err.Error())
		}
	}

	// Host terminal is offered on Linux native/host-mounted runs; off by default
	// in a container (where "host" would just be the container).
	termSvc := terminal.NewService(dc, cfg.AllowHostTerminal)
	braveMgr := webapps.NewBrave(log, cfg.DataDir)
	defer braveMgr.Close()
	guacd := rdp.NewGuacd(log, dc, cfg.GuacdAddr)

	catalog, err := appstore.LoadCatalog()
	if err != nil {
		return err
	}
	appMgr := appstore.NewManager(catalog, dc, st)

	fileIndex := files.NewIndex(fsvc, ctx.Done())
	photoLib := photos.New(fsvc, fileIndex, st, cfg.DataDir, log)
	if _, err := photoLib.EnsureFolder(); err != nil {
		log.Warn("photos: no library folder", "err", err)
	}

	backups := backup.NewManager(st, box, fsvc, cfg.DataDir, log)
	go backups.RunScheduler(ctx)

	srv := api.New(ctx, api.Deps{
		Config: cfg, Log: log, Store: st, Auth: svc, Docker: dc, Sampler: sampler,
		Files: fsvc, AI: aiSvc, Terminal: termSvc, Brave: braveMgr, Guacd: guacd, Accounts: dir, Version: version,
		Scripts: scripts.NewRunner(cfg.AllowHostTerminal), AppStore: appMgr,
		FileIndex: fileIndex,
		Photos:    photoLib,
		Backup:    backups,
		FileJobs:  files.NewJobs(fsvc),
	})
	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	certFile, keyFile := cfg.TLSCert, cfg.TLSKey
	if cfg.TLSAuto {
		if certFile, keyFile, err = tlsutil.EnsureSelfSigned(filepath.Join(cfg.DataDir, "tls")); err != nil {
			return fmt.Errorf("self-signed certificate: %w", err)
		}
	}
	// Bind before announcing readiness so a busy port fails loudly and early.
	ln, err := listen(cfg, log)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	if certFile != "" {
		httpSrv.TLSConfig = tlsutil.ServerConfig()
		go func() { errCh <- httpSrv.ServeTLS(ln, certFile, keyFile) }()
	} else {
		go func() { errCh <- httpSrv.Serve(ln) }()
	}
	log.Info("alfad ready", "addr", ln.Addr().String(), "tls", certFile != "")

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

// listen binds the configured address. When the address is the built-in
// default and already taken, it tries the next 10 ports instead of failing;
// an explicit ALFA_LISTEN_ADDR is always honored exactly.
func listen(cfg *config.Config, log *slog.Logger) (net.Listener, error) {
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	// On Linux the port is fixed (Traefik routes to it), so never wander there.
	if err == nil || cfg.ListenAddrExplicit || runtime.GOOS == "linux" {
		if err != nil {
			return nil, fmt.Errorf("listen on %s: %w (is another program using this port? change ALFA_LISTEN_ADDR)", cfg.ListenAddr, err)
		}
		return ln, nil
	}
	host, portStr, splitErr := net.SplitHostPort(cfg.ListenAddr)
	port, convErr := strconv.Atoi(portStr)
	if splitErr != nil || convErr != nil {
		return nil, fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
	}
	for p := port + 1; p <= port+10; p++ {
		addr := net.JoinHostPort(host, strconv.Itoa(p))
		if l, e := net.Listen("tcp", addr); e == nil {
			log.Warn("default port is busy; using the next free port", "busy", cfg.ListenAddr, "addr", addr)
			cfg.ListenAddr = addr
			return l, nil
		}
	}
	return nil, fmt.Errorf("listen on %s: %w (ports %d-%d are busy too; set ALFA_LISTEN_ADDR)", cfg.ListenAddr, err, port+1, port+10)
}

func purgeTokens(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := st.PurgeExpiredTokens(ctx, time.Now()); err != nil {
				log.Error("purge expired tokens", "err", err)
			} else if n > 0 {
				log.Debug("purged expired refresh tokens", "count", n)
			}
		}
	}
}

// healthcheck is used by the container HEALTHCHECK; distroless images have no curl.
func healthcheck() error {
	addr := os.Getenv("ALFA_LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	client := &http.Client{Timeout: 3 * time.Second}
	if os.Getenv("ALFA_TLS") == "auto" || os.Getenv("ALFA_TLS_CERT") != "" {
		// Loopback probe of our own (possibly self-signed) certificate.
		scheme = "https"
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	}
	resp, err := client.Get(scheme + "://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: HTTP %d", resp.StatusCode)
	}
	return nil
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:16] + "-" + h[16:24] + "-" + h[24:32]
}
