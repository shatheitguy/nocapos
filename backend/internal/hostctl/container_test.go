package hostctl

import (
	"context"
	"runtime"
	"testing"

	"alfaos/alfad/internal/virt"
)

// In a system container without NetworkManager the host sets up the
// network: the switches say so instead of asking for NetworkManager.
func TestNetworkInSystemContainer(t *testing.T) {
	if runtime.GOOS != "linux" || hasNmcli() || InContainer() {
		t.Skip("needs Linux without NetworkManager, outside an app container")
	}
	old := systemContainer
	t.Cleanup(func() { systemContainer = old })

	systemContainer = func() bool { return true }
	st := Network(context.Background())
	if st.Manager != "none" || st.WiFi.Supported || st.WiFi.Reason != virt.NetworkReason || st.Networking.Reason != virt.NetworkReason {
		t.Fatalf("in a container: %+v", st)
	}

	systemContainer = func() bool { return false }
	if st := Network(context.Background()); st.WiFi.Reason == virt.NetworkReason {
		t.Fatalf("on a machine: %+v", st)
	}
}
