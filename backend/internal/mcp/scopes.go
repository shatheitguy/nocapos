package mcp

import (
	"errors"
	"fmt"
	"net/netip"
	"path"
	"slices"
	"strings"
)

// Scopes are what one agent may do: a level per capability (deny by
// default: a capability that isn't listed is off), the folders file and
// photo tools are limited to, and which capabilities run their actions without asking.
// The server checks them on every tool call; the UI only edits them.

// Risk is how much harm a level can do.
type Risk string

const (
	RiskNone   Risk = "none"
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

func riskRank(r Risk) int {
	switch r {
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	}
	return 0
}

// Level is one step of a capability, from "off" up.
type Level struct {
	ID   string `json:"id"`
	Risk Risk   `json:"risk"`
}

// Capability is one thing an agent can be allowed to touch.
type Capability struct {
	ID     string  `json:"id"`
	Group  string  `json:"group"`  // data | system | apps | danger
	Levels []Level `json:"levels"` // lowest ("off") first
	// Acts: its higher levels change things, so they ask first unless
	// relaxed. NeverAuto: they always ask.
	Acts      bool `json:"acts"`
	NeverAuto bool `json:"never_auto,omitempty"`
}

// Capability ids.
const (
	CapLogs       = "logs"
	CapFiles      = "files"
	CapPhotos     = "photos"
	CapSystem     = "system"
	CapNetwork    = "network"
	CapUsers      = "users"
	CapScreen     = "screen"
	CapApps       = "apps"
	CapContainers = "containers"
	CapTerminal   = "terminal"
	CapCode       = "code"
	CapPower      = "power"
)

// Level ids.
const (
	Off     = "off"
	Read    = "read"
	Write   = "write"
	View    = "view"
	Change  = "change"
	Manage  = "manage"
	Control = "control"
	On      = "on"
	Sandbox = "sandbox"
	Host    = "host"
)

func lv(ids ...string) []Level {
	out := []Level{{ID: Off, Risk: RiskNone}}
	for _, s := range ids {
		id, r, _ := strings.Cut(s, ":")
		out = append(out, Level{ID: id, Risk: Risk(r)})
	}
	return out
}

// Capabilities, grouped as Settings shows them.
var Capabilities = []Capability{
	{ID: CapLogs, Group: "data", Levels: lv("read:low")},
	{ID: CapFiles, Group: "data", Levels: lv("read:low", "write:medium"), Acts: true},
	{ID: CapPhotos, Group: "data", Levels: lv("read:low", "write:medium"), Acts: true},

	{ID: CapSystem, Group: "system", Levels: lv("view:low", "change:medium"), Acts: true},
	{ID: CapNetwork, Group: "system", Levels: lv("view:low", "change:high"), Acts: true},
	{ID: CapUsers, Group: "system", Levels: lv("view:medium", "manage:high"), Acts: true},
	{ID: CapScreen, Group: "system", Levels: lv("view:medium", "control:high"), Acts: true},

	{ID: CapApps, Group: "apps", Levels: lv("view:low", "manage:medium"), Acts: true},
	{ID: CapContainers, Group: "apps", Levels: lv("view:low", "manage:medium"), Acts: true},

	{ID: CapTerminal, Group: "danger", Levels: lv("on:high"), Acts: true},
	{ID: CapCode, Group: "danger", Levels: lv("sandbox:medium", "host:high"), Acts: true},
	{ID: CapPower, Group: "danger", Levels: lv("on:high"), Acts: true, NeverAuto: true},
}

// FindCapability returns the capability with that id, or nil.
func FindCapability(id string) *Capability {
	for i := range Capabilities {
		if Capabilities[i].ID == id {
			return &Capabilities[i]
		}
	}
	return nil
}

// rank is a level's place in its capability (0 = off), or -1 if unknown.
func (c *Capability) rank(level string) int {
	for i, l := range c.Levels {
		if l.ID == level {
			return i
		}
	}
	return -1
}

// LevelRisk is the risk of one level of a capability.
func LevelRisk(capID, level string) Risk {
	c := FindCapability(capID)
	if c == nil {
		return RiskNone
	}
	if i := c.rank(level); i >= 0 {
		return c.Levels[i].Risk
	}
	return RiskNone
}

// Scopes is one agent's profile.
type Scopes struct {
	Levels  map[string]string `json:"levels"`
	Folders []string          `json:"folders"` // "location:/path"; empty = every location its owner can open
	Auto    []string          `json:"auto"`    // capabilities whose actions run without asking
}

// Level is the agent's level for a capability ("off" when unset or unknown).
func (s Scopes) Level(capID string) string {
	c := FindCapability(capID)
	if c == nil {
		return Off
	}
	l := s.Levels[capID]
	if c.rank(l) < 0 {
		return Off
	}
	return l
}

// Allows reports whether the agent has at least level need of capID.
// Unknown capabilities and levels are always denied.
func (s Scopes) Allows(capID, need string) bool {
	c := FindCapability(capID)
	if c == nil {
		return false
	}
	want := c.rank(need)
	if want <= 0 { // "off" or unknown is never a grant
		return false
	}
	return c.rank(s.Level(capID)) >= want
}

// Asks reports whether an action under capID waits for the owner.
func (s Scopes) Asks(capID string) bool {
	c := FindCapability(capID)
	if c == nil || c.NeverAuto {
		return true
	}
	return !slices.Contains(s.Auto, capID)
}

// AnyOn reports whether any capability is above off.
func (s Scopes) AnyOn() bool {
	for _, c := range Capabilities {
		if s.Level(c.ID) != Off {
			return true
		}
	}
	return false
}

// Risk is the profile's overall risk: its riskiest level, one step higher
// when a medium-risk capability also runs without asking.
func (s Scopes) Risk() Risk {
	worst := RiskNone
	for _, c := range Capabilities {
		r := c.Levels[c.rank(s.Level(c.ID))].Risk
		if r == RiskMedium && !s.Asks(c.ID) {
			r = RiskHigh
		}
		if riskRank(r) > riskRank(worst) {
			worst = r
		}
	}
	return worst
}

// ErrBadScopes: a profile that names unknown capabilities, levels or folders.
var ErrBadScopes = errors.New("bad scopes")

// Normalize checks a profile and returns it tidied: only known
// capabilities above off, sorted unique folders and auto list (auto only
// for capabilities that are on and may run without asking).
func (s Scopes) Normalize() (Scopes, error) {
	out := Scopes{Levels: map[string]string{}, Folders: []string{}, Auto: []string{}}
	for id, l := range s.Levels {
		c := FindCapability(id)
		if c == nil {
			return Scopes{}, fmt.Errorf("%w: unknown capability %q", ErrBadScopes, id)
		}
		if c.rank(l) < 0 {
			return Scopes{}, fmt.Errorf("%w: %s can't be %q", ErrBadScopes, id, l)
		}
		if l != Off {
			out.Levels[id] = l
		}
	}
	if len(s.Folders) > 50 {
		return Scopes{}, fmt.Errorf("%w: at most 50 folders", ErrBadScopes)
	}
	for _, f := range s.Folders {
		n, err := CleanFolder(f)
		if err != nil {
			return Scopes{}, err
		}
		if !slices.Contains(out.Folders, n) {
			out.Folders = append(out.Folders, n)
		}
	}
	for _, id := range s.Auto {
		c := FindCapability(id)
		if c == nil {
			return Scopes{}, fmt.Errorf("%w: unknown capability %q", ErrBadScopes, id)
		}
		if !c.Acts || c.NeverAuto || out.Levels[id] == "" || slices.Contains(out.Auto, id) {
			continue
		}
		out.Auto = append(out.Auto, id)
	}
	slices.Sort(out.Folders)
	slices.Sort(out.Auto)
	return out, nil
}

// CleanFolder tidies "location:/path" (".." and doubled slashes resolved).
func CleanFolder(f string) (string, error) {
	root, p, ok := strings.Cut(strings.TrimSpace(f), ":")
	if !ok || root == "" || len(f) > 1024 || strings.ContainsAny(root, "/\\\x00") || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: folder %q must look like location:/path", ErrBadScopes, f)
	}
	return root + ":" + cleanPath(p), nil
}

