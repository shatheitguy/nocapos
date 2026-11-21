package hostaccess

import (
	"context"
	"sort"
	"strings"
	"time"
)

// ---- the earlier sign-in service ----
//
// Earlier versions (and some distributions) ran another display manager.
// An update switches the boot link to greetd and disables the old one, but
// leaves it running while someone is signed in through it (switching then
// would sign them out): the switch happens at the next restart. Until then
// greetd must never start next to it, or two sign-in services fight over
// the screen. So:
//
//   - nobody signed in on the old one (only its sign-in screen shows, e.g.
//     right after a boot that still started it): it's stopped and disabled,
//     then greetd starts (Ensure does this by itself, Repair too);
//   - someone signed in on it: nothing on the screen changes; Health says
//     a restart is needed and Repair offers one.
//
// greetd's own drop-in (GreetdDropIn) also conflicts with them, so systemd
// itself never runs both.

// OldDisplayManagers is lib.sh's OLD_DISPLAY_MANAGERS.
var OldDisplayManagers = []string{"gdm3.service", "gdm.service", "lightdm.service", "sddm.service", "lxdm.service", "slim.service", "xdm.service", "nodm.service"}

// GreetdDropInPath is greetd's drop-in written by setup.sh (desk_greetd).
const GreetdDropInPath = "/etc/systemd/system/greetd.service.d/nocapos.conf"

// GreetdDropIn is lib.sh's render_greetd_dropin: the sign-in screen after
// the boot splash, and never next to another sign-in service.
func GreetdDropIn() string {
	units := strings.Join(OldDisplayManagers, " ")
	return "# NoCap Desktop (written by NoCapOS): the sign-in screen after the boot splash,\n" +
		"# and never next to another sign-in service (starting it stops them first).\n" +
		"[Unit]\nAfter=plymouth-quit-wait.service systemd-user-sessions.service\n" +
		"Conflicts=" + units + "\nAfter=" + units + "\n"
}

// otherDMs are the other sign-in services running right now (dmUnit
// excluded), in OldDisplayManagers' order.
func (d *Desktop) otherDMs(ctx context.Context, dmUnit string) []string {
	var units []string
	for _, u := range OldDisplayManagers {
		if u != dmUnit {
			units = append(units, u)
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, _ := d.Run(cctx, "systemctl", append([]string{"is-active"}, units...)...)
	return ActiveUnits(units, string(out))
}

// ActiveUnits reads `systemctl is-active A B C` (one state per line, in
// order) and returns the units that run or are on their way.
func ActiveUnits(units []string, out string) []string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var on []string
	for i, u := range units {
		if i >= len(lines) {
			break
		}
		switch strings.TrimSpace(lines[i]) {
		case "active", "activating", "reloading", "deactivating":
			on = append(on, u)
		}
	}
	return on
}

// OldDesktopUser: a session's properties (loginctl show-session) are a
// person signed in on the machine's own screen through some other sign-in
// service than greetd (the earlier desktop).
func OldDesktopUser(p map[string]string) bool {
	if p["Class"] != "user" || p["Remote"] == "yes" {
		return false
	}
	if p["Type"] != "wayland" && p["Type"] != "x11" {
		return false
	}
	if p["State"] != "" && p["State"] != "active" && p["State"] != "online" {
		return false
	}
	if svc := p["Service"]; svc == "greetd" || strings.HasPrefix(svc, "greetd-") {
		return false
	}
	return p["Name"] != "" && !greeterAccounts[p["Name"]]
}

// oldDesktopUsers are the people signed in on the earlier desktop.
func (d *Desktop) oldDesktopUsers(ctx context.Context) []string {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := d.Run(cctx, "loginctl", "list-sessions", "--no-legend")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var users []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || !sessionIDRe.MatchString(f[0]) {
			continue
		}
		props, err := d.Run(cctx, "loginctl", "show-session", f[0], "-p", "Name", "-p", "Class", "-p", "Type", "-p", "Remote", "-p", "State", "-p", "Service")
		if err != nil {
			continue
		}
		p := ParseLoginctlProps(string(props))
		if OldDesktopUser(p) && !seen[p["Name"]] {
			seen[p["Name"]] = true
			users = append(users, p["Name"])
		}
	}
	sort.Strings(users)
	return users
}

// retireOldDMs stops (when stop) and disables the other sign-in services.
func (d *Desktop) retireOldDMs(ctx context.Context, units []string, stop bool) []string {
	var did []string
	for _, u := range units {
		if !unitRe.MatchString(u) {
			continue
		}
		if stop {
			_, _ = d.Run(ctx, "systemctl", "stop", u)
			did = append(did, "stopped "+u)
		}
		_, _ = d.Run(ctx, "systemctl", "disable", u)
	}
	return did
}

// ensureGreetdDropIn writes greetd's drop-in when it's missing or old.
func (d *Desktop) ensureGreetdDropIn(ctx context.Context) bool {
	if b, err := d.readFile(GreetdDropInPath); err == nil && string(b) == GreetdDropIn() {
		return false
	}
	if err := d.writeFile(GreetdDropInPath, []byte(GreetdDropIn()), 0o644); err != nil {
		return false
	}
	_, _ = d.Run(ctx, "systemctl", "daemon-reload")
	return true
}
