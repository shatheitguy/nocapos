// Package nativeapps installs desktop Linux apps from Flathub (system-wide
// Flatpak) and opens them: as a real window on the server's own screen when
// the local NoCap Desktop session is running, otherwise streamed into a
// NoCapOS window the way Brave is.
//
// Only when NoCapOS runs natively as root on Linux; elsewhere every call
// reports a friendly "unsupported". Commands are argv slices, never shell
// strings, and app ids are checked against a strict reverse-DNS pattern.
package nativeapps

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

// Remote is the Flatpak remote apps come from.
const (
	Remote    = "flathub"
	RemoteURL = "https://dl.flathub.org/repo/flathub.flatpakrepo"
)

// App is an app as the App Store shows it.
type App struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	Developer string `json:"developer,omitempty"`
	// Icon is a remote icon URL (Flathub); the UI loads icons through alfad.
	Icon      string `json:"icon,omitempty"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
}

// Details is an app's page.
type Details struct {
	App
	Description string   `json:"description"`
	Homepage    string   `json:"homepage,omitempty"`
	License     string   `json:"license,omitempty"`
	Screenshots []string `json:"screenshots,omitempty"`
}

// Flatpak ids are reverse-DNS: at least three dot-separated parts, each
// starting with a letter or underscore, then letters, digits, _ or -
// (the last part may not contain -). 255 characters at most.
var idRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z_][A-Za-z0-9_-]*)+\.[A-Za-z_][A-Za-z0-9_]*$`)

// ValidID reports whether id is a safe Flatpak application id.
func ValidID(id string) bool {
	return len(id) <= 255 && idRe.MatchString(id)
}

// --- command lines (argv, never a shell) ---

// RemoteAddArgs adds Flathub system-wide (a no-op when it is there).
func RemoteAddArgs() []string {
	return []string{"flatpak", "remote-add", "--if-not-exists", "--system", Remote, RemoteURL}
}

// InstallArgs installs an app system-wide from Flathub.
func InstallArgs(id string) []string {
	return []string{"flatpak", "install", "-y", "--noninteractive", "--system", Remote, id}
}

// UninstallArgs removes an app (its runtimes stay for other apps).
func UninstallArgs(id string) []string {
	return []string{"flatpak", "uninstall", "-y", "--noninteractive", "--system", id}
}

// ListArgs lists installed apps.
func ListArgs() []string {
	return []string{"flatpak", "list", "--app", "--system", "--columns=application,name,version"}
}

// RemoteLsArgs lists every app on Flathub (the offline catalogue).
func RemoteLsArgs() []string {
	return []string{"flatpak", "remote-ls", "--system", "--app", "--columns=application,name,description", Remote}
}

// RunArgs starts an app.
func RunArgs(id string) []string {
	return []string{"flatpak", "run", id}
}

// --- output parsing ---

// columns splits a `flatpak … --columns=` line: tabs when piped, runs of
// two or more spaces when a terminal formatted it.
var wideGap = regexp.MustCompile(`\s{2,}`)

func columns(line string) []string {
	var parts []string
	if strings.Contains(line, "\t") {
		parts = strings.Split(line, "\t")
	} else {
		parts = wideGap.Split(line, -1)
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// ParseList reads `flatpak list --app --columns=application,name,version`.
func ParseList(out string) []App {
	apps := []App{}
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		c := columns(line)
		if !ValidID(c[0]) || seen[c[0]] {
			continue // a header ("Application ID") or noise
		}
		seen[c[0]] = true
		a := App{ID: c[0], Name: c[0], Installed: true}
		if len(c) > 1 && c[1] != "" {
			a.Name = c[1]
		}
		if len(c) > 2 {
			a.Version = c[2]
		}
		apps = append(apps, a)
	}
	return apps
}

// ParseRemoteLs reads `flatpak remote-ls --app --columns=application,name,description`.
func ParseRemoteLs(out string) []App {
	apps := []App{}
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		c := columns(strings.TrimRight(sc.Text(), "\r"))
		if !ValidID(c[0]) || seen[c[0]] {
			continue
		}
		seen[c[0]] = true
		a := App{ID: c[0], Name: c[0]}
		if len(c) > 1 && c[1] != "" {
			a.Name = c[1]
		}
		if len(c) > 2 {
			a.Summary = c[2]
		}
		apps = append(apps, a)
	}
	return apps
}

// Progress follows `flatpak install|uninstall` output. Flatpak redraws its
// progress line with carriage returns; each piece is fed to Line.
type Progress struct {
	Percent int
	Phase   string
	step    int
	steps   int
}

var (
	stepRe    = regexp.MustCompile(`(Installing|Updating|Uninstalling)\s+(\d+)/(\d+)`)
	percentRe = regexp.MustCompile(`(\d{1,3})%`)
)

// Line reads one line (or one redraw) of output.
func (p *Progress) Line(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if m := stepRe.FindStringSubmatch(s); m != nil {
		p.step, _ = strconv.Atoi(m[2])
		p.steps, _ = strconv.Atoi(m[3])
		switch m[1] {
		case "Uninstalling":
			p.Phase = "Removing"
		case "Updating":
			p.Phase = "Updating"
		default:
			p.Phase = "Downloading and installing"
		}
		if p.steps > 1 {
			p.Phase += " (" + m[2] + " of " + m[3] + ")"
		}
	} else if strings.HasPrefix(s, "Looking for matches") || strings.HasPrefix(s, "Looking for updates") {
		p.Phase = "Checking Flathub"
	}
	all := percentRe.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return
	}
	pct, _ := strconv.Atoi(all[len(all)-1][1])
	pct = min(pct, 100)
	overall := pct
	if p.steps > 0 && p.step > 0 {
		overall = ((p.step-1)*100 + pct) / p.steps
	}
	if overall > p.Percent { // never goes backwards
		p.Percent = min(overall, 99)
	}
}

