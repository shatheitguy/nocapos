// Package localaddr gives containers names on the home network:
// http(s)://<container>.nocap.local and <service>.<project>.nocap.local,
// or a name a container picks with the nocap.domain label. It keeps the
// name → container table, issues certificates for those names from
// NoCapOS's own certificate authority (which can only ever sign names
// under nocap.local), and proxies requests to the containers.
package localaddr

import (
	"errors"
	"regexp"
	"strings"
)

// Suffix is the domain every local address lives under.
const Suffix = "nocap.local"

// LabelDomain lets a container pick its own name: nocap.domain=api.myapp
// gives https://api.myapp.nocap.local.
const LabelDomain = "nocap.domain"

// LabelPort picks which port of the container the address opens.
const LabelPort = "nocap.port"

var hostLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Names a container can't take: they belong to the server itself.
var reserved = map[string]bool{"www": true, "localhost": true, "nocap": true, "_tcp": true, "_udp": true}

// maxLabels bounds how deep a chosen name may go (a.b.c.nocap.local).
const maxLabels = 3

// ErrBadName explains what a nocap.domain label may hold.
var ErrBadName = errors.New("a local name uses lowercase letters, numbers and dashes, with up to 3 parts separated by dots (e.g. api.myapp)")

// NormalizeLabel checks the value of a nocap.domain label and returns the
// full name. "api.myapp", "API.MyApp" and "api.myapp.nocap.local" all give
// "api.myapp.nocap.local". Anything else (other domains, IPs, wildcards,
// underscores, empty parts) is refused.
func NormalizeLabel(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimSuffix(v, ".")
	v = strings.TrimSuffix(v, "."+Suffix)
	if v == "" || len(v) > 120 || v == Suffix {
		return "", ErrBadName
	}
	parts := strings.Split(v, ".")
	if len(parts) > maxLabels {
		return "", ErrBadName
	}
	for _, p := range parts {
		if !hostLabelRe.MatchString(p) {
			return "", ErrBadName
		}
	}
	if reserved[parts[len(parts)-1]] && len(parts) == 1 {
		return "", ErrBadName
	}
	return v + "." + Suffix, nil
}

// Slug turns a container, service or project name into one DNS label
// ("My_App.1" → "my-app-1"); "" when nothing usable is left.
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	if !hostLabelRe.MatchString(out) || reserved[out] {
		return ""
	}
	return out
}

// NamesFor lists the local names of a container: its own name, its compose
// service in its project, and the name its nocap.domain label picks (the
// label comes first when it's valid). Duplicates are dropped.
func NamesFor(container, project, service, label string) []string {
	var out []string
	add := func(n string) {
		for _, x := range out {
			if x == n {
				return
			}
		}
		out = append(out, n)
	}
	if label != "" {
		if n, err := NormalizeLabel(label); err == nil {
			add(n)
		}
	}
	if s := Slug(container); s != "" {
		add(s + "." + Suffix)
	}
	if p, sv := Slug(project), Slug(service); p != "" && sv != "" {
		add(sv + "." + p + "." + Suffix)
	}
	return out
}

// IsLocalName reports whether host (no port) is a name under nocap.local
// (and not nocap.local itself, which is the server).
func IsLocalName(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return strings.HasSuffix(host, "."+Suffix) && len(host) > len(Suffix)+1
}
