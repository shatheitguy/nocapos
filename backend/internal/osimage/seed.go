package osimage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The installer (cmd/nocap-installer) leaves the choices made during setup
// on the new data partition, readable by root only. NoCapOS applies them on
// its first start (making the owner's account, the machine's name, time
// zone, keyboard and how it's used) and then destroys the file.

// SeedPath is where the installed system finds the seed. Replaced in tests.
var SeedPath = "/run/rugix/mounts/data/nocapos-seed.json"

// SeedName is the seed's file name at the data partition's top.
const SeedName = "nocapos-seed.json"

// Usage modes: how NoCapOS is used.
const (
	UsageDesktopWeb = "desktop-web" // the desktop on the screen + NoCapOS from other devices
	UsageDesktop    = "desktop"     // the desktop only; other devices are kept out
	UsageWeb        = "web"         // no screen: NoCapOS from other devices only
)

// ValidUsage reports whether u names a usage mode.
func ValidUsage(u string) bool {
	return u == UsageDesktopWeb || u == UsageDesktop || u == UsageWeb
}

// Seed is what the installer asked for.
type Seed struct {
	Username string `json:"username"`
	FullName string `json:"full_name,omitempty"`
	Password string `json:"password"`
	Hostname string `json:"hostname,omitempty"`
	TimeZone string `json:"timezone,omitempty"`
	Keyboard string `json:"keyboard,omitempty"` // XKB layout, e.g. "us", "de", "gb"
	Variant  string `json:"keyboard_variant,omitempty"`
	Language string `json:"language,omitempty"` // e.g. "en-US"
	Usage    string `json:"usage,omitempty"`
}

var (
	xkbRe  = regexp.MustCompile(`^[a-z]{2,6}(\([a-z0-9_-]+\))?$`)
	xkbVar = regexp.MustCompile(`^[a-z0-9_-]{0,32}$`)
)

// Validate checks the parts NoCapOS writes into system files.
func (s *Seed) Validate() error {
	if s.Username == "" || s.Password == "" {
		return errors.New("the seed has no account")
	}
	if strings.ContainsAny(s.FullName, ":,\n") {
		return errors.New("the name can't contain ':', ',' or line breaks")
	}
	if s.Keyboard != "" && !xkbRe.MatchString(s.Keyboard) {
		return errors.New("unknown keyboard layout")
	}
	if !xkbVar.MatchString(s.Variant) {
		return errors.New("unknown keyboard variant")
	}
	if s.Usage != "" && !ValidUsage(s.Usage) {
		return errors.New("unknown usage mode")
	}
	return nil
}

// WriteSeed saves s at the top of a data partition mounted at dataRoot.
func WriteSeed(dataRoot string, s *Seed) error {
	if err := s.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataRoot, SeedName), b, 0o600)
}

// ReadSeed returns the installer's seed, or nil when there is none.
func ReadSeed() (*Seed, error) {
	b, err := os.ReadFile(SeedPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Seed
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, s.Validate()
}

// DestroySeed overwrites the seed (it holds a password) and removes it.
func DestroySeed() error {
	if fi, err := os.Stat(SeedPath); err == nil {
		if f, err := os.OpenFile(SeedPath, os.O_WRONLY, 0); err == nil {
			_, _ = f.Write(make([]byte, fi.Size()))
			_ = f.Sync()
			_ = f.Close()
		}
	}
	err := os.Remove(SeedPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// KeyboardFile is /etc/default/keyboard's content for a layout.
func KeyboardFile(layout, variant string) string {
	return "# Keyboard layout (set by NoCapOS)\nXKBMODEL=\"pc105\"\nXKBLAYOUT=\"" + layout + "\"\nXKBVARIANT=\"" + variant + "\"\nXKBOPTIONS=\"\"\nBACKSPACE=\"guess\"\n"
}
