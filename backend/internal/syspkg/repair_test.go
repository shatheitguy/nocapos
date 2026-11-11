package syspkg

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Raspberry Pi OS (Pi 5) after an interrupted kernel update: the headers
// show the installed version as the "new" one, and apt-get stops on the
// meta-packages' unmet dependencies.
const piUpgradable = `Listing... Done
linux-headers-6.18.60+rpt-rpi-2712/stable 1:6.18.60-1+rpt1 arm64 [upgradable from: 1:6.18.60-1+rpt1]
linux-headers-6.18.60+rpt-rpi-v8/stable 1:6.18.60-1+rpt1 arm64 [upgradable from: 1:6.18.60-1+rpt1]
linux-image-rpi-2712/stable 1:6.18.60-1+rpt1 arm64 [upgradable from: 1:6.12.47-1+rpt1]
linux-headers-rpi-2712/stable 1:6.18.60-1+rpt1 arm64 [upgradable from: 1:6.12.47-1+rpt1]
oldtool/now 1.2-1 arm64 [residual-config]
curl/stable,now 7.88.1-10+deb12u12 arm64 [installed,upgradable to: 7.88.1-10+deb12u14]
nano/stable-security 7.2-1+deb12u1 arm64 [upgradable from: 7.2-1]
`

const piCheck = `Reading package lists...
Building dependency tree...
Reading state information...
You might want to run 'apt --fix-broken install' to correct these.
The following packages have unmet dependencies:
 linux-image-rpi-2712 : Depends: linux-image-6.18.60+rpt-rpi-2712 (= 1:6.18.60-1+rpt1) but it is not going to be installed
 linux-image-rpi-v8 : Depends: linux-image-6.18.60+rpt-rpi-v8 (= 1:6.18.60-1+rpt1) but it is not going to be installed
E: Unmet dependencies. Try 'apt --fix-broken install' with no packages (or specify a solution).
`

