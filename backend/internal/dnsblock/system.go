package dnsblock

import (
	"encoding/json"
	"os"
	"strings"
)

func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// nameservers reads the "nameserver" lines of a resolv.conf.
func nameservers(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nameserver" {
			out = append(out, f[1])
		}
	}
	return out
}

func readNameservers(paths ...string) []string {
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			if ns := nameservers(string(b)); len(ns) > 0 {
				return ns
			}
		}
	}
	return nil
}
