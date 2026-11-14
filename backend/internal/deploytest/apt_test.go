package deploytest

// Tests for install.sh's package helpers (between "# >>> package helpers"
// and "# <<< package helpers") against a fake apt: a lock held by another
// package tool, a broken package state (unmet dependencies, a half
// configured package), packages missing from the repositories and packages
// whose install fails.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeApt plays apt-get, apt-cache, dpkg, dpkg-query and fuser from the
// files in $FAKE:
//
//	broken      a package with unmet dependencies (apt-get check/install fail)
//	fix-removes the --fix-broken repair removes the broken package (else it installs what's missing)
//	halfconf    a package dpkg left half configured
//	conf-ok     dpkg --configure -a can finish it
//	missing     packages with no install candidate, one per line
//	fails       packages whose install fails, one per line
//	installed   installed packages, one per line
//	lock        how many more lock checks find apt busy ("forever": always)
//	update-fails apt-get update fails
//	log         every command run
const fakeApt = `#!/usr/bin/env bash
F="$FAKE"
me="$(basename "$0")"
echo "$me $*" >>"$F/log"
listed() { [ -f "$F/$1" ] && grep -qxF -- "$2" "$F/$1"; }
unmet() {
  echo "The following packages have unmet dependencies:"
  echo " $(cat "$F/broken") : Depends: linux-headers-6.12.47+rpt-rpi-2712 but it is not installable"
  echo "E: Unmet dependencies. Try 'apt --fix-broken install' with no packages (or specify a solution)."
}
case "$me" in
fuser)
  n="$(cat "$F/lock" 2>/dev/null || echo 0)"
  if [ "$n" = forever ]; then echo " 4242"; exit 0; fi
  if [ "$n" -gt 0 ]; then echo $((n - 1)) >"$F/lock"; echo " 4242"; exit 0; fi
  exit 1
  ;;
apt-cache)
  [ "$1" = policy ] || exit 0
  echo "$2:"
  echo "  Installed: (none)"
  if listed missing "$2"; then echo "  Candidate: (none)"; else echo "  Candidate: 1.0-1"; fi
  ;;
dpkg-query)
  if [ "$#" -eq 3 ]; then # -W -f=FORMAT PACKAGE
    if listed installed "$3"; then printf 'ii '; exit 0; fi
    if [ -f "$F/halfconf" ] && [ "$(cat "$F/halfconf")" = "$3" ]; then printf 'iF '; exit 0; fi
    exit 1
  fi
  [ -f "$F/installed" ] && sed 's/$/\tii /' "$F/installed"
  [ -f "$F/halfconf" ] && printf '%s\tiF \n' "$(cat "$F/halfconf")"
  exit 0
  ;;
dpkg)
  case "$*" in
  *--audit*)
    if [ -f "$F/halfconf" ]; then
      echo "The following packages are only half configured, probably due to problems"
      echo "configuring them the first time.  The configuration should be retried using"
      echo "dpkg --configure <package> or the configure menu option in dselect:"
      printf ' %-20s %s\n' "$(cat "$F/halfconf")" "OpenZFS filesystem kernel modules for Linux"
    fi
    ;;
  *--configure*)
    [ -f "$F/halfconf" ] || exit 0
    if [ -f "$F/conf-ok" ]; then
      echo "Setting up $(cat "$F/halfconf") (2.3.2-1) ..."
      rm -f "$F/halfconf"; exit 0
    fi
    echo "Setting up $(cat "$F/halfconf") (2.3.2-1) ..."
    echo "Building module(s)....(bad exit status: 2)"
    echo "dpkg: error processing package $(cat "$F/halfconf") (--configure):"
    echo " installed $(cat "$F/halfconf") package post-installation script subprocess returned error exit status 10"
    echo "Errors were encountered while processing:"
    echo " $(cat "$F/halfconf")"
    exit 1
    ;;
  esac
  ;;
apt-get)
  sim=0 noremove=0 fix=0 cmd="" pkgs=()
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -o) shift ;;
      -s) sim=1 ;;
      --no-remove) noremove=1 ;;
      -f | --fix-broken) fix=1 ;;
      -*) ;;
      *) if [ -z "$cmd" ]; then cmd="$1"; else pkgs+=("$1"); fi ;;
    esac
    shift
  done
  echo "Reading package lists..."
  case "$cmd" in
  update)
    if [ -f "$F/update-fails" ]; then
      echo "E: The repository 'http://archive.raspberrypi.com/debian trixie Release' does not have a Release file."
      exit 100
    fi
    exit 0
    ;;
  check)
    if [ -s "$F/broken" ]; then unmet; exit 100; fi
    if [ -f "$F/halfconf" ]; then echo "E: dpkg was interrupted, you must manually run 'dpkg --configure -a' to correct the problem."; exit 100; fi
    exit 0
    ;;
  purge | remove)
    for p in "${pkgs[@]}"; do echo "Removing $p ..."; [ -f "$F/halfconf" ] && [ "$(cat "$F/halfconf")" = "$p" ] && rm -f "$F/halfconf"; done
    exit 0
    ;;
  install)
    if [ "$fix" -eq 1 ] && [ "${#pkgs[@]}" -eq 0 ]; then
      [ -s "$F/broken" ] || exit 0
      b="$(cat "$F/broken")"
      if [ -f "$F/fix-removes" ]; then
        if [ "$sim" -eq 1 ]; then echo "Remv $b [6.12.47-1+rpt1]"; exit 0; fi
        if [ "$noremove" -eq 1 ]; then echo "E: Packages need to be removed but remove is disabled."; exit 100; fi
        echo "Removing $b ..."; : >"$F/broken"; echo "$b" >>"$F/removed"; exit 0
      fi
      if [ "$sim" -eq 1 ]; then echo "Inst linux-headers-6.12.47+rpt-rpi-2712 (1:6.12.47-1+rpt1 Raspberry Pi:stable [arm64])"; exit 0; fi
      : >"$F/broken"; exit 0
    fi
    if [ -s "$F/broken" ]; then unmet; exit 100; fi
    if [ -f "$F/halfconf" ]; then echo "E: dpkg was interrupted, you must manually run 'dpkg --configure -a' to correct the problem."; exit 100; fi
    for p in "${pkgs[@]}"; do
      if listed missing "$p"; then echo "E: Unable to locate package $p"; exit 100; fi
    done
    for p in "${pkgs[@]}"; do
      if listed fails "$p"; then
        echo "Setting up $p (1.0-1) ..."
        echo "Job for $p.service failed because the control process exited with error code."
        echo "dpkg: error processing package $p (--configure):"
        echo " installed $p package post-installation script subprocess returned error exit status 1"
        echo "E: Sub-process /usr/bin/dpkg returned an error code (1)"
        exit 100
      fi
    done
    printf '%s\n' "${pkgs[@]}" >>"$F/installed"
    if [ -f "$F/blocker" ]; then # installs, but dpkg fails on another package
      b="$(cat "$F/blocker")"
      echo "dpkg: error processing package $b (--configure):"
      echo "Errors were encountered while processing:"
      echo " $b"
      echo "E: Sub-process /usr/bin/dpkg returned an error code (1)"
      exit 100
    fi
    exit 0
    ;;
  esac
  ;;
esac
exit 0
`

