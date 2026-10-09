package docker

import (
	"encoding/json"
	"strings"
	"testing"
)

// An inspect result trimmed to what matters, shaped like Docker's.
const inspectFixture = `{
  "Id": "4f1c2a9b7e3d0000000000000000000000000000000000000000000000000000",
  "Name": "/jellyfin",
  "State": {"Running": true},
  "Config": {
    "Hostname": "4f1c2a9b7e3d",
    "Image": "jellyfin/jellyfin:10.9",
    "Env": ["TZ=Asia/Dubai", "JELLYFIN_PublishedServerUrl=http://nas"],
    "Cmd": null,
    "Entrypoint": ["/jellyfin/jellyfin"],
    "User": "1000:1000",
    "WorkingDir": "/",
    "Labels": {"nocapos.app": "jellyfin", "nocapos.service": "app"},
    "Healthcheck": {"Test": ["CMD", "curl", "-f", "http://localhost:8096/health"]},
    "ExposedPorts": {"8096/tcp": {}}
  },
  "HostConfig": {
    "Binds": ["/srv/media:/media:ro"],
    "PortBindings": {"8096/tcp": [{"HostIp": "", "HostPort": "18101"}], "7359/udp": [{"HostIp": "0.0.0.0", "HostPort": "7359"}]},
    "RestartPolicy": {"Name": "unless-stopped", "MaximumRetryCount": 0},
    "NetworkMode": "nocap-jellyfin",
    "Memory": 2147483648,
    "NanoCpus": 1500000000,
    "Privileged": false,
    "CapAdd": ["SYS_ADMIN"],
    "LogConfig": {"Type": "json-file", "Config": {"max-size": "10m"}},
    "Devices": [{"PathOnHost": "/dev/dri", "PathInContainer": "/dev/dri", "CgroupPermissions": "rwm"}]
  },
  "Mounts": [
    {"Type": "volume", "Name": "nocap-jellyfin-config", "Source": "/var/lib/docker/volumes/nocap-jellyfin-config/_data", "Destination": "/config", "RW": true},
    {"Type": "bind", "Source": "/srv/media", "Destination": "/media", "RW": false}
  ],
  "NetworkSettings": {"Networks": {
    "nocap-jellyfin": {"IPAMConfig": null, "Aliases": ["app", "4f1c2a9b7e3d", "jellyfin"]},
    "lan": {"IPAMConfig": {"IPv4Address": "192.168.1.50"}, "Aliases": null}
  }}
}`

func fixture(t *testing.T) *rawContainer {
	t.Helper()
	var raw rawContainer
	if err := json.Unmarshal([]byte(inspectFixture), &raw); err != nil {
		t.Fatal(err)
	}
	return &raw
}

func TestSpecFrom(t *testing.T) {
	s := specFrom(fixture(t))
	if s.Name != "jellyfin" || s.Image != "jellyfin/jellyfin:10.9" || s.Hostname != "" {
		t.Errorf("basics: %+v", s)
	}
	if len(s.Env) != 2 || s.Env[0] != (EnvVar{"TZ", "Asia/Dubai"}) {
		t.Errorf("env %+v", s.Env)
	}
	if len(s.Ports) != 2 || s.Ports[0] != (PortMap{Host: 7359, HostIP: "0.0.0.0", Container: 7359, Protocol: "udp"}) || s.Ports[1].Host != 18101 {
		t.Errorf("ports %+v", s.Ports)
	}
	if len(s.Mounts) != 2 || s.Mounts[0] != (SpecMount{Type: "volume", Source: "nocap-jellyfin-config", Target: "/config"}) ||
		s.Mounts[1] != (SpecMount{Type: "bind", Source: "/srv/media", Target: "/media", ReadOnly: true}) {
		t.Errorf("mounts %+v", s.Mounts)
	}
	if s.Restart != "unless-stopped" || s.MemoryMB != 2048 || s.CPUs != 1.5 || len(s.Devices) != 1 || s.Devices[0] != "/dev/dri" {
		t.Errorf("limits %+v", s)
	}
	if s.NetworkMode != "nocap-jellyfin" || len(s.Networks) != 2 || s.Networks[0].Name != "nocap-jellyfin" || len(s.Networks[0].Aliases) != 1 || s.Networks[0].Aliases[0] != "app" ||
		s.Networks[1].Name != "lan" || s.Networks[1].IPv4 != "192.168.1.50" || len(s.Networks[1].Aliases) != 0 {
		t.Errorf("networks %+v", s.Networks)
	}
}

