package syspkg

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---- the earlier desktop's packages ----
//
// Before NoCap Desktop, NoCapOS put a different desktop on the screen: its
// installer (install.sh --desktop, apt) and its earlier images installed
// that desktop's packages. Updates moved the screen to NoCap Desktop and
// left them installed. Once NoCap Desktop is confirmed working on the
// machine (hostaccess.DesktopConfirm), they can go, but only the ones
// NoCapOS itself installed:
//
//   - a package counts as NoCapOS's when the machine came from an earlier
//     NoCapOS image, or apt's history shows NoCapOS's installer put it in;
//   - and it's still marked as installed on purpose (apt-mark showmanual):
//     one that's only there because something else needs it is left alone;
//   - anything else (installed by the person, or unsure) stays.
//
// Removal never asks apt to remove a package by name: NoCapOS's ones are
// marked "automatically installed" and only what apt then finds unneeded
// (and wasn't unneeded before) is removed, after a simulation shows
// exactly that. Whatever is still needed by something the person
// installed stays, marked as before.

// OldDesktopPackages are the earlier desktop's own packages (package
// names from earlier versions' desktop/lib.sh and image lists). Shared
// pieces NoCap Desktop still uses (portals, keyring, pipewire, fonts,
// icons, flatpak, Xwayland, drivers) aren't in it.
var OldDesktopPackages = []string{
	"gdm3", "gdm", "gnome-session", "gnome-shell", "gnome-control-center",
	"nautilus", "gnome-console", "gnome-terminal", "gnome-text-editor", "gedit",
	"loupe", "eog", "papers", "evince", "file-roller", "gnome-system-monitor",
	"xdg-desktop-portal-gnome", "fonts-cantarell",
	"gnome-software", "gnome-software-plugin-flatpak",
	"gnome-shell-extension-dashtodock", "gnome-shell-extension-ubuntu-dock", "gnome-shell-extension-dash-to-dock",
	"gnome-shell-extension-appindicator", "gnome-shell-extension-desktop-icons-ng",
	"gnome-remote-desktop", "x11vnc",
	"xserver-xorg-core", "xserver-xorg-input-libinput", "xserver-xorg-input-all", "xserver-xorg-video-fbdev",
}

// nocapDesktopNeeds are packages NoCap Desktop runs on: never removed here,
// whatever apt thinks.
var nocapDesktopNeeds = map[string]bool{
	"labwc": true, "greetd": true, "xwayland": true, "wayvnc": true, "swayidle": true,
	"libgtk-3-0t64": true, "libgtk-3-0": true, "libgtk-layer-shell0": true, "libwebkit2gtk-4.1-0": true,
	"libjson-glib-1.0-0": true, "libpolkit-agent-1-0": true, "libxkbcommon0": true, "libpam0g": true,
	"polkitd": true, "policykit-1": true, "xdg-desktop-portal": true, "xdg-desktop-portal-wlr": true,
	"xdg-desktop-portal-gtk": true, "xdg-user-dirs": true, "gnome-keyring": true, "libpam-gnome-keyring": true,
	"dbus-user-session": true, "pipewire": true, "wireplumber": true, "pipewire-pulse": true, "wl-clipboard": true,
	"grim": true, "brightnessctl": true, "adwaita-icon-theme": true, "fonts-dejavu-core": true, "flatpak": true,
	"guacd": true, "libgl1-mesa-dri": true, "libegl-mesa0": true, "libgbm1": true, "libpam-systemd": true,
}

// OldImageMarker is on machines installed from a NoCapOS image (earlier
// ones put the earlier desktop in themselves).
var OldImageMarker = "/etc/nocapos/image.conf"

// AptHistoryGlob finds apt's history logs (the current one and rotated ones).
var AptHistoryGlob = "/var/log/apt/history.log*"

// OldDesktopFacts is what the decision looks at.
type OldDesktopFacts struct {
	Installed map[string]bool // of OldDesktopPackages, the installed ones
	Manual    map[string]bool // of those, the ones marked installed on purpose
	FromImage bool            // an earlier NoCapOS image installed this machine
	History   string          // apt's history logs, all of them
}

// OldDesktopPlan is the decision.
type OldDesktopPlan struct {
	// Remove: NoCapOS's own, to be marked automatic and cleaned up.
	Remove []string `json:"remove"`
	// Keep: installed, but not (provably) NoCapOS's: left alone.
	Keep []string `json:"keep"`
}

