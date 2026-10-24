package syspkg

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Package is an installed system package.
type Package struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Arch        string `json:"arch,omitempty"`
	Size        int64  `json:"size"` // bytes installed
	Description string `json:"description"`
	Held        bool   `json:"held"`
}

// Update is a package with a newer version available.
type Update struct {
	Name      string `json:"name"`
	Current   string `json:"current,omitempty"`
	Available string `json:"available"`
	Arch      string `json:"arch,omitempty"`
	Held      bool   `json:"held"`
}

func lines(out string) []string {
	var ls []string
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		ls = append(ls, sc.Text())
	}
	return ls
}

func sortPackages(p []Package) []Package {
	sort.Slice(p, func(i, j int) bool { return p[i].Name < p[j].Name })
	return p
}

// dpkgQueryFormat is the -f format parseDpkg reads.
const dpkgQueryFormat = `${binary:Package}\t${Version}\t${Architecture}\t${Installed-Size}\t${db:Status-Abbrev}\t${binary:Summary}\n`

// parseDpkg reads dpkg-query -W -f=dpkgQueryFormat (sizes are KiB).
func parseDpkg(out string) []Package {
	var pkgs []Package
	for _, l := range lines(out) {
		f := strings.SplitN(l, "\t", 6)
		if len(f) < 5 {
			continue
		}
		status := strings.TrimSpace(f[4])
		// "ii" installed, "hi" held + installed; skip removed ("rc") and half-installed.
		if len(status) < 2 || status[1] != 'i' {
			continue
		}
		kib, _ := strconv.ParseInt(strings.TrimSpace(f[3]), 10, 64)
		p := Package{Name: f[0], Version: f[1], Arch: f[2], Size: kib * 1024, Held: status[0] == 'h'}
		if len(f) == 6 {
			p.Description = strings.TrimSpace(f[5])
		}
		pkgs = append(pkgs, p)
	}
	return sortPackages(pkgs)
}

// rpmQueryFormat is the --qf format parseRPM reads (size in bytes).
const rpmQueryFormat = `%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\t%{SIZE}\t%{SUMMARY}\n`

func parseRPM(out string) []Package {
	var pkgs []Package
	for _, l := range lines(out) {
		f := strings.SplitN(l, "\t", 5)
		if len(f) < 4 || f[0] == "gpg-pubkey" {
			continue
		}
		size, _ := strconv.ParseInt(strings.TrimSpace(f[3]), 10, 64)
		p := Package{Name: f[0], Version: f[1], Arch: f[2], Size: size}
		if p.Arch == "(none)" {
			p.Arch = ""
		}
		if len(f) == 5 {
			p.Description = strings.TrimSpace(f[4])
		}
		pkgs = append(pkgs, p)
	}
	return sortPackages(pkgs)
}

