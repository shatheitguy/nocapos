package syspkg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Repairing a broken apt/dpkg state, and updates that may pull in new
// packages (a kernel arrives as a new package that its meta-package depends
// on) or remove some. Every apt action that changes packages first finishes
// what dpkg left half-done, then checks a simulation of the change: nothing
// is removed unless the person confirmed exactly those removals.

// Health says whether the package system is in a consistent state.
type Health struct {
	Broken bool `json:"broken"`
	// Interrupted: dpkg stopped part way (packages unpacked or half
	// configured); "dpkg --configure -a" finishes it.
	Interrupted bool `json:"interrupted"`
	// Packages: the packages in a half-done state or with unmet dependencies.
	Packages []string `json:"packages,omitempty"`
	// Detail: what the package tools said, for the log.
	Detail string `json:"detail,omitempty"`
}

// Preview is what an action would do, from a simulation.
type Preview struct {
	Install []string `json:"install"` // new packages
	Upgrade []string `json:"upgrade"`
	Remove  []string `json:"remove"`
}

// ActRepair finishes interrupted installs and fixes unmet dependencies.
const ActRepair = "repair"

// halfDone: dpkg status letters of a package that isn't fully installed
// (half-installed, unpacked, half-configured, awaiting or pending triggers).
const halfDone = "HUFWt"

// parseDpkgBroken reads dpkg-query -W -f=dpkgQueryFormat (or any format
// whose second tab field is ${db:Status-Abbrev}) and returns the packages
// dpkg left half-done or marked as needing a reinstall.
func parseDpkgBroken(out string) []string {
	var names []string
	for _, l := range lines(out) {
		f := strings.Split(l, "\t")
		if len(f) < 2 {
			continue
		}
		status := f[1]
		if len(f) >= 5 {
			status = f[4] // the full dpkgQueryFormat
		}
		if len(status) < 2 {
			continue
		}
		if strings.IndexByte(halfDone, status[1]) >= 0 || (len(status) >= 3 && status[2] == 'R') {
			names = append(names, f[0])
		}
	}
	return names
}

// parseAptCheck reads `apt-get check` (or any apt-get run that stopped on
// unmet dependencies): whether it reports them, and for which packages.
func parseAptCheck(out string) (bool, []string) {
	broken := strings.Contains(out, "Unmet dependencies") || strings.Contains(out, "--fix-broken") ||
		strings.Contains(out, "dpkg was interrupted")
	var names []string
	for _, l := range lines(out) {
		// " linux-image-rpi-2712 : Depends: linux-image-6.18.60+rpt-rpi-2712 (= …) but it is not going to be installed"
		if !strings.HasPrefix(l, " ") {
			continue
		}
		name, rest, ok := strings.Cut(strings.TrimSpace(l), " : ")
		if ok && ValidName(name) && strings.Contains(rest, ":") {
			names = append(names, name)
		}
	}
	return broken || len(names) > 0, names
}

// parseDpkgAudit reads `dpkg --audit`: the packages it lists (indented
// "name   description" lines). Empty output means all is well.
func parseDpkgAudit(out string) []string {
	var names []string
	for _, l := range lines(out) {
		if !strings.HasPrefix(l, " ") {
			continue
		}
		if f := strings.Fields(l); len(f) > 0 && ValidName(f[0]) {
			names = append(names, f[0])
		}
	}
	return names
}

// parseAptSim reads `apt-get -s …`: "Inst name [old] (new …)" is an upgrade
// when the old version is shown and a new package otherwise; "Remv name"
// removes. Packages are listed once each, sorted.
func parseAptSim(out string) Preview {
	inst, upg, rem := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "Inst":
			if len(f) >= 3 && strings.HasPrefix(f[2], "[") {
				upg[f[1]] = true
			} else {
				inst[f[1]] = true
			}
		case "Remv", "Purg":
			rem[f[1]] = true
		}
	}
	keys := func(m map[string]bool) []string {
		s := make([]string, 0, len(m))
		for k := range m {
			s = append(s, k)
		}
		sort.Strings(s)
		return s
	}
	return Preview{Install: keys(inst), Upgrade: keys(upg), Remove: keys(rem)}
}

