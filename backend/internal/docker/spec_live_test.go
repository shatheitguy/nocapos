package docker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRecreateLive runs against a real engine: set NOCAP_TEST_DOCKER (e.g.
// unix:///tmp/x/podman.sock) and NOCAP_TEST_IMAGE (a local image whose
// default command keeps running).
func TestRecreateLive(t *testing.T) {
	host, image := os.Getenv("NOCAP_TEST_DOCKER"), os.Getenv("NOCAP_TEST_IMAGE")
	if host == "" || image == "" {
		t.Skip("set NOCAP_TEST_DOCKER and NOCAP_TEST_IMAGE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := New(host)
	if err != nil {
		t.Fatal(err)
	}
	bind := t.TempDir()
	const name = "nocap-edit-test"
	_ = c.removeContainer(ctx, name, true)
	_ = c.RemoveNetwork(ctx, "nocap-edit-net")
	defer func() {
		_ = c.removeContainer(context.Background(), name, true)
		_ = c.RemoveNetwork(context.Background(), "nocap-edit-net")
		_ = c.RemoveVolume(context.Background(), "nocap-edit-data")
	}()

	// A container made from a spec.
	spec := Spec{Name: name, Image: image, Restart: "unless-stopped", NetworkMode: "bridge",
		Env:    []EnvVar{{Key: "GREETING", Value: "hello"}},
		Ports:  []PortMap{{Host: 18931, Container: 8080, Protocol: "tcp"}},
		Mounts: []SpecMount{{Type: "volume", Source: "nocap-edit-data", Target: "/data"}}}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	id, err := c.CreateContainer(ctx, spec.Name, spec.CreateConfig(map[string]string{"nocapos.custom": "true"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StartContainer(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetSpec(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Env) == 0 || len(got.Mounts) != 1 || got.Mounts[0].Source != "nocap-edit-data" || len(got.Ports) != 1 || got.Ports[0].Host != 18931 || got.Restart != "unless-stopped" {
		t.Fatalf("read back: %+v", got)
	}

	// Edit: new variable, a folder from the server, another port, and a
	// network with a fixed address.
	if err := c.CreateNetworkFrom(ctx, NewNetwork{Name: "nocap-edit-net", Driver: "bridge", Subnet: "10.93.0.0/24", Gateway: "10.93.0.1"}, nil); err != nil {
		t.Fatal(err)
	}
	got.Env = append(got.Env, EnvVar{Key: "EXTRA", Value: "yes"})
	got.Mounts = append(got.Mounts, SpecMount{Type: "bind", Source: bind, Target: "/host", ReadOnly: true})
	got.Ports = []PortMap{{Host: 18932, Container: 8080, Protocol: "tcp"}}
	got.NetworkMode = "nocap-edit-net"
	got.Networks = []NetLink{{Name: "nocap-edit-net", IPv4: "10.93.0.50"}}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	newID, err := c.Recreate(ctx, name, got, nil)
	if err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if newID == id {
		t.Fatal("expected a new container")
	}
	after, err := c.GetSpec(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, e := range after.Env {
		env[e.Key] = e.Value
	}
	if env["EXTRA"] != "yes" || env["GREETING"] != "hello" {
		t.Errorf("env after edit: %v", after.Env)
	}
	if len(after.Mounts) != 2 || len(after.Ports) != 1 || after.Ports[0].Host != 18932 {
		t.Errorf("mounts/ports after edit: %+v %+v", after.Mounts, after.Ports)
	}
	if len(after.Networks) != 1 || after.Networks[0].Name != "nocap-edit-net" || after.Networks[0].IPv4 != "10.93.0.50" {
		t.Errorf("network after edit: %+v (mode %s)", after.Networks, after.NetworkMode)
	}
	if _, _, ok, _ := c.FindContainer(ctx, id); ok {
		t.Error("the old container is still there")
	}
	if all, _ := c.ListContainers(ctx, true); func() bool {
		for _, ct := range all {
			if strings.Contains(ct.Name, "-old-") {
				return true
			}
		}
		return false
	}() {
		t.Error("a set-aside container was left behind")
	}

	// A change that can't work (an address outside the network) leaves the
	// container exactly as it was, running.
	bad := after
	bad.Networks = []NetLink{{Name: "nocap-edit-net", IPv4: "10.99.0.9"}}
	if _, err := c.Recreate(ctx, name, bad, nil); err == nil {
		t.Fatal("an impossible edit should fail")
	}
	cur, running, ok, err := c.FindContainer(ctx, name)
	if err != nil || !ok || !running || cur != newID {
		t.Fatalf("after a failed edit: id %s running %v ok %v err %v (want %s running)", cur, running, ok, err, newID)
	}
	restored, _ := c.GetSpec(ctx, name)
	if len(restored.Networks) != 1 || restored.Networks[0].IPv4 != "10.93.0.50" {
		t.Errorf("restored container changed: %+v", restored.Networks)
	}
}
