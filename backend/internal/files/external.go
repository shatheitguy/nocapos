package files

import (
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// External storage: USB drives and other disks the OS mounted under /media
// or /run/media (desktop automounting) or by hand under /mnt. Files only
// lists them; mounting and ejecting stay with the OS.

type ExternalMount struct {
	Name   string `json:"name"`
	Path   string `json:"path"` // absolute mount point
	Device string `json:"device"`
	FSType string `json:"fs_type"`
}

// ExternalMounts reads /proc/self/mountinfo (Linux); elsewhere it is empty.
// skip is a folder whose mounts are left out (network drives).
func ExternalMounts(skip string) []ExternalMount {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	return parseMountinfo(string(b), skip)
}

var pseudoFS = map[string]bool{"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true, "autofs": true, "proc": true, "sysfs": true,
	"cifs": true, "smb3": true, "nfs": true, "nfs4": true, "fuse.rclone": true, "fuse.sshfs": true}

func parseMountinfo(text, skip string) []ExternalMount {
	var out []ExternalMount
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		pre, post, ok := strings.Cut(line, " - ")
		f := strings.Fields(pre)
		g := strings.Fields(post)
		if !ok || len(f) < 5 || len(g) < 2 {
			continue
		}
		mp := unescapeOctal(f[4])
		fstype, dev := g[0], unescapeOctal(g[1])
		if pseudoFS[fstype] || !strings.HasPrefix(dev, "/dev/") || strings.HasPrefix(dev, "/dev/loop") {
			continue
		}
		if !(strings.HasPrefix(mp, "/media/") || strings.HasPrefix(mp, "/run/media/") || strings.HasPrefix(mp, "/mnt/")) {
			continue
		}
		if skip != "" && (mp == skip || strings.HasPrefix(mp, strings.TrimSuffix(skip, "/")+"/")) {
			continue
		}
		if seen[mp] {
			continue
		}
		seen[mp] = true
		out = append(out, ExternalMount{Name: path.Base(mp), Path: mp, Device: dev, FSType: fstype})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// unescapeOctal decodes \040-style escapes in mountinfo fields.
func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if c, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(c))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
