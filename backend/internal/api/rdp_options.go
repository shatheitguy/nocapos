package api

import (
	"strings"

	"alfaos/alfad/internal/rdp"
)

// Sealer encrypts secrets at rest (the server's secret box).
type Sealer interface {
	Seal(plain string) []byte
	Open(sealed []byte) (string, error)
}

// rdpOptions are a remote connection's settings, as Remote Desktop's option
// tabs show them. Each maps to a guacd parameter the RDP or VNC client
// really has; nothing here is for show.
type rdpOptions struct {
	// Advanced (RDP): security mode, and whether to accept a certificate
	// this server can't verify.
	Security   string `json:"security,omitempty"`
	IgnoreCert bool   `json:"ignore_cert,omitempty"`
	// Display: fit (follow the window), fullscreen (start full screen and
	// follow it) or fixed (FixedWidth x FixedHeight, scaled to the window).
	SizeMode    string `json:"size_mode,omitempty"`
	FixedWidth  int    `json:"fixed_width,omitempty"`
	FixedHeight int    `json:"fixed_height,omitempty"`
	// Scale is the remote desktop's text and icon size in percent (RDP DPI).
	Scale      int `json:"scale,omitempty"`
	ColorDepth int `json:"color_depth,omitempty"`
	// Local resources: Clipboard is both (""), to-remote, from-remote or off.
	Clipboard string `json:"clipboard,omitempty"`
	Audio     bool   `json:"audio,omitempty"`
	// Experience (RDP): Speed is the preset the switches came from.
	Speed              string `json:"speed,omitempty"`
	Wallpaper          bool   `json:"wallpaper,omitempty"`
	Theming            bool   `json:"theming,omitempty"`
	FontSmoothing      bool   `json:"font_smoothing,omitempty"`
	FullWindowDrag     bool   `json:"full_window_drag,omitempty"`
	DesktopComposition bool   `json:"desktop_composition,omitempty"`
	MenuAnimations     bool   `json:"menu_animations,omitempty"`
	// Gateway (RDP). Its password travels like the computer's.
	GatewayHost   string `json:"gateway_host,omitempty"`
	GatewayPort   int    `json:"gateway_port,omitempty"`
	GatewayUser   string `json:"gateway_user,omitempty"`
	GatewayDomain string `json:"gateway_domain,omitempty"`
}

// validHost: a host name or IP address, nothing that could smuggle more.
func validHost(h string) bool {
	return h != "" && len(h) <= 253 && !strings.ContainsAny(h, " /\\\t\r\n,;")
}

func validProtocol(p string) bool { return p == "rdp" || p == "vnc" }

// check normalises o and returns what's wrong with it ("" when fine).
func (o *rdpOptions) check() string {
	switch o.Security {
	case "", "any", "nla", "tls", "rdp":
	default:
		return "security must be any, nla, tls or rdp"
	}
	switch o.SizeMode {
	case "", "fit", "fullscreen":
	case "fixed":
		o.FixedWidth = clampRange(o.FixedWidth, 640, 7680, 1280)
		o.FixedHeight = clampRange(o.FixedHeight, 480, 4320, 800)
	default:
		return "size must be fit, fullscreen or fixed"
	}
	if o.SizeMode != "fixed" {
		o.FixedWidth, o.FixedHeight = 0, 0
	}
	if o.Scale != 0 {
		o.Scale = clampRange(o.Scale, 100, 300, 100)
	}
	switch o.ColorDepth {
	case 0, 8, 16, 24, 32:
	default:
		return "colour quality must be 8, 16, 24 or 32 bits"
	}
	switch o.Clipboard {
	case "", "both", "to-remote", "from-remote", "off":
	default:
		return "clipboard must be both, to-remote, from-remote or off"
	}
	switch o.Speed {
	case "", "auto", "lan", "broadband", "low", "custom":
	default:
		return "unknown connection speed"
	}
	o.GatewayHost = strings.TrimSpace(o.GatewayHost)
	if o.GatewayHost != "" && !validHost(o.GatewayHost) {
		return "enter a valid gateway address"
	}
	if o.GatewayPort < 0 || o.GatewayPort > 65535 {
		return "invalid gateway port"
	}
	if len(o.GatewayUser) > 256 || len(o.GatewayDomain) > 256 {
		return "gateway user name is too long"
	}
	return ""
}

// params builds guacd's settings for one session.
func (o rdpOptions) params(protocol, host string, port int, user, pass, domain, gatewayPass string, width, height int) rdp.Params {
	p := rdp.Params{
		Protocol: protocol, Hostname: host, Port: port, Username: user, Password: pass, Domain: domain,
		Security: o.Security, IgnoreCert: o.IgnoreCert,
		Width: width, Height: height, DPI: 96,
		ColorDepth:   o.ColorDepth,
		Audio:        o.Audio && protocol == "rdp",
		DisableCopy:  o.Clipboard == "off" || o.Clipboard == "to-remote",
		DisablePaste: o.Clipboard == "off" || o.Clipboard == "from-remote",
		Wallpaper:    o.Wallpaper, Theming: o.Theming, FullWindowDrag: o.FullWindowDrag,
		DesktopComposition: o.DesktopComposition, MenuAnimations: o.MenuAnimations,
		NoFontSmoothing: !o.FontSmoothing,
	}
	if o.Scale > 0 {
		p.DPI = 96 * o.Scale / 100
	}
	if o.SizeMode == "fixed" {
		p.FixedSize, p.Width, p.Height = true, o.FixedWidth, o.FixedHeight
	}
	if protocol == "rdp" && o.GatewayHost != "" {
		p.GatewayHost, p.GatewayPort, p.GatewayUser, p.GatewayDomain, p.GatewayPassword =
			o.GatewayHost, o.GatewayPort, o.GatewayUser, o.GatewayDomain, gatewayPass
	}
	return p
}
