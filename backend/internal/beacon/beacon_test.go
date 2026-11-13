package beacon

import "testing"

func TestServiceUUID(t *testing.T) {
	if got := ServiceUUID(true); got != "6e6f6361-7001-4e0c-a9b1-0d15c0f3b201" {
		t.Errorf("new: %s", got)
	}
	if got := ServiceUUID(false); got != "6e6f6361-7001-4e0c-a9b1-0d15c0f3b200" {
		t.Errorf("done: %s", got)
	}
}

func TestShortName(t *testing.T) {
	for in, want := range map[string]string{
		"media-box":           "media-box",
		"media-box.local":     "media-box",
		"a-very-long-hostname": "a-very-lon",
		"":                    "NoCap",
		"..":                  "NoCap",
		"wohnzimmer_pc":       "wohnzimmer",
		"Küche":               "Kche",
	} {
		if got := ShortName(in); got != want {
			t.Errorf("ShortName(%q) = %q, want %q", in, got, want)
		}
	}
}