// --- the server's own screen ---

// DesktopEnvPath is written by the local NoCap Desktop session while it runs.
const DesktopEnvPath = "/run/nocapos/desktop.env"

// DesktopEnv is the local session apps open on.
type DesktopEnv struct {
	User           string
	WaylandDisplay string
	XDGRuntimeDir  string
	Display        string
}

// ParseDesktopEnv reads KEY=VALUE lines (comments, blanks, `export` and
// quotes allowed). Unknown keys are ignored.
func ParseDesktopEnv(s string) DesktopEnv {
	var e DesktopEnv
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		switch strings.TrimSpace(k) {
		case "USER":
			e.User = v
		case "WAYLAND_DISPLAY":
			e.WaylandDisplay = v
		case "XDG_RUNTIME_DIR":
			e.XDGRuntimeDir = v
		case "DISPLAY":
			e.Display = v
		}
	}
	return e
}

var (
	userRe    = regexp.MustCompile(`^[a-z_][a-z0-9_-]*[$]?$`)
	displayRe = regexp.MustCompile(`^:(\d{1,4})(\.\d+)?$`)
)

// Usable reports whether the file names a session apps could open on.
func (e DesktopEnv) Usable() bool {
	if !userRe.MatchString(e.User) || len(e.User) > 32 || e.User == "root" {
		return false
	}
	if !strings.HasPrefix(e.XDGRuntimeDir, "/") || strings.Contains(e.XDGRuntimeDir, "..") {
		return false
	}
	return e.WaylandDisplay != "" || displayRe.MatchString(e.Display)
}

// Sockets are the files whose presence shows the session is alive: the
// Wayland socket, else the X socket.
func (e DesktopEnv) Sockets() []string {
	var out []string
	if w := e.WaylandDisplay; w != "" && !strings.Contains(w, "..") {
		if strings.HasPrefix(w, "/") {
			out = append(out, w)
		} else if !strings.Contains(w, "/") {
			out = append(out, strings.TrimRight(e.XDGRuntimeDir, "/")+"/"+w)
		}
	}
	if m := displayRe.FindStringSubmatch(e.Display); m != nil {
		out = append(out, "/tmp/.X11-unix/X"+m[1])
	}
	return out
}

// Env is the environment an app gets on that screen (plus HOME etc.).
func (e DesktopEnv) Env() []string {
	env := []string{"XDG_RUNTIME_DIR=" + e.XDGRuntimeDir}
	if e.WaylandDisplay != "" {
		env = append(env, "WAYLAND_DISPLAY="+e.WaylandDisplay, "XDG_SESSION_TYPE=wayland")
	}
	if e.Display != "" {
		env = append(env, "DISPLAY="+e.Display)
		if e.WaylandDisplay == "" {
			env = append(env, "XDG_SESSION_TYPE=x11")
		}
	}
	return env
}

// --- the landing page ---

// Popular is a hand-picked set shown before anyone searches. Names and
// summaries are fallbacks; Flathub fills in icons and fresher text.
var Popular = []App{
	{ID: "org.mozilla.firefox", Name: "Firefox", Summary: "Fast, private and safe web browser", Developer: "Mozilla"},
	{ID: "org.libreoffice.LibreOffice", Name: "LibreOffice", Summary: "The office suite: documents, spreadsheets, slides", Developer: "The Document Foundation"},
	{ID: "org.mozilla.Thunderbird", Name: "Thunderbird", Summary: "Email, calendar and contacts", Developer: "Mozilla"},
	{ID: "md.obsidian.Obsidian", Name: "Obsidian", Summary: "Markdown-based knowledge base", Developer: "Obsidian"},
	{ID: "com.visualstudio.code", Name: "Visual Studio Code", Summary: "Code editing. Redefined.", Developer: "Microsoft"},
	{ID: "org.gimp.GIMP", Name: "GIMP", Summary: "High-end image creation and manipulation", Developer: "The GIMP team"},
	{ID: "org.inkscape.Inkscape", Name: "Inkscape", Summary: "Vector graphics editor", Developer: "Inkscape Project"},
	{ID: "org.kde.krita", Name: "Krita", Summary: "Digital painting, creative freedom", Developer: "KDE"},
	{ID: "org.blender.Blender", Name: "Blender", Summary: "Free and open 3D creation software", Developer: "Blender Foundation"},
	{ID: "org.videolan.VLC", Name: "VLC", Summary: "Plays every video and audio format", Developer: "VideoLAN"},
	{ID: "com.obsproject.Studio", Name: "OBS Studio", Summary: "Live streaming and video recording", Developer: "OBS Project"},
	{ID: "org.kde.kdenlive", Name: "Kdenlive", Summary: "Video editor", Developer: "KDE"},
	{ID: "org.audacityteam.Audacity", Name: "Audacity", Summary: "Record and edit audio", Developer: "Audacity Team"},
	{ID: "org.telegram.desktop", Name: "Telegram", Summary: "Fast and secure messaging", Developer: "Telegram"},
	{ID: "org.signal.Signal", Name: "Signal", Summary: "Private messenger", Developer: "Signal"},
	{ID: "com.bitwarden.desktop", Name: "Bitwarden", Summary: "Password manager", Developer: "Bitwarden"},
}
