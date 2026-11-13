//go:build !linux

package hostctl

import (
	"context"
	"time"
)

// TimeZone reports the time zone NoCapOS sees; changing it needs Linux.
func TimeZone(context.Context) TimeZoneState {
	zone := time.Now().Location().String()
	if zone == "Local" {
		zone = ""
	}
	return TimeZoneState{Zone: zone, Reason: "Changing the time zone needs NoCapOS installed directly on Linux"}
}

// SetTimeZone isn't available off Linux.
func SetTimeZone(context.Context, string) (string, error) { return "", ErrUnsupported }
