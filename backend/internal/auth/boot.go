package auth

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"alfaos/alfad/internal/store"
)

const keyBootID = "host.boot_id"

var processStart = strconv.FormatInt(time.Now().UnixNano(), 10)

// BootID identifies the current boot of the machine: the kernel's boot id on
// Linux (also visible inside containers), otherwise this process's start.
func BootID() string {
	if b, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return "boot:" + id
		}
	}
	return "process:" + processStart
}

// EndSessionsAfterReboot signs everyone out when the machine has restarted
// since NoCapOS last ran, so a session never outlives a reboot. It reports
// how many sessions it ended.
func EndSessionsAfterReboot(ctx context.Context, st *store.Store, bootID string) (int64, error) {
	prev, err := st.Setting(ctx, keyBootID)
	if err != nil {
		return 0, err
	}
	if prev == bootID {
		return 0, nil
	}
	n, err := st.RevokeAllSessions(ctx, time.Now())
	if err != nil {
		return 0, err
	}
	return n, st.SetSetting(ctx, keyBootID, bootID)
}
