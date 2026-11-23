package storage

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"alfaos/alfad/internal/virt"
)

// In a system container the host owns the physical disks: Storage still
// reports, but explains instead of creating pools, formatting, spinning
// drives down or mirroring the boot drive.
func TestSystemContainerKeepsDisksOnHost(t *testing.T) {
	m, fr, _, _ := newTestManager(t)
	m.ctr = func() string { return "lxc" }
	ctx := context.Background()

	st := m.Status(ctx)
	if !st.Supported || st.Container != "lxc" || st.ContainerNote != virt.DisksReason {
		t.Fatalf("status: %+v", st)
	}
	if st.ZFS.CanInstall || st.Smart.CanInstall {
		t.Fatalf("nothing to install in a container: %+v", st)
	}

	conflict := func(what string, err error) {
		t.Helper()
		var e *Error
		if !errors.As(err, &e) || e.Status != http.StatusConflict || e.Msg != virt.DisksReason {
			t.Fatalf("%s: want the container explanation, got %v", what, err)
		}
	}
	fr.mu.Lock()
	fr.calls = nil
	fr.mu.Unlock()

	_, err := m.CreatePool(ctx, CreatePoolRequest{Name: "tank2", Layout: "mirror", Disks: []string{"sdb", "sdc"}, Confirm: "ERASE"}, nil)
	conflict("create pool", err)
	_, err = m.PlanPool(ctx, PlanRequest{Layout: "mirror", Disks: []string{"sdb", "sdc"}})
	conflict("plan pool", err)
	_, err = m.Format(ctx, "sdb", FormatRequest{Label: "data", Confirm: "ERASE"}, nil)
	conflict("format", err)
	conflict("install zfs", m.Install(ctx, "zfs"))
	conflict("spin-down", m.SetSpindown(ctx, "sdb", 20))
	_, err = m.MirrorBoot(ctx, BootMirrorRequest{Disk: "sdb", Confirm: "sdb"}, nil)
	conflict("boot mirror", err)

	fr.mu.Lock()
	calls := len(fr.calls)
	fr.mu.Unlock()
	if calls != 0 {
		t.Fatalf("ran %d commands for refused changes", calls)
	}

	if l, err := m.Spindown(ctx); err != nil || len(l) != 0 {
		t.Fatalf("spin-down list: %v %v", l, err)
	}
	b, err := m.Boot(ctx)
	if err != nil || b.CanMirror || b.Why != "container" || b.WhyText != virt.BootMirrorReason {
		t.Fatalf("boot drive: %+v %v", b, err)
	}
}

func TestNoContainerNoteOnAMachine(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	if st := m.Status(context.Background()); st.Container != "" || st.ContainerNote != "" {
		t.Fatalf("status: %+v", st)
	}
}
