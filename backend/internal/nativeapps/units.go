package nativeapps

import (
	"strings"

	"alfaos/alfad/internal/webapps"
)

// AppsAccountArgs is useradd's argv for AppsUser: a system account that
// can't log in, with its own group and home.
func AppsAccountArgs() []string {
	return []string{"--system", "--user-group", "--create-home", "--home-dir", "/var/lib/nocap-apps",
		"--shell", "/usr/sbin/nologin", "--comment", "NoCapOS apps", AppsUser}
}

// ScreenUnit is the transient unit an app opened on the server's own screen
// runs in: the screen's account, its display and session bus.
func ScreenUnit(env DesktopEnv, a *webapps.RunAs, argv []string, id, lang string, exists func(string) bool) webapps.UnitSpec {
	e := []string{
		"HOME=" + a.Home, "USER=" + a.Username, "LOGNAME=" + a.Username,
		"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=" + lang,
		"XDG_DATA_DIRS=/var/lib/flatpak/exports/share:/usr/local/share:/usr/share",
	}
	e = append(e, env.Env()...)
	if bus := strings.TrimRight(env.XDGRuntimeDir, "/") + "/bus"; exists(bus) {
		e = append(e, "DBUS_SESSION_BUS_ADDRESS=unix:path="+bus)
	}
	return webapps.UnitSpec{
		Unit:        webapps.UnitName("nocap-screen-", a.Username, id),
		Description: "NoCapOS app on the server's screen",
		User:        a.Username, GID: a.GID, WorkDir: a.Home,
		Env: e, Wait: true, Argv: argv,
	}
}