func TestCreateBodyKeepsTheRest(t *testing.T) {
	raw := fixture(t)
	s := specFrom(raw)
	s.Name = "jellyfin"
	s.Env = append(s.Env, EnvVar{"NEW", "1"})
	s.Mounts[0] = SpecMount{Type: "bind", Source: "/srv/nocapos/jellyfin-config", Target: "/config"}
	s.Ports = []PortMap{{Host: 8096, Container: 8096, Protocol: "tcp"}}
	s.MemoryMB, s.CPUs = 0, 0
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	b := createBody(raw, s)
	hc := b["HostConfig"].(map[string]any)

	// Kept as they were.
	if b["User"] != "1000:1000" || b["Healthcheck"] == nil || b["Entrypoint"] == nil {
		t.Errorf("config not kept: %v", b)
	}
	if hc["CapAdd"] == nil || hc["LogConfig"] == nil {
		t.Errorf("host config not kept: %v", hc)
	}
	labels := b["Labels"].(map[string]string)
	if labels["nocapos.app"] != "jellyfin" {
		t.Error("labels lost")
	}
	if labels["nocapos.ipv4.lan"] != "192.168.1.50" {
		t.Errorf("fixed address not recorded: %v", labels)
	}
	// Replaced.
	if _, ok := b["Hostname"]; ok {
		t.Error("the old generated hostname must not carry over")
	}
	if _, ok := hc["Binds"]; ok {
		t.Error("old -v binds must be replaced by Mounts")
	}
	mounts := hc["Mounts"].([]map[string]any)
	if len(mounts) != 2 || mounts[0]["Type"] != "bind" || mounts[0]["Source"] != "/srv/nocapos/jellyfin-config" {
		t.Errorf("mounts %v", mounts)
	}
	env := b["Env"].([]string)
	if env[len(env)-1] != "NEW=1" {
		t.Errorf("env %v", env)
	}
	pb := hc["PortBindings"].(map[string]any)
	if len(pb) != 1 || pb["8096/tcp"] == nil {
		t.Errorf("ports %v", pb)
	}
	if hc["Memory"].(int64) != 0 || hc["NanoCpus"].(int64) != 0 {
		t.Error("limits should be cleared")
	}
	ec := b["NetworkingConfig"].(map[string]any)["EndpointsConfig"].(map[string]any)
	if ec["nocap-jellyfin"] == nil || len(ec) != 1 {
		t.Errorf("primary network %v", ec)
	}
}

