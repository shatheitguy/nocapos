//go:build !windows

package docker

import "errors"

func pipeDialer(string) (dialFunc, error) {
	return nil, errors.New("npipe:// is only supported on Windows")
}
