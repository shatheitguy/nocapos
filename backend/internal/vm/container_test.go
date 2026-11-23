package vm

import (
	"context"
	"testing"

	"alfaos/alfad/internal/virt"
)

// noKVM is a host with the virtualization tools but no /dev/kvm (a system
// container that wasn't given it).
type noKVM struct{ *Demo }

func (n noKVM) Tools(ctx context.Context) Tools {
	t := n.Demo.Tools(ctx)
	t.KVM = false
	return t
}

func containerManager(t *testing.T, be Backend, kind string) *Manager {
	return NewManager(Options{Backend: be, Dir: t.TempDir(), Log: quiet(),
		Native: func() bool { return true }, CanInstall: func() bool { return true },
		Container: func() string { return kind }})
}

func TestSystemContainerWithoutKVM(t *testing.T) {
	m := containerManager(t, noKVM{NewDemo(t.TempDir())}, "lxc")
	st := m.Status(context.Background())
	if st.Available || st.Container != "lxc" || st.Hint != virt.VMReason || st.CanInstall {
		t.Fatalf("status %+v", st)
	}
	if err := m.checkReady(context.Background()); err == nil || err.Error() != virt.VMReason {
		t.Fatalf("ready: %v", err)
	}
}

func TestSystemContainerWithKVMPassedThrough(t *testing.T) {
	m := containerManager(t, NewDemo(t.TempDir()), "lxc")
	st := m.Status(context.Background())
	if !st.Available || st.Container != "lxc" || st.Hint != "" {
		t.Fatalf("status %+v", st)
	}
}

func TestMachineWithoutKVMKeepsFirmwareHint(t *testing.T) {
	m := containerManager(t, noKVM{NewDemo(t.TempDir())}, "")
	if st := m.Status(context.Background()); st.Hint != hintKVM || st.Container != "" {
		t.Fatalf("status %+v", st)
	}
}
