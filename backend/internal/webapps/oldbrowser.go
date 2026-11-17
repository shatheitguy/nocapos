package webapps

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Earlier NoCapOS versions came with a web browser streamed into a window:
// alfad installed it from its maker's package repository on first use and
// ran it on a virtual display from a script in the data folder. That feature
// is gone. OldBrowserCleanup undoes it once, on the first start after the
// update, and only where NoCapOS itself set it up:
//
//   - the stream's leftover processes stop;
//   - the package and its repository go only when NoCapOS's own files show
//     it installed them and nobody on this machine has a profile of their own
//     in it (then it's theirs, and it stays with its updates);
//   - NoCapOS's launcher, policy file, script and log go.
//
// Browser profiles (in home folders or the data folder) and everyone's files
// stay untouched.

// oldBrowserDone marks the cleanup as done (in the data folder).
const oldBrowserDone = ".old-browser-removed"

const (
	oldBrowserPkg      = "brave-browser"
	oldBrowserAptList  = "/etc/apt/sources.list.d/brave-browser-release.list"
	oldBrowserKeyring  = "/usr/share/keyrings/brave-browser-archive-keyring.gpg"
	oldBrowserAptURL   = "brave-browser-apt-release.s3.brave.com"
	oldBrowserRpmRepo  = "/etc/yum.repos.d/brave-browser.repo"
	oldBrowserRpmURL   = "brave-browser-rpm-release"
	oldBrowserPolicy   = "/etc/brave/policies/managed/nocapos.json"
	oldBrowserLauncher = "/usr/local/share/applications/nocap-brave.desktop"
	oldBrowserProfiles = ".config/BraveSoftware" // under a home folder
)

// oldBrowserContainers are the container names NoCapOS gave the stream.
var oldBrowserContainers = []string{"nocap-brave", "alfa-brave"}

// oldBrowserHost is what the cleanup touches: the real machine, or a test's
// stand-in.
type oldBrowserHost struct {
	dataDir string
	root    string // "/" on the machine; a temp folder in tests
	// run runs a host command; it reports whether it succeeded.
	run func(ctx context.Context, name string, args ...string) bool
	// removeContainer removes a container by name (nil: no Docker).
	removeContainer func(ctx context.Context, name string) error
	log             *slog.Logger
}

func (h oldBrowserHost) path(p string) string { return filepath.Join(h.root, filepath.FromSlash(p)) }

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// nocapMade reports whether NoCapOS set the browser up on this machine: its
// launcher script, log or profile are in the data folder.
func (h oldBrowserHost) nocapMade() bool {
	for _, f := range []string{"brave-run.sh", "brave.log", "brave-profile", "brave"} {
		if exists(filepath.Join(h.dataDir, f)) {
			return true
		}
	}
	m, _ := filepath.Glob(filepath.Join(h.dataDir, "webapps", "brave*"))
	return len(m) > 0
}

// someoneUsesIt reports whether a person has a browser profile of their own
// in a home folder: they use the browser themselves, so it stays.
func (h oldBrowserHost) someoneUsesIt() bool {
	homes, _ := filepath.Glob(h.path("/home/*"))
	for _, home := range homes {
		if exists(filepath.Join(home, filepath.FromSlash(oldBrowserProfiles))) {
			return true
		}
	}
	return false
}

func fileHas(p, s string) bool {
	b, err := os.ReadFile(p)
	return err == nil && bytes.Contains(b, []byte(s))
}

func (h oldBrowserHost) remove(p string) {
	if err := os.Remove(h.path(p)); err == nil {
		h.log.Info("old browser: removed", "path", p)
	}
}

// cleanup does the work and reports whether it finished (false: try again on
// the next start).
func (h oldBrowserHost) cleanup(ctx context.Context) bool {
	if exists(filepath.Join(h.dataDir, oldBrowserDone)) {
		return true
	}
	if h.removeContainer != nil {
		for _, name := range oldBrowserContainers {
			if err := h.removeContainer(ctx, name); err == nil {
				h.log.Info("old browser: removed its container", "name", name)
			}
		}
	}
	if !h.nocapMade() {
		return h.markDone()
	}

	// The stream: its script, the browser on its profile, and the display,
	// VNC server and web bridge on the ports only it used.
	for _, pat := range []string{
		filepath.Join(h.dataDir, "brave-run.sh"),
		"--user-data-dir=" + filepath.Join(h.dataDir, "brave-profile"),
		"Xvfb :99 -screen 0",
		"x11vnc -display :99 -rfbport 5999",
		"127.0.0.1:6080 127.0.0.1:5999",
	} {
		h.run(ctx, "pkill", "-f", "--", pat)
	}

	if !h.someoneUsesIt() {
		removed := true
		switch {
		case h.run(ctx, "sh", "-c", "dpkg-query -W -f='${Status}' "+oldBrowserPkg+" 2>/dev/null | grep -q 'ok installed'"):
			removed = h.run(ctx, "apt-get", "remove", "-y", "-q", oldBrowserPkg)
		case h.run(ctx, "rpm", "-q", oldBrowserPkg):
			removed = h.run(ctx, "dnf", "remove", "-y", "-q", oldBrowserPkg)
		}
		if !removed {
			h.log.Warn("old browser: the package couldn't be removed; trying again on the next start")
			return false
		}
		if fileHas(h.path(oldBrowserAptList), oldBrowserAptURL) {
			h.remove(oldBrowserAptList)
			h.remove(oldBrowserKeyring)
		}
		if fileHas(h.path(oldBrowserRpmRepo), oldBrowserRpmURL) {
			h.remove(oldBrowserRpmRepo)
		}
	} else {
		h.log.Info("old browser: a person on this machine uses it, so the package stays")
	}

	h.remove(oldBrowserPolicy)
	h.remove(oldBrowserLauncher)
	for _, f := range []string{"brave-run.sh", "brave.log"} {
		_ = os.Remove(filepath.Join(h.dataDir, f))
	}
	return h.markDone()
}

func (h oldBrowserHost) markDone() bool {
	_ = os.WriteFile(filepath.Join(h.dataDir, oldBrowserDone), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	return true
}
