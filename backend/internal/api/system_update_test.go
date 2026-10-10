package api

import "testing"

func TestVersionCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v0.3.0", "0.2.9", 1}, {"0.2.0", "v0.2.0", 0}, {"v1.0", "1.0.1", -1}, {"v0.10.0", "v0.9.9", 1}, {"v0.3.0-rc1", "0.3.0", 0},
	} {
		a, okA := parseVersion(c.a)
		b, okB := parseVersion(c.b)
		if !okA || !okB {
			t.Fatalf("parse %q %q", c.a, c.b)
		}
		if got := compareVersion(a, b); got != c.want {
			t.Errorf("compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"dev", "", "1.2.3.4", "v1.x"} {
		if _, ok := parseVersion(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}
