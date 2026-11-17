// Command nocap-installer is the NoCapOS installer's backend: it runs on the
// NoCapOS USB stick, serves the full-screen setup (the NoCap web UI's
// /installer page) to the kiosk window on this machine only, lists the
// disks and writes NoCapOS onto the chosen one (internal/installer).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"alfaos/alfad/internal/installer"
	"alfaos/alfad/internal/osimage"
	"alfaos/alfad/web"
)

var version = "dev"

// The installer's own mount points: the disk holding one is the USB stick.
var bootMounts = []string{"/run/rugix/mounts/config", "/run/rugix/mounts/data"}

func main() {
	listen := flag.String("listen", "127.0.0.1:8790", "address to serve the setup on (loopback only)")
	configImage := flag.String("config-image", "/run/rugix/mounts/data/nocapos-install/config.img", "the pristine startup partition")
	demo := flag.Bool("demo", false, "made-up disks and a pretend install (for working on the screens)")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if host, _, err := net.SplitHostPort(*listen); err != nil || !(host == "127.0.0.1" || host == "::1" || host == "localhost") {
		log.Error("the installer only listens on this machine (loopback)", "listen", *listen)
		os.Exit(2)
	}
	s := &server{log: log, inst: installer.New(installer.Source{ConfigImage: *configImage}), list: listDisks}
	if *demo {
		s.inst, s.list = demoInstaller(), demoDisks
		log.Warn("demo mode: nothing is written to any disk")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: *listen, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("NoCapOS installer ready", "version", version, "listen", *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("installer", "err", err)
		os.Exit(1)
	}
}

type server struct {
	log  *slog.Logger
	inst *installer.Installer
	list func(context.Context) ([]installer.Disk, error)
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/installer/info", s.info)
	mux.HandleFunc("POST /api/installer/install", s.install)
	mux.HandleFunc("GET /api/installer/progress", s.progress)
	mux.HandleFunc("POST /api/installer/restart", s.power("reboot"))
	mux.HandleFunc("POST /api/installer/poweroff", s.power("poweroff"))
	mux.Handle("/", spa(web.Assets()))
	return loopbackOnly(mux)
}

// loopbackOnly: the setup is for this machine's own screen.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// spa serves the web UI; unknown paths (/installer) get index.html.
func spa(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := assets.Open(p); err == nil {
				_ = f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		b, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.Error(w, "the setup screen is missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"message": msg}})
}

func listDisks(ctx context.Context) ([]installer.Disk, error) {
	out, err := exec.CommandContext(ctx, "lsblk", installer.LsblkArgs...).Output()
	if err != nil {
		return nil, err
	}
	return installer.ParseDisks(out, bootMounts)
}

type infoResp struct {
	Version  string           `json:"version"`
	Disks    []installer.Disk `json:"disks"`
	MinBytes int64            `json:"min_bytes"`
	Layout   map[string]int64 `json:"layout"`
	TPM      bool             `json:"tpm"`
	Usages   []string         `json:"usages"`
}

func (s *server) info(w http.ResponseWriter, r *http.Request) {
	disks, err := s.list(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Couldn't list the disks: "+err.Error())
		return
	}
	if disks == nil {
		disks = []installer.Disk{}
	}
	writeJSON(w, http.StatusOK, infoResp{
		Version: version, Disks: disks, MinBytes: installer.MinDiskBytes, TPM: installer.HasTPM(),
		Layout: map[string]int64{"startup": installer.ConfigBytes + 2*installer.BootBytes, "system": 2 * installer.SystemBytes},
		Usages: []string{osimage.UsageDesktopWeb, osimage.UsageDesktop, osimage.UsageWeb},
	})
}

func (s *server) install(w http.ResponseWriter, r *http.Request) {
	var req installer.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	disks, err := s.list(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Couldn't list the disks: "+err.Error())
		return
	}
	if err := installer.Validate(&req, disks); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, d := range disks {
		if d.Boot {
			s.inst.Src.Disk = d.Path
		}
	}
	if s.inst.Src.Disk == "" {
		writeErr(w, http.StatusConflict, "Couldn't find the drive the installer runs from.")
		return
	}
	if err := s.inst.Start(req); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.log.Info("installing", "disk", req.Disk, "encrypt", req.Encrypt, "tpm", req.UseTPM, "usage", req.Usage)
	writeJSON(w, http.StatusAccepted, s.inst.Progress())
}

func (s *server) progress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.inst.Progress())
}

func (s *server) power(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p := s.inst.Progress(); p.Running {
			writeErr(w, http.StatusConflict, "NoCapOS is still being installed.")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
		go func() {
			time.Sleep(time.Second)
			_ = exec.Command("systemctl", action).Run()
		}()
	}
}
