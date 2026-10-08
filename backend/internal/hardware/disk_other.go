//go:build !linux && !windows

package hardware

import "errors"

// Linux and Windows are implemented; this stub keeps other platforms (macOS
// dev machines) building.
func diskUsage(string) (total, used, free uint64, err error) {
	return 0, 0, 0, errors.New("disk usage is not supported on this platform")
}
