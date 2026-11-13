package dnsblock

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Upstream answers DNS messages for the blocker.
type Upstream interface {
	Exchange(ctx context.Context, msg []byte) ([]byte, error)
	String() string
}

const upstreamTimeout = 4 * time.Second

// ParseUpstream reads an upstream spec:
//
//	9.9.9.9, 9.9.9.9:53, udp://9.9.9.9   plain DNS (TCP when the answer is big)
//	tcp://9.9.9.9                         plain DNS over TCP only
//	tls://1.1.1.1, tls://dns.example:853  DNS-over-TLS
//	https://1.1.1.1/dns-query             DNS-over-HTTPS
//
// Plain DNS needs an IP address; TLS and HTTPS may use a name, found
// through bootstrap (a plain DNS server's IP address).
func ParseUpstream(spec, bootstrap string) (Upstream, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || len(spec) > 256 || strings.ContainsAny(spec, " \t\r\n") {
		return nil, bad("%q isn't an upstream resolver", spec)
	}
	scheme, rest, ok := strings.Cut(spec, "://")
	if !ok {
		scheme, rest = "udp", spec
	}
	switch scheme {
	case "udp", "tcp":
		addr, err := ipPort(rest, 53)
		if err != nil {
			return nil, bad("plain DNS needs an IP address, like 9.9.9.9")
		}
		return &plain{addr: addr, tcpOnly: scheme == "tcp", spec: spec}, nil
	case "tls":
		host, port, err := hostPort(rest, 853)
		if err != nil {
			return nil, bad("DNS-over-TLS looks like tls://1.1.1.1 or tls://dns.example")
		}
		return &dot{host: host, port: port, dial: bootstrapDialer(bootstrap), spec: spec, idle: make(chan net.Conn, 4)}, nil
	case "https":
		u, err := url.Parse(spec)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, bad("DNS-over-HTTPS looks like https://1.1.1.1/dns-query")
		}
		if _, _, err := hostPort(u.Host, 443); err != nil {
			return nil, bad("DNS-over-HTTPS looks like https://1.1.1.1/dns-query")
		}
		if u.Path == "" {
			u.Path = "/dns-query"
		}
		tr := &http.Transport{
			DialContext:         bootstrapDialer(bootstrap),
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2:   true,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		}
		return &doh{url: u.String(), client: &http.Client{Transport: tr, Timeout: upstreamTimeout}, spec: spec}, nil
	}
	return nil, bad("upstreams start with udp://, tcp://, tls:// or https://")
}

func ipPort(s string, def int) (string, error) {
	host, port, err := hostPort(s, def)
	if err != nil {
		return "", err
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func hostPort(s string, def int) (string, int, error) {
	host, port := s, def
	if h, p, err := net.SplitHostPort(s); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, errors.New("bad port")
		}
		host, port = h, n
	} else if strings.Count(s, ":") > 1 {
		host = strings.Trim(s, "[]") // a bare IPv6 address
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return host, port, nil
	}
	if _, ok := NormalizeDomain(host); !ok {
		return "", 0, errors.New("bad host")
	}
	return host, port, nil
}

// bootstrapDialer dials names through the bootstrap resolver, so finding
// an upstream never goes through this machine's own DNS (which may be us).
func bootstrapDialer(bootstrap string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if bootstrap == "" {
		bootstrap = "9.9.9.9"
	}
	d := &net.Dialer{Timeout: upstreamTimeout}
	d.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(bootstrap, "53"))
	}}
	return d.DialContext
}

// ---------- plain DNS ----------

type plain struct {
	addr    string
	tcpOnly bool
	spec    string
}

func (p *plain) String() string { return p.spec }

