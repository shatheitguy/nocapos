package syspkg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Status says whether the Packages app works here.
type Status struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	Manager   string `json:"manager,omitempty"` // apt | dnf | pacman | zypper
	CanHold   bool   `json:"can_hold"`
	FileTypes string `json:"file_types,omitempty"` // ".deb" or ".rpm"
}

// Admin manages the host's packages. Run swaps out command execution in tests.
type Admin struct {
	Run func(ctx context.Context, args ...string) (string, error)

	mu       sync.Mutex
	cached   []Package
	cachedAt time.Time
}

func NewAdmin() *Admin { return &Admin{Run: runQuiet} }

// InstalledCached is Installed, reusing a list younger than maxAge (paging
// and searching shouldn't re-read the package database every time).
func (a *Admin) InstalledCached(ctx context.Context, maxAge time.Duration) ([]Package, error) {
	a.mu.Lock()
	if a.cached != nil && time.Since(a.cachedAt) < maxAge {
		defer a.mu.Unlock()
		return a.cached, nil
	}
	a.mu.Unlock()
	pkgs, err := a.Installed(ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.cached, a.cachedAt = pkgs, time.Now()
	a.mu.Unlock()
	return pkgs, nil
}

// Invalidate drops the cached list (after a package task).
func (a *Admin) Invalidate() {
	a.mu.Lock()
	a.cached = nil
	a.mu.Unlock()
}

func runQuiet(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil && out.Len() == 0 {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s: %s", args[0], msg)
	}
	return out.String(), err
}

// Status reports whether packages can be managed here.
func (a *Admin) Status() Status {
	if !Native() {
		return Status{Reason: "System packages can be managed when NoCapOS is installed on Linux and runs as root. This NoCapOS instance runs somewhere else (Windows, macOS or a container)."}
	}
	switch m := Manager(); m {
	case "apt":
		return Status{Supported: true, Manager: m, CanHold: true, FileTypes: ".deb"}
	case "dnf":
		return Status{Supported: true, Manager: m, CanHold: hasVersionlock(), FileTypes: ".rpm"}
	case "zypper":
		return Status{Supported: true, Manager: m, CanHold: true, FileTypes: ".rpm"}
	case "pacman":
		return Status{Supported: true, Manager: m}
	case "":
		return Status{Reason: "No supported package manager was found on this system."}
	default:
		return Status{Reason: "This system's package manager (" + m + ") isn't supported by the Packages app yet."}
	}
}

func hasVersionlock() bool {
	out, err := runQuiet(context.Background(), "dnf", "versionlock", "--help")
	return err == nil && out != ""
}

// Installed lists installed packages, with the held ones marked.
func (a *Admin) Installed(ctx context.Context) ([]Package, error) {
	var (
		pkgs []Package
		out  string
		err  error
	)
	switch Manager() {
	case "apt":
		out, err = a.Run(ctx, "dpkg-query", "-W", "-f="+dpkgQueryFormat)
		pkgs = parseDpkg(out)
	case "dnf", "zypper":
		out, err = a.Run(ctx, "rpm", "-qa", "--qf", rpmQueryFormat)
		pkgs = parseRPM(out)
	case "pacman":
		out, err = a.Run(ctx, "pacman", "-Qi")
		pkgs = parsePacmanQi(out)
	default:
		return nil, errors.New("no supported package manager found")
	}
	if err != nil && len(pkgs) == 0 {
		return nil, err
	}
	held := a.Held(ctx)
	for i := range pkgs {
		pkgs[i].Held = pkgs[i].Held || held[pkgs[i].Name]
	}
	return pkgs, nil
}

// Held returns the names of held (version-locked) packages.
func (a *Admin) Held(ctx context.Context) map[string]bool {
	var names []string
	switch Manager() {
	case "apt":
		out, _ := a.Run(ctx, "apt-mark", "showhold")
		names = strings.Fields(out)
	case "dnf":
		out, _ := a.Run(ctx, "dnf", "-q", "versionlock", "list")
		names = parseDnfVersionlock(out)
	case "zypper":
		out, _ := a.Run(ctx, "zypper", "-q", "locks")
		names = parseZypperLocks(out)
	case "pacman":
		b, _ := os.ReadFile("/etc/pacman.conf")
		names = parsePacmanIgnore(string(b))
	}
	held := make(map[string]bool, len(names))
	for _, n := range names {
		held[n] = true
	}
	return held
}

// Updates lists packages with newer versions in the (already fetched)
// package lists; run ActRefresh first for fresh results.
func (a *Admin) Updates(ctx context.Context) ([]Update, error) {
	var (
		ups []Update
		out string
		err error
	)
	switch Manager() {
	case "apt":
		out, err = a.Run(ctx, "apt", "list", "--upgradable")
		ups = parseAptUpgradable(out)
	case "dnf":
		// Exit code 100 means "updates available": not an error.
		out, err = a.Run(ctx, "dnf", "-q", "--cacheonly", "check-update")
		ups = parseDnfCheckUpdate(out)
		if err != nil && out != "" {
			err = nil
		}
	case "pacman":
		if _, e := exec.LookPath("checkupdates"); e == nil {
			out, err = a.Run(ctx, "checkupdates")
		} else {
			out, err = a.Run(ctx, "pacman", "-Qu")
		}
		ups = parsePacmanQu(out)
		if err != nil && out == "" {
			err = nil // both exit non-zero when there's nothing to update
		}
	case "zypper":
		out, err = a.Run(ctx, "zypper", "-q", "--non-interactive", "list-updates")
		ups = parseZypperUpdates(out)
	default:
		return nil, errors.New("no supported package manager found")
	}
	if err != nil && len(ups) == 0 {
		return nil, err
	}
	held := a.Held(ctx)
	if Manager() == "dnf" && len(ups) > 0 {
		cur := map[string]string{}
		if out, err := a.Run(ctx, "rpm", "-qa", "--qf", rpmQueryFormat); err == nil {
			for _, p := range parseRPM(out) {
				cur[p.Name] = p.Version
			}
		}
		for i := range ups {
			ups[i].Current = cur[ups[i].Name]
		}
	}
	for i := range ups {
		ups[i].Held = ups[i].Held || held[ups[i].Name]
	}
	return ups, nil
}

// CheckRemove refuses removals that would take away a protected package,
// including (on apt) packages removed along with it.
func (a *Admin) CheckRemove(ctx context.Context, name string) error {
	if Protected(name) {
		return fmt.Errorf("%s is part of the system or NoCapOS itself and can't be removed here", name)
	}
	if Manager() != "apt" {
		return nil
	}
	if out, err := a.Run(ctx, "dpkg-query", "-W", "-f=${Essential}", name); err == nil && strings.TrimSpace(out) == "yes" {
		return fmt.Errorf("%s is an essential package and can't be removed", name)
	}
	out, err := a.Run(ctx, "apt-get", "-s", "remove", name)
	if err != nil && out == "" {
		return err
	}
	var bad []string
	for _, n := range parseAptSimRemovals(out) {
		if Protected(n) {
			bad = append(bad, n)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("removing %s would also remove %s, which the system needs", name, strings.Join(bad, ", "))
	}
	return nil
}

// InspectFile checks that a local package file really is a package of the
// right kind and returns its description from the package tool.
func (a *Admin) InspectFile(ctx context.Context, path string) (string, error) {
	var (
		out string
		err error
	)
	switch {
	case strings.HasSuffix(path, ".deb") && Manager() == "apt":
		out, err = a.Run(ctx, "dpkg-deb", "-I", path)
	case strings.HasSuffix(path, ".rpm") && (Manager() == "dnf" || Manager() == "zypper"):
		out, err = a.Run(ctx, "rpm", "-qip", path)
	default:
		return "", errors.New("this system installs " + a.Status().FileTypes + " files")
	}
	if err != nil {
		return "", errors.New("that file isn't a valid package: " + err.Error())
	}
	return out, nil
}
