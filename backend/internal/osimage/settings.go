package osimage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Settings are Glow Up's choices on an image install (os-update-settings.json).
type Settings struct {
	Channel string `json:"channel"` // stable or beta
	// AutoSecurity: install security refreshes by themselves at AutoTime
	// (local time, "HH:MM") and restart. Off unless turned on.
	AutoSecurity bool   `json:"auto_security"`
	AutoTime     string `json:"auto_time"`
}

var timeRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// DefaultAutoTime: the middle of the night.
const DefaultAutoTime = "03:30"

func settingsPath(dataDir string) string { return filepath.Join(dataDir, "os-update-settings.json") }

// LoadSettings returns the saved settings, with the image's channel when
// none was chosen.
func LoadSettings(dataDir string, info Info) Settings {
	s := Settings{Channel: info.Channel, AutoTime: DefaultAutoTime}
	if b, err := os.ReadFile(settingsPath(dataDir)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.Channel != ChannelBeta {
		s.Channel = ChannelStable
	}
	if !timeRe.MatchString(s.AutoTime) {
		s.AutoTime = DefaultAutoTime
	}
	return s
}

// SaveSettings validates and saves s.
func SaveSettings(dataDir string, s Settings) error {
	if s.Channel != ChannelStable && s.Channel != ChannelBeta {
		return errors.New("channel must be stable or beta")
	}
	if !timeRe.MatchString(s.AutoTime) {
		return errors.New("time must look like 03:30")
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(dataDir), b, 0o600)
}

// InWindow reports whether now falls in the hour starting at s.AutoTime.
func (s Settings) InWindow(now time.Time) bool {
	t, err := time.ParseInLocation("15:04", s.AutoTime, now.Location())
	if err != nil {
		return false
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if now.Before(start) {
		start = start.Add(-24 * time.Hour)
	}
	return now.Sub(start) < time.Hour
}
