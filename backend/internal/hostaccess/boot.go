package hostaccess

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ---- Show boot details ----
//
// The machine boots silently: no boot menu (it shows while Shift or Esc is
// held at power-on), no boot messages, the NoCapOS splash from power-on
// until NoCap's sign-in screen. "Show boot details" (Settings → Screen Real
// Estate) brings the menu and the messages back for troubleshooting.
// nocap-boot, installed with NoCap Desktop's helpers, makes the change the
// same way the installer does on every machine: GRUB (backed up, checked
// and put back if update-grub fails), the Raspberry Pi's cmdline.txt and
// other boot menus.

// BootToolPath is nocap-boot.
const BootToolPath = "/usr/local/lib/nocapos/desktop/nocap-boot"

// BootDetailsPath holds the choice ("on" shows the boot details).
const BootDetailsPath = "/etc/nocapos/boot-details"

// ErrBootUnavailable means this machine has no nocap-boot (not installed
// straight on Linux, or installed before it existed: an update adds it).
var ErrBootUnavailable = errors.New("this switch needs NoCapOS installed straight on Linux and up to date")

// BootState is the "Show boot details" switch.
type BootState struct {
	Supported bool `json:"supported"`
	// Details: the boot menu and the boot messages show.
	Details bool `json:"details"`
}

// Boot reports the switch.
func (d *Desktop) Boot() BootState {
	st := BootState{Supported: d.Native && d.exists(BootToolPath)}
	if b, err := d.readFile(BootDetailsPath); err == nil {
		st.Details = strings.TrimSpace(string(b)) == "on"
	}
	return st
}

// bootTimeout bounds update-grub and update-initramfs.
const bootTimeout = 5 * time.Minute

// SetBootDetails turns the boot details on or off. It finishes even when
// the browser goes away: the boot menu is never left half rebuilt.
func (d *Desktop) SetBootDetails(ctx context.Context, on bool) error {
	if !d.Boot().Supported {
		return ErrBootUnavailable
	}
	arg := "off"
	if on {
		arg = "on"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bootTimeout)
	defer cancel()
	return d.run(ctx, BootToolPath, "details", arg)
}
