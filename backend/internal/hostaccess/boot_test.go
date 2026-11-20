package hostaccess

import (
	"context"
	"errors"
	"testing"
)

func TestBootDetails(t *testing.T) {
	f := newFakeHost()
	d := f.desktop()
	// Not installed yet (an older install): no switch.
	if st := d.Boot(); st.Supported || st.Details {
		t.Fatalf("without nocap-boot: %+v", st)
	}
	if err := d.SetBootDetails(context.Background(), true); !errors.Is(err, ErrBootUnavailable) {
		t.Fatalf("err = %v", err)
	}

	f.files[BootToolPath] = "#!/bin/bash\n"
	if st := d.Boot(); !st.Supported || st.Details {
		t.Fatalf("silent by default: %+v", st)
	}
	if err := d.SetBootDetails(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if last := f.calls[len(f.calls)-1]; last != BootToolPath+" details on" {
		t.Fatalf("ran %q", last)
	}
	f.files[BootDetailsPath] = "on\n"
	if !d.Boot().Details {
		t.Fatal("details on not read")
	}
	if err := d.SetBootDetails(context.Background(), false); err != nil || f.calls[len(f.calls)-1] != BootToolPath+" details off" {
		t.Fatalf("off: %v %q", err, f.calls[len(f.calls)-1])
	}

	// nocap-boot's own message comes back when GRUB couldn't be rebuilt.
	f.failRun["nocap-boot details on"] = true
	err := d.SetBootDetails(context.Background(), true)
	if err == nil || err.Error() == "" {
		t.Fatal("failure not reported")
	}

	// A canceled request still lets nocap-boot finish.
	f.failRun = map[string]bool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.SetBootDetails(ctx, true); err != nil {
		t.Fatal(err)
	}

	d.Native = false
	if d.Boot().Supported {
		t.Fatal("supported in a container")
	}
}
