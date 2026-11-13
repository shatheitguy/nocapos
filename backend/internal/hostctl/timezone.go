package hostctl

import (
	"errors"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // zone names check out the same on every device
)

// The machine's time zone (an IANA name like Europe/Berlin). Reading and
// setting it is Linux-only (timezone_linux.go); elsewhere it's read-only.

// TimeZoneState is what the setup and Settings show.
type TimeZoneState struct {
	Zone      string `json:"zone"`             // "" when unknown
	Supported bool   `json:"supported"`        // NoCapOS can change it here
	Reason    string `json:"reason,omitempty"` // why not
}

var zoneRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+\-]*(/[A-Za-z0-9_+\-]+){0,2}$`)

// ValidTimeZone checks an IANA zone name: the right shape, no path tricks,
// and known to Go's time zone database.
func ValidTimeZone(zone string) (string, error) {
	z := strings.TrimSpace(zone)
	if z == "UTC" || z == "Etc/UTC" {
		return z, nil
	}
	if len(z) > 64 || !zoneRe.MatchString(z) || strings.Contains(z, "..") {
		return "", errors.New("pick a time zone from the list, e.g. Europe/Berlin")
	}
	if _, err := time.LoadLocation(z); err != nil {
		return "", errors.New("that time zone isn't known on this server")
	}
	return z, nil
}
