package storage

import (
	"context"
	"time"
)

// Extras is the second half of a Backend: the boot drive, cache drives,
// spin-down, scheduled snapshots, encrypted folders and the flexible array.
// Host and Demo both implement it; the Manager checks every request first.
type Extras interface {
	BootFacts(ctx context.Context) (*BootFacts, error)
	MirrorBoot(ctx context.Context, p BootMirrorPlan) error

	AddSupport(ctx context.Context, pool, kind string, devs []string) error
	RemoveSupport(ctx context.Context, pool, dev string) error
	Expand(ctx context.Context, pool string, leaves []string) error
	ARC(ctx context.Context) (*ARCStats, error)

	ApplySpindown(ctx context.Context, rules []SpindownRule) error

	Clone(ctx context.Context, snap, target string) error
	BrowseSnapshot(ctx context.Context, dir string) ([]SnapEntry, error)

	Encryption(ctx context.Context, pool string) ([]CryptInfo, error)
	CreateEncrypted(ctx context.Context, name, passphrase string, quota *int64) error
	Unlock(ctx context.Context, name, passphrase string) error
	Lock(ctx context.Context, name string) error

	CreateFlexArray(ctx context.Context, p FlexPlan, devs []string) error
}

// BootFacts is the raw view of the system drive.
type BootFacts struct {
	Source string `json:"source"` // what / is mounted from: /dev/sda2 or rpool/ROOT/debian
	FSType string `json:"fstype"`
	Size   int64  `json:"size"`
	Used   int64  `json:"used"`
	Avail  int64  `json:"avail"`
	EFI    bool   `json:"efi"`
	ESP    string `json:"esp,omitempty"` // /boot/efi's partition
	Model  string `json:"model,omitempty"`
	Arch   string `json:"arch,omitempty"`
}

// ARCStats is ZFS's RAM read cache (and the optional speed-boost drive).
type ARCStats struct {
	Size     int64   `json:"size"`
	Target   int64   `json:"target"`
	Max      int64   `json:"max"`
	Hits     int64   `json:"hits"`
	Misses   int64   `json:"misses"`
	HitRate  float64 `json:"hit_rate"` // 0..100
	L2Size   int64   `json:"l2_size"`
	L2Hits   int64   `json:"l2_hits"`
	L2Misses int64   `json:"l2_misses"`
}

// SpindownRule sleeps a hard drive after Minutes idle (0 = never).
type SpindownRule struct {
	ByID    string `json:"by_id"`
	Minutes int    `json:"minutes"`
}

// SnapEntry is one item inside a snapshot.
type SnapEntry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// CryptInfo is the encryption state of one dataset.
type CryptInfo struct {
	Name       string `json:"name"`
	Encrypted  bool   `json:"encrypted"`
	Root       string `json:"root,omitempty"` // encryption root
	Unlocked   bool   `json:"unlocked"`
	Mounted    bool   `json:"mounted"`
	AutoLock   int    `json:"auto_lock_minutes,omitempty"`
	UnlockedAt string `json:"unlocked_at,omitempty"`
}