type fakeSys struct {
	t    *testing.T
	dir  string // $FAKE
	bin  string
	env  []string
	tool string // bash
}

func newFakeSys(t *testing.T) *fakeSys {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake apt runs on Linux (CI)")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	f := &fakeSys{t: t, dir: t.TempDir(), bin: t.TempDir(), tool: bash}
	for _, name := range []string{"apt-get", "apt-cache", "dpkg", "dpkg-query", "fuser"} {
		if err := os.WriteFile(filepath.Join(f.bin, name), []byte(fakeApt), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lock := filepath.Join(f.dir, "dpkg-lock")
	f.write("dpkg-lock", "")
	f.env = append(os.Environ(),
		"PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE="+f.dir, "APT_LOCKS="+lock, "APT_LOCK_POLL=1", "APT_LOCK_TIMEOUT=30",
		"NOCAP_TTY=/nonexistent-tty")
	return f
}

func (f *fakeSys) write(name, body string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeSys) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(f.dir, name))
	return string(b)
}

// run runs script after install.sh's package helpers; extra is more env.
func (f *fakeSys) run(script string, extra ...string) (string, error) {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "install.sh"))
	if err != nil {
		f.t.Fatal(err)
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	i, j := strings.Index(s, "# >>> package helpers"), strings.Index(s, "# <<< package helpers")
	if i < 0 || j < i {
		f.t.Fatal("package helpers markers not found in install.sh")
	}
	prelude := `set -euo pipefail
NAME=NoCapOS
ASSUME_YES="${ASSUME_YES:-0}"
info() { printf '==> %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
has() { command -v "$1" >/dev/null 2>&1; }
`
	file := filepath.Join(f.t.TempDir(), "run.sh")
	if err := os.WriteFile(file, []byte(prelude+s[i:j]+"\n"+script+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command(f.tool, file)
	cmd.Env = append(append([]string{}, f.env...), extra...)
	out, err := cmd.CombinedOutput()
	if testing.Verbose() {
		f.t.Logf("output:\n%s", out)
	}
	return string(out), err
}

func mustContain(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func mustNotContain(t *testing.T, out string, bad ...string) {
	t.Helper()
	for _, w := range bad {
		if strings.Contains(out, w) {
			t.Errorf("output has %q:\n%s", w, out)
		}
	}
}

func TestAptWaitsForTheLock(t *testing.T) {
	f := newFakeSys(t)
	f.write("lock", "2")
	out, err := f.run(`apt_wait_lock && echo FREE`)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	mustContain(t, out, "Waiting for another package tool to finish (pid 4242", "apt is free again", "FREE")

	f.write("lock", "forever")
	out, _ = f.run(`apt_wait_lock || echo GAVE-UP`, "APT_LOCK_TIMEOUT=2")
	mustContain(t, out, "apt is still busy after 2 seconds", "GAVE-UP")

	f.write("lock", "0")
	out, _ = f.run(`apt_wait_lock && echo FREE`)
	mustNotContain(t, out, "Waiting")
}

func TestAptUpdateFailureIsReported(t *testing.T) {
	f := newFakeSys(t)
	f.write("update-fails", "")
	out, _ := f.run(`apt_refresh || echo FAILED; apt_refresh || echo CACHED`)
	mustContain(t, out, "apt-get update failed", "does not have a Release file", "FAILED", "CACHED")
	if n := strings.Count(f.read("log"), "apt-get update"); n != 1 {
		t.Errorf("apt-get update ran %d times, want once per run", n)
	}
}

// The Raspberry Pi case: a kernel headers meta-package whose dependency is
// missing. With --yes the repair removes it and says so.
func TestAptRepairRemovesWithYesAndSaysSo(t *testing.T) {
	f := newFakeSys(t)
	f.write("broken", "linux-headers-rpi-2712\n")
	f.write("fix-removes", "")
	out, err := f.run(`pkg_install_group apt sharing full samba smbclient; pkg_summary`, "ASSUME_YES=1")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	mustContain(t, out,
		"Repairing the package system first",
		"broken: linux-headers-rpi-2712",
		"would remove: linux-headers-rpi-2712",
		"Removed to repair the package system: linux-headers-rpi-2712",
		"package system repaired")
	mustNotContain(t, out, "didn't install")
	if !strings.Contains(f.read("installed"), "samba") {
		t.Errorf("samba wasn't installed after the repair; log:\n%s", f.read("log"))
	}
	log := f.read("log")
	if !strings.Contains(log, "apt-get update") || strings.Index(log, "apt-get update") > strings.Index(log, "--fix-broken") {
		t.Errorf("the lists should be refreshed before the repair:\n%s", log)
	}
}

// Without --yes and with nobody to ask, nothing is removed; the installs
// that then fail show apt's real error, and the summary says why.
func TestAptRepairNeverRemovesWithoutAsking(t *testing.T) {
	f := newFakeSys(t)
	f.write("broken", "linux-headers-rpi-2712\n")
	f.write("fix-removes", "")
	out, _ := f.run(`pkg_install_group apt desktop lean gdm3 fonts-dejavu-core || echo GROUP-FAILED; pkg_summary`)
	if f.read("removed") != "" {
		t.Fatal("removed a package without asking")
	}
	mustContain(t, out,
		"would remove: linux-headers-rpi-2712",
		"There's no terminal to ask on, so nothing is removed.",
		"The package system is still broken (linux-headers-rpi-2712)",
		"desktop: nothing could install while the package system is broken (linux-headers-rpi-2712). apt said:",
		"| E: Unmet dependencies. Try 'apt --fix-broken install'",
		"GROUP-FAILED",
		"Some packages didn't install.",
		"desktop: gdm3 fonts-dejavu-core (the package system is broken: E: Unmet dependencies.",
		"Settings → Glow Up → Update everything")
	if !strings.Contains(f.read("log"), "--no-remove") {
		t.Error("the repair should run with --no-remove")
	}

	// Asked on the terminal: "y" allows it.
	f2 := newFakeSys(t)
	f2.write("broken", "linux-headers-rpi-2712\n")
	f2.write("fix-removes", "")
	f2.write("answer", "y\n")
	out, err := f2.run(`apt_prepare; pkg_summary`, "NOCAP_TTY="+filepath.Join(f2.dir, "answer"))
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	mustContain(t, out, "Removed to repair the package system: linux-headers-rpi-2712")
}

// A package dpkg left half configured: dpkg --configure -a finishes it.
func TestAptRepairFinishesHalfConfigured(t *testing.T) {
	f := newFakeSys(t)
	f.write("halfconf", "zfs-dkms")
	f.write("conf-ok", "")
	out, err := f.run(`pkg_install_group apt media full ffmpeg; pkg_summary`)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	mustContain(t, out, "broken: zfs-dkms", "package system repaired")
	mustNotContain(t, out, "Some packages didn't install")

	// It can't be finished: the summary names it with dpkg's error.
	f2 := newFakeSys(t)
	f2.write("halfconf", "zfs-dkms")
	out, _ = f2.run(`pkg_install_group apt media full ffmpeg || true; pkg_summary`)
	mustContain(t, out,
		"The package system is still broken (zfs-dkms)",
		"dpkg: error processing package zfs-dkms (--configure)",
		"package system: zfs-dkms (still broken: dpkg: error processing package zfs-dkms (--configure):)",
		"media: ffmpeg (the package system is broken: E: dpkg was interrupted")
}

// Missing from the repositories vs failing to install; "a/b" picks
// whichever exists; guacd missing isn't a warning.
func TestHostFeaturePackages(t *testing.T) {
	f := newFakeSys(t)
	f.write("missing", "wsdd\nguacd\nnot-here\n")
	f.write("fails", "badpkg\n")
	out, err := f.run(`
host_feature_packages apt discovery "wsdd/wsdd2"
host_feature_packages apt remote-desktop "guacd libguac-client-rdp0 libguac-client-vnc0"
host_feature_packages apt media "ffmpeg not-here badpkg"
pkg_summary`)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	mustContain(t, out,
		"discovery: wsdd2",
		"remote-desktop: guacd isn't packaged for this system; NoCapOS runs it as a container",
		"media: not in this system's repositories, skipped: not-here",
		"media: badpkg didn't install. The package tool said:",
		"| dpkg: error processing package badpkg (--configure):",
		"media: not-here (not in this system's repositories)",
		"media: badpkg (install failed: dpkg: error processing package badpkg (--configure):)")
	mustNotContain(t, out, "wsdd (", "warning: discovery", "warning: remote-desktop", "libguac")
	installed := f.read("installed")
	if !strings.Contains(installed, "wsdd2") || !strings.Contains(installed, "ffmpeg") || strings.Contains(installed, "badpkg") {
		t.Errorf("installed:\n%s", installed)
	}
	// The group went in one install first, then one by one.
	if !strings.Contains(f.read("log"), "ffmpeg badpkg") {
		t.Errorf("the feature should be tried as one install first:\n%s", f.read("log"))
	}

	// Already installed alternative: nothing to do.
	f2 := newFakeSys(t)
	f2.write("installed", "wsdd\n")
	out, _ = f2.run(`host_feature_packages apt discovery "wsdd/wsdd2"; pkg_summary`)
	mustNotContain(t, out, "discovery", "Some packages")
}

// apt reports dpkg's error over another package even though this group's
// packages went in: they count as installed.
func TestInstalledDespiteAnotherPackagesError(t *testing.T) {
	f := newFakeSys(t)
	f.write("blocker", "zfs-dkms")
	out, err := f.run(`pkg_install_group apt desktop lean gdm3 gnome-shell && echo GROUP-OK; pkg_summary`)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	mustContain(t, out, "GROUP-OK")
	mustNotContain(t, out, "didn't install", "Some packages")
}

func TestPkgSummaryQuietWhenAllWell(t *testing.T) {
	f := newFakeSys(t)
	out, err := f.run(`host_feature_packages apt backup "restic rclone"; pkg_summary`)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	mustNotContain(t, out, "warning", "Some packages", "Glow Up", "Repairing")
}