// historyInstalls reads apt's history: the packages NoCapOS's installer
// installed explicitly (its commands use --no-install-recommends and keep
// existing settings, and name several of the earlier desktop's packages).
func historyInstalls(history string) map[string]bool {
	old := map[string]bool{}
	for _, p := range OldDesktopPackages {
		old[p] = true
	}
	ours := map[string]bool{}
	var cmd string
	sc := bufio.NewScanner(strings.NewReader(history))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "Start-Date:"):
			cmd = ""
		case strings.HasPrefix(line, "Commandline:"):
			cmd = strings.TrimSpace(strings.TrimPrefix(line, "Commandline:"))
		case strings.HasPrefix(line, "Install:") && installerCommand(cmd, old):
			for _, item := range splitHistoryList(strings.TrimPrefix(line, "Install:")) {
				name, auto := item.name, item.auto
				if !auto && old[name] {
					ours[name] = true
				}
			}
		}
	}
	return ours
}

// installerCommand: an apt-get install run the way NoCapOS's installer
// runs it, naming at least three of the earlier desktop's packages.
func installerCommand(cmd string, old map[string]bool) bool {
	if !strings.Contains(cmd, "apt-get") || !strings.Contains(cmd, " install") ||
		!strings.Contains(cmd, "--no-install-recommends") || !strings.Contains(cmd, "--force-confold") {
		return false
	}
	n := 0
	for _, f := range strings.Fields(cmd) {
		if old[f] {
			n++
		}
	}
	return n >= 3
}

type historyItem struct {
	name string
	auto bool
}

// splitHistoryList reads "a:amd64 (1.0), b:amd64 (2.0, automatic)".
func splitHistoryList(s string) []historyItem {
	var out []historyItem
	depth, start := 0, 0
	s = strings.TrimSpace(s)
	flush := func(part string) {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		name, rest, _ := strings.Cut(part, " ")
		name, _, _ = strings.Cut(name, ":")
		out = append(out, historyItem{name: name, auto: strings.Contains(rest, "automatic")})
	}
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				flush(s[start:i])
				start = i + 1
			}
		}
	}
	flush(s[start:])
	return out
}

// DecideOldDesktop works out what may go.
func DecideOldDesktop(f OldDesktopFacts) OldDesktopPlan {
	ours := historyInstalls(f.History)
	var p OldDesktopPlan
	for _, name := range OldDesktopPackages {
		if !f.Installed[name] {
			continue
		}
		if f.Manual[name] && (f.FromImage || ours[name]) && !nocapDesktopNeeds[name] && !Protected(name) {
			p.Remove = append(p.Remove, name)
		} else {
			p.Keep = append(p.Keep, name)
		}
	}
	return p
}

// readHistory concatenates apt's history logs (oldest first).
func readHistory() string {
	files, _ := filepath.Glob(AptHistoryGlob)
	sort.Sort(sort.Reverse(sort.StringSlice(files))) // .log.2.gz, .log.1.gz, .log
	var b strings.Builder
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		var r io.Reader = fh
		if strings.HasSuffix(f, ".gz") {
			if gz, err := gzip.NewReader(fh); err == nil {
				r = gz
			} else {
				fh.Close()
				continue
			}
		}
		_, _ = io.Copy(&b, io.LimitReader(r, 64<<20))
		b.WriteString("\n")
		fh.Close()
	}
	return b.String()
}

// OldDesktop gathers the facts on this machine and decides (apt only;
// other package managers: nothing to do).
func (a *Admin) OldDesktop(ctx context.Context) (OldDesktopPlan, error) {
	if Manager() != "apt" || ImageOS() {
		return OldDesktopPlan{}, nil
	}
	f := OldDesktopFacts{Installed: map[string]bool{}, Manual: map[string]bool{}}
	out, _ := a.Run(ctx, append([]string{"dpkg-query", "-W", "-f=${Package}\t${db:Status-Abbrev}\n"}, OldDesktopPackages...)...)
	for _, l := range lines(out) {
		name, st, ok := strings.Cut(l, "\t")
		if ok && strings.HasPrefix(st, "ii") {
			f.Installed[name] = true
		}
	}
	if len(f.Installed) == 0 {
		return OldDesktopPlan{}, nil
	}
	var names []string
	for n := range f.Installed {
		names = append(names, n)
	}
	sort.Strings(names)
	out, err := a.Run(ctx, append([]string{"apt-mark", "showmanual"}, names...)...)
	if err != nil {
		return OldDesktopPlan{}, err
	}
	for _, n := range strings.Fields(out) {
		f.Manual[n] = true
	}
	_, statErr := os.Stat(OldImageMarker)
	f.FromImage = statErr == nil
	f.History = readHistory()
	return DecideOldDesktop(f), nil
}

