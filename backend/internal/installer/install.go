package installer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/osimage"
)

// Request is everything the installer's screens asked.
type Request struct {
	Disk     string `json:"disk"`
	Encrypt  bool   `json:"encrypt"`
	UseTPM   bool   `json:"use_tpm"` // unlock with this PC's security chip
	Passcode string `json:"passphrase,omitempty"`
	Account  struct {
		FullName string `json:"full_name"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"account"`
	Hostname string `json:"hostname"`
	TimeZone string `json:"timezone"`
	Keyboard string `json:"keyboard"`
	Variant  string `json:"keyboard_variant"`
	Language string `json:"language"`
	Usage    string `json:"usage"`
}

// Steps of an install, as the screen shows them.
const (
	StepPrepare = "prepare" // erase and lay out the disk
	StepStartup = "startup" // startup files
	StepSystem  = "system"  // the system itself
	StepData    = "data"    // your stuff's partition (locked when asked)
	StepFinish  = "finish"  // your account and settings
	StepDone    = "done"
)

// Progress is GET /api/installer/progress.
type Progress struct {
	Running bool   `json:"running"`
	Step    string `json:"step"`
	Percent int    `json:"percent"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done"`
	// RecoveryKey: with the security chip unlocking the drive, the key that
	// unlocks it when the chip can't (shown once, at the end).
	RecoveryKey string `json:"recovery_key,omitempty"`
}

// Source is where the installer copies NoCapOS from: the USB stick.
type Source struct {
	Disk string // the stick
	// ConfigImage: the pristine startup partition (made by os/scripts/make-usb.sh).
	ConfigImage string
}

// Runner runs a command (replaced in tests).
type Runner func(ctx context.Context, stdin string, name string, args ...string) (string, error)

// Exec is the real Runner.
func Exec(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return string(out), fmt.Errorf("%s: %s", name, msg)
	}
	return string(out), nil
}

// Installer runs one install at a time.
type Installer struct {
	Src    Source
	Run    Runner
	Copy   func(ctx context.Context, src, dst string, progress func(done, total int64)) error
	Mounts string // where target partitions are mounted while working

	mu   sync.Mutex
	prog Progress
}

// New returns an installer copying from src.
func New(src Source) *Installer {
	return &Installer{Src: src, Run: Exec, Copy: CopyBlocks, Mounts: "/run/nocap-installer"}
}

// Progress returns where the install is.
func (in *Installer) Progress() Progress {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.prog
}

func (in *Installer) set(step string, pct int) {
	in.mu.Lock()
	in.prog.Step, in.prog.Percent = step, pct
	in.mu.Unlock()
}

// ErrBusy: an install is already running or finished.
var ErrBusy = errors.New("an install is already running")

// Validate checks a request against the disks on this machine.
func Validate(req *Request, disks []Disk) error {
	var target *Disk
	for i := range disks {
		if disks[i].Path == req.Disk {
			target = &disks[i]
		}
	}
	switch {
	case target == nil:
		return errors.New("that disk isn't there any more")
	case target.Boot:
		return errors.New("NoCapOS can't be installed onto the drive it's running from")
	case target.TooSmall:
		return errors.New("that disk is too small for NoCapOS")
	case req.Account.Username == "" || req.Account.Password == "":
		return errors.New("make your account first")
	case req.Encrypt && !req.UseTPM && len(req.Passcode) < 8:
		return errors.New("the passphrase needs at least 8 characters")
	case req.Usage != "" && !osimage.ValidUsage(req.Usage):
		return errors.New("unknown usage")
	}
	seed := seedOf(req)
	return seed.Validate()
}

func seedOf(req *Request) *osimage.Seed {
	return &osimage.Seed{
		Username: req.Account.Username, FullName: req.Account.FullName, Password: req.Account.Password,
		Hostname: req.Hostname, TimeZone: req.TimeZone, Keyboard: req.Keyboard, Variant: req.Variant,
		Language: req.Language, Usage: req.Usage,
	}
}

// Start installs in the background.
func (in *Installer) Start(req Request) error {
	in.mu.Lock()
	if in.prog.Running || in.prog.Done {
		in.mu.Unlock()
		return ErrBusy
	}
	in.prog = Progress{Running: true, Step: StepPrepare}
	in.mu.Unlock()
	go func() {
		key, err := in.install(context.Background(), req)
		in.mu.Lock()
		defer in.mu.Unlock()
		in.prog.Running = false
		if err != nil {
			in.prog.Error = err.Error()
			return
		}
		in.prog.Done, in.prog.Step, in.prog.Percent, in.prog.RecoveryKey = true, StepDone, 100, key
	}()
	return nil
}

