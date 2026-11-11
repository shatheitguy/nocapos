package hostaccess

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
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
// The NoCap Desktop window shows exactly what's on the server's monitor.
// For the full desktop that's GNOME's own remote desktop server
// (gnome-remote-desktop, RDP): screen sharing of whoever is signed in, or,
// with nobody signed in, a remote sign-in where GNOME supports it (46 and
// newer). For NoCapOS full screen it's a VNC server on the kiosk's display.
// Either one listens on this machine only (install.sh's remote guard drops
// other connections to those ports), and NoCapOS's Remote Desktop engine
// (guacd) connects over loopback with credentials NoCapOS made and keeps in
// its data folder. Secrets reach programs on stdin or in files, never in
// their arguments.

// Ports on 127.0.0.1.
const (
	RemoteLoginPort = 3389 // gnome-remote-desktop's system daemon (remote sign-in)
	SharePort       = 3390 // screen sharing of the signed-in session (GNOME 46+)
	ShareOldPort    = 3389 // the same on older GNOME, which can't move it
	VNCPort         = 5905
)

// RemoteTarget is what guacd connects to.
type RemoteTarget struct {
	Protocol string // rdp | vnc
	Port     int
	Username string
	Password string
}

// ErrRemoteOff: NoCap Desktop is off, so there's no screen to show.
var ErrRemoteOff = errors.New("NoCap Desktop is off, so there's no screen to show. Turn it on in Settings → General → This Screen & Access")

// ErrNoRemoteTool: the remote screen server isn't installed.
var ErrNoRemoteTool = errors.New("the remote screen server isn't installed here; re-run the NoCapOS installer with --desktop to add it")

// ErrSignInFirst: nobody is signed in on the screen and this GNOME can't
// offer a remote sign-in.
var ErrSignInFirst = errors.New("nobody is signed in on the server's screen yet. Sign in there once, then open this again")

type remoteCreds struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// remote state shared across requests.
type remoteState struct {
	mu     sync.Mutex
	shared map[string]int // session ID -> port, already set up
}

var remoteMu remoteState

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

