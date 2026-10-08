package rdp

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeCountsCodePoints(t *testing.T) {
	got := Encode("name", "héllo", "")
	if want := "4.name,5.héllo,0.;"; got != want {
		t.Fatalf("Encode = %q, want %q", got, want)
	}
}

func TestReaderRoundTrip(t *testing.T) {
	stream := Encode("args", "VERSION_1_5_0", "hostname", "port") + Encode("sync", "123") + Encode("img", "a,b;c", "日本")
	rd := NewReader(strings.NewReader(stream))
	want := []Instruction{
		{Op: "args", Args: []string{"VERSION_1_5_0", "hostname", "port"}},
		{Op: "sync", Args: []string{"123"}},
		{Op: "img", Args: []string{"a,b;c", "日本"}},
	}
	for i, w := range want {
		got, err := rd.Read()
		if err != nil {
			t.Fatalf("instruction %d: %v", i, err)
		}
		if got.Op != w.Op || strings.Join(got.Args, "|") != strings.Join(w.Args, "|") {
			t.Fatalf("instruction %d = %+v, want %+v", i, got, w)
		}
		if got.Raw != Encode(w.Op, w.Args...) {
			t.Fatalf("instruction %d raw = %q", i, got.Raw)
		}
	}
}

func TestHandshake(t *testing.T) {
	// guacd's side of the conversation.
	server := Encode("args", "VERSION_1_5_0", "hostname", "port", "username", "password", "ignore-cert", "unknown-arg") +
		Encode("ready", "$abc")
	var sent bytes.Buffer
	id, err := Handshake(&sent, NewReader(strings.NewReader(server)), Params{
		Hostname: "192.168.1.170", Username: "me", Password: "p,w;d", IgnoreCert: true, Width: 1280, Height: 800,
	})
	if err != nil || id != "$abc" {
		t.Fatalf("Handshake = %q, %v", id, err)
	}
	out := sent.String()
	for _, frag := range []string{
		Encode("select", "rdp"),
		Encode("size", "1280", "800", "96"),
		Encode("connect", "VERSION_1_5_0", "192.168.1.170", "3389", "me", "p,w;d", "true", ""),
	} {
		if !strings.Contains(out, frag) {
			t.Fatalf("client output missing %q in %q", frag, out)
		}
	}
}

func TestHandshakeError(t *testing.T) {
	server := Encode("args", "VERSION_1_5_0", "hostname") + Encode("error", "Host unreachable", "519")
	_, err := Handshake(&bytes.Buffer{}, NewReader(strings.NewReader(server)), Params{Hostname: "x", Width: 1, Height: 1})
	if err == nil || !strings.Contains(err.Error(), "Host unreachable") {
		t.Fatalf("want guacd error, got %v", err)
	}
}
