// Package rdp connects NoCapOS's Remote Desktop app to real RDP servers, and
// Virtual Desk to its virtual machines' VNC screens.
//
// The RDP protocol itself is spoken by guacd (Apache Guacamole's proxy daemon).
// alfad performs the Guacamole handshake with guacd and then relays Guacamole
// instructions between guacd and the browser's guacamole-common-js client over
// a WebSocket.
package rdp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Encode builds one Guacamole instruction: "LEN.VALUE,LEN.VALUE;". Lengths
// count Unicode code points, not bytes.
func Encode(op string, args ...string) string {
	var b strings.Builder
	writeElem(&b, op)
	for _, a := range args {
		b.WriteByte(',')
		writeElem(&b, a)
	}
	b.WriteByte(';')
	return b.String()
}

func writeElem(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(utf8.RuneCountInString(s)))
	b.WriteByte('.')
	b.WriteString(s)
}

// Instruction is a parsed instruction plus its exact wire form.
type Instruction struct {
	Op   string
	Args []string
	Raw  string
}

// Reader parses instructions from a guacd stream.
type Reader struct{ r *bufio.Reader }

func NewReader(r io.Reader) *Reader { return &Reader{r: bufio.NewReaderSize(r, 64*1024)} }

// Buffered reports bytes already read from the socket but not yet parsed.
func (rd *Reader) Buffered() int { return rd.r.Buffered() }

// maxElem bounds a single element (an image chunk is typically < 8 KiB).
const maxElem = 4 << 20

func (rd *Reader) Read() (Instruction, error) {
	var raw strings.Builder
	var elems []string
	for {
		lenStr, err := rd.r.ReadString('.')
		if err != nil {
			return Instruction{}, err
		}
		raw.WriteString(lenStr)
		n, err := strconv.Atoi(lenStr[:len(lenStr)-1])
		if err != nil || n < 0 || n > maxElem {
			return Instruction{}, fmt.Errorf("guacamole: bad element length %q", lenStr)
		}
		var val strings.Builder
		for i := 0; i < n; i++ {
			r, _, err := rd.r.ReadRune()
			if err != nil {
				return Instruction{}, err
			}
			val.WriteRune(r)
		}
		raw.WriteString(val.String())
		elems = append(elems, val.String())
		term, err := rd.r.ReadByte()
		if err != nil {
			return Instruction{}, err
		}
		raw.WriteByte(term)
		switch term {
		case ',':
			continue
		case ';':
			return Instruction{Op: elems[0], Args: elems[1:], Raw: raw.String()}, nil
		default:
			return Instruction{}, fmt.Errorf("guacamole: unexpected terminator %q", term)
		}
	}
}

// Params are the connection settings for one session.
type Params struct {
	Protocol   string // rdp (default) | vnc
	Hostname   string
	Port       int
	Username   string
	Password   string
	Domain     string
	Security   string // any | nla | tls | rdp
	IgnoreCert bool
	Width      int
	Height     int
	DPI        int

	// The options below are Remote Desktop's; their zero values keep the
	// defaults (and Virtual Desk's screens as they are).

	// ColorDepth is 8, 16, 24 or 32 bits (0: the protocol's default).
	ColorDepth int
	// FixedSize keeps the remote screen at Width x Height instead of
	// following the window (RDP's display-update).
	FixedSize bool
	// Audio plays the computer's sound here (RDP).
	Audio bool
	// DisableCopy / DisablePaste stop the clipboard going one way.
	DisableCopy, DisablePaste bool
	// Experience (RDP): what the computer draws; all off is fastest.
	Wallpaper, Theming, FullWindowDrag, DesktopComposition, MenuAnimations bool
	NoFontSmoothing                                                        bool
	// Gateway (RDP): a remote desktop gateway in front of the computer.
	GatewayHost     string
	GatewayPort     int
	GatewayUser     string
	GatewayPassword string
	GatewayDomain   string
}

// AudioTypes are the sound formats the browser client plays (raw PCM).
var AudioTypes = []string{"audio/L8", "audio/L16"}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return ""
}

func (p Params) protocol() string {
	if p.Protocol == "vnc" {
		return "vnc"
	}
	return "rdp"
}