func (p *plain) Exchange(ctx context.Context, msg []byte) ([]byte, error) {
	if !p.tcpOnly {
		resp, err := p.udp(ctx, msg)
		if err != nil || len(resp) < 3 || resp[2]&0x02 == 0 { // not truncated
			return resp, err
		}
	}
	d := &net.Dialer{Timeout: upstreamTimeout}
	c, err := d.DialContext(ctx, "tcp", p.addr)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return streamExchange(ctx, c, msg)
}

func (p *plain) udp(ctx context.Context, msg []byte) ([]byte, error) {
	d := &net.Dialer{Timeout: upstreamTimeout}
	c, err := d.DialContext(ctx, "udp", p.addr)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	setDeadline(ctx, c)
	if _, err := c.Write(msg); err != nil {
		return nil, err
	}
	buf := make([]byte, 65535)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return nil, err
		}
		// Only an answer to this question counts (spoofing, stragglers).
		if n >= 12 && buf[0] == msg[0] && buf[1] == msg[1] {
			return append([]byte(nil), buf[:n]...), nil
		}
	}
}

func setDeadline(ctx context.Context, c net.Conn) {
	dl := time.Now().Add(upstreamTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	_ = c.SetDeadline(dl)
}

// streamExchange sends msg with a length prefix and reads one answer
// (DNS over TCP and TLS).
func streamExchange(ctx context.Context, c net.Conn, msg []byte) ([]byte, error) {
	setDeadline(ctx, c)
	out := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(out, uint16(len(msg)))
	copy(out[2:], msg)
	if _, err := c.Write(out); err != nil {
		return nil, err
	}
	for {
		var l [2]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return nil, err
		}
		resp := make([]byte, binary.BigEndian.Uint16(l[:]))
		if _, err := io.ReadFull(c, resp); err != nil {
			return nil, err
		}
		if len(resp) >= 12 && resp[0] == msg[0] && resp[1] == msg[1] {
			return resp, nil
		}
	}
}

// ---------- DNS-over-TLS ----------

type dot struct {
	host string
	port int
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	spec string
	idle chan net.Conn // reused connections
	mu   sync.Mutex
}

func (d *dot) String() string { return d.spec }

func (d *dot) conn(ctx context.Context) (net.Conn, bool, error) {
	select {
	case c := <-d.idle:
		return c, true, nil
	default:
	}
	raw, err := d.dial(ctx, "tcp", net.JoinHostPort(d.host, strconv.Itoa(d.port)))
	if err != nil {
		return nil, false, err
	}
	c := tls.Client(raw, &tls.Config{ServerName: d.host, MinVersion: tls.VersionTLS12})
	setDeadline(ctx, c)
	if err := c.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, false, err
	}
	return c, false, nil
}

func (d *dot) Exchange(ctx context.Context, msg []byte) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		c, reused, err := d.conn(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := streamExchange(ctx, c, msg)
		if err != nil {
			c.Close()
			if reused {
				continue // the server closed an idle connection: dial again
			}
			return nil, err
		}
		select {
		case d.idle <- c:
		default:
			c.Close()
		}
		return resp, nil
	}
	return nil, errors.New("DNS-over-TLS connection failed")
}

// ---------- DNS-over-HTTPS ----------

type doh struct {
	url    string
	client *http.Client
	spec   string
}

func (d *doh) String() string { return d.spec }

func (d *doh) Exchange(ctx context.Context, msg []byte) ([]byte, error) {
	// ID 0 lets HTTP caches share answers (RFC 8484); the real ID goes back after.
	q := append([]byte(nil), msg...)
	q[0], q[1] = 0, 0
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(q))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	res, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DNS-over-HTTPS answered %s", res.Status)
	}
	resp, err := io.ReadAll(io.LimitReader(res.Body, 65535))
	if err != nil {
		return nil, err
	}
	if len(resp) < 12 {
		return nil, errors.New("short DNS-over-HTTPS answer")
	}
	resp[0], resp[1] = msg[0], msg[1]
	return resp, nil
}

// randomID returns a fresh DNS message ID.
func randomID() uint16 {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint16(b[:])
}
