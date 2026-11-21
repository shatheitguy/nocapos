package deploytest

import (
	"strings"
	"testing"

	"alfaos/alfad/internal/hostaccess"
)

// greetd never runs next to another sign-in service, and alfad's Repair
// writes the very same drop-in as setup.sh.
func TestGreetdDropIn(t *testing.T) {
	got := lib(t, "", "render_greetd_dropin")
	if got != hostaccess.GreetdDropIn() {
		t.Errorf("lib.sh:\n%s\nalfad:\n%s", got, hostaccess.GreetdDropIn())
	}
	if dms := strings.TrimSpace(lib(t, "", "eval", `echo "$OLD_DISPLAY_MANAGERS"`)); dms != strings.Join(hostaccess.OldDisplayManagers, " ") {
		t.Errorf("OLD_DISPLAY_MANAGERS = %q", dms)
	}
	if !strings.Contains(readRepo(t, "deploy/desktop/setup.sh"), "render_greetd_dropin >") {
		t.Error("setup.sh doesn't write the drop-in")
	}
	portals := keyfile(t, "portals", lib(t, "", "render_portals_conf"))
	if portals["preferred"]["org.freedesktop.impl.portal.Inhibit"] != "gtk" {
		t.Errorf("Inhibit portal: %v", portals)
	}
}

// The idle lock and the shell come back after a failure, without giving up.
func TestSessionUnitsRestart(t *testing.T) {
	for _, u := range []string{"deploy/desktop/session/nocap-shell.service", "deploy/desktop/session/nocap-idle.service"} {
		src := readRepo(t, u)
		for _, want := range []string{"Restart=on-failure", "StartLimitIntervalSec=0", "RestartMaxDelaySec="} {
			if !strings.Contains(src, want) {
				t.Errorf("%s lacks %q", u, want)
			}
		}
	}
	lock := readRepo(t, "deploy/desktop/session/nocap-lock-start")
	if !strings.Contains(lock, "nocap-lock: locked") || !strings.Contains(lock, "starting it again") {
		t.Error("nocap-lock-start doesn't wait for the lock or restart it")
	}
}
