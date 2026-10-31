package syspkg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ZFS on Debian and Raspberry Pi OS: unlike Ubuntu, whose kernels ship the
// ZFS module, Debian builds it for the running kernel with DKMS from the
// "contrib" component. The steps here mirror install.sh's install_zfs_debian.

// IsUbuntu reports whether this is Ubuntu (ZFS comes ready-built there).
func IsUbuntu() bool { return isUbuntu() }

// ZFSNeedsBuild: on apt systems other than Ubuntu, ZFS is built with DKMS.
func ZFSNeedsBuild(mgr string) bool { return mgr == "apt" && !isUbuntu() }

// ---- contrib ----

// debianURI: an apt source that's Debian's own archive (a mirror, security
// updates or Raspbian), not a third-party or Raspberry Pi's own repository.
func debianURI(uri string) bool {
	u := strings.ToLower(uri)
	if strings.HasPrefix(u, "cdrom:") || strings.Contains(u, "raspberrypi.com") || strings.Contains(u, "raspberrypi.org/debian") {
		return false
	}
	return strings.Contains(u, "debian") || strings.Contains(u, "raspbian")
}

// contribList adds "contrib" to one-line sources (sources.list format) for
// Debian's archive that have "main" but not "contrib". Other lines,
// comments and options are left exactly as they are.
func contribList(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	changed := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "deb ") && !strings.HasPrefix(trim, "deb-src ") && !strings.HasPrefix(trim, "deb\t") {
			continue
		}
		body := trim
		if j := strings.IndexByte(body, '#'); j >= 0 {
			body = strings.TrimSpace(body[:j])
		}
		f := strings.Fields(body)
		k := 1
		if k < len(f) && strings.HasPrefix(f[k], "[") { // [arch=arm64 signed-by=...]
			for k < len(f) && !strings.HasSuffix(f[k], "]") {
				k++
			}
			k++
		}
		// f[k] = URI, f[k+1] = suite, then components.
		if k+2 >= len(f) || !debianURI(f[k]) {
			continue
		}
		comps := f[k+2:]
		if !has2(comps, "main") || has2(comps, "contrib") {
			continue
		}
		at := strings.Index(line, " main")
		if at < 0 {
			at = strings.Index(line, "\tmain")
		}
		if at < 0 {
			continue
		}
		at += len(" main")
		lines[i] = line[:at] + " contrib" + line[at:]
		changed = true
	}
	return strings.Join(lines, "\n"), changed
}

// contribDeb822 does the same for deb822 .sources files: stanzas of
// "Field: value" lines separated by blank lines.
func contribDeb822(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	changed := false
	start := 0
	for start < len(lines) {
		end := start
		for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
			end++
		}
		if fixStanza(lines[start:end]) {
			changed = true
		}
		start = end + 1
	}
	return strings.Join(lines, "\n"), changed
}

func fixStanza(st []string) bool {
	field := func(name string) (int, string) {
		for i, l := range st {
			if k, v, ok := strings.Cut(l, ":"); ok && strings.EqualFold(strings.TrimSpace(k), name) && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "#") {
				return i, strings.TrimSpace(v)
			}
		}
		return -1, ""
	}
	if _, en := field("Enabled"); strings.EqualFold(en, "no") {
		return false
	}
	_, uris := field("URIs")
	debian := false
	for _, u := range strings.Fields(uris) {
		if debianURI(u) {
			debian = true
		}
	}
	ci, comps := field("Components")
	if !debian || ci < 0 {
		return false
	}
	cs := strings.Fields(comps)
	if !has2(cs, "main") || has2(cs, "contrib") {
		return false
	}
	line := st[ci]
	cr := strings.HasSuffix(line, "\r")
	line = strings.TrimRight(line, " \t\r") + " contrib"
	if cr {
		line += "\r"
	}
	st[ci] = line
	return true
}

func has2(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// EnableContrib turns on Debian's contrib component in aptDir (normally
// /etc/apt) where it's missing, in sources.list, sources.list.d/*.list and
// sources.list.d/*.sources. It returns the files it changed; each changed
// file keeps a .nocapos.bak copy of the original (apt quietly ignores *.bak).
func EnableContrib(aptDir string) ([]string, error) {
	files := []string{filepath.Join(aptDir, "sources.list")}
	for _, pat := range []string{"*.list", "*.sources"} {
		m, _ := filepath.Glob(filepath.Join(aptDir, "sources.list.d", pat))
		files = append(files, m...)
	}
	var changed []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var out string
		var ok bool
		if strings.HasSuffix(f, ".sources") {
			out, ok = contribDeb822(string(b))
		} else {
			out, ok = contribList(string(b))
		}
		if !ok {
			continue
		}
		if _, err := os.Stat(f + ".nocapos.bak"); errors.Is(err, os.ErrNotExist) {
			_ = os.WriteFile(f+".nocapos.bak", b, 0o644)
		}
		if err := os.WriteFile(f, []byte(out), 0o644); err != nil {
			return changed, fmt.Errorf("couldn't update %s: %w", f, err)
		}
		changed = append(changed, f)
	}
	return changed, nil
}

