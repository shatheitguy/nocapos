package docker

import "testing"

func TestSpecCapabilitiesAndDevices(t *testing.T) {
	s := Spec{Name: "x", Image: "nginx", CapAdd: []string{"cap_net_admin", "NET_ADMIN", " SYS_TIME ", ""}, Devices: []string{"/dev/dri", "/dev/ttyUSB0:/dev/ttyACM0"}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(s.CapAdd) != 2 || s.CapAdd[0] != "NET_ADMIN" || s.CapAdd[1] != "SYS_TIME" {
		t.Errorf("caps = %v", s.CapAdd)
	}
	if cfg := s.CreateConfig(nil); len(cfg.HostConfig.CapAdd) != 2 {
		t.Errorf("caps not passed to the container: %+v", cfg.HostConfig)
	}
	for _, bad := range []Spec{
		{Name: "x", Image: "nginx", CapAdd: []string{"NET ADMIN"}},
		{Name: "x", Image: "nginx", CapAdd: []string{"$(reboot)"}},
		{Name: "x", Image: "nginx", Devices: []string{"/dev/../etc/shadow"}},
		{Name: "x", Image: "nginx", Devices: []string{"/dev/dri:/etc"}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	raw := &rawContainer{Name: "/x", Config: map[string]any{"Image": "nginx"}, HostConfig: map[string]any{"CapAdd": []any{"CAP_NET_RAW"}}}
	if got := specFrom(raw).CapAdd; len(got) != 1 || got[0] != "NET_RAW" {
		t.Errorf("read caps = %v", got)
	}
}