// cleanPath turns a client path into "/a/b" ("/" for the top), resolving
// ".." the same way the file tools do (backslashes count as slashes).
func cleanPath(p string) string {
	return path.Clean("/" + strings.ReplaceAll(p, `\`, "/"))
}

// FolderAllows reports whether a file at root:p is inside the profile's
// folders (always true when there are none).
func (s Scopes) FolderAllows(root, p string) bool {
	if len(s.Folders) == 0 {
		return true
	}
	if root == "" || strings.ContainsRune(p, 0) {
		return false
	}
	want := cleanPath(p)
	for _, f := range s.Folders {
		fr, fp, _ := strings.Cut(f, ":")
		if fr != root {
			continue
		}
		if fp == "/" || want == fp || strings.HasPrefix(want, fp+"/") {
			return true
		}
	}
	return false
}

// FolderRoots are the locations the folders mention (nil = no limit).
func (s Scopes) FolderRoots() []string {
	if len(s.Folders) == 0 {
		return nil
	}
	out := []string{}
	for _, f := range s.Folders {
		r, _, _ := strings.Cut(f, ":")
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// Preset is a ready-made profile.
type Preset struct {
	ID     string `json:"id"`
	Scopes Scopes `json:"scopes"`
}

func levels(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

// Presets, from nothing to everything.
var Presets = []Preset{
	{ID: "off", Scopes: Scopes{Levels: levels()}},
	{ID: "read_only", Scopes: Scopes{Levels: levels(
		CapLogs, Read, CapFiles, Read, CapPhotos, Read, CapSystem, View, CapNetwork, View,
		CapApps, View, CapContainers, View)}},
	{ID: "monitoring", Scopes: Scopes{Levels: levels(
		CapLogs, Read, CapSystem, View, CapNetwork, View, CapContainers, View)}},
	{ID: "files_photos", Scopes: Scopes{Levels: levels(CapFiles, Write, CapPhotos, Write)}},
	{ID: "admin", Scopes: Scopes{Levels: levels(
		CapLogs, Read, CapFiles, Write, CapPhotos, Write, CapSystem, Change, CapNetwork, Change,
		CapUsers, Manage, CapScreen, Control, CapApps, Manage, CapContainers, Manage,
		CapTerminal, On, CapCode, Sandbox, CapPower, On)}},
	{ID: "full", Scopes: Scopes{Levels: levels(
		CapLogs, Read, CapFiles, Write, CapPhotos, Write, CapSystem, Change, CapNetwork, Change,
		CapUsers, Manage, CapScreen, Control, CapApps, Manage, CapContainers, Manage,
		CapTerminal, On, CapCode, Host, CapPower, On),
		Auto: []string{CapFiles, CapPhotos, CapSystem, CapApps, CapContainers, CapScreen, CapTerminal, CapCode}}},
}

// PresetScopes returns a copy of a preset's profile.
func PresetScopes(id string) (Scopes, bool) {
	for _, p := range Presets {
		if p.ID == id {
			s, err := p.Scopes.Normalize()
			return s, err == nil
		}
	}
	return Scopes{}, false
}

// LegacyScopes is what tokens made before scopes could do: "read" looked
// around, "act" could also ask for any action.
func LegacyScopes(scope string) Scopes {
	if scope != "read" && scope != "act" {
		return Scopes{Levels: map[string]string{}, Folders: []string{}, Auto: []string{}} // unknown: nothing
	}
	s := Scopes{Levels: levels(CapLogs, Read, CapFiles, Read, CapSystem, View, CapNetwork, View,
		CapScreen, View, CapApps, View, CapContainers, View)}
	if scope == "act" {
		s.Levels[CapFiles] = Write
		s.Levels[CapScreen] = Control
		s.Levels[CapApps] = Manage
		s.Levels[CapContainers] = Manage
		s.Levels[CapTerminal] = On
	}
	n, _ := s.Normalize()
	return n
}

// ParseAllowList checks an IP allowlist: single addresses or CIDR ranges.
func ParseAllowList(list []string) ([]string, error) {
	if len(list) > 50 {
		return nil, fmt.Errorf("%w: at most 50 addresses", ErrBadScopes)
	}
	out := []string{}
	for _, e := range list {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if p, err := netip.ParsePrefix(e); err == nil {
			e = p.Masked().String()
		} else if a, err := netip.ParseAddr(e); err == nil {
			e = a.Unmap().String()
		} else {
			return nil, fmt.Errorf("%w: %q isn't an IP address or range", ErrBadScopes, truncate(e, 50))
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}

// AllowListed reports whether ip matches the list (an empty list allows all).
func AllowListed(list []string, ip string) bool {
	if len(list) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, e := range list {
		if p, err := netip.ParsePrefix(e); err == nil && p.Contains(a) {
			return true
		}
		if b, err := netip.ParseAddr(e); err == nil && b.Unmap() == a {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