func TestParseAptUpgradablePiBrokenKernel(t *testing.T) {
	got := parseAptUpgradable(piUpgradable)
	var names []string
	for _, u := range got {
		names = append(names, u.Name)
	}
	want := []string{"linux-image-rpi-2712", "linux-headers-rpi-2712", "nano"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	if !got[0].Kernel || got[0].Group != "kernel" || got[0].Current != "1:6.12.47-1+rpt1" {
		t.Errorf("image meta: %+v", got[0])
	}
	if got[1].Kernel || got[1].Group != "kernel" {
		t.Errorf("headers meta should be grouped with the kernel without needing a restart itself: %+v", got[1])
	}
	if got[2].Group != "" || !got[2].Security {
		t.Errorf("nano: %+v", got[2])
	}
}

func TestMarkKernelsDropsSameVersion(t *testing.T) {
	ups := MarkKernels([]Update{
		{Name: "a", Current: "1", Available: "1"},
		{Name: "linux-headers-6.1.0-28-amd64", Current: "1", Available: "2"},
		{Name: "b", Available: "3"},
	})
	if len(ups) != 2 || ups[0].Group != "kernel" || ups[1].Name != "b" {
		t.Fatalf("got %+v", ups)
	}
}

func TestParseAptCheck(t *testing.T) {
	broken, names := parseAptCheck(piCheck)
	if !broken || !reflect.DeepEqual(names, []string{"linux-image-rpi-2712", "linux-image-rpi-v8"}) {
		t.Fatalf("got %v %v", broken, names)
	}
	if broken, names := parseAptCheck("Reading package lists...\nBuilding dependency tree...\n"); broken || names != nil {
		t.Errorf("healthy: %v %v", broken, names)
	}
	if broken, _ := parseAptCheck("E: dpkg was interrupted, you must manually run 'dpkg --configure -a' to correct the problem."); !broken {
		t.Error("an interrupted dpkg is broken")
	}
}

func TestParseDpkgBroken(t *testing.T) {
	short := "bash\tii \n" +
		"linux-headers-6.18.60+rpt-rpi-2712\tiF \n" +
		"linux-image-rpi-2712\tiU \n" +
		"half\tiH \n" +
		"trig\tiW \n" +
		"pend\tit \n" +
		"reinst\tiiR\n" +
		"gone\trc \n" +
		"held\thi \n"
	want := []string{"linux-headers-6.18.60+rpt-rpi-2712", "linux-image-rpi-2712", "half", "trig", "pend", "reinst"}
	if got := parseDpkgBroken(short); !reflect.DeepEqual(got, want) {
		t.Errorf("short format: %v", got)
	}
	full := "bash\t5.2\tarm64\t7164\tii \tshell\nfoo\t1.0\tarm64\t10\tiU \tunpacked\n"
	if got := parseDpkgBroken(full); !reflect.DeepEqual(got, []string{"foo"}) {
		t.Errorf("full format: %v", got)
	}
}

func TestParseDpkgAudit(t *testing.T) {
	out := `The following packages are only half configured, probably due to problems
configuring them the first time.  The configuration should be retried using
dpkg --configure <package> or the configure menu option in dselect:
 linux-headers-6.18.60+rpt-rpi-2712 Header files for Linux 6.18.60+rpt-rpi-2712
 linux-headers-6.18.60+rpt-rpi-v8 Header files for Linux 6.18.60+rpt-rpi-v8
`
	want := []string{"linux-headers-6.18.60+rpt-rpi-2712", "linux-headers-6.18.60+rpt-rpi-v8"}
	if got := parseDpkgAudit(out); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

const simFullUpgrade = `Reading package lists...
Building dependency tree...
Calculating upgrade...
The following packages will be REMOVED:
  oldmeta
The following NEW packages will be installed:
  linux-image-6.18.60+rpt-rpi-2712
Inst linux-image-6.18.60+rpt-rpi-2712 (1:6.18.60-1+rpt1 Raspberry Pi Foundation:stable [arm64])
Remv oldmeta [1.0]
Inst linux-image-rpi-2712 [1:6.12.47-1+rpt1] (1:6.18.60-1+rpt1 Raspberry Pi Foundation:stable [arm64])
Conf linux-image-6.18.60+rpt-rpi-2712 (1:6.18.60-1+rpt1 Raspberry Pi Foundation:stable [arm64])
Conf linux-image-rpi-2712 (1:6.18.60-1+rpt1 Raspberry Pi Foundation:stable [arm64])
`

func TestParseAptSim(t *testing.T) {
	got := parseAptSim(simFullUpgrade)
	want := Preview{Install: []string{"linux-image-6.18.60+rpt-rpi-2712"}, Upgrade: []string{"linux-image-rpi-2712"}, Remove: []string{"oldmeta"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func argsOf(steps []Step) [][]string {
	var a [][]string
	for _, s := range steps {
		if s.Fn != nil {
			a = append(a, []string{"<guard>"})
		} else {
			a = append(a, s.Args)
		}
	}
	return a
}

func TestAptStepsUpgradeAll(t *testing.T) {
	steps, err := AptSteps(ActUpgradeAll, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	q := []string{"apt-get", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "-y", "--no-remove"}
	want := [][]string{
		{"apt-get", "update"},
		dpkgConfigure,
		{"<guard>"},
		append(append([]string{}, q...), "--fix-broken", "install"),
		dpkgConfigure,
		append(append([]string{}, q...), "dist-upgrade"),
	}
	if got := argsOf(steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if !steps[1].Optional || steps[4].Optional || steps[5].Optional {
		t.Error("only the first configure -a may fail")
	}
	for _, s := range steps {
		if strings.Contains(strings.Join(s.Args, " "), "--only-upgrade") || strings.Contains(strings.Join(s.Args, " "), " upgrade") {
			t.Errorf("update all must not hold back new packages: %v", s.Args)
		}
	}
}

func TestAptStepsUpgradeNamesAndRepair(t *testing.T) {
	steps, err := AptSteps(ActUpgrade, Options{Names: []string{"linux-image-rpi-2712", "linux-headers-rpi-2712"}, AllowRemove: []string{"oldmeta"}, KeepAuto: []string{"linux-headers-rpi-2712"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := argsOf(steps)
	inst := strings.Join(got[len(got)-2], " ")
	if strings.Contains(inst, "--only-upgrade") || strings.Contains(inst, "--no-remove") || !strings.HasSuffix(inst, "install linux-image-rpi-2712 linux-headers-rpi-2712") {
		t.Errorf("install step: %s", inst)
	}
	if last := got[len(got)-1]; !reflect.DeepEqual(last, []string{"apt-mark", "auto", "linux-headers-rpi-2712"}) || !steps[len(steps)-1].Optional {
		t.Errorf("auto marks: %v", last)
	}
	if got[0][0] != "dpkg" || got[1][0] != "<guard>" {
		t.Errorf("repair and guard come first: %q", got)
	}

	steps, _ = AptSteps(ActRepair, Options{}, nil)
	want := [][]string{dpkgConfigure, {"<guard>"},
		{"apt-get", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "-y", "--no-remove", "--fix-broken", "install"},
		dpkgConfigure}
	if got := argsOf(steps); !reflect.DeepEqual(got, want) {
		t.Errorf("repair: %q", got)
	}
	if _, err := AptSteps(ActUpgrade, Options{Names: []string{"-y"}}, nil); err == nil {
		t.Error("option as a package name")
	}
	if _, err := AptSteps(ActUpgrade, Options{}, nil); err == nil {
		t.Error("upgrade with no names")
	}
	if sim, _ := AptSimulate(ActUpgradeAll, nil); strings.Join(sim, " ") != "apt-get -s --fix-broken dist-upgrade" {
		t.Errorf("simulate: %v", sim)
	}
}

func TestRemovalGuard(t *testing.T) {
	run := func(out string, err error) func(context.Context, ...string) (string, error) {
		return func(context.Context, ...string) (string, error) { return out, err }
	}
	var buf bytes.Buffer
	g := RemovalGuard([]string{"apt-get", "-s"}, nil, run(simFullUpgrade, nil))
	if err := g.Fn(context.Background(), &buf); err == nil || !strings.Contains(err.Error(), "oldmeta") {
		t.Errorf("unconfirmed removal: %v", err)
	}
	g = RemovalGuard([]string{"apt-get", "-s"}, []string{"oldmeta"}, run(simFullUpgrade, nil))
	if err := g.Fn(context.Background(), &buf); err != nil {
		t.Errorf("confirmed removal: %v", err)
	}
	g = RemovalGuard([]string{"apt-get", "-s"}, nil, run("", errors.New("E: boom")))
	if err := g.Fn(context.Background(), &buf); err == nil {
		t.Error("a failed simulation stops the job")
	}
}

func TestPlanAptUpgradeNeverHoldsBack(t *testing.T) {
	p, err := Plan("apt", ActUpgradeAll, "")
	if err != nil {
		t.Fatal(err)
	}
	last := strings.Join(p[len(p)-1], " ")
	if !strings.HasSuffix(last, "--no-remove dist-upgrade") {
		t.Errorf("upgrade-all: %s", last)
	}
	p, _ = Plan("apt", ActUpgrade, "nano")
	if s := strings.Join(p[len(p)-1], " "); strings.Contains(s, "--only-upgrade") || !strings.HasSuffix(s, "install nano") {
		t.Errorf("upgrade: %s", s)
	}
	if p, err := Plan("apt", ActRepair, ""); err != nil || p[0][len(p[0])-2] != "--fix-broken" {
		t.Errorf("repair: %v %v", p, err)
	}
	if _, err := Plan("dnf", ActRepair, ""); err != ErrUnsupported {
		t.Errorf("dnf repair: %v", err)
	}
}
