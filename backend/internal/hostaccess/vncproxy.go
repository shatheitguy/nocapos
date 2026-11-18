package hostaccess

import (
	"bytes"
	"crypto/des"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// ProxyVNC answers one VNC client (guacd) with the classic VNC password
// check, then connects it to the screen server (wayvnc, which has no
// password of its own: its socket is reachable only by the session's owner
// and root) and passes everything through. Speaks RFB 3.7/3.8 to the client.
func ProxyVNC(client net.Conn, password string, dial func() (net.Conn, error)) error {
	_ = client.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := client.Write([]byte("RFB 003.008\n")); err != nil {
		return err
	}
	ver := make([]byte, 12)
	if _, err := io.ReadFull(client, ver); err != nil {
		return err
	}
	if !bytes.Equal(ver, []byte("RFB 003.008\n")) && !bytes.Equal(ver, []byte("RFB 003.007\n")) {
		return fmt.Errorf("unsupported client version %q", ver)
	}
	// One security type: VNC authentication (2).
	if _, err := client.Write([]byte{1, 2}); err != nil {
		return err
	}
	pick := make([]byte, 1)
	if _, err := io.ReadFull(client, pick); err != nil {
		return err
	}
	if pick[0] != 2 {
		return errors.New("the client didn't pick the password check")
	}
	challenge := make([]byte, 16)
	if _, err := rand.Read(challenge); err != nil {
		return err
	}
	if _, err := client.Write(challenge); err != nil {
		return err
	}
	resp := make([]byte, 16)
	if _, err := io.ReadFull(client, resp); err != nil {
		return err
	}
	want, err := VNCResponse(password, challenge)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(resp, want) != 1 {
		reason := "wrong password"
		msg := make([]byte, 8+len(reason))
		binary.BigEndian.PutUint32(msg[0:], 1)
		binary.BigEndian.PutUint32(msg[4:], uint32(len(reason)))
		copy(msg[8:], reason)
		_, _ = client.Write(msg)
		return errors.New("wrong VNC password")
	}

	server, err := dial()
	if err != nil {
		return err
	}
	defer server.Close()
	_ = server.SetDeadline(time.Now().Add(20 * time.Second))
	if err := serverHandshake(server); err != nil {
		return err
	}
	// The password was right and the screen server is there.
	if _, err := client.Write([]byte{0, 0, 0, 0}); err != nil {
		return err
	}
	_ = client.SetDeadline(time.Time{})
	_ = server.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(server, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, server); done <- struct{}{} }()
	<-done
	return nil
}

// serverHandshake takes the screen server through RFB 3.8 with no
// password (security type 1).
func serverHandshake(server net.Conn) error {
	ver := make([]byte, 12)
	if _, err := io.ReadFull(server, ver); err != nil {
		return err
	}
	if !bytes.HasPrefix(ver, []byte("RFB 003.")) {
		return fmt.Errorf("not a VNC server: %q", ver)
	}
	if _, err := server.Write([]byte("RFB 003.008\n")); err != nil {
		return err
	}
	n := make([]byte, 1)
	if _, err := io.ReadFull(server, n); err != nil {
		return err
	}
	if n[0] == 0 {
		return errors.New("the screen server refused the connection")
	}
	types := make([]byte, n[0])
	if _, err := io.ReadFull(server, types); err != nil {
		return err
	}
	if bytes.IndexByte(types, 1) < 0 {
		return errors.New("the screen server wants a password of its own")
	}
	if _, err := server.Write([]byte{1}); err != nil {
		return err
	}
	res := make([]byte, 4)
	if _, err := io.ReadFull(server, res); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(res) != 0 {
		return errors.New("the screen server refused the connection")
	}
	return nil
}

// VNCResponse is the classic VNC password answer to a 16-byte challenge:
// DES with the password (8 bytes, each byte's bits reversed) as the key.
func VNCResponse(password string, challenge []byte) ([]byte, error) {
	if len(challenge) != 16 {
		return nil, errors.New("bad challenge")
	}
	key := make([]byte, 8)
	copy(key, password)
	for i, b := range key {
		var r byte
		for j := 0; j < 8; j++ {
			if b&(1<<j) != 0 {
				r |= 1 << (7 - j)
			}
		}
		key[i] = r
	}
	c, err := des.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	c.Encrypt(out[:8], challenge[:8])
	c.Encrypt(out[8:], challenge[8:])
	return out, nil
}