// waitPort waits up to max for 127.0.0.1:port to answer.
func (d *Desktop) waitPort(port int, max time.Duration) error {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(max)
	for {
		if err := d.dial(addr, 500*time.Millisecond); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the desktop's remote screen didn't start (nothing on %s)", addr)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (d *Desktop) remoteSupported(mode string) bool {
	switch mode {
	case ModeFull:
		return d.lookPath("grdctl")
	case ModeKiosk:
		return d.lookPath("wayvnc") || d.lookPath("x11vnc")
	}
	return false
}

func (d *Desktop) remoteDir() string { return path.Join(d.DataDir, "desktop-remote") }

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

// credentials are made once and kept (0600) in the data folder.
func (d *Desktop) credentials() (remoteCreds, error) {
	p := path.Join(d.remoteDir(), "credentials.json")
	if b, err := d.readFile(p); err == nil {
		var c remoteCreds
		if json.Unmarshal(b, &c) == nil && c.Username != "" && len(c.Password) >= 16 {
			return c, nil
		}
	}
	c := remoteCreds{Username: "nocap", Password: randomPassword(32)}
	b, _ := json.Marshal(c)
	if err := os.MkdirAll(d.remoteDir(), 0o700); err != nil && d.WriteFile == nil {
		return c, err
	}
	return c, d.writeFile(p, b, 0o600)
}

// tlsPair is the remote screen's self-signed certificate (guacd is told to
// accept it: the connection never leaves this machine).
func (d *Desktop) tlsPair() (certPEM, keyPEM []byte, err error) {
	cp, kp := path.Join(d.remoteDir(), "tls.crt"), path.Join(d.remoteDir(), "tls.key")
	if c, err1 := d.readFile(cp); err1 == nil {
		if k, err2 := d.readFile(kp); err2 == nil {
			return c, k, nil
		}
	}
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
	_ = os.MkdirAll(d.remoteDir(), 0o700)
	if err := d.writeFile(cp, certPEM, 0o644); err != nil {
		return nil, nil, err
	}
	if err := d.writeFile(kp, keyPEM, 0o600); err != nil {
		return nil, nil, err
	}
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

// placeTLS copies the certificate and key where account can read them.
func (d *Desktop) placeTLS(dir string, a Account) (certPath, keyPath string, err error) {
	c, k, err := d.tlsPair()
	if err != nil {
		return "", "", err
	}
	certPath, keyPath = path.Join(dir, "nocapos-tls.crt"), path.Join(dir, "nocapos-tls.key")
	if d.WriteFile == nil {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", err
		}
		_ = d.chown(dir, a.UID, a.GID)
	}
	if err := d.writeFile(certPath, c, 0o644); err != nil {
		return "", "", err
	}
	if err := d.writeFile(keyPath, k, 0o600); err != nil {
		return "", "", err
	}
	_ = d.chown(certPath, a.UID, a.GID)
	_ = d.chown(keyPath, a.UID, a.GID)
	return certPath, keyPath, nil
}

// asUser runs argv inside a signed-in person's session (their D-Bus and
// keyring), as them.
func asUser(s Session, argv ...string) []string {
	rt := "/run/user/" + strconv.Itoa(s.UID)
	return append([]string{"-u", s.User, "--", "env", "XDG_RUNTIME_DIR=" + rt, "DBUS_SESSION_BUS_ADDRESS=unix:path=" + rt + "/bus"}, argv...)
}

// rdpSecret is gnome-remote-desktop's stored form of the credentials (a
// GVariant dictionary), for its keyring entry or credentials file.
func rdpSecret(c remoteCreds) string {
	return fmt.Sprintf("{'username': <'%s'>, 'password': <'%s'>}", c.Username, c.Password)
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

// shareSession turns on screen sharing (RDP) for the person signed in on
// the screen and returns its port.
func (d *Desktop) shareSession(ctx context.Context, s Session, c remoteCreds) (int, error) {
	remoteMu.mu.Lock()
	defer remoteMu.mu.Unlock()
	if p, ok := remoteMu.shared[s.ID]; ok && d.dial("127.0.0.1:"+strconv.Itoa(p), 500*time.Millisecond) == nil {
		return p, nil
	}
	a, err := d.account(s.User)
	if err != nil {
		return 0, err
	}
	certPath, keyPath, err := d.placeTLS(path.Join(a.Home, ".local", "share", "nocapos-remote"), a)
	if err != nil {
		return 0, err
	}
	for _, args := range [][]string{{"grdctl", "rdp", "set-tls-cert", certPath}, {"grdctl", "rdp", "set-tls-key", keyPath}} {
		if err := d.run(ctx, "runuser", asUser(s, args...)...); err != nil {
			return 0, err
		}
	}
	// Credentials on stdin: newer grdctl reads them when they aren't given;
	// older ones (GNOME 43-45) keep them in the keyring, so they go there.
	if _, err := d.RunInput(ctx, []byte(c.Username+"\n"+c.Password+"\n"), "runuser", asUser(s, "grdctl", "rdp", "set-credentials")...); err != nil {
		if out, err := d.RunInput(ctx, []byte(rdpSecret(c)), "runuser", asUser(s, "secret-tool", "store", "--label=GNOME Remote Desktop RDP credentials", "xdg:schema", "org.gnome.RemoteDesktop.RdpCredentials")...); err != nil {
			return 0, fmt.Errorf("couldn't store the remote screen's password in %s's keyring (is it unlocked?): %s", s.User, strings.TrimSpace(string(out)))
		}
	}
	for _, args := range [][]string{{"grdctl", "rdp", "disable-view-only"}, {"grdctl", "rdp", "enable"}} {
		if err := d.run(ctx, "runuser", asUser(s, args...)...); err != nil {
			return 0, err
		}
	}
	// GNOME 46+ can move it off 3389, which the remote sign-in uses.
	port := ShareOldPort
	if d.run(ctx, "runuser", asUser(s, "gsettings", "set", "org.gnome.desktop.remote-desktop.rdp", "port", strconv.Itoa(SharePort))...) == nil {
		port = SharePort
	}
	_ = d.run(ctx, "runuser", asUser(s, "systemctl", "--user", "enable", "gnome-remote-desktop.service")...)
	if err := d.run(ctx, "runuser", asUser(s, "systemctl", "--user", "restart", "gnome-remote-desktop.service")...); err != nil {
		return 0, err
	}
	if err := d.waitPort(port, 15*time.Second); err != nil {
		return 0, err
	}
	if remoteMu.shared == nil {
		remoteMu.shared = map[string]int{}
	}
	remoteMu.shared[s.ID] = port
	return port, nil
}

// grdSystemUnit is gnome-remote-desktop's system daemon (remote sign-in,
// GNOME 46+); older versions only have the per-person one.
var grdSystemUnits = []string{"/usr/lib/systemd/system/gnome-remote-desktop.service", "/lib/systemd/system/gnome-remote-desktop.service"}

func (d *Desktop) remoteLoginSupported() bool {
	for _, u := range grdSystemUnits {
		if d.exists(u) {
			return true
		}
	}
	return false
}

// remoteLogin turns on GNOME's remote sign-in: the browser gets NoCapOS's
// sign-in screen and a desktop session of its own after signing in.
func (d *Desktop) remoteLogin(ctx context.Context, c remoteCreds) (int, error) {
	if d.dial("127.0.0.1:"+strconv.Itoa(RemoteLoginPort), 500*time.Millisecond) == nil {
		return RemoteLoginPort, nil
	}
	a, err := d.account("gnome-remote-desktop")
	if err != nil {
		return 0, errors.New("the remote screen server's system account is missing; re-run the NoCapOS installer with --desktop")
	}
	dir := path.Join(a.Home, ".local", "share", "gnome-remote-desktop")
	certPath, keyPath, err := d.placeTLS(dir, a)
	if err != nil {
		return 0, err
	}
	for _, args := range [][]string{{"--system", "rdp", "set-tls-cert", certPath}, {"--system", "rdp", "set-tls-key", keyPath}} {
		if err := d.run(ctx, "grdctl", args...); err != nil {
			return 0, err
		}
	}
	if _, err := d.RunInput(ctx, []byte(c.Username+"\n"+c.Password+"\n"), "grdctl", "--system", "rdp", "set-credentials"); err != nil {
		// Its credentials file, written directly.
		p := path.Join(dir, "credentials.ini")
		if err := d.writeFile(p, []byte("[RDP]\ncredentials="+rdpSecret(c)+"\n"), 0o600); err != nil {
			return 0, err
		}
		_ = d.chown(p, a.UID, a.GID)
	}
	if err := d.run(ctx, "grdctl", "--system", "rdp", "enable"); err != nil {
		return 0, err
	}
	if err := d.run(ctx, "systemctl", "enable", "--now", "gnome-remote-desktop.service"); err != nil {
		return 0, err
	}
	_ = d.run(ctx, "systemctl", "restart", "gnome-remote-desktop.service")
	return RemoteLoginPort, d.waitPort(RemoteLoginPort, 15*time.Second)
}

// vncKiosk starts a VNC server on NoCapOS full screen's display.
func (d *Desktop) vncKiosk(ctx context.Context) (int, error) {
	if d.dial("127.0.0.1:"+strconv.Itoa(VNCPort), 500*time.Millisecond) == nil {
		return VNCPort, nil
	}
	b, err := d.readFile(DesktopEnvFile)
	if err != nil {
		return 0, errors.New("NoCapOS full screen isn't running on the server's screen right now")
	}
	env := ParseLoginctlProps(string(b))
	who := env["USER"]
	if !userNameRe.MatchString(who) || who == "root" || !strings.HasPrefix(env["XDG_RUNTIME_DIR"], "/") || strings.Contains(env["XDG_RUNTIME_DIR"], "..") {
		return 0, errors.New("the screen's session file isn't usable")
	}
	args := []string{"--unit=nocapos-desktop-vnc", "--collect", "--quiet", "--uid=" + who, "--setenv=XDG_RUNTIME_DIR=" + env["XDG_RUNTIME_DIR"]}
	switch {
	case env["WAYLAND_DISPLAY"] != "" && d.lookPath("wayvnc"):
		args = append(args, "--setenv=WAYLAND_DISPLAY="+env["WAYLAND_DISPLAY"], "wayvnc", "127.0.0.1", strconv.Itoa(VNCPort))
	case xDisplayRe.MatchString(env["DISPLAY"]) && d.lookPath("x11vnc"):
		args = append(args, "x11vnc", "-display", env["DISPLAY"], "-localhost", "-rfbport", strconv.Itoa(VNCPort), "-forever", "-shared", "-nopw", "-quiet")
	default:
		return 0, ErrNoRemoteTool
	}
	_ = d.run(ctx, "systemctl", "reset-failed", "nocapos-desktop-vnc.service")
	if err := d.run(ctx, "systemd-run", args...); err != nil {
		return 0, err
	}
	return VNCPort, d.waitPort(VNCPort, 10*time.Second)
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
		if !d.lookPath("grdctl") {
			return RemoteTarget{}, ErrNoRemoteTool
		}
		c, err := d.credentials()
		if err != nil {
			return RemoteTarget{}, err
		}
		var port int
		if s, ok := d.ActiveSession(ctx); ok {
			port, err = d.shareSession(ctx, s, c)
		} else if d.remoteLoginSupported() {
			port, err = d.remoteLogin(ctx, c)
		} else {
			err = ErrSignInFirst
		}
		if err != nil {
			return RemoteTarget{}, err
		}
		return RemoteTarget{Protocol: "rdp", Port: port, Username: c.Username, Password: c.Password}, nil
	case ModeKiosk:
		port, err := d.vncKiosk(ctx)
		if err != nil {
			return RemoteTarget{}, err
		}
		return RemoteTarget{Protocol: "vnc", Port: port}, nil
	}
	return RemoteTarget{}, ErrRemoteOff
}
