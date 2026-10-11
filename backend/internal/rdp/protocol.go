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
		return map[string]string{
			"hostname":    p.Hostname,
			"port":        strconv.Itoa(p.Port),
			"password":    p.Password,
			"color-depth": "24",
			"cursor":      "remote",
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
		"hostname":              p.Hostname,
		"port":                  strconv.Itoa(port),
		"username":              p.Username,
		"password":              p.Password,
		"domain":                p.Domain,
		"security":              sec,
		"resize-method":         "display-update",
		"enable-wallpaper":      "false",
		"enable-font-smoothing": "true",
		"disable-audio":         "true",
		"width":                 strconv.Itoa(p.Width),
		"height":                strconv.Itoa(p.Height),
		"dpi":                   strconv.Itoa(p.DPI),
	}
	if p.IgnoreCert {
		v["ignore-cert"] = "true"
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
	hello := Encode("size", strconv.Itoa(p.Width), strconv.Itoa(p.Height), strconv.Itoa(dpi)) +
		Encode("audio") +
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