// install writes NoCapOS onto req.Disk. Returns the recovery key, if any.
func (in *Installer) install(ctx context.Context, req Request) (string, error) {
	r := in.Run
	disk := req.Disk
	p := func(n int) string { return PartPath(disk, n) }
	src := func(n int) string { return PartPath(in.Src.Disk, n) }

	// ---- prepare: nothing of the old disk stays
	in.set(StepPrepare, 1)
	_, _ = r(ctx, "", "swapoff", "-a")
	for i := 1; i <= 16; i++ {
		_, _ = r(ctx, "", "umount", "-q", p(i))
	}
	if _, err := r(ctx, "", "wipefs", "-a", "-f", disk); err != nil {
		return "", err
	}
	if _, err := r(ctx, SfdiskScript(), "sfdisk", "--wipe", "always", "--wipe-partitions", "always", disk); err != nil {
		return "", err
	}
	_, _ = r(ctx, "", "partprobe", disk)
	_, _ = r(ctx, "", "udevadm", "settle", "--timeout=30")
	in.set(StepPrepare, 3)

	// ---- startup: the pristine startup partition, then the boot files
	in.set(StepStartup, 4)
	if err := in.Copy(ctx, in.Src.ConfigImage, p(1), nil); err != nil {
		return "", fmt.Errorf("writing the startup files: %w", err)
	}
	if err := in.Copy(ctx, src(2), p(2), nil); err != nil {
		return "", fmt.Errorf("writing the boot files: %w", err)
	}
	// The layout is complete already: Rugix mustn't lay it out again.
	cfgMnt := filepath.Join(in.Mounts, "startup")
	if err := os.MkdirAll(cfgMnt, 0o700); err != nil {
		return "", err
	}
	if _, err := r(ctx, "", "mount", "-t", "vfat", p(1), cfgMnt); err != nil {
		return "", err
	}
	_ = os.Remove(filepath.Join(cfgMnt, ".rugix", "bootstrap"))
	_ = os.Remove(filepath.Join(cfgMnt, "rugix", "bootstrap"))
	_ = os.Remove(filepath.Join(cfgMnt, "nocapos-installer"))
	_, _ = r(ctx, "", "sync")
	if _, err := r(ctx, "", "umount", cfgMnt); err != nil {
		return "", err
	}
	in.set(StepStartup, 8)

	// ---- the system (most of the time)
	in.set(StepSystem, 8)
	err := in.Copy(ctx, src(4), p(4), func(done, total int64) {
		if total > 0 {
			in.set(StepSystem, 8+int(80*done/total))
		}
	})
	if err != nil {
		return "", fmt.Errorf("writing the system: %w", err)
	}
	in.set(StepSystem, 88)

	// ---- your stuff
	in.set(StepData, 89)
	dataDev := p(6)
	mapper := ""
	key := ""
	if req.Encrypt {
		pass := req.Passcode
		if req.UseTPM {
			key = recoveryKey()
			pass = key
		}
		if _, err := r(ctx, pass, "cryptsetup", "luksFormat", "--type", "luks2", "--batch-mode", "--key-file=-", dataDev); err != nil {
			return "", err
		}
		if req.UseTPM {
			kf := filepath.Join(in.Mounts, "unlock.key")
			if err := os.WriteFile(kf, []byte(pass), 0o600); err != nil {
				return "", err
			}
			_, err := r(ctx, "", "systemd-cryptenroll", "--tpm2-device=auto", "--tpm2-pcrs=7", "--unlock-key-file="+kf, dataDev)
			_ = os.WriteFile(kf, make([]byte, len(pass)), 0o600)
			_ = os.Remove(kf)
			if err != nil {
				return "", fmt.Errorf("the security chip couldn't lock the drive: %w", err)
			}
		}
		mapper = "nocap-install-data"
		if _, err := r(ctx, pass, "cryptsetup", "open", "--type", "luks2", "--key-file=-", dataDev, mapper); err != nil {
			return "", err
		}
		dataDev = "/dev/mapper/" + mapper
	}
	if _, err := r(ctx, "", "mkfs.ext4", "-q", "-F", "-L", "data", dataDev); err != nil {
		return "", err
	}
	in.set(StepData, 93)

	// ---- your account and settings, for the first start
	in.set(StepFinish, 94)
	dataMnt := filepath.Join(in.Mounts, "data")
	if err := os.MkdirAll(dataMnt, 0o700); err != nil {
		return "", err
	}
	if _, err := r(ctx, "", "mount", "-t", "ext4", dataDev, dataMnt); err != nil {
		return "", err
	}
	werr := osimage.WriteSeed(dataMnt, seedOf(&req))
	_, _ = r(ctx, "", "sync")
	if _, err := r(ctx, "", "umount", dataMnt); err != nil && werr == nil {
		werr = err
	}
	if mapper != "" {
		_, _ = r(ctx, "", "cryptsetup", "close", mapper)
	}
	if werr != nil {
		return "", fmt.Errorf("saving your account for the first start: %w", werr)
	}
	_, _ = r(ctx, "", "sync")
	in.set(StepFinish, 99)
	return key, nil
}

// recoveryKey: 8 groups of 4 easy-to-read characters.
func recoveryKey() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(alphabet[int(c)%len(alphabet)])
	}
	return sb.String()
}

// CopyBlocks copies a partition (or image file) onto a block device.
func CopyBlocks(ctx context.Context, src, dst string, progress func(done, total int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	total, _ := in.Seek(0, io.SeekEnd)
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer out.Close()
	buf := make([]byte, 4<<20)
	var done int64
	last := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			done += int64(n)
			if progress != nil && time.Since(last) > 500*time.Millisecond {
				last = time.Now()
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if progress != nil {
		progress(done, total)
	}
	return nil
}

// HasTPM reports whether this PC has a TPM 2.0 security chip.
func HasTPM() bool {
	b, err := os.ReadFile("/sys/class/tpm/tpm0/tpm_version_major")
	return err == nil && strings.TrimSpace(string(b)) == "2"
}