// parsePacmanQi reads `pacman -Qi` (blocks of "Key : value" lines).
func parsePacmanQi(out string) []Package {
	var (
		pkgs []Package
		cur  *Package
	)
	flush := func() {
		if cur != nil && cur.Name != "" {
			pkgs = append(pkgs, *cur)
		}
		cur = nil
	}
	for _, l := range lines(out) {
		if strings.TrimSpace(l) == "" {
			flush()
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok || strings.HasPrefix(l, " ") {
			continue // a wrapped value
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if cur == nil {
			cur = &Package{}
		}
		switch k {
		case "Name":
			cur.Name = v
		case "Version":
			cur.Version = v
		case "Description":
			cur.Description = v
		case "Architecture":
			cur.Arch = v
		case "Installed Size":
			cur.Size = parseSize(v)
		}
	}
	flush()
	return sortPackages(pkgs)
}

// parseSize reads "9.37 MiB" style sizes.
func parseSize(s string) int64 {
	num, unit, _ := strings.Cut(strings.TrimSpace(s), " ")
	v, err := strconv.ParseFloat(strings.ReplaceAll(num, ",", "."), 64)
	if err != nil {
		return 0
	}
	mult := map[string]float64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}[strings.TrimSpace(unit)]
	if mult == 0 {
		mult = 1
	}
	return int64(v * mult)
}

var aptUpgradableRe = regexp.MustCompile(`^(\S+?)/\S+\s+(\S+)\s+(\S+)(?:\s+\[upgradable from:\s*([^\]]+)\])?`)

// parseAptUpgradable reads `apt list --upgradable`.
func parseAptUpgradable(out string) []Update {
	var ups []Update
	for _, l := range lines(out) {
		m := aptUpgradableRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		ups = append(ups, Update{Name: m[1], Available: m[2], Arch: m[3], Current: strings.TrimSpace(m[4])})
	}
	return ups
}

// parseDnfCheckUpdate reads `dnf check-update` ("name.arch version repo";
// long names wrap onto the next line). Everything after the "Obsoleting"
// header is ignored.
func parseDnfCheckUpdate(out string) []Update {
	var (
		ups     []Update
		pending []string
	)
	for _, l := range lines(out) {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "Obsoleting") {
			break
		}
		if t == "" || strings.HasPrefix(t, "Last metadata") || strings.HasPrefix(t, "Security:") {
			pending = nil
			continue
		}
		pending = append(pending, strings.Fields(t)...)
		if len(pending) < 3 {
			continue
		}
		if len(pending) == 3 {
			na := pending[0]
			if i := strings.LastIndex(na, "."); i > 0 {
				ups = append(ups, Update{Name: na[:i], Arch: na[i+1:], Available: pending[1]})
			}
		}
		pending = nil
	}
	return ups
}

// parsePacmanQu reads `pacman -Qu` / `checkupdates` ("name old -> new").
func parsePacmanQu(out string) []Update {
	var ups []Update
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) >= 4 && f[2] == "->" {
			u := Update{Name: f[0], Current: f[1], Available: f[3]}
			u.Held = len(f) > 4 && strings.Contains(l, "[ignored]")
			ups = append(ups, u)
		}
	}
	return ups
}

// parseZypperUpdates reads `zypper -q list-updates`:
// "v | Repository | Name | Current Version | Available Version | Arch".
func parseZypperUpdates(out string) []Update {
	var ups []Update
	for _, l := range lines(out) {
		f := strings.Split(l, "|")
		if len(f) < 6 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		if f[0] != "v" && f[0] != "i" {
			continue
		}
		ups = append(ups, Update{Name: f[2], Current: f[3], Available: f[4], Arch: f[5]})
	}
	return ups
}

// parseDnfVersionlock reads `dnf versionlock list` ("bash-0:5.1.8-6.el9.*").
func parseDnfVersionlock(out string) []string {
	var names []string
	for _, l := range lines(out) {
		t := strings.TrimPrefix(strings.TrimSpace(l), "!")
		if t == "" || strings.Contains(t, " ") {
			continue // headers ("Last metadata expiration…")
		}
		// Drop "-[epoch:]version-release.*" from the end.
		parts := strings.Split(t, "-")
		if len(parts) < 3 {
			continue
		}
		names = append(names, strings.Join(parts[:len(parts)-2], "-"))
	}
	return names
}

// parsePacmanIgnore reads IgnorePkg lines of pacman.conf.
func parsePacmanIgnore(conf string) []string {
	var names []string
	for _, l := range lines(conf) {
		t := strings.TrimSpace(l)
		k, v, ok := strings.Cut(t, "=")
		if !ok || strings.TrimSpace(k) != "IgnorePkg" {
			continue
		}
		names = append(names, strings.Fields(v)...)
	}
	return names
}

// parseZypperLocks reads `zypper -q locks` ("# | Name | Matches | Type | Repository").
func parseZypperLocks(out string) []string {
	var names []string
	for _, l := range lines(out) {
		f := strings.Split(l, "|")
		if len(f) < 3 {
			continue
		}
		if n := strings.TrimSpace(f[0]); n == "" || n[0] < '0' || n[0] > '9' {
			continue
		}
		names = append(names, strings.TrimSpace(f[1]))
	}
	return names
}

