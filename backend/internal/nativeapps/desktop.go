package nativeapps

import (
	"errors"
	"path"
	"strconv"

	"alfaos/alfad/internal/webapps"
)

// Who streamed apps run as, the full Linux desktop, and the server's screen.
//
// A person with their own Linux account (sign-in uses this machine's users)
// runs apps as that account, in their own home, which Files shows as their
// home. Anyone else (NoCapOS's own accounts, or an account that is root)
// runs them as the dedicated unprivileged AppsUser, with a home and runtime
// folder of their own per person. Never as root.

// AppsUser is the unprivileged account for people without a Linux account.
const AppsUser = "nocap-apps"

// AppsHomeBase holds AppsUser's per-person homes.
const AppsHomeBase = "/var/lib/nocap-apps/homes"

// Stream keys of the desktop and the server's screen (one each per person).
const (
	DesktopKey = "desktop"
	ScreenKey  = "screen"
)

// ErrNoScreen: viewing the server's screen while it's off.
var ErrNoScreen = errors.New("The server's own screen isn't on right now")

// ErrSandbox is why Linux app sandboxes can't start here, with the fix.
type ErrSandbox struct{ Check webapps.SandboxCheck }

func (e ErrSandbox) Error() string { return e.Check.Problem + " Fix: " + e.Check.Fix }

// runAs is the account a person's streamed apps run as.
func (s *Service) runAs(who Person) (*webapps.RunAs, error) {
	if who.Username != "" && s.Account != nil {
		if a := s.Account(who.Username); a != nil && a.UID != 0 && a.Username != "root" && a.Home != "" {
			acct := *a
			acct.Runtime = "/run/user/" + strconv.FormatUint(uint64(a.UID), 10)
			acct.OwnHome = false
			return &acct, nil
		}
	}
	if s.AppsAccount == nil {
		return nil, webapps.ErrNoAccount
	}
	shared, err := s.AppsAccount()
	if err != nil {
		return nil, err
	}
	if shared == nil || shared.UID == 0 || shared.Username == "root" {
		return nil, webapps.ErrNoAccount
	}
	acct := *shared
	dir := safeDir(who.ID)
	acct.Home = path.Join(AppsHomeBase, dir)
	acct.Runtime = webapps.AppsRuntimeBase + "/" + dir
	acct.OwnHome = true
	return &acct, nil
}

// sandboxErr reports a host whose kernel stops app sandboxes.
func (s *Service) sandboxErr() error {
	if s.Sandbox == nil {
		return nil
	}
	if chk := s.Sandbox(); !chk.OK {
		return ErrSandbox{chk}
	}
	return nil
}

// Desktop opens the person's full Linux desktop, streamed into a window.
func (s *Service) Desktop(who Person) (Launched, error) {
	if !s.Native() {
		return Launched{}, ErrUnsupported{reasonNotNative}
	}
	if s.Streams == nil {
		return Launched{}, ErrUnsupported{reasonNotNative}
	}
	acct, err := s.runAs(who)
	if err != nil {
		return Launched{}, err
	}
	st, err := s.Streams.Open(who.ID, webapps.StreamSpec{Key: DesktopKey, Title: "Linux Desktop", Mode: webapps.ModeDesktop, RunAs: acct})
	if err != nil {
		return Launched{}, err
	}
	return Launched{Where: "stream", Stream: &st}, nil
}

// ViewScreen streams the server's own screen (the NoCap Desktop session),
// as that session's account. Admins only (the caller checks).
func (s *Service) ViewScreen(who Person) (Launched, error) {
	if !s.Native() || s.Streams == nil {
		return Launched{}, ErrUnsupported{reasonNotNative}
	}
	env, on := s.screen()
	if !on {
		return Launched{}, ErrNoScreen
	}
	if s.Account == nil {
		return Launched{}, ErrNoScreen
	}
	a := s.Account(env.User)
	if a == nil || a.UID == 0 || a.Home == "" {
		return Launched{}, errors.New("the server's screen has no usable account")
	}
	acct := *a
	acct.Runtime = env.XDGRuntimeDir
	acct.OwnHome = false
	var extra []string
	if env.WaylandDisplay != "" {
		extra = append(extra, "WAYLAND_DISPLAY="+env.WaylandDisplay)
	}
	if env.Display != "" {
		extra = append(extra, "DISPLAY="+env.Display)
	}
	st, err := s.Streams.Open(who.ID, webapps.StreamSpec{Key: ScreenKey, Title: "Server screen", Mode: webapps.ModeScreen, RunAs: &acct, Env: extra})
	if err != nil {
		return Launched{}, err
	}
	return Launched{Where: "stream", Stream: &st}, nil
}
