package api

import (
	"regexp"
	"strings"
)

// Progress of an update, read from what install.sh prints: each "==> ..."
// step it announces maps to a stage and a rough percentage. Percentages only
// ever go up, so the progress bar never jumps backwards.

type updateProgress struct {
	Stage       string // download | verify | packages | install | restart | failed ("" before the first step)
	Percent     int
	RollingBack bool   // the new version didn't start and install.sh is putting the old one back
	RolledBack  bool   // ... and it reported doing so
	Error       string // install.sh's last "error:" line
}

// updateSteps: install.sh's "==>" lines, in the order it prints them.
var updateSteps = []struct {
	prefix  string
	stage   string
	percent int
}{
	{"Updating NoCapOS from ", "download", 2}, // alfad's own first line
	{"Downloading NoCapOS for ", "download", 8},
	{"Checksum verified", "verify", 18},
	{"Installing NoCapOS ", "verify", 22},
	{"Upgrading the previous Alfa OS install", "install", 24},
	{"Installing Docker", "packages", 26},
	{"Installing host packages", "packages", 30},
	{"Minimal install", "packages", 52},
	{"Installing /", "install", 58}, // "Installing /usr/local/bin/nocapos"
	{"Kept the previous version", "install", 64},
	{"Waiting for NoCapOS", "restart", 72},
	{"Installing Brave", "restart", 86},
	{"NoCapOS is running.", "restart", 97},
}

// updateInstallPercent: from this step on, the old binary has been replaced.
const updateInstallPercent = 58

const updatePackagesMax = 54 // each host-package line nudges the bar, up to here

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func parseUpdateProgress(log string) updateProgress {
	var p updateProgress
	set := func(stage string, pct int) {
		if pct >= p.Percent {
			p.Percent, p.Stage = pct, stage
		}
	}
	for _, line := range strings.Split(stripANSI(log), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "==> "):
			msg := strings.TrimPrefix(line, "==> ")
			for _, st := range updateSteps {
				if strings.HasPrefix(msg, st.prefix) {
					set(st.stage, st.percent)
					break
				}
			}
		case strings.HasPrefix(line, "    ") && p.Stage == "packages":
			// "    sharing: samba smbclient ..." — one per feature
			if strings.Contains(line, ": ") && p.Percent+3 <= updatePackagesMax {
				set("packages", p.Percent+3)
			}
		case strings.HasPrefix(line, "warning: ") && strings.Contains(line, "did not start; putting back"):
			p.RollingBack = true
		case strings.HasPrefix(line, "error: "):
			p.Error = strings.TrimPrefix(line, "error: ")
			p.Stage = "failed"
			if strings.Contains(line, "was rolled back") {
				p.RolledBack = true
			}
		}
	}
	return p
}
