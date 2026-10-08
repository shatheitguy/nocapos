package hardware

import (
	"math"
	"strings"
	"testing"
)

func TestParseCPUTimes(t *testing.T) {
	stat := `cpu  100 0 100 700 100 0 0 0 50 0
cpu0 50 0 50 350 50 0 0 0 25 0
cpu1 50 0 50 350 50 0 0 0 25 0
intr 12345
`
	total, cores, err := parseCPUTimes(strings.NewReader(stat))
	if err != nil {
		t.Fatal(err)
	}
	if len(cores) != 2 {
		t.Fatalf("cores = %d, want 2", len(cores))
	}
	// guest (50) must not be double counted.
	if total.total != 1000 || total.idle != 800 {
		t.Fatalf("total = %+v, want total=1000 idle=800", total)
	}
	next := cpuTimes{total: 2000, idle: 1300}
	if got := next.usage(total); math.Abs(got-50) > 1e-9 {
		t.Fatalf("usage = %v, want 50", got)
	}
}

func TestParseNetDev(t *testing.T) {
	dev := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  1000      10    0    0    0     0          0         0     1000      10    0    0    0     0       0          0
  eth0: 123456    100    0    0    0     0          0         0   654321     200    0    0    0     0       0          0
`
	got := parseNetDev([]byte(dev))
	if c := got["eth0"]; c.rx != 123456 || c.tx != 654321 {
		t.Fatalf("eth0 = %+v", c)
	}
	if !isVirtualInterface("lo") || !isVirtualInterface("veth12ab") || isVirtualInterface("eth0") {
		t.Fatal("virtual interface filter is wrong")
	}
}

func TestBusKey(t *testing.T) {
	if busKey("00000000:01:00.0") != busKey("0000:01:00.0") {
		t.Fatal("nvidia-smi and sysfs PCI addresses should normalize to the same key")
	}
}

func TestDevfreqLoadParsing(t *testing.T) {
	if v := optFloat("37", 1); v == nil || *v != 37 {
		t.Fatal("plain value")
	}
	if optFloat("[N/A]", 1) != nil || optFloat("[Not Supported]", 1) != nil {
		t.Fatal("nvidia-smi placeholders must be nil")
	}
}