// ---- kernel headers ----

// HeaderCandidates lists the header packages that fit kernel release rel, in
// the order to try them: the exact version first, then Raspberry Pi's
// flavour packages, then Debian's per-architecture package.
func HeaderCandidates(rel, goarch string, pi bool) []string {
	out := []string{"linux-headers-" + rel}
	if pi || strings.Contains(rel, "rpi") || strings.Contains(rel, "+rpt") || strings.HasSuffix(rel, "-v8+") || strings.HasSuffix(rel, "-v7l+") {
		var flav []string
		switch {
		case strings.Contains(rel, "2712"):
			flav = []string{"linux-headers-rpi-2712", "linux-headers-rpi-v8"}
		case strings.Contains(rel, "v7l"):
			flav = []string{"linux-headers-rpi-v7l", "linux-headers-rpi-v7"}
		case strings.Contains(rel, "v7"):
			flav = []string{"linux-headers-rpi-v7"}
		default:
			flav = []string{"linux-headers-rpi-v8", "linux-headers-rpi-2712"}
		}
		out = append(out, flav...)
		out = append(out, "raspberrypi-kernel-headers")
		return out
	}
	switch goarch {
	case "amd64":
		out = append(out, "linux-headers-amd64")
	case "arm64":
		out = append(out, "linux-headers-arm64")
	case "arm":
		out = append(out, "linux-headers-armmp")
	}
	return out
}

// PickHeaders returns the first candidate the package manager has.
func PickHeaders(cands []string, available func(string) bool) string {
	for _, c := range cands {
		if available(c) {
			return c
		}
	}
	return ""
}

// onRaspberryPi reads the board model the firmware reports.
func onRaspberryPi() bool {
	b, err := os.ReadFile("/proc/device-tree/model")
	return err == nil && bytes.Contains(b, []byte("Raspberry Pi"))
}

func kernelRelease() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func aptEnv() []string {
	return append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a", "LC_ALL=C")
}

// installHeaders installs the kernel headers DKMS needs to build ZFS.
func installHeaders(ctx context.Context, out io.Writer) error {
	rel := kernelRelease()
	if rel == "" {
		return errors.New("couldn't tell which kernel is running")
	}
	cands := HeaderCandidates(rel, runtime.GOARCH, onRaspberryPi())
	pick := PickHeaders(cands, func(p string) bool {
		return exec.CommandContext(ctx, "apt-cache", "show", p).Run() == nil
	})
	if pick == "" {
		return fmt.Errorf("no kernel headers for kernel %s in your apt sources (looked for %s); update the system and reboot, then try again", rel, strings.Join(cands, ", "))
	}
	fmt.Fprintf(out, "kernel %s: installing %s\n", rel, pick)
	cmd := exec.CommandContext(ctx, "apt-get", "install", "-y", pick)
	cmd.Env = aptEnv()
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// ZFSDebianSteps installs ZFS the Debian way: contrib on, package lists
// refreshed, kernel headers, zfs-dkms (built for this kernel) and the tools,
// then the module loaded. Header problems don't stop it (they may already
// be there); the build and the module load are what count.
func ZFSDebianSteps() []Step {
	return []Step{
		{Label: "turn on Debian's contrib component (where ZFS lives)", Fn: func(_ context.Context, out io.Writer) error {
			changed, err := EnableContrib("/etc/apt")
			for _, f := range changed {
				fmt.Fprintf(out, "added contrib to %s\n", f)
			}
			if err == nil && len(changed) == 0 {
				fmt.Fprintln(out, "contrib is already on")
			}
			return err
		}},
		{Args: []string{"apt-get", "update"}, Optional: true},
		{Label: "install kernel headers for this kernel", Fn: installHeaders, Optional: true},
		{Args: []string{"apt-get", "install", "-y", "zfs-dkms", "zfsutils-linux"}},
		{Args: []string{"modprobe", "zfs"}},
	}
}

// RunSteps runs steps now (not as a background job), writing their output
// to out. Same rules as a job: optional steps may fail.
func RunSteps(ctx context.Context, steps []Step, out io.Writer) error {
	js := NewJobs()
	x := &job{}
	err := js.run(ctx, x, steps)
	if out != nil {
		_, _ = out.Write(x.buf)
	}
	return err
}

// ZFSBuildTimeout: building the module can take a while on small boards.
const ZFSBuildTimeout = 45 * time.Minute
