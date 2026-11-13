//go:build !linux

package dnsblock

import "errors"

var errNotLinux = errors.New("this needs NoCapOS running on Linux")

func systemPort53(string) PortInfo { return PortInfo{} }
func systemUpstreams() []string    { return nil }
func freePort53(string) error      { return errNotLinux }
func restorePort53(string) error   { return errNotLinux }

func useOnServer(_ string, on bool) error {
	if on {
		return errNotLinux
	}
	return nil
}
