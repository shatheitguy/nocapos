//go:build !linux

package nativeapps

import (
	"errors"

	"alfaos/alfad/internal/webapps"
)

func startOnScreen(DesktopEnv, []string) error {
	return errors.New("the server's screen is only on Linux")
}

func lookupAccount(string) *webapps.RunAs { return nil }

func ensureAppsAccount() (*webapps.RunAs, error) { return nil, webapps.ErrNoAccount }