func TestSpecValidate(t *testing.T) {
	good := Spec{Name: "web", Image: "nginx:latest", Restart: "always", NetworkMode: "lan",
		Ports: []PortMap{{Host: 8080, Container: 80}}, Mounts: []SpecMount{{Type: "bind", Source: "/srv/www", Target: "/usr/share/nginx/html", ReadOnly: true}},
		Networks: []NetLink{{Name: "lan", IPv4: "192.168.1.60"}}}
	if err := good.Validate(); err != nil {
		t.Fatalf("good spec: %v", err)
	}
	if good.Ports[0].Protocol != "tcp" {
		t.Error("protocol default")
	}
	bad := []func(*Spec){
		func(s *Spec) { s.Name = "-bad" },
		func(s *Spec) { s.Image = "" },
		func(s *Spec) { s.Env = []EnvVar{{Key: "BAD KEY"}} },
		func(s *Spec) { s.Env = []EnvVar{{Key: "A"}, {Key: "A"}} },
		func(s *Spec) { s.Ports = []PortMap{{Host: 70000, Container: 80}} },
		func(s *Spec) { s.Ports = []PortMap{{Host: 80, Container: 80}, {Host: 80, Container: 81}} },
		func(s *Spec) { s.Ports = []PortMap{{Host: 80, Container: 80, Protocol: "sctp"}} },
		func(s *Spec) { s.Mounts = []SpecMount{{Type: "bind", Source: "relative/path", Target: "/x"}} },
		func(s *Spec) { s.Mounts = []SpecMount{{Type: "volume", Source: "ok", Target: "relative"}} },
		func(s *Spec) { s.Restart = "sometimes" },
		func(s *Spec) { s.Devices = []string{"/etc/passwd"} },
		func(s *Spec) { s.Networks = []NetLink{{Name: "lan", IPv4: "not-an-ip"}} },
		func(s *Spec) { s.NetworkMode = "host" }, // ports can't be published on host networking
	}
	for i, mod := range bad {
		s := good
		s.Ports = append([]PortMap(nil), good.Ports...)
		s.Mounts = append([]SpecMount(nil), good.Mounts...)
		s.Networks = append([]NetLink(nil), good.Networks...)
		mod(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("bad spec %d accepted: %+v", i, s)
		}
	}
	// A user network as the main one is added to the list.
	s := Spec{Name: "x", Image: "alpine", NetworkMode: "lan"}
	if err := s.Validate(); err != nil || len(s.Networks) != 1 || s.Networks[0].Name != "lan" {
		t.Errorf("network mode not added: %+v %v", s.Networks, err)
	}
}

func TestNewNetworkValidate(t *testing.T) {
	ok := []NewNetwork{
		{Name: "lan", Driver: "macvlan", Parent: "eth0", Subnet: "192.168.1.0/24", Gateway: "192.168.1.1", IPRange: "192.168.1.192/27"},
		{Name: "lan2", Driver: "ipvlan", Parent: "enp3s0", Subnet: "192.168.1.5/24", Mode: "l2"},
		{Name: "apps", Driver: "bridge"},
		{Name: "apps2", Driver: "bridge", Subnet: "172.30.0.0/16", Internal: true},
	}
	for _, n := range ok {
		if err := n.Validate(); err != nil {
			t.Errorf("%+v: %v", n, err)
		}
	}
	if n := ok[1]; n.Subnet != "192.168.1.0/24" {
		// Validate works on a copy here; check masking on a pointer.
		m := ok[1]
		_ = m.Validate()
		if m.Subnet != "192.168.1.0/24" {
			t.Errorf("subnet not masked: %s", m.Subnet)
		}
	}
	bad := []NewNetwork{
		{Name: "x", Driver: "overlay"},
		{Name: "x", Driver: "macvlan", Subnet: "192.168.1.0/24"}, // no parent
		{Name: "x", Driver: "macvlan", Parent: "eth0"},           // no subnet
		{Name: "x", Driver: "macvlan", Parent: "eth0; rm", Subnet: "10.0.0.0/8"},
		{Name: "x", Driver: "bridge", Subnet: "10.0.0.0/24", Gateway: "10.1.0.1"}, // gateway outside
		{Name: "x", Driver: "bridge", Subnet: "10.0.0.0/24", IPRange: "10.0.0.0/16"},
		{Name: "x", Driver: "ipvlan", Parent: "eth0", Subnet: "10.0.0.0/24", Mode: "l9"},
	}
	for _, n := range bad {
		if err := n.Validate(); err == nil {
			t.Errorf("%+v accepted", n)
		}
	}
}

func TestDevicesAndAliases(t *testing.T) {
	s := Spec{Devices: []string{"/dev/dri", "/dev/ttyUSB0:/dev/ttyACM0"}}
	d := s.devices()
	if d[0]["PathInContainer"] != "/dev/dri" || d[1]["PathOnHost"] != "/dev/ttyUSB0" || d[1]["PathInContainer"] != "/dev/ttyACM0" {
		t.Errorf("devices %v", d)
	}
	if !strings.Contains(inspectFixture, "4f1c2a9b7e3d") {
		t.Skip()
	}
}
