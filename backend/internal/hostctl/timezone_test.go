package hostctl

import "testing"

func TestValidTimeZone(t *testing.T) {
	for _, z := range []string{"UTC", "Etc/UTC", "Europe/Berlin", "America/Argentina/Buenos_Aires", "Asia/Kolkata", " Africa/Lagos "} {
		if _, err := ValidTimeZone(z); err != nil {
			t.Errorf("%q: %v", z, err)
		}
	}
	for _, z := range []string{"", "Europe/../../etc/passwd", "/etc/passwd", "Europe/Nowhere", "Europe Berlin", "Europe/Berlin;reboot", "-Europe/Berlin"} {
		if _, err := ValidTimeZone(z); err == nil {
			t.Errorf("%q accepted", z)
		}
	}
}
