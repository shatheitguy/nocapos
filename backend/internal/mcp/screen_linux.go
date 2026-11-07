//go:build linux

package mcp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"alfaos/alfad/internal/nativeapps"
	"alfaos/alfad/internal/terminal"
)

const maxScreenshot = 32 << 20

// run runs argv on the screen as the session's own (never root) account
// and returns what it printed on stdout.
func (s Screen) run(ctx context.Context, argv ...string) ([]byte, error) {
	b, err := os.ReadFile(s.EnvFile)
	if err != nil {
		return nil, ErrNoScreen
	}
	env := nativeapps.ParseDesktopEnv(string(b))
	if !env.Usable() {
		return nil, ErrNoScreen
	}
	alive := false
	for _, sock := range env.Sockets() {
		if _, err := os.Stat(sock); err == nil {
			alive = true
		}
	}
	if !alive {
		return nil, ErrNoScreen
	}
	acct, err := terminal.LookupAccount(env.User)
	if err != nil || acct.UID == 0 {
		return nil, ErrNoScreen
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, ErrNoScreenTool
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, argv[1:]...)
	cmd.Env = append([]string{
		"HOME=" + acct.Home, "USER=" + acct.Username, "LOGNAME=" + acct.Username,
		"PATH=/usr/local/bin:/usr/bin:/bin",
	}, env.Env()...)
	if bus := strings.TrimRight(env.XDGRuntimeDir, "/") + "/bus"; fileExists(bus) {
		cmd.Env = append(cmd.Env, "DBUS_SESSION_BUS_ADDRESS=unix:path="+bus)
	}
	cmd.Dir = "/"
	if st, err := os.Stat(acct.Home); err == nil && st.IsDir() {
		cmd.Dir = acct.Home
	}
	out := &capWriter{max: maxScreenshot}
	errOut := &capWriter{max: 4096}
	cmd.Stdout, cmd.Stderr = out, errOut
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Credential: &syscall.Credential{Uid: acct.UID, Gid: acct.GID, Groups: acct.Groups},
	}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(argv[0] + ": " + msg)
	}
	return []byte(out.String()), nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func have(name string) bool { _, err := exec.LookPath(name); return err == nil }

// Screenshot captures the screen as PNG: grim on Wayland, else ImageMagick's
// import on an X display (Xwayland).
func (s Screen) Screenshot(ctx context.Context, scale float64) ([]byte, error) {
	var (
		png []byte
		err error
	)
	switch {
	case have("grim"):
		args := []string{"grim"}
		if scale > 0 && scale < 1 {
			args = append(args, "-s", strconv.FormatFloat(scale, 'f', 2, 64))
		}
		png, err = s.run(ctx, append(args, "-")...)
	case have("import"):
		png, err = s.run(ctx, "import", "-window", "root", "png:-")
	default:
		return nil, errors.New("no screenshot tool on the server: install grim (Wayland) or ImageMagick")
	}
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG")) {
		return nil, errors.New("the screenshot tool didn't return a picture")
	}
	return png, nil
}

// OpenURL opens an http(s) address on the screen.
func (s Screen) OpenURL(ctx context.Context, raw string) error {
	u, err := CheckURL(raw)
	if err != nil {
		return err
	}
	_, err = s.run(ctx, "xdg-open", u)
	return err
}

// TypeText types text into whatever has focus (wtype, else ydotool).
func (s Screen) TypeText(ctx context.Context, text string) error {
	if text == "" || len(text) > MaxTypeText {
		return errors.New("text must be 1 to 2000 characters")
	}
	if have("wtype") {
		_, err := s.run(ctx, "wtype", "--", text)
		return err
	}
	if have("ydotool") {
		_, err := s.run(ctx, "ydotool", "type", "--", text)
		return err
	}
	return errors.New("typing needs wtype or ydotool installed on the server")
}

// Click clicks at screen coordinates with ydotool.
func (s Screen) Click(ctx context.Context, x, y int, button string) error {
	if x < 0 || y < 0 || x > 16384 || y > 16384 {
		return errors.New("x and y must be screen coordinates")
	}
	if !have("ydotool") {
		return errors.New("clicking needs ydotool installed on the server")
	}
	code := "0xC0" // left down + up
	switch button {
	case "right":
		code = "0xC1"
	case "middle":
		code = "0xC2"
	}
	if _, err := s.run(ctx, "ydotool", "mousemove", "--absolute", "-x", strconv.Itoa(x), "-y", strconv.Itoa(y)); err != nil {
		return err
	}
	_, err := s.run(ctx, "ydotool", "click", code)
	return err
}