// Health checks apt/dpkg for a broken state (only apt is checked; other
// managers report healthy). It takes the package lock briefly, so call it
// when no package task runs.
func (a *Admin) Health(ctx context.Context) Health {
	if Manager() != "apt" {
		return Health{}
	}
	return a.aptHealth(ctx)
}

func (a *Admin) aptHealth(ctx context.Context) Health {
	var h Health
	seen := map[string]bool{}
	add := func(names []string) {
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				h.Packages = append(h.Packages, n)
			}
		}
	}
	var detail []string
	if out, _ := a.Run(ctx, "dpkg-query", "-W", "-f=${binary:Package}\t${db:Status-Abbrev}\n"); out != "" {
		if names := parseDpkgBroken(out); len(names) > 0 {
			h.Broken, h.Interrupted = true, true
			add(names)
			detail = append(detail, "Not fully installed: "+strings.Join(names, ", "))
		}
	}
	if out, _ := a.Run(ctx, "dpkg", "--audit"); strings.TrimSpace(out) != "" {
		h.Broken, h.Interrupted = true, true
		add(parseDpkgAudit(out))
		detail = append(detail, strings.TrimSpace(out))
	}
	out, err := a.Run(ctx, "apt-get", "check")
	msg := out
	if err != nil {
		msg += "\n" + err.Error()
	}
	if strings.Contains(msg, "Could not get lock") || strings.Contains(msg, "Unable to acquire") {
		msg = "" // another package tool is running; not a broken state
	}
	if broken, names := parseAptCheck(msg); broken {
		h.Broken = true
		add(names)
		detail = append(detail, strings.TrimSpace(msg))
	}
	sort.Strings(h.Packages)
	h.Detail = strings.Join(detail, "\n\n")
	return h
}

// HealthCached is Health, reusing a result younger than maxAge.
func (a *Admin) HealthCached(ctx context.Context, maxAge time.Duration) Health {
	a.mu.Lock()
	if a.health != nil && time.Since(a.healthAt) < maxAge {
		h := *a.health
		a.mu.Unlock()
		return h
	}
	a.mu.Unlock()
	h := a.Health(ctx)
	a.mu.Lock()
	a.health, a.healthAt = &h, time.Now()
	a.mu.Unlock()
	return h
}

// ---------- apt plans ----------

var (
	dpkgConfigure = []string{"dpkg", "--force-confdef", "--force-confold", "--configure", "-a"}
)

func aptCmd(noRemove bool, args ...string) []string {
	c := append([]string{"apt-get"}, aptQuiet...)
	c = append(c, "-y")
	if noRemove {
		c = append(c, "--no-remove")
	}
	return append(c, args...)
}

// AptSimulate is the apt-get simulation of an action (ActRepair,
// ActUpgradeAll, or ActUpgrade of names). --fix-broken is part of it because
// every one of these actions repairs first.
func AptSimulate(action string, names []string) ([]string, error) {
	switch action {
	case ActRepair:
		return []string{"apt-get", "-s", "--fix-broken", "install"}, nil
	case ActUpgradeAll:
		return []string{"apt-get", "-s", "--fix-broken", "dist-upgrade"}, nil
	case ActUpgrade:
		if err := checkNames(names); err != nil {
			return nil, err
		}
		return append([]string{"apt-get", "-s", "--fix-broken", "install"}, names...), nil
	}
	return nil, ErrUnsupported
}

func checkNames(names []string) error {
	if len(names) == 0 {
		return errors.New("no package named")
	}
	for _, n := range names {
		if !ValidName(n) {
			return fmt.Errorf("%q isn't a valid package name", n)
		}
	}
	return nil
}

// aptRepairCmds finish an interrupted dpkg run and fix unmet dependencies.
// The first --configure -a may fail (a package whose dependency is
// missing can't be configured yet); --fix-broken installs what's missing.
func aptRepairCmds(noRemove bool) (first []string, rest [][]string) {
	return dpkgConfigure, [][]string{aptCmd(noRemove, "--fix-broken", "install"), dpkgConfigure}
}

// Options for apt actions that change packages.
type Options struct {
	Names []string // ActUpgrade: the packages (a kernel and its meta-packages together)
	// AllowRemove: removals the person saw in the preview and confirmed.
	// Empty: the task stops rather than remove anything.
	AllowRemove []string
	// KeepAuto: of Names, the ones installed automatically; they stay so.
	KeepAuto []string
}

