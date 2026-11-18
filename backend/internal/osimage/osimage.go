// Package osimage is Glow Up on an image install of NoCapOS: the system is
// one read-only image with two slots (A/B, managed by Rugix Ctrl), and an
// update writes a whole new system image into the slot not in use, boots it
// once on trial, and keeps it only when its health check passes (see
// os/README.md). Script installs (Debian plus install.sh) never get here:
// they have no image marker.
package osimage

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// MarkerPath is written into every NoCapOS image by the image build
// (os/recipes/nocapos-os). Replaced in tests.
var MarkerPath = "/usr/lib/nocapos/image-os"

// Info describes the running image.
type Info struct {
	Target   string `json:"target"`  // "pc" or "pi"
	Version  string `json:"version"` // NoCapOS version of this image ("0.14.0", "0.14.0-s1")
	Channel  string `json:"channel"` // channel the image was built for
	Snapshot string `json:"snapshot,omitempty"`
	Built    string `json:"built,omitempty"`
}

// Detect reads the image marker. ok is false on a script install.
func Detect() (Info, bool) {
	f, err := os.Open(MarkerPath)
	if err != nil {
		return Info{}, false
	}
	defer f.Close()
	var in Info
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "TARGET":
			in.Target = v
		case "VERSION":
			in.Version = v
		case "CHANNEL":
			in.Channel = v
		case "DEBIAN_SNAPSHOT":
			in.Snapshot = v
		case "BUILT":
			in.Built = v
		}
	}
	if in.Channel == "" {
		in.Channel = ChannelStable
	}
	return in, in.Target != "" && in.Version != ""
}

// IsImage reports whether NoCapOS runs from a NoCapOS image.
func IsImage() bool {
	_, err := os.Stat(MarkerPath)
	return err == nil
}

// Channels.
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// ---- versions ----------------------------------------------------------------

// Version is a NoCapOS image version: X.Y.Z, optionally with a suffix.
//
//	0.14.0          a release
//	0.14.0-s2       the release's 2nd security refresh: same NoCapOS, newer
//	                Debian security fixes (sorts after 0.14.0)
//	0.14.0-beta.3   a beta before 0.14.0 (sorts before it)
type Version struct {
	Major, Minor, Patch int
	Kind                int // 0 pre-release, 1 release, 2 security refresh
	N                   int // beta/rc number or security refresh number
	Pre                 string
}

// ParseVersion reads "v0.14.0", "0.14.0-s2", "0.14.0-beta.3".
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	core, suffix, hasSuffix := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var v Version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, false
		}
		switch i {
		case 0:
			v.Major = n
		case 1:
			v.Minor = n
		case 2:
			v.Patch = n
		}
	}
	v.Kind = 1
	if !hasSuffix {
		return v, true
	}
	if rest, ok := strings.CutPrefix(suffix, "s"); ok {
		if n, err := strconv.Atoi(rest); err == nil && n > 0 {
			v.Kind, v.N = 2, n
			return v, true
		}
	}
	// Anything else is a pre-release: "beta.3", "rc.1", "beta3".
	digits := strings.TrimLeft(suffix, "abcdefghijklmnopqrstuvwxyz.-_")
	v.Kind, v.Pre = 0, strings.TrimRight(strings.TrimSuffix(suffix, digits), ".-_")
	if n, err := strconv.Atoi(digits); err == nil {
		v.N = n
	}
	return v, true
}

// Compare returns -1, 0 or 1. Pre-releases compare by label ("beta" <
// "rc"), then number.
func (a Version) Compare(b Version) int {
	if c := cmpInts(a.Major, b.Major, a.Minor, b.Minor, a.Patch, b.Patch, a.Kind, b.Kind); c != 0 {
		return c
	}
	if c := strings.Compare(a.Pre, b.Pre); c != 0 {
		return c
	}
	return cmpInts(a.N, b.N)
}

// cmpInts compares pairs in order: a1,b1, a2,b2, ...
func cmpInts(pairs ...int) int {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] != pairs[i+1] {
			if pairs[i] > pairs[i+1] {
				return 1
			}
			return -1
		}
	}
	return 0
}

// Newer reports whether version a is newer than b. Unparsable versions are
// never newer.
func Newer(a, b string) bool {
	va, okA := ParseVersion(a)
	vb, okB := ParseVersion(b)
	return okA && okB && va.Compare(vb) > 0
}

// SameRelease reports whether a and b are the same NoCapOS release (only a
// security refresh apart).
func SameRelease(a, b string) bool {
	va, okA := ParseVersion(a)
	vb, okB := ParseVersion(b)
	return okA && okB && va.Major == vb.Major && va.Minor == vb.Minor && va.Patch == vb.Patch && va.Kind > 0 && vb.Kind > 0
}
