// Package storage manages local disks: health (SMART), ZFS pools (RAID),
// datasets and snapshots, and single disks formatted as storage. It touches
// real disks, so every safety rule lives here, not in the UI: disks are
// re-checked at action time, destructive actions need a typed confirmation,
// names are validated and commands run as argument lists, never a shell.
package storage

import (
	"fmt"
	"net/http"
	"time"
)

// Usage says what a disk is used for.
const (
	UsageSystem  = "system"
	UsagePool    = "pool"
	UsageMounted = "mounted"
	UsageMD      = "md"
	UsageLVM     = "lvm"
	UsageStorage = "storage"
	UsageFree    = "free"
)

// Where NoCapOS mounts what it creates.
const (
	PoolBase = "/srv/nocapos/pools"
	DiskBase = "/srv/nocapos/disks"
)

const minDiskSize = 1 << 30 // 1 GiB

type Partition struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Size        int64    `json:"size"`
	FSType      string   `json:"fstype,omitempty"`
	Label       string   `json:"label,omitempty"`
	UUID        string   `json:"uuid,omitempty"`
	Mountpoints []string `json:"mountpoints"`
}

type Disk struct {
	Name       string        `json:"name"`
	Path       string        `json:"path"`
	ByID       string        `json:"by_id,omitempty"`
	Size       int64         `json:"size"`
	Model      string        `json:"model,omitempty"`
	Serial     string        `json:"serial,omitempty"`
	Transport  string        `json:"transport,omitempty"`
	Rotational bool          `json:"rotational"`
	Removable  bool          `json:"removable"`
	Partitions []Partition   `json:"partitions"`
	Usage      string        `json:"usage"`
	Available  bool          `json:"available"`
	Reason     string        `json:"reason,omitempty"`
	Smart      *SmartSummary `json:"smart,omitempty"`

	hidden bool // loop/ram/zram: never listed
}

type SmartSummary struct {
	Available     bool   `json:"available"`
	Passed        *bool  `json:"passed,omitempty"`
	TemperatureC  *int64 `json:"temperature_c,omitempty"`
	PowerOnHours  *int64 `json:"power_on_hours,omitempty"`
	Reallocated   *int64 `json:"reallocated,omitempty"`
	Pending       *int64 `json:"pending,omitempty"`
	PercentUsed   *int64 `json:"percent_used,omitempty"`
	MediaErrors   *int64 `json:"media_errors,omitempty"`
	TestRunning   bool   `json:"test_running,omitempty"`
	TestRemaining *int64 `json:"test_remaining,omitempty"` // percent left
	Asleep        bool   `json:"asleep,omitempty"`         // in standby; not woken to read SMART
}

type SmartAttribute struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Value   int64  `json:"value"`
	Worst   int64  `json:"worst"`
	Thresh  int64  `json:"thresh"`
	Raw     string `json:"raw"`
	Failing bool   `json:"failing"`
}

type NVMeHealth struct {
	CriticalWarning  int64 `json:"critical_warning"`
	AvailableSpare   int64 `json:"available_spare"`
	SpareThreshold   int64 `json:"available_spare_threshold"`
	PercentageUsed   int64 `json:"percentage_used"`
	DataUnitsRead    int64 `json:"data_units_read"`
	DataUnitsWritten int64 `json:"data_units_written"`
	PowerCycles      int64 `json:"power_cycles"`
	UnsafeShutdowns  int64 `json:"unsafe_shutdowns"`
	MediaErrors      int64 `json:"media_errors"`
	ErrorLogEntries  int64 `json:"num_err_log_entries"`
}

type SelfTest struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Hours  int64  `json:"hours"`
}

type SmartReport struct {
	Summary    SmartSummary     `json:"summary"`
	Attributes []SmartAttribute `json:"attributes"`
	NVMe       *NVMeHealth      `json:"nvme,omitempty"`
	SelfTests  []SelfTest       `json:"self_tests"`
}

