package firewall

import (
	"context"
	"io"
	"os"
	"sort"
	"time"
)

// RecentBlocked returns the connections the firewall turned away in the last
// day, newest first, at most max. It reads the kernel log from the journal,
// or ufw's own log file.
func (e *Engine) RecentBlocked(ctx context.Context, max int) []Blocked {
	if ok, _ := hostSupported(); !ok && e.etc == "/etc" {
		return []Blocked{}
	}
	now := time.Now()
	var list []Blocked
	if out, err := e.run(ctx, "journalctl", "-k", "--since", "-24h", "-o", "short-iso", "--no-pager", "-n", "5000"); err == nil {
		list = ParseBlocked(out, now)
	}
	if len(list) == 0 {
		list = ParseBlocked(tailFile("/var/log/ufw.log", 1<<20), now)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Time.After(list[j].Time) })
	if len(list) > max {
		list = list[:max]
	}
	if list == nil {
		list = []Blocked{}
	}
	return list
}

func tailFile(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > n {
		_, _ = f.Seek(st.Size()-n, io.SeekStart)
	}
	b, _ := io.ReadAll(io.LimitReader(f, n))
	return string(b)
}

// Listeners lists the sockets listening on this machine ("ss").
func (e *Engine) Listeners(ctx context.Context) []Listener {
	out, err := e.run(ctx, "ss", "-H", "-tulpn")
	if err != nil {
		return nil
	}
	return ParseListeners(out)
}
