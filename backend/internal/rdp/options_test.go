package rdp

import (
	"bytes"
	"strings"
	"testing"
)

func TestRemoteDesktopOptions(t *testing.T) {
	v := Params{Hostname: "pc", ColorDepth: 16, FixedSize: true, Audio: true, DisablePaste: true, Wallpaper: true,
		NoFontSmoothing: true, GatewayHost: "gw", GatewayUser: "u", GatewayPassword: "p"}.values()
	for k, want := range map[string]string{
		"color-depth": "16", "resize-method": "", "disable-audio": "false", "disable-paste": "true", "disable-copy": "",
		"enable-wallpaper": "true", "enable-font-smoothing": "false", "enable-theming": "false",
		"gateway-hostname": "gw", "gateway-port": "443", "gateway-username": "u", "gateway-password": "p",
	} {
		if v[k] != want {
			t.Errorf("%s = %q, want %q", k, v[k], want)
		}
	}
	// Defaults stay as they were.
	d := Params{Hostname: "pc"}.values()
	if d["resize-method"] != "display-update" || d["disable-audio"] != "true" || d["enable-font-smoothing"] != "true" || d["gateway-hostname"] != "" {
		t.Errorf("defaults changed: %v", d)
	}
	vnc := Params{Protocol: "vnc", Hostname: "pc", Username: "me", ColorDepth: 8, DisableCopy: true}.values()
	if vnc["port"] != "5900" || vnc["username"] != "me" || vnc["color-depth"] != "8" || vnc["disable-copy"] != "true" {
		t.Errorf("vnc: %v", vnc)
	}

	// Sound: the client's formats are offered only when asked for.
	server := Encode("args", "VERSION_1_5_0", "hostname") + Encode("ready", "$a")
	var sent bytes.Buffer
	if _, err := Handshake(&sent, NewReader(strings.NewReader(server)), Params{Hostname: "pc", Audio: true, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sent.String(), Encode("audio", AudioTypes...)) {
		t.Fatalf("audio types not offered: %q", sent.String())
	}
}
