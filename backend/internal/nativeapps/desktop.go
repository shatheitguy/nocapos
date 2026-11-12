package nativeapps

import (
	"path"
	"strconv"

	"alfaos/alfad/internal/webapps"
)

// Who streamed apps run as.
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
