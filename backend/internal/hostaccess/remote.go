package hostaccess

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
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
//   - Wayland sessions (NoCap's sign-in screen and people's desktops, both
//     labwc): wayvnc, run inside that session as its owner (greetd's
//     account or the person). It listens on a Unix socket in a folder only
//     that account (and root) can open; alfad puts the VNC password in
//     front of it (screenProxy) on 127.0.0.1.
//   - X11 sessions: x11vnc on that session's display, over VNC.
//
// When the screen's session changes (someone signs in or out), the old
// server stops and the browser window reconnects to the new one on its own.
// Each start gets a new random password, so nobody who could read one
// session's files can use it on the next. The VNC port is on 127.0.0.1 only
// (install.sh's remote guard drops other connections to it), and NoCapOS's
// Remote Desktop engine (guacd) connects over loopback. Secrets reach
// programs in files, never in their arguments.

// ScreenVNCPort is where guacd connects, on 127.0.0.1.
const ScreenVNCPort = 5906

// ScreenUnit is the transient unit the screen server runs in.
const ScreenUnit = "nocapos-screen.service"

// ScreenDir holds the screen server's settings and socket.
const ScreenDir = "/run/nocapos/screen"

// RemoteTarget is what guacd connects to.
type RemoteTarget struct {
	Protocol string // vnc
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
	proxy  io.Closer // the password in front of wayvnc
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

// waitSocket waits up to max for a Unix socket to appear.
func (d *Desktop) waitSocket(p string, max time.Duration) error {
	if d.WaitSocket != nil {
		return d.WaitSocket(p, max)
	}
	deadline := time.Now().Add(max)
	for {
		if st, err := os.Stat(p); err == nil && st.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the desktop's remote screen didn't start")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (d *Desktop) remoteSupported(mode string) bool {
	return mode == ModeFull && (d.lookPath("wayvnc") || d.lookPath("x11vnc"))
}

const pwChars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// randomPassword is letters and digits only.
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

// screenUnitArgs are systemd-run's options for the screen server unit.
func screenUnitArgs() []string {
	return []string{"--unit=" + strings.TrimSuffix(ScreenUnit, ".service"), "--collect", "--quiet", "--property=KillMode=mixed"}
}

// WayvncArgs is the screen server's command line for a session: its own
// (empty) settings file, so a person's own wayvnc settings never apply, and
// a Unix socket instead of a port. The pointer is not drawn into the
// picture (--render-cursor): with wayvnc 0.9.1 on Debian 13 that path fails
// in pixman and crashes wayvnc as soon as a viewer connects, so "Open
// screen" only ever showed Reconnect. The viewer draws the pointer itself.
func WayvncArgs(dir string) []string {
	return []string{"wayvnc", "--config=" + path.Join(dir, "config"), "--unix-socket", path.Join(dir, "vnc.sock")}
}

// mirrorWayland runs wayvnc inside session s (as its owner) and puts the
// password in front of it on 127.0.0.1.
func (d *Desktop) mirrorWayland(ctx context.Context, s Session, env map[string]string) (RemoteTarget, error) {
	if !d.lookPath("wayvnc") {
		return RemoteTarget{}, ErrNoRemoteTool
	}
	a, err := d.account(s.User)
	if err != nil {
		return RemoteTarget{}, fmt.Errorf("the screen's account %s: %w", s.User, err)
	}
	wl := env["WAYLAND_DISPLAY"]
	if wl == "" {
		wl = waylandSocket(env["XDG_RUNTIME_DIR"])
	}
	if !waylandRe.MatchString(wl) {
		return RemoteTarget{}, errors.New("the screen's Wayland display isn't known")
	}
	dir := path.Join(ScreenDir, strconv.Itoa(s.UID))
	// A fresh folder every time (never one the session's owner could have
	// changed): /run/nocapos/screen is root's, the folder in it theirs.
	if d.WriteFile == nil {
		_ = os.RemoveAll(dir)
	}
	if err := d.mkdir(ScreenDir, 0o755); err != nil {
		return RemoteTarget{}, err
	}
	if err := d.mkdir(dir, 0o700); err != nil {
		return RemoteTarget{}, err
	}
	conf := path.Join(dir, "config")
	if err := d.writeFile(conf, []byte("# NoCap Desktop's screen server (written by NoCapOS)\n"), 0o600); err != nil {
		return RemoteTarget{}, err
	}
	for _, p := range []string{dir, conf} {
		_ = d.chown(p, a.UID, a.GID)
	}
	args := append(screenUnitArgs(), "--uid="+s.User,
		"--setenv=XDG_RUNTIME_DIR="+env["XDG_RUNTIME_DIR"],
		"--setenv=WAYLAND_DISPLAY="+wl,
		"--setenv=HOME="+dir,
	)
	args = append(args, WayvncArgs(dir)...)
	if err := d.run(ctx, "systemd-run", args...); err != nil {
		return RemoteTarget{}, err
	}
	sock := path.Join(dir, "vnc.sock")
	if err := d.waitSocket(sock, 15*time.Second); err != nil {
		return RemoteTarget{}, fmt.Errorf("%w (see: journalctl -u %s)", err, ScreenUnit)
	}
	pw := randomPassword(8) // VNC passwords are 8 characters
	closer, err := d.serveScreen("127.0.0.1:"+strconv.Itoa(ScreenVNCPort), pw, sock)
	if err != nil {
		return RemoteTarget{}, err
	}
	d.mirror.proxy = closer
	return RemoteTarget{Protocol: "vnc", Port: ScreenVNCPort, Password: pw}, nil
}

// serveScreen puts the password in front of wayvnc's socket on addr.
func (d *Desktop) serveScreen(addr, password, sock string) (io.Closer, error) {
	if d.ServeScreen != nil {
		return d.ServeScreen(addr, password, sock)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("the screen's port: %w", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = ProxyVNC(c, password, func() (net.Conn, error) { return net.DialTimeout("unix", sock, 5*time.Second) })
			}()
		}
	}()
	return ln, nil
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
	if d.mirror.proxy != nil {
		_ = d.mirror.proxy.Close()
		d.mirror.proxy = nil
	}
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
		d.stopMirrorLocked(ctx)
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
