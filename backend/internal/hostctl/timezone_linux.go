//go:build linux

package hostctl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

var (
	zoneinfoDir  = "/usr/share/zoneinfo"
	localtimeLnk = "/etc/localtime"
	timezoneFile = "/etc/timezone"
)

// TimeZone reports the machine's time zone.
func TimeZone(ctx context.Context) TimeZoneState {
	st := TimeZoneState{Zone: currentZone(ctx), Supported: true}
	if InContainer() {
		st.Supported, st.Reason = false, "NoCapOS is running in a container, so the host's time zone is set on the host"
	} else if os.Geteuid() != 0 {
		st.Supported, st.Reason = false, "Changing the time zone needs NoCapOS installed directly on Linux"
	}
	return st
}

func currentZone(ctx context.Context) string {
	if _, err := lookPath("timedatectl"); err == nil {
		if out, err := runCmd(ctx, "timedatectl", "show", "-p", "Timezone", "--value"); err == nil && out != "" {
			return out
		}
	}
	if dest, err := os.Readlink(localtimeLnk); err == nil {
		if i := strings.Index(dest, "zoneinfo/"); i >= 0 {
			return dest[i+len("zoneinfo/"):]
		}
	}
	if b, err := os.ReadFile(timezoneFile); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

// SetTimeZone changes the machine's time zone right away.
func SetTimeZone(ctx context.Context, zone string) (string, error) {
	if st := TimeZone(ctx); !st.Supported {
		return "", ErrUnsupported
	}
	z, err := ValidTimeZone(zone)
	if err != nil {
		return "", err
	}
	if _, err := lookPath("timedatectl"); err == nil {
		if _, err := runCmd(ctx, "timedatectl", "set-timezone", z); err == nil {
			return z, nil
		}
	}
	// No systemd: point /etc/localtime at the zone file ourselves.
	src := filepath.Join(zoneinfoDir, filepath.FromSlash(z))
	if _, err := os.Stat(src); err != nil {
		return "", err
	}
	tmp := localtimeLnk + ".nocapos-tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(src, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, localtimeLnk); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	_ = writeAtomic(timezoneFile, []byte(z+"\n"), 0o644)
	return z, nil
}
