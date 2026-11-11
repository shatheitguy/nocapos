package hostaccess

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- the screen in the browser ----
//
// The NoCap Desktop window always shows exactly what's on the server's
// monitor: the sign-in screen when nobody is signed in, and whoever is
// signed in after that. Signing in through the window signs in on the
// screen itself (it's the same sign-in screen), so the window and the
// monitor are always one and the same session.
//
// alfad follows seat0's active session and attaches a screen server to it:
//
//   - Wayland sessions (the sign-in screen and people's desktops):
//     GNOME's own remote desktop daemon, run inside that session as its
//     owner (the sign-in screen's gdm account or the person) in mirror
//     mode, over RDP. GNOME's system-wide remote sign-in isn't used: it
//     opens a separate session instead of showing the monitor.
//   - X11 sessions (Xorg, e.g. with NVIDIA's driver): x11vnc on that
//     session's display, over VNC.
//
// When the screen's session changes (someone signs in or out), the old
// server stops and the browser window reconnects to the new one on its own.
// Each start gets a new random password and certificate, so nobody who
// could read one session's files can use them on the next. Both servers
// listen on 127.0.0.1 only (install.sh's remote guard drops other
// connections to those ports), and NoCapOS's Remote Desktop engine (guacd)
// connects over loopback. Secrets reach programs in files, never in their
// arguments.

// Ports on 127.0.0.1.
const (
	ScreenRDPPort = 3391 // the screen, Wayland sessions
	ScreenVNCPort = 5906 // the screen, X11 sessions
)

// ScreenUnit is the transient unit the screen server runs in.
const ScreenUnit = "nocapos-screen.service"

// ScreenDir holds the screen server's settings, password and certificate.
const ScreenDir = "/run/nocapos/screen"

// GNOME's remote desktop daemon, wherever this distribution keeps it.
var grdDaemons = []string{
	"/usr/libexec/gnome-remote-desktop-daemon",
	"/usr/lib/gnome-remote-desktop-daemon",
	"/usr/lib/gnome-remote-desktop/gnome-remote-desktop-daemon",
}

// RemoteTarget is what guacd connects to.
type RemoteTarget struct {
	Protocol string // rdp | vnc
	Port     int
	Username string
	Password string
	// Session is the screen's session being shown; SignIn: it's the
	// sign-in screen.
	Session string
	SignIn  bool
}

// ErrRemoteOff: NoCap Desktop is off, so there's no screen to show.
var ErrRemoteOff = errors.New("NoCap Desktop is off, so there's no screen to show. Turn it on in Settings → General → This Screen & Access")

// ErrNoRemoteTool: the remote screen server isn't installed.
var ErrNoRemoteTool = errors.New("the remote screen server isn't installed here; re-run the NoCapOS installer with --desktop to add it")

// ErrScreenStarting: the screen has no session right now (the sign-in
// screen is starting, or a sign-in or sign-out is under way).
var ErrScreenStarting = errors.New("the server's screen is starting or switching sessions; trying again in a moment")

// screenMirror is the screen server alfad has running.
type screenMirror struct {
	mu     sync.Mutex
	key    string // session ID/type it shows ("" = none)
	target RemoteTarget
}

func (d *Desktop) lookPath(name string) bool {
	if d.LookPath != nil {
		return d.LookPath(name)
	}
	_, err := exec.LookPath(name)
	return err == nil
}

func (d *Desktop) dial(addr string, timeout time.Duration) error {
	if d.Dial != nil {
		return d.Dial(addr, timeout)
	}
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err == nil {
		c.Close()
	}
	return err
}

func (d *Desktop) portOpen(port int) bool {
	return d.dial("127.0.0.1:"+strconv.Itoa(port), 500*time.Millisecond) == nil
}

// waitPort waits up to max for 127.0.0.1:port to answer.
func (d *Desktop) waitPort(port int, max time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		if d.portOpen(port) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the desktop's remote screen didn't start (nothing on 127.0.0.1:%d)", port)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (d *Desktop) grdDaemon() string {
	for _, p := range grdDaemons {
		if d.exists(p) {
			return p
		}
	}
	return ""
}

