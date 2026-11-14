// Package beacon lets phones nearby find this server over Bluetooth LE. It
// only advertises: the NoCap service UUID (whose last byte says whether the
// server is still waiting for its first setup) and a short name. Nothing is
// paired, nothing is connectable and no data goes over Bluetooth; the phone
// still reaches the server over the home network to set it up or link.
//
// The Linux build talks to the Bluetooth daemon over D-Bus
// (beacon_linux.go); elsewhere there is no adapter to use (beacon_other.go).
package beacon

import (
	"errors"
	"strings"
)

// ErrUnavailable: no powered Bluetooth adapter that can advertise.
var ErrUnavailable = errors.New("no Bluetooth adapter that can advertise")

// uuidBase is the NoCap service UUID without its last byte. The phone app
// scans for it with that last byte masked out (android sync/Finder.kt).
const uuidBase = "6e6f6361-7001-4e0c-a9b1-0d15c0f3b2"

// ServiceUUID is what the server advertises: ...00 once it's set up,
// ...01 while it waits for its first administrator.
func ServiceUUID(setupNew bool) string {
	if setupNew {
		return uuidBase + "01"
	}
	return uuidBase + "00"
}

// Ad is what goes on the air.
type Ad struct {
	Name     string // the server's name; shortened to fit (ShortName)
	SetupNew bool   // no administrator yet
}

// maxName: an advertisement holds 31 bytes; the 128-bit UUID takes 18 of
// them, the name's own header 2, which leaves 11. Ten keeps a byte spare.
const maxName = 10

// ShortName keeps a name to letters, digits and dashes, at most maxName
// bytes, so it fits next to the UUID ("media-box", "nocap").
func ShortName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if b.Len() >= maxName {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '.':
			// "box.local" → "box": the domain says nothing on the air.
			if b.Len() > 0 {
				return b.String()
			}
		}
	}
	if b.Len() == 0 {
		return "NoCap"
	}
	return b.String()
}
