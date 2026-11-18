package hostaccess

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// fakeScreenServer speaks RFB 3.8 with no password, then sends "hello".
func fakeScreenServer(t *testing.T, c net.Conn) {
	t.Helper()
	defer c.Close()
	_, _ = c.Write([]byte("RFB 003.008\n"))
	v := make([]byte, 12)
	if _, err := io.ReadFull(c, v); err != nil {
		return
	}
	_, _ = c.Write([]byte{1, 1})
	pick := make([]byte, 1)
	if _, err := io.ReadFull(c, pick); err != nil || pick[0] != 1 {
		return
	}
	_, _ = c.Write([]byte{0, 0, 0, 0})
	_, _ = c.Write([]byte("hello"))
	buf := make([]byte, 5)
	_, _ = io.ReadFull(c, buf)
}

// vncClient does a client's side of the handshake; returns the security result.
func vncClient(t *testing.T, c net.Conn, password string) uint32 {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	v := make([]byte, 12)
	if _, err := io.ReadFull(c, v); err != nil || string(v) != "RFB 003.008\n" {
		t.Fatalf("version %q %v", v, err)
	}
	_, _ = c.Write([]byte("RFB 003.008\n"))
	types := make([]byte, 2)
	if _, err := io.ReadFull(c, types); err != nil || !bytes.Equal(types, []byte{1, 2}) {
		t.Fatalf("types %v %v", types, err)
	}
	_, _ = c.Write([]byte{2})
	ch := make([]byte, 16)
	if _, err := io.ReadFull(c, ch); err != nil {
		t.Fatal(err)
	}
	resp, err := VNCResponse(password, ch)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write(resp)
	res := make([]byte, 4)
	if _, err := io.ReadFull(c, res); err != nil {
		t.Fatal(err)
	}
	return binary.BigEndian.Uint32(res)
}

func TestProxyVNC(t *testing.T) {
	run := func(password string) (net.Conn, chan error) {
		client, proxySide := net.Pipe()
		done := make(chan error, 1)
		go func() {
			done <- ProxyVNC(proxySide, "s3cretPW", func() (net.Conn, error) {
				a, b := net.Pipe()
				go fakeScreenServer(t, b)
				return a, nil
			})
			proxySide.Close()
		}()
		return client, done
	}

	// The right password: the screen server's data comes through.
	c, done := run("s3cretPW")
	if r := vncClient(t, c, "s3cretPW"); r != 0 {
		t.Fatalf("result = %d", r)
	}
	got := make([]byte, 5)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "hello" {
		t.Fatalf("passed through %q %v", got, err)
	}
	_, _ = c.Write([]byte("abcde"))
	c.Close()
	<-done

	// A wrong one: refused, the screen server is never reached.
	c, done = run("nope")
	if r := vncClient(t, c, "wrongpw1"); r != 1 {
		t.Fatalf("wrong password accepted: %d", r)
	}
	c.Close()
	if err := <-done; err == nil {
		t.Fatal("no error for a wrong password")
	}
}

// The password check is the classic one VNC clients (guacd) compute.
func TestVNCResponseKnownAnswer(t *testing.T) {
	ch := make([]byte, 16)
	a, err := VNCResponse("password", ch)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := VNCResponse("password", ch)
	c, _ := VNCResponse("passworx", ch)
	if !bytes.Equal(a, b) || bytes.Equal(a, c) || bytes.Equal(a[:8], ch[:8]) {
		t.Fatalf("responses %x %x %x", a, b, c)
	}
	// Only the first 8 characters count, as in every VNC client.
	d, _ := VNCResponse("password-and-more", ch)
	if !bytes.Equal(a, d) {
		t.Fatal("characters past the eighth changed the answer")
	}
	if _, err := VNCResponse("x", ch[:8]); err == nil {
		t.Fatal("short challenge accepted")
	}
}
