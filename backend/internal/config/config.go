// Package config loads alfad configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr string
	// ListenAddrExplicit is false when the default was used; only then may
	// alfad move to a nearby free port if the default is taken.
	ListenAddrExplicit bool
	DataDir            string
	DockerHost         string

	// Host filesystem views. In a container these are read-only bind mounts
	// of the host's /proc, /sys and selected /etc files.
	HostProc string
	HostSys  string
	HostEtc  string

	JWTSecret     []byte // nil => generated and persisted under DataDir
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	SessionMaxAge time.Duration // absolute lifetime of a login, regardless of refreshes
	CookieSecure  bool
	SetupToken    string // nil => generated at first run and printed to the log

	AllowedOrigins []string // extra WebSocket origin patterns, e.g. "*.alfa.local"
	TrustedProxies []netip.Prefix

	MetricsInterval time.Duration
	DiskPaths       []string
	LogLevel        string

	// FileRoots are the storage locations exposed in the Files app.
	FileRoots []FileRoot
	MaxUpload int64

	// AllowHostTerminal enables the host shell in the Terminal app. Defaults on
	// for native runs, off inside a container (where it'd just be the container).
	AllowHostTerminal bool

	// SystemRoot adds the whole filesystem ("/") to Files as "System". On by
	// default for native Linux installs running as root, off in containers.
	SystemRoot bool

	// GuacdAddr points Remote Desktop at an existing guacd (host:port). Empty =
	// find or provision one automatically.
	GuacdAddr string

	// AuthMode picks who can sign in: "system" = this machine's Linux users,
	// "app" = NoCapOS's own accounts, "auto" = system when possible.
	AuthMode string

	// TLS for native installs without Traefik: "auto" (self-signed) or
	// explicit cert/key files. Empty = plain HTTP (behind Traefik or loopback).
	TLSCert string
	TLSKey  string
	TLSAuto bool
}

func (c *Config) TLSEnabled() bool { return c.TLSAuto || c.TLSCert != "" }

type FileRoot struct {
	ID, Name, Path string
}

// alfad is never published directly; only Traefik on the Docker network
// reaches it, so private ranges cover the proxy hop.
var defaultTrustedProxies = []string{
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7",
}

func Load() (*Config, error) {
	c := &Config{
		ListenAddr:         env("ALFA_LISTEN_ADDR", defaultListenAddr()),
		ListenAddrExplicit: strings.TrimSpace(os.Getenv("ALFA_LISTEN_ADDR")) != "",
		DataDir:            env("ALFA_DATA_DIR", defaultDataDir()),
		DockerHost:         env("ALFA_DOCKER_HOST", env("DOCKER_HOST", defaultDockerHost())),
		HostProc:           hostPath("ALFA_HOST_PROC", "/host/proc", "/proc"),
		HostSys:            hostPath("ALFA_HOST_SYS", "/host/sys", "/sys"),
		HostEtc:            hostPath("ALFA_HOST_ETC", "/host/etc", "/etc"),
		SetupToken:         os.Getenv("ALFA_SETUP_TOKEN"),
		LogLevel:           env("ALFA_LOG_LEVEL", "info"),
		GuacdAddr:          strings.TrimSpace(os.Getenv("ALFA_GUACD_ADDR")),
		AuthMode:           strings.ToLower(env("ALFA_AUTH", "auto")),
	}

	var err error
	if c.AccessTTL, err = envDuration("ALFA_ACCESS_TTL", 15*time.Minute); err != nil {
		return nil, err
	}
	if c.RefreshTTL, err = envDuration("ALFA_REFRESH_TTL", 7*24*time.Hour); err != nil {
		return nil, err
	}
	if c.SessionMaxAge, err = envDuration("ALFA_SESSION_MAX_AGE", 30*24*time.Hour); err != nil {
		return nil, err
	}
	if c.MetricsInterval, err = envDuration("ALFA_METRICS_INTERVAL", 2*time.Second); err != nil {
		return nil, err
	}
	switch c.AuthMode {
	case "auto", "system", "app":
	default:
		return nil, fmt.Errorf("ALFA_AUTH: expected auto, system or app, got %q", c.AuthMode)
	}
	switch tlsMode := strings.ToLower(os.Getenv("ALFA_TLS")); tlsMode {
	case "", "off", "false":
	case "auto":
		c.TLSAuto = true
	default:
		return nil, fmt.Errorf("ALFA_TLS: expected auto or off, got %q", tlsMode)
	}
	c.TLSCert, c.TLSKey = os.Getenv("ALFA_TLS_CERT"), os.Getenv("ALFA_TLS_KEY")
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return nil, errors.New("ALFA_TLS_CERT and ALFA_TLS_KEY must be set together")
	}

	// Secure cookies need HTTPS (Traefik or built-in TLS). A loopback-only
	// native run is plain HTTP, and loopback traffic never leaves the machine.
	if c.CookieSecure, err = envBool("ALFA_COOKIE_SECURE", c.TLSEnabled() || !isLoopback(c.ListenAddr)); err != nil {
		return nil, err
	}
	if s := os.Getenv("ALFA_JWT_SECRET"); s != "" {
		c.JWTSecret = []byte(s)
	}

	c.AllowedOrigins = envList("ALFA_ALLOWED_ORIGINS", nil)
	c.DiskPaths = envList("ALFA_DISK_PATHS", defaultDiskPaths(c.DataDir))
	// Without Traefik in front (built-in TLS = clients connect directly), no
	// LAN peer is a proxy: trusting private ranges would let any device on the
	// network spoof X-Forwarded-For and dodge the login rate limit.
	trusted := defaultTrustedProxies
	if c.TLSEnabled() {
		trusted = []string{"127.0.0.0/8", "::1/128"}
	}
	for _, p := range envList("ALFA_TRUSTED_PROXIES", trusted) {
		pfx, err := netip.ParsePrefix(p)
		if err != nil {
			return nil, fmt.Errorf("ALFA_TRUSTED_PROXIES: %w", err)
		}
		c.TrustedProxies = append(c.TrustedProxies, pfx.Masked())
	}

	if c.FileRoots, err = parseFileRoots(os.Getenv("ALFA_FILE_ROOTS"), c.DataDir); err != nil {
		return nil, err
	}
	if c.SystemRoot, err = envBool("ALFA_SYSTEM_ROOT", runtime.GOOS == "linux" && os.Geteuid() == 0 && !InContainer()); err != nil {
		return nil, err
	}
	if c.SystemRoot {
		c.FileRoots = append(c.FileRoots, FileRoot{ID: "system", Name: "System", Path: "/"})
	}
	// Default: allow the host terminal unless we're clearly in a container
	// (the container mount point exists). An explicit env var overrides.
	if c.AllowHostTerminal, err = envBool("ALFA_ALLOW_HOST_TERMINAL", !InContainer()); err != nil {
		return nil, err
	}

	c.MaxUpload = 16 << 30 // 16 GiB per request
	if v := os.Getenv("ALFA_MAX_UPLOAD_MB"); v != "" {
		mb, err := strconv.ParseInt(v, 10, 64)
		if err != nil || mb <= 0 {
			return nil, fmt.Errorf("ALFA_MAX_UPLOAD_MB: invalid value %q", v)
		}
		c.MaxUpload = mb << 20
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate() error {
	var errs []error
	if c.JWTSecret != nil && len(c.JWTSecret) < 32 {
		errs = append(errs, errors.New("ALFA_JWT_SECRET must be at least 32 bytes"))
	}
	if c.AccessTTL < time.Minute {
		errs = append(errs, errors.New("ALFA_ACCESS_TTL must be at least 1m"))
	}
	if c.RefreshTTL <= c.AccessTTL {
		errs = append(errs, errors.New("ALFA_REFRESH_TTL must be longer than ALFA_ACCESS_TTL"))
	}
	if c.SessionMaxAge < c.RefreshTTL {
		errs = append(errs, errors.New("ALFA_SESSION_MAX_AGE must be at least ALFA_REFRESH_TTL"))
	}
	if c.MetricsInterval < 500*time.Millisecond {
		errs = append(errs, errors.New("ALFA_METRICS_INTERVAL must be at least 500ms"))
	}
	return errors.Join(errs...)
}

// Linux defaults target the container image; other platforms are native
// desktop runs (development, or Alfa OS as a normal app).
// Native desktop runs avoid 8080: it is a popular port (UniFi, Tomcat,
// dev servers, port proxies) and a clash there looks like Alfa OS failing.
func defaultListenAddr() string {
	if runtime.GOOS == "linux" {
		return ":8080" // container port behind Traefik
	}
	return "127.0.0.1:8088"
}

func defaultDataDir() string {
	if runtime.GOOS == "linux" {
		return "/data"
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "AlfaOS")
	}
	return "data"
}