// autoremovable is what `apt-get -s autoremove` would remove now.
func autoremovable(ctx context.Context, run func(ctx context.Context, args ...string) (string, error)) (map[string]bool, error) {
	out, err := run(ctx, "apt-get", "-s", "autoremove")
	p := parseAptSim(out)
	if err != nil && len(p.Remove) == 0 && !strings.Contains(out, "0 to remove") {
		return nil, fmt.Errorf("apt couldn't work out what's unneeded: %v", err)
	}
	m := map[string]bool{}
	for _, n := range p.Remove {
		m[n] = true
	}
	return m, nil
}

// OldDesktopSteps removes plan.Remove the safe way (see above). done gets
// what was removed (nil when nothing was).
func OldDesktopSteps(names []string, run func(ctx context.Context, args ...string) (string, error), done func(removed []string)) ([]Step, error) {
	if err := checkNames(names); err != nil {
		return nil, err
	}
	for _, n := range names {
		if nocapDesktopNeeds[n] || Protected(n) {
			return nil, fmt.Errorf("%s is needed and stays", n)
		}
	}
	var before map[string]bool
	var unneeded []string
	restore := func(ctx context.Context, w io.Writer) {
		// The ones still installed (something else needs them) are marked
		// as before.
		out, _ := run(ctx, append([]string{"dpkg-query", "-W", "-f=${Package}\t${db:Status-Abbrev}\n"}, names...)...)
		var still []string
		for _, l := range lines(out) {
			name, st, ok := strings.Cut(l, "\t")
			if ok && strings.HasPrefix(st, "ii") {
				still = append(still, name)
			}
		}
		if len(still) > 0 {
			_, _ = run(ctx, append([]string{"apt-mark", "manual"}, still...)...)
			fmt.Fprintf(w, "kept (something else needs them): %s\n", strings.Join(still, ", "))
		}
	}
	steps := []Step{
		{Label: "check what's unneeded already (left as it is)", Fn: func(ctx context.Context, w io.Writer) error {
			var err error
			before, err = autoremovable(ctx, run)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%d unneeded before; those stay\n", len(before))
			return nil
		}},
		{Label: "apt-mark auto " + strings.Join(names, " "), Fn: func(ctx context.Context, w io.Writer) error {
			out, err := run(ctx, append([]string{"apt-mark", "auto"}, names...)...)
			fmt.Fprint(w, out)
			return err
		}},
		{Label: "work out what's unneeded now", Fn: func(ctx context.Context, w io.Writer) error {
			after, err := autoremovable(ctx, run)
			if err != nil {
				restore(ctx, w)
				return err
			}
			unneeded = nil
			for n := range after {
				if before[n] {
					continue
				}
				base, _, _ := strings.Cut(n, ":")
				if nocapDesktopNeeds[base] || Protected(base) {
					restore(ctx, w)
					return fmt.Errorf("apt would remove %s, which NoCap Desktop needs; nothing was removed", n)
				}
				unneeded = append(unneeded, n)
			}
			sort.Strings(unneeded)
			fmt.Fprintf(w, "%d to remove: %s\n", len(unneeded), strings.Join(unneeded, " "))
			return nil
		}},
		{Label: "remove them (after a simulation shows exactly those)", Fn: func(ctx context.Context, w io.Writer) error {
			if len(unneeded) == 0 {
				restore(ctx, w)
				done(nil)
				return nil
			}
			allowed := map[string]bool{}
			for _, n := range unneeded {
				allowed[n] = true
			}
			sim, err := run(ctx, append([]string{"apt-get", "-s", "remove"}, unneeded...)...)
			p := parseAptSim(sim)
			if err != nil && len(p.Remove) == 0 {
				restore(ctx, w)
				return fmt.Errorf("the simulation failed: %v", err)
			}
			for _, n := range p.Remove {
				if !allowed[n] {
					restore(ctx, w)
					return fmt.Errorf("this would also remove %s; nothing was removed", n)
				}
			}
			if len(p.Install)+len(p.Upgrade) > 0 {
				restore(ctx, w)
				return errors.New("removing them would install or upgrade other packages; nothing was removed")
			}
			args := append([]string{"apt-get"}, aptQuiet...)
			args = append(args, "-y", "-q", "remove")
			out, err := run(ctx, append(args, unneeded...)...)
			fmt.Fprint(w, out)
			restore(ctx, w)
			if err != nil {
				return err
			}
			done(unneeded)
			return nil
		}},
	}
	return steps, nil
}
