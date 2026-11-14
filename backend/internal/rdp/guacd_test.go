package rdp

import (
	"regexp"
	"testing"
)

func TestParseGuacdRef(t *testing.T) {
	cases := []struct{ in, image, tag string }{
		{"# note\n\nguacamole/guacd:1.6.0@sha256:abc\n", "guacamole/guacd", "1.6.0"},
		{"registry.local:5000/guacd:2.0\n", "registry.local:5000/guacd", "2.0"},
		{"registry.local:5000/guacd@sha256:abc\n", "registry.local:5000/guacd", "latest"},
	}
	for _, c := range cases {
		image, tag := parseGuacdRef(c.in)
		if image != c.image || tag != c.tag {
			t.Errorf("parseGuacdRef(%q) = %q, %q; want %q, %q", c.in, image, tag, c.image, c.tag)
		}
	}
}

// The embedded pin must name an image, a tag and a digest: the OS image
// build preloads exactly that digest.
func TestGuacdRefPinned(t *testing.T) {
	if guacdImage == "" || guacdTag == "" || guacdTag == "latest" {
		t.Fatalf("guacd.ref must pin a tag, got %q:%q", guacdImage, guacdTag)
	}
	if !regexp.MustCompile(`(?m)^[^#].*:[^/]+@sha256:[0-9a-f]{64}\s*$`).MatchString(guacdRef) {
		t.Fatal("guacd.ref must pin name:tag@sha256:<digest>")
	}
}