type Vdev struct {
	Name           string `json:"name"`
	Type           string `json:"type"` // mirror raidz1 raidz2 raidz3 draid disk file replacing spare logs cache spares special dedup
	Health         string `json:"health"`
	ReadErrors     int64  `json:"read_errors"`
	WriteErrors    int64  `json:"write_errors"`
	ChecksumErrors int64  `json:"checksum_errors"`
	Disk           string `json:"disk,omitempty"` // kernel name of the disk behind a leaf
	Path           string `json:"path,omitempty"`
	GUID           string `json:"guid,omitempty"`
	Was            string `json:"was,omitempty"`  // former path of a missing device
	Note           string `json:"note,omitempty"` // e.g. "resilvering"
	Children       []Vdev `json:"children"`
}

type Scan struct {
	Function   string   `json:"function,omitempty"` // scrub | resilver
	State      string   `json:"state"`              // none | scanning | paused | finished | canceled
	Percent    *float64 `json:"percent,omitempty"`
	EtaSeconds *int64   `json:"eta_seconds,omitempty"`
	Errors     *int64   `json:"errors,omitempty"`
	Finished   string   `json:"finished,omitempty"` // RFC 3339
}

type Pool struct {
	Name          string `json:"name"`
	Health        string `json:"health"`
	Size          int64  `json:"size"`
	Allocated     int64  `json:"allocated"`
	Free          int64  `json:"free"`
	Fragmentation int64  `json:"fragmentation"`
	Capacity      int64  `json:"capacity"`
	Mountpoint    string `json:"mountpoint"`
	Layout        string `json:"layout"`
	Vdevs         []Vdev `json:"vdevs"`
	Scan          Scan   `json:"scan"`
	Errors        string `json:"errors,omitempty"`
	Status        string `json:"status,omitempty"` // zpool's explanation when not healthy
	InFiles       bool   `json:"in_files"`
}

type ImportablePool struct {
	Name   string   `json:"name"`
	ID     string   `json:"id"`
	Health string   `json:"health"`
	Disks  []string `json:"disks"`
}

type Dataset struct {
	Name          string  `json:"name"`
	Used          int64   `json:"used"`
	Available     int64   `json:"available"`
	Referenced    int64   `json:"referenced"`
	Mountpoint    string  `json:"mountpoint"`
	Compression   string  `json:"compression"`
	CompressRatio float64 `json:"compressratio"`
	Quota         int64   `json:"quota"`
}

type Snapshot struct {
	Name       string    `json:"name"`
	Created    time.Time `json:"created"`
	Used       int64     `json:"used"`
	Referenced int64     `json:"referenced"`
}

// Tools says which host tools are present.
type Tools struct {
	ZFSInstalled   bool
	ZFSModule      bool
	ZFSVersion     string
	SmartInstalled bool
	Lsblk          bool
}

// Facts is a raw scan of the machine's block devices.
type Facts struct {
	Devices []BlockDev
	Links   map[string]string // /dev/disk/by-*/X -> kernel name
	Swaps   []string          // devices from /proc/swaps
}

// Job is a long-running action the UI polls.
type Job struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Target   string     `json:"target"`
	State    string     `json:"state"` // running | done | failed
	Error    string     `json:"error,omitempty"`
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
}

// Alert is a health problem worth telling the admin about.
type Alert struct {
	Key   string `json:"key"`
	Level string `json:"level"` // warning | error
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Error is a problem with a request, with the HTTP status to answer with.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func bad(format string, a ...any) error {
	return &Error{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}
func notFound(format string, a ...any) error {
	return &Error{http.StatusNotFound, fmt.Sprintf(format, a...)}
}
func busy(format string, a ...any) error {
	return &Error{http.StatusConflict, fmt.Sprintf(format, a...)}
}

// ErrUnsupported means storage management can't run on this server.
var ErrUnsupported = &Error{http.StatusServiceUnavailable, "storage management isn't available on this server"}

// confirmError is returned when the typed confirmation is wrong or missing.
func confirmError(want string) error {
	return &Error{http.StatusBadRequest, fmt.Sprintf("type %s to confirm", want)}
}