// AptSteps are the job steps of ActRepair, ActUpgradeAll or ActUpgrade on
// apt: refresh (upgrade-all), repair, check the simulation against the
// allowed removals, then the change. run executes the simulation.
//
// Update all uses dist-upgrade: a plain upgrade (even --with-new-pkgs) holds
// back an update that needs a package removed or replaced, and one package
// is installed without --only-upgrade so it can pull in what it newly needs.
func AptSteps(action string, o Options, run func(ctx context.Context, args ...string) (string, error)) ([]Step, error) {
	sim, err := AptSimulate(action, o.Names)
	if err != nil {
		return nil, err
	}
	noRemove := len(o.AllowRemove) == 0
	var steps []Step
	if action == ActUpgradeAll {
		steps = append(steps, Step{Args: []string{"apt-get", "update"}})
	}
	first, repair := aptRepairCmds(noRemove)
	steps = append(steps, Step{Args: first, Optional: true})
	steps = append(steps, RemovalGuard(sim, o.AllowRemove, run))
	for _, r := range repair {
		steps = append(steps, Step{Args: r})
	}
	switch action {
	case ActUpgradeAll:
		steps = append(steps, Step{Args: aptCmd(noRemove, "dist-upgrade")})
	case ActUpgrade:
		steps = append(steps, Step{Args: aptCmd(noRemove, append([]string{"install"}, o.Names...)...)})
		var keep []string
		for _, n := range o.KeepAuto {
			if ValidName(n) {
				keep = append(keep, n)
			}
		}
		if len(keep) > 0 {
			steps = append(steps, Step{Args: append([]string{"apt-mark", "auto"}, keep...), Optional: true})
		}
	}
	return steps, nil
}

// RemovalGuard simulates the change and stops the job when it would remove a
// package the person didn't confirm.
func RemovalGuard(sim, allowed []string, run func(ctx context.Context, args ...string) (string, error)) Step {
	ok := map[string]bool{}
	for _, n := range allowed {
		ok[n] = true
	}
	return Step{Label: "check what this changes (" + strings.Join(sim, " ") + ")", Fn: func(ctx context.Context, w io.Writer) error {
		out, err := run(ctx, sim...)
		p := parseAptSim(out)
		if err != nil && len(p.Install)+len(p.Upgrade)+len(p.Remove) == 0 {
			return fmt.Errorf("the simulation failed: %v", err)
		}
		fmt.Fprintf(w, "%d to upgrade, %d new, %d to remove\n", len(p.Upgrade), len(p.Install), len(p.Remove))
		var surprise []string
		for _, n := range p.Remove {
			if !ok[n] {
				surprise = append(surprise, n)
			}
		}
		if len(surprise) > 0 {
			return fmt.Errorf("this would remove %s, which wasn't confirmed; nothing was changed", strings.Join(surprise, ", "))
		}
		return nil
	}}
}

// Preview simulates an action (apt only; other managers return an empty
// preview and ErrUnsupported).
func (a *Admin) Preview(ctx context.Context, action string, names []string) (Preview, error) {
	if Manager() != "apt" {
		return Preview{}, ErrUnsupported
	}
	sim, err := AptSimulate(action, names)
	if err != nil {
		return Preview{}, err
	}
	out, err := a.Run(ctx, sim...)
	p := parseAptSim(out)
	if err != nil && len(p.Install)+len(p.Upgrade)+len(p.Remove) == 0 {
		if _, names := parseAptCheck(out + "\n" + err.Error()); len(names) > 0 {
			return p, fmt.Errorf("apt can't work out this change while packages are broken (%s); repair the packages first", strings.Join(names, ", "))
		}
		return p, err
	}
	return p, nil
}

// AutoInstalled returns which of names apt marks as installed automatically.
func (a *Admin) AutoInstalled(ctx context.Context, names []string) []string {
	if Manager() != "apt" || checkNames(names) != nil {
		return nil
	}
	out, _ := a.Run(ctx, append([]string{"apt-mark", "showauto"}, names...)...)
	return strings.Fields(out)
}
