package mcp

import (
	"errors"
	"net/url"
	"strings"
)

// Screen acts on the NoCap Desktop session on this server's own screen.
type Screen struct {
	// EnvFile is where the running session says where it is
	// (nativeapps.DesktopEnvPath).
	EnvFile string
}

var (
	// ErrNoScreen: NoCap Desktop isn't running on the server's screen.
	ErrNoScreen = errors.New("NoCap Desktop isn't running on this server's screen right now")
	// ErrNoScreenTool: the helper program a screen tool needs isn't installed.
	ErrNoScreenTool = errors.New("the program this needs isn't installed on the server")
)

// MaxTypeText is the most text type_text sends at once.
const MaxTypeText = 2000

// CheckURL accepts only http and https addresses for open_url.
func CheckURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("only http:// and https:// addresses can be opened")
	}
	return u.String(), nil
}