// parseAptSimRemovals lists the packages `apt-get -s remove` would remove.
func parseAptSimRemovals(out string) []string {
	var names []string
	for _, l := range lines(out) {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "Remv" {
			names = append(names, f[1])
		}
	}
	return names
}

// ---------- actions ----------

var pkgNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9+._@:~-]{0,127}$`)

// ValidName reports whether s is a plausible package name (never an option).
func ValidName(s string) bool { return pkgNameRe.MatchString(s) }

// Actions the Packages app can run.
const (
	ActRefresh     = "refresh"     // update the package lists
	ActUpgradeAll  = "upgrade-all" // install every available update
	ActUpgrade     = "upgrade"     // update one package
	ActInstall     = "install"
	ActRemove      = "remove"
	ActHold        = "hold"
	ActUnhold      = "unhold"
	ActAutoremove  = "autoremove"
	ActClean       = "clean"
	ActInstallFile = "install-file" // a local .deb/.rpm
)

// ErrUnsupported is returned for actions this package manager can't do.
var ErrUnsupported = errors.New("not available with this system's package manager")

var aptQuiet = []string{"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}

// Plan returns the commands for an action. arg is a package name, or for
// ActInstallFile an absolute file path; it is checked here too.
func Plan(mgr, action, arg string) ([][]string, error) {
	needsName := action == ActUpgrade || action == ActInstall || action == ActRemove || action == ActHold || action == ActUnhold
	if needsName && !ValidName(arg) {
		return nil, fmt.Errorf("%q isn't a valid package name", arg)
	}
	if action == ActInstallFile && (!strings.HasPrefix(arg, "/") || strings.ContainsAny(arg, "\n\r")) {
		return nil, errors.New("the package file needs an absolute path")
	}
	cmd := func(args ...string) []string { return args }
	switch mgr {
	case "apt":
		apt := func(args ...string) []string { return append(append([]string{"apt-get"}, aptQuiet...), args...) }
		switch action {
		case ActRefresh:
			return [][]string{cmd("apt-get", "update")}, nil
		case ActUpgradeAll:
			return [][]string{cmd("apt-get", "update"), apt("-y", "upgrade")}, nil
		case ActUpgrade:
			return [][]string{apt("-y", "install", "--only-upgrade", arg)}, nil
		case ActInstall:
			return [][]string{cmd("apt-get", "update"), apt("-y", "install", arg)}, nil
		case ActRemove:
			return [][]string{apt("-y", "remove", arg)}, nil
		case ActHold:
			return [][]string{cmd("apt-mark", "hold", arg)}, nil
		case ActUnhold:
			return [][]string{cmd("apt-mark", "unhold", arg)}, nil
		case ActAutoremove:
			return [][]string{apt("-y", "autoremove")}, nil
		case ActClean:
			return [][]string{cmd("apt-get", "clean")}, nil
		case ActInstallFile:
			return [][]string{apt("-y", "install", arg)}, nil
		}
	case "dnf":
		switch action {
		case ActRefresh:
			return [][]string{cmd("dnf", "makecache")}, nil
		case ActUpgradeAll:
			return [][]string{cmd("dnf", "-y", "upgrade")}, nil
		case ActUpgrade:
			return [][]string{cmd("dnf", "-y", "upgrade", arg)}, nil
		case ActInstall, ActInstallFile:
			return [][]string{cmd("dnf", "-y", "install", arg)}, nil
		case ActRemove:
			return [][]string{cmd("dnf", "-y", "remove", arg)}, nil
		case ActHold:
			return [][]string{cmd("dnf", "versionlock", "add", arg)}, nil
		case ActUnhold:
			return [][]string{cmd("dnf", "versionlock", "delete", arg)}, nil
		case ActAutoremove:
			return [][]string{cmd("dnf", "-y", "autoremove")}, nil
		case ActClean:
			return [][]string{cmd("dnf", "clean", "all")}, nil
		}
	case "pacman":
		switch action {
		case ActRefresh:
			return [][]string{cmd("pacman", "-Sy")}, nil
		case ActUpgradeAll:
			return [][]string{cmd("pacman", "-Syu", "--noconfirm")}, nil
		case ActUpgrade, ActInstall:
			return [][]string{cmd("pacman", "-S", "--noconfirm", "--needed", arg)}, nil
		case ActRemove:
			return [][]string{cmd("pacman", "-R", "--noconfirm", arg)}, nil
		case ActHold, ActUnhold:
			return nil, errors.New("on Arch, hold a package by adding it to IgnorePkg in /etc/pacman.conf")
		case ActAutoremove:
			return [][]string{cmd("bash", "-c", `orphans="$(pacman -Qdtq)" || true; if [ -n "$orphans" ]; then pacman -Rns --noconfirm $orphans; else echo "Nothing to remove."; fi`)}, nil
		case ActClean:
			return [][]string{cmd("pacman", "-Sc", "--noconfirm")}, nil
		}
	case "zypper":
		z := func(args ...string) []string { return append([]string{"zypper", "--non-interactive"}, args...) }
		switch action {
		case ActRefresh:
			return [][]string{z("refresh")}, nil
		case ActUpgradeAll:
			return [][]string{z("refresh"), z("update")}, nil
		case ActUpgrade:
			return [][]string{z("update", arg)}, nil
		case ActInstall, ActInstallFile:
			return [][]string{z("install", arg)}, nil
		case ActRemove:
			return [][]string{z("remove", arg)}, nil
		case ActHold:
			return [][]string{z("addlock", arg)}, nil
		case ActUnhold:
			return [][]string{z("removelock", arg)}, nil
		case ActClean:
			return [][]string{z("clean", "--all")}, nil
		}
	default:
		return nil, errors.New("no supported package manager found")
	}
	return nil, ErrUnsupported
}

// protectedNames can't be removed from the Packages app: the system, its
// package manager, and what NoCapOS itself runs on.
var protectedNames = map[string]bool{
	"base": true, "filesystem": true, "systemd": true, "systemd-sysv": true, "init": true, "udev": true,
	"bash": true, "coreutils": true, "util-linux": true, "mount": true, "login": true, "passwd": true, "shadow-utils": true,
	"sudo": true, "openssh-server": true, "openssh": true, "iproute2": true, "iproute": true, "ca-certificates": true,
	"glibc": true, "libc6": true, "libc-bin": true, "dpkg": true, "apt": true, "rpm": true, "dnf": true, "yum": true,
	"pacman": true, "zypper": true, "libzypp": true, "tar": true, "gzip": true, "grep": true, "sed": true, "procps": true,
	"network-manager": true, "NetworkManager": true, "netplan.io": true, "ifupdown": true, "dbus": true,
	"docker-ce": true, "docker-ce-cli": true, "docker.io": true, "docker": true, "moby-engine": true, "containerd": true, "containerd.io": true, "podman": true,
	"linux": true, "linux-lts": true, "kernel": true, "kernel-core": true, "raspberrypi-kernel": true, "raspberrypi-bootloader": true,
}

var protectedPrefix = []string{"linux-image", "linux-modules", "linux-firmware", "kernel-", "grub", "shim", "systemd-boot", "libsystemd", "firmware-", "raspi-firmware"}

// Protected reports whether removing name is refused.
func Protected(name string) bool {
	base, _, _ := strings.Cut(name, ":") // "bash:amd64"
	if protectedNames[base] {
		return true
	}
	for _, p := range protectedPrefix {
		if strings.HasPrefix(base, p) {
			return true
		}
	}
	return false
}
