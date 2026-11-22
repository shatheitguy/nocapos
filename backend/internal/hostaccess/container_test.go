package hostaccess

import (
	"context"
	"errors"
	"testing"

	"alfaos/alfad/internal/virt"
)

// In a system container there is no screen and no boot menu: NoCap Desktop
// explains itself and the switches stay off, even if a desktop was set up.
func TestSystemContainerHasNoScreen(t *testing.T) {
	f := newFakeHost()
	f.files[DesktopConfPath] = "FULL=1\nDM_UNIT=greetd.service\n"
	f.files[BootToolPath] = "#!/bin/bash\n"
	d := f.desktop()
	d.Container = func() string { return "lxc" }
	ctx := context.Background()

	st := d.State(ctx)
	if st.Available || st.Reason != virt.DesktopReason || len(st.Modes) != 0 {
		t.Fatalf("state: %+v", st)
	}
	f.calls = nil
	if err := d.SetMode(ctx, ModeFull); !errors.Is(err, ErrDesktopUnavailable) {
		t.Fatalf("set mode: %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("ran %v", f.calls)
	}
	if b := d.Boot(); b.Supported {
		t.Fatalf("boot: %+v", b)
	}

	d.Container = func() string { return "" }
	if b := d.Boot(); !b.Supported {
		t.Fatalf("boot outside a container: %+v", b)
	}
}
