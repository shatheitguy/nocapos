package storage

import (
	"path"
	"regexp"
	"strings"
)

var (
	poolNameRe  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.:-]{0,49}$`)
	segmentRe   = regexp.MustCompile(`^[a-zA-Z0-9_.:-]+$`)
	labelRe     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,15}$`)
	kernelRe    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)
	poolIDRe    = regexp.MustCompile(`^[0-9]{1,20}$`)
	reservedRe  = regexp.MustCompile(`^c[0-9]`)
	reservedSet = map[string]bool{"mirror": true, "raidz": true, "raidz1": true, "raidz2": true, "raidz3": true,
		"draid": true, "spare": true, "log": true, "cache": true, "special": true}
)

// ValidPoolName checks a ZFS pool name against our (stricter than ZFS) rules.
func ValidPoolName(name string) error {
	if !poolNameRe.MatchString(name) {
		return bad("pool names start with a letter and use only letters, numbers and _ . : - (up to 50 characters)")
	}
	if reservedSet[strings.ToLower(name)] || strings.HasPrefix(strings.ToLower(name), "raidz") ||
		strings.HasPrefix(strings.ToLower(name), "draid") || reservedRe.MatchString(name) {
		return bad("%q is a reserved name; pick another", name)
	}
	return nil
}

// ValidDatasetName checks pool/child[/child...] (max depth 5 below the pool).
// The pool root itself is a valid dataset name for listing, not for create/delete.
func ValidDatasetName(name string) error {
	parts := strings.Split(name, "/")
	if len(parts) > 6 {
		return bad("datasets can be nested at most 5 levels deep")
	}
	if err := ValidPoolName(parts[0]); err != nil {
		return bad("dataset names start with the pool name, e.g. tank/media")
	}
	for _, p := range parts[1:] {
		if !segmentRe.MatchString(p) || p == "." || p == ".." || len(p) > 64 {
			return bad("dataset names use only letters, numbers and _ . : - between the slashes")
		}
	}
	return nil
}

// ValidChildDataset is a dataset below the pool root (what users create or delete).
func ValidChildDataset(name string) error {
	if err := ValidDatasetName(name); err != nil {
		return err
	}
	if !strings.Contains(name, "/") {
		return bad("that's the pool itself; pick a dataset inside it, e.g. %s/media", name)
	}
	return nil
}

// ValidSnapshotName checks dataset@snap. It must contain exactly one "@", so
// a snapshot delete can never be turned into a dataset delete.
func ValidSnapshotName(full string) error {
	ds, snap, ok := strings.Cut(full, "@")
	if !ok || strings.Contains(snap, "@") {
		return bad("snapshot names look like tank/media@before-update")
	}
	if err := ValidDatasetName(ds); err != nil {
		return err
	}
	return validSnapLabel(snap)
}

func validSnapLabel(snap string) error {
	if !segmentRe.MatchString(snap) || len(snap) > 64 {
		return bad("snapshot names use only letters, numbers and _ . : - (up to 64 characters)")
	}
	return nil
}

// ValidLabel checks a disk label (ext4 allows 16 bytes; also the folder name).
func ValidLabel(l string) error {
	if !labelRe.MatchString(l) {
		return bad("labels start with a letter or number and use only letters, numbers, _ and - (up to 16 characters)")
	}
	return nil
}

func validKernelName(n string) error {
	if !kernelRe.MatchString(n) {
		return bad("unknown disk %q", n)
	}
	return nil
}

func validCompression(c string, allowInherit bool) error {
	switch c {
	case "lz4", "zstd", "off":
		return nil
	case "inherit":
		if allowInherit {
			return nil
		}
	}
	return bad("compression must be lz4, zstd or off")
}

// Layouts and their minimum disk counts.
var minDisks = map[string]int{"stripe": 1, "mirror": 2, "raidz1": 3, "raidz2": 4, "raidz3": 5}

func validLayout(layout string, n int) error {
	min, ok := minDisks[layout]
	if !ok {
		return bad("layout must be stripe, mirror, raidz1, raidz2 or raidz3")
	}
	if n < min {
		return bad("%s needs at least %d disks", layoutTitle(layout), min)
	}
	return nil
}

func layoutTitle(l string) string {
	switch l {
	case "stripe":
		return "Stripe"
	case "mirror":
		return "Mirror"
	}
	return strings.ToUpper(l[:1]) + strings.ToUpper(l[1:5]) + l[5:]
}

// poolMountpoint returns the mountpoint for a new pool: PoolBase/<name>
// unless a custom one inside PoolBase is given.
func poolMountpoint(name, custom string) (string, error) {
	if custom == "" {
		return PoolBase + "/" + name, nil
	}
	clean := path.Clean(custom)
	if clean != custom || !strings.HasPrefix(clean, PoolBase+"/") || strings.ContainsAny(clean, " \t\n,") {
		return "", bad("the mountpoint must be a folder inside %s", PoolBase)
	}
	return clean, nil
}

// checkConfirm compares the typed confirmation exactly.
func checkConfirm(got, want string) error {
	if got != want {
		return confirmError(want)
	}
	return nil
}

// within reports whether p is dir or inside it (clean absolute slash paths).
func within(p, dir string) bool {
	if dir == "" || p == "" {
		return false
	}
	p, dir = path.Clean(p), path.Clean(dir)
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}