func defaultDockerHost() string {
	if runtime.GOOS == "windows" {
		return "npipe:////./pipe/docker_engine"
	}
	return "unix:///var/run/docker.sock"
}

func defaultDiskPaths(dataDir string) []string {
	if runtime.GOOS == "windows" {
		return []string{nonEmptyEnv("SystemDrive", "C:") + `\`}
	}
	return []string{"/", dataDir}
}

// parseFileRoots reads "Name=path,Other Name=path". Without it, Alfa Drive
// (<data>/files) is exposed, plus the user's home folder on desktop platforms.
func parseFileRoots(spec, dataDir string) ([]FileRoot, error) {
	var roots []FileRoot
	if strings.TrimSpace(spec) == "" {
		roots = append(roots, FileRoot{ID: "drive", Name: "NoCap Drive", Path: filepath.Join(dataDir, "files")})
		if runtime.GOOS != "linux" {
			if home, err := os.UserHomeDir(); err == nil {
				roots = append(roots, FileRoot{ID: "home", Name: "Home", Path: home})
			}
		}
		return roots, nil
	}
	seen := map[string]bool{}
	for _, item := range strings.Split(spec, ",") {
		name, p, ok := strings.Cut(strings.TrimSpace(item), "=")
		name, p = strings.TrimSpace(name), strings.TrimSpace(p)
		if !ok || name == "" || p == "" {
			return nil, fmt.Errorf("ALFA_FILE_ROOTS: expected Name=path, got %q", item)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("ALFA_FILE_ROOTS: %w", err)
		}
		id := slug(name)
		for i := 2; seen[id]; i++ {
			id = fmt.Sprintf("%s-%d", slug(name), i)
		}
		seen[id] = true
		roots = append(roots, FileRoot{ID: id, Name: name, Path: abs})
	}
	return roots, nil
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "root"
	}
	return out
}

func isLoopback(listenAddr string) bool {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// InContainer reports whether alfad runs inside a Docker/Podman container.
func InContainer() bool { return fileExists("/.dockerenv") || fileExists("/run/.containerenv") }

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func nonEmptyEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// hostPath prefers an explicit override, then the container mount point,
// then the native path (bare-metal / dev runs).
func hostPath(key, mounted, native string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if _, err := os.Stat(mounted); err == nil {
		return mounted
	}
	return native
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func envBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}

func envList(key string, def []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