func (p Params) values() map[string]string {
	if p.protocol() == "vnc" {
		// A virtual machine's own screen: loopback only, no password (it is
		// reachable only through this authenticated tunnel).
		port := p.Port
		if port == 0 {
			port = 5900
		}
		depth := "24"
		if p.ColorDepth == 8 || p.ColorDepth == 16 || p.ColorDepth == 32 {
			depth = strconv.Itoa(p.ColorDepth)
		}
		return map[string]string{
			"hostname":      p.Hostname,
			"port":          strconv.Itoa(port),
			"username":      p.Username,
			"password":      p.Password,
			"color-depth":   depth,
			"cursor":        "remote",
			"disable-copy":  boolStr(p.DisableCopy),
			"disable-paste": boolStr(p.DisablePaste),
		}
	}
	port := p.Port
	if port == 0 {
		port = 3389
	}
	sec := p.Security
	if sec == "" {
		sec = "any"
	}
	v := map[string]string{
		"hostname":                   p.Hostname,
		"port":                       strconv.Itoa(port),
		"username":                   p.Username,
		"password":                   p.Password,
		"domain":                     p.Domain,
		"security":                   sec,
		"resize-method":              "display-update",
		"enable-wallpaper":           strconv.FormatBool(p.Wallpaper),
		"enable-theming":             strconv.FormatBool(p.Theming),
		"enable-font-smoothing":      strconv.FormatBool(!p.NoFontSmoothing),
		"enable-full-window-drag":    strconv.FormatBool(p.FullWindowDrag),
		"enable-desktop-composition": strconv.FormatBool(p.DesktopComposition),
		"enable-menu-animations":     strconv.FormatBool(p.MenuAnimations),
		"disable-audio":              strconv.FormatBool(!p.Audio),
		"disable-copy":               boolStr(p.DisableCopy),
		"disable-paste":              boolStr(p.DisablePaste),
		"width":                      strconv.Itoa(p.Width),
		"height":                     strconv.Itoa(p.Height),
		"dpi":                        strconv.Itoa(p.DPI),
	}
	if p.FixedSize {
		v["resize-method"] = ""
	}
	switch p.ColorDepth {
	case 8, 16, 24, 32:
		v["color-depth"] = strconv.Itoa(p.ColorDepth)
	}
	if p.IgnoreCert {
		v["ignore-cert"] = "true"
	}
	if p.GatewayHost != "" {
		gp := p.GatewayPort
		if gp == 0 {
			gp = 443
		}
		v["gateway-hostname"] = p.GatewayHost
		v["gateway-port"] = strconv.Itoa(gp)
		v["gateway-username"] = p.GatewayUser
		v["gateway-password"] = p.GatewayPassword
		v["gateway-domain"] = p.GatewayDomain
	}
	return v
}

// ErrServer is a failure reported by guacd during the handshake.
var ErrServer = errors.New("remote desktop error")

// Handshake selects the protocol (RDP or VNC) on a fresh guacd connection and connects
// it to the target, returning guacd's connection id once it reports "ready".
func Handshake(w io.Writer, rd *Reader, p Params) (string, error) {
	if _, err := io.WriteString(w, Encode("select", p.protocol())); err != nil {
		return "", err
	}
	args, err := rd.Read()
	if err != nil {
		return "", err
	}
	if args.Op != "args" {
		return "", fmt.Errorf("%w: guacd sent %q instead of args", ErrServer, args.Op)
	}

	dpi := p.DPI
	if dpi <= 0 {
		dpi = 96
	}
	var audio []string
	if p.Audio {
		audio = AudioTypes
	}
	hello := Encode("size", strconv.Itoa(p.Width), strconv.Itoa(p.Height), strconv.Itoa(dpi)) +
		Encode("audio", audio...) +
		Encode("video") +
		Encode("image", "image/png", "image/jpeg", "image/webp")
	if _, err := io.WriteString(w, hello); err != nil {
		return "", err
	}

	vals := p.values()
	connect := make([]string, len(args.Args))
	for i, name := range args.Args {
		if strings.HasPrefix(name, "VERSION_") {
			connect[i] = name // speak whatever protocol version guacd offers
			continue
		}
		connect[i] = vals[name]
	}
	if _, err := io.WriteString(w, Encode("connect", connect...)); err != nil {
		return "", err
	}

	for {
		ins, err := rd.Read()
		if err != nil {
			return "", err
		}
		switch ins.Op {
		case "ready":
			if len(ins.Args) > 0 {
				return ins.Args[0], nil
			}
			return "", nil
		case "error":
			msg := "connection failed"
			if len(ins.Args) > 0 {
				msg = ins.Args[0]
			}
			return "", fmt.Errorf("%w: %s", ErrServer, msg)
		}
		// Anything else before "ready" (e.g. log/nop) is ignored.
	}
}