func (d *Desktop) remoteSupported(mode string) bool {
	return mode == ModeFull && (d.grdDaemon() != "" || d.lookPath("x11vnc"))
}

const pwChars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// randomPassword is letters and digits only, so it needs no quoting in
// GVariant text or key files.
func randomPassword(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(pwChars)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b[i] = pwChars[v.Int64()]
	}
	return string(b)
}

// newTLSPair is a fresh self-signed certificate for the screen server
// (guacd is told to accept it: the connection never leaves this machine).
func newTLSPair() (certPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "NoCap Desktop", Organization: []string{"NoCapOS"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

// Account is a Linux account's numbers and home.
type Account struct {
	UID, GID int
	Home     string
}

func (d *Desktop) account(name string) (Account, error) {
	if d.LookupAccount != nil {
		return d.LookupAccount(name)
	}
	u, err := user.Lookup(name)
	if err != nil {
		return Account{}, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return Account{UID: uid, GID: gid, Home: u.HomeDir}, nil
}

func (d *Desktop) chown(p string, uid, gid int) error {
	if d.Chown != nil {
		return d.Chown(p, uid, gid)
	}
	return os.Chown(p, uid, gid)
}

func (d *Desktop) mkdir(p string, mode os.FileMode) error {
	if d.WriteFile != nil {
		return nil // a fake machine: files only
	}
	return os.MkdirAll(p, mode)
}

func (d *Desktop) run(ctx context.Context, name string, args ...string) error {
	out, err := d.Run(ctx, name, args...)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s: %s", name, msg)
	}
	return nil
}

// GRDSettings renders the key file GNOME's remote desktop daemon reads its
// settings from (GSETTINGS_BACKEND=keyfile): RDP on port, mirroring the
// monitor, with input. These never touch the person's own GNOME settings
// (their own screen sharing stays as they set it). Keys an older GNOME
// doesn't have are ignored.
func GRDSettings(port int, certPath, keyPath string) string {
	return fmt.Sprintf(`# NoCap Desktop's screen server (written by NoCapOS)
[org/gnome/desktop/remote-desktop/rdp]
enable=true
port=%d
negotiate-port=false
view-only=false
screen-share-mode='mirror-primary'
tls-cert='%s'
tls-key='%s'

[org/gnome/desktop/remote-desktop/vnc]
enable=false
`, port, certPath, keyPath)
}

// GRDCredentials renders the daemon's credentials file (GVariant text).
func GRDCredentials(username, password string) string {
	return fmt.Sprintf("[RDP]\ncredentials={'username': <'%s'>, 'password': <'%s'>}\n", username, password)
}

// screenUnitArgs are systemd-run's options for the screen server unit.
func screenUnitArgs() []string {
	return []string{"--unit=" + strings.TrimSuffix(ScreenUnit, ".service"), "--collect", "--quiet", "--property=KillMode=mixed"}
}

// mirrorWayland runs GNOME's remote desktop daemon inside session s (as its
// owner, on its D-Bus), with settings, password and certificate of its own.
func (d *Desktop) mirrorWayland(ctx context.Context, s Session, env map[string]string) (RemoteTarget, error) {
	daemon := d.grdDaemon()
	if daemon == "" {
		return RemoteTarget{}, ErrNoRemoteTool
	}
	a, err := d.account(s.User)
	if err != nil {
		return RemoteTarget{}, fmt.Errorf("the screen's account %s: %w", s.User, err)
	}
	dir := path.Join(ScreenDir, strconv.Itoa(s.UID))
	conf, data := path.Join(dir, "config"), path.Join(dir, "data")
	settingsDir, credsDir := path.Join(conf, "glib-2.0", "settings"), path.Join(data, "gnome-remote-desktop")
	// A fresh folder every time (never one the session's owner could have
	// changed): /run/nocapos/screen is root's, the folder in it theirs.
	if d.WriteFile == nil {
		_ = os.RemoveAll(dir)
	}
	if err := d.mkdir(ScreenDir, 0o755); err != nil {
		return RemoteTarget{}, err
	}
	for _, p := range []string{dir, settingsDir, credsDir} {
		if err := d.mkdir(p, 0o700); err != nil {
			return RemoteTarget{}, err
		}
	}
	certPEM, keyPEM, err := newTLSPair()
	if err != nil {
		return RemoteTarget{}, err
	}
	pw := randomPassword(32)
	certPath, keyPath := path.Join(dir, "tls.crt"), path.Join(dir, "tls.key")
	files := []struct {
		p    string
		b    string
		mode os.FileMode
	}{
		{certPath, string(certPEM), 0o644},
		{keyPath, string(keyPEM), 0o600},
		{path.Join(settingsDir, "keyfile"), GRDSettings(ScreenRDPPort, certPath, keyPath), 0o600},
		{path.Join(credsDir, "credentials.ini"), GRDCredentials("nocap", pw), 0o600},
	}
	for _, f := range files {
		if err := d.writeFile(f.p, []byte(f.b), f.mode); err != nil {
			return RemoteTarget{}, err
		}
	}
	for _, p := range []string{dir, conf, path.Join(conf, "glib-2.0"), settingsDir, data, credsDir, certPath, keyPath, files[2].p, files[3].p} {
		_ = d.chown(p, a.UID, a.GID)
	}
	// Screen capture goes through the session's PipeWire.
	_ = d.run(ctx, "systemctl", "--user", "--machine="+s.User+"@.host", "start", "pipewire.service", "wireplumber.service")
	args := append(screenUnitArgs(), "--uid="+s.User,
		"--setenv=XDG_RUNTIME_DIR="+env["XDG_RUNTIME_DIR"],
		"--setenv=DBUS_SESSION_BUS_ADDRESS="+env["DBUS_SESSION_BUS_ADDRESS"],
		"--setenv=HOME="+dir,
		"--setenv=XDG_CONFIG_HOME="+conf,
		"--setenv=XDG_DATA_HOME="+data,
		"--setenv=GSETTINGS_BACKEND=keyfile",
	)
	if w := env["WAYLAND_DISPLAY"]; w != "" {
		args = append(args, "--setenv=WAYLAND_DISPLAY="+w)
	}
	args = append(args, daemon, "--headless")
	if err := d.run(ctx, "systemd-run", args...); err != nil {
		return RemoteTarget{}, err
	}
	if err := d.waitPort(ScreenRDPPort, 20*time.Second); err != nil {
		return RemoteTarget{}, fmt.Errorf("%w (see: journalctl -u %s)", err, ScreenUnit)
	}
	return RemoteTarget{Protocol: "rdp", Port: ScreenRDPPort, Username: "nocap", Password: pw}, nil
}

// mirrorX11 runs x11vnc on session s's X display.
func (d *Desktop) mirrorX11(ctx context.Context, s Session, env map[string]string) (RemoteTarget, error) {
	if !d.lookPath("x11vnc") {
		return RemoteTarget{}, ErrNoRemoteTool
	}
	disp := env["DISPLAY"]
	if !xDisplayRe.MatchString(disp) {
		return RemoteTarget{}, errors.New("the screen's X display isn't known")
	}
	if err := d.mkdir(ScreenDir, 0o755); err != nil {
		return RemoteTarget{}, err
	}
	pw := randomPassword(8) // VNC passwords are 8 characters
	pass := path.Join(ScreenDir, "vnc.pass")
	if err := d.writeFile(pass, []byte(pw+"\n"), 0o600); err != nil {
		return RemoteTarget{}, err
	}
	args := append(screenUnitArgs(), "x11vnc", "-display", disp)
	if xa := env["XAUTHORITY"]; xa != "" {
		args = append(args, "-auth", xa)
	} else {
		args = append(args, "-auth", "guess")
	}
	// rm: x11vnc reads the password file and deletes it.
	args = append(args, "-localhost", "-rfbport", strconv.Itoa(ScreenVNCPort), "-forever", "-shared", "-noxdamage", "-repeat", "-passwdfile", "rm:"+pass, "-quiet")
	if err := d.run(ctx, "systemd-run", args...); err != nil {
		return RemoteTarget{}, err
	}
	if err := d.waitPort(ScreenVNCPort, 15*time.Second); err != nil {
		return RemoteTarget{}, fmt.Errorf("%w (see: journalctl -u %s)", err, ScreenUnit)
	}
	return RemoteTarget{Protocol: "vnc", Port: ScreenVNCPort, Password: pw}, nil
}

func sessionKey(s Session) string { return s.ID + "/" + s.Type }

// stopMirrorLocked stops the screen server (d.mirror.mu held).
func (d *Desktop) stopMirrorLocked(ctx context.Context) {
	_, _ = d.Run(ctx, "systemctl", "stop", ScreenUnit)
	_, _ = d.Run(ctx, "systemctl", "reset-failed", ScreenUnit)
	d.mirror.key, d.mirror.target = "", RemoteTarget{}
}

// StopScreen stops the screen server, if one runs.
func (d *Desktop) StopScreen(ctx context.Context) {
	d.mirror.mu.Lock()
	defer d.mirror.mu.Unlock()
	if d.mirror.key != "" {
		d.stopMirrorLocked(ctx)
	}
}

// showSession attaches the screen server to session s (or keeps the one
// already showing it).
func (d *Desktop) showSession(ctx context.Context, s Session) (RemoteTarget, error) {
	d.mirror.mu.Lock()
	defer d.mirror.mu.Unlock()
	if d.mirror.key == sessionKey(s) && d.portOpen(d.mirror.target.Port) {
		return d.mirror.target, nil
	}
	// Whatever ran before (another session, or an earlier alfad's) goes.
	d.stopMirrorLocked(ctx)
	env := d.screenEnv(s)
	var t RemoteTarget
	var err error
	if s.Type == "x11" {
		t, err = d.mirrorX11(ctx, s, env)
	} else {
		t, err = d.mirrorWayland(ctx, s, env)
	}
	if err != nil {
		_, _ = d.Run(ctx, "systemctl", "stop", ScreenUnit)
		return RemoteTarget{}, err
	}
	t.Session, t.SignIn = s.ID, s.Greeter
	d.mirror.key, d.mirror.target = sessionKey(s), t
	return t, nil
}

// FollowScreen stops the screen server once the screen's session changed
// (a sign-in or sign-out): the browser window then reconnects, and
// RemoteView attaches to the new session. Run by WatchSession.
func (d *Desktop) FollowScreen(ctx context.Context) {
	d.mirror.mu.Lock()
	defer d.mirror.mu.Unlock()
	if d.mirror.key == "" {
		return
	}
	if s, ok := d.ScreenSession(ctx); ok && sessionKey(s) == d.mirror.key {
		return
	}
	d.stopMirrorLocked(ctx)
}

// RemoteView gets the screen ready to show in the browser and says where
// guacd should connect.
func (d *Desktop) RemoteView(ctx context.Context) (RemoteTarget, error) {
	st := d.State(ctx)
	if !st.Available {
		return RemoteTarget{}, ErrDesktopUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	switch st.Mode {
	case ModeFull:
		if !d.remoteSupported(ModeFull) {
			return RemoteTarget{}, ErrNoRemoteTool
		}
		// Between a sign-in and the desktop coming up (or a sign-out and
		// the sign-in screen), the screen has no session for a moment.
		wait := d.SessionWait
		if wait == 0 {
			wait = 15 * time.Second
		}
		deadline := time.Now().Add(wait)
		for {
			if s, ok := d.ScreenSession(ctx); ok {
				return d.showSession(ctx, s)
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				return RemoteTarget{}, ErrScreenStarting
			}
			time.Sleep(time.Second)
		}
	}
	return RemoteTarget{}, ErrRemoteOff
}
