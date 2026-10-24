package storage

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SMART self-tests run inside the disk; smartctl only starts them. While one
// runs NoCapOS re-reads the disk every few seconds (only smartctl, nothing
// else on the machine is touched) so the Storage app can show how far along
// it is, and reports the result when it ends.

// TestResult is a finished self-test.
type TestResult struct {
	Disk   string `json:"disk"`  // kernel name, e.g. sdb
	Model  string `json:"model"` // what people call it
	Kind   string `json:"kind"`  // short | long
	Status string `json:"status"`
	Passed bool   `json:"passed"`
	// Aborted: stopped before the end (by the host, a reset, power) — neither
	// passed nor failed.
	Aborted bool `json:"aborted"`
}

type runningTest struct {
	disk    Disk
	kind    string
	started time.Time
	seen    bool // seen running at least once
}

// Longest we follow a test before giving up on hearing its end.
var testGiveUp = map[string]time.Duration{"short": 30 * time.Minute, "long": 72 * time.Hour}

// testETA estimates the seconds left: from the pace so far once a fifth is
// done (disks count in tenths and say "90% left" from the first second),
// before that from the disk's own estimate of the test's length.
func testETA(s *SmartSummary, kind string, elapsed time.Duration) *int64 {
	if s == nil || !s.TestRunning || s.TestRemaining == nil {
		return nil
	}
	rem := *s.TestRemaining
	if rem <= 0 {
		return i64(0)
	}
	if done := 100 - rem; done >= 20 && elapsed > 0 {
		return i64(int64(elapsed.Seconds()) * rem / done)
	}
	var mins *int64
	switch kind {
	case "short":
		mins = s.ShortTestMin
		if mins == nil {
			mins = i64(2) // the ATA and NVMe norm for a short test
		}
	case "long":
		mins = s.LongTestMin
	}
	if mins == nil {
		return nil
	}
	left := *mins * 60 * rem / 100
	if elapsed > 0 {
		if total := *mins * 60; int64(elapsed.Seconds()) < total {
			left = max(left, total-int64(elapsed.Seconds()))
		}
	}
	return i64(left)
}

// withTest adds the test kind and time left to a summary (a copy).
func (m *Manager) withTest(d Disk, s *SmartSummary) *SmartSummary {
	if s == nil || !s.TestRunning {
		return s
	}
	cp := *s
	kind, elapsed := "", time.Duration(0)
	m.mu.Lock()
	if t := m.tests[smartKey(d)]; t != nil {
		kind, elapsed = t.kind, time.Since(t.started)
	}
	m.mu.Unlock()
	cp.TestKind = kind
	cp.TestETASec = testETA(&cp, kind, elapsed)
	return &cp
}

// followTest re-reads a disk while its self-test runs and reports the end.
func (m *Manager) followTest(d Disk, kind string) {
	key := smartKey(d)
	t := &runningTest{disk: d, kind: kind, started: time.Now()}
	m.mu.Lock()
	if m.tests[key] != nil {
		m.mu.Unlock()
		return
	}
	m.tests[key] = t
	poll := m.testPoll
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.tests, key)
		m.mu.Unlock()
	}()
	giveUp := testGiveUp[kind]
	if giveUp == 0 {
		giveUp = time.Hour
	}
	for time.Since(t.started) < giveUp {
		time.Sleep(poll)
		ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
		r, err := m.be.Smart(ctx, d)
		cancel()
		if err != nil || r == nil {
			continue // busy or briefly unreachable: try again
		}
		if r.Summary.Asleep {
			continue
		}
		m.mu.Lock()
		m.smart[key] = smartEntry{s: &r.Summary, at: time.Now()}
		m.mu.Unlock()
		if r.Summary.TestRunning {
			t.seen = true
			continue
		}
		// Not running: finished — unless it never showed as running and
		// the disk is slow to report it (allow a minute).
		if !t.seen && time.Since(t.started) < time.Minute && len(r.SelfTests) == 0 {
			continue
		}
		res := testResult(d, kind, r.SelfTests)
		m.log.Info("storage: self-test finished", "disk", d.Name, "kind", kind, "status", res.Status)
		if m.OnTestDone != nil {
			m.OnTestDone(res)
		}
		return
	}
	m.log.Warn("storage: stopped following a self-test", "disk", d.Name, "kind", kind)
}

// testResult reads the newest self-test log entry.
func testResult(d Disk, kind string, log []SelfTest) TestResult {
	res := TestResult{Disk: d.Name, Model: firstNonEmpty(d.Model, d.Name), Kind: kind, Status: "No result in the disk's test log"}
	if len(log) == 0 {
		return res
	}
	res.Status = log[0].Status
	low := strings.ToLower(res.Status)
	switch {
	case strings.Contains(low, "without error") || low == "completed" || strings.HasPrefix(low, "completed: no error"):
		res.Passed = true
	case strings.Contains(low, "abort") || strings.Contains(low, "interrupt"):
		res.Aborted = true
	}
	return res
}

var selfTestStatusRe = regexp.MustCompile(`(?s)Self-test execution status:\s*\(\s*(\d+)\)(.*?)(?:\n\S|\z)`)
var percentLeftRe = regexp.MustCompile(`(\d+)% of test remaining`)

// ParseSelfTestStatus reads the "Self-test execution status" block of
// `smartctl -c` / `-a` text output (ATA disks).
func ParseSelfTestStatus(text string) (running bool, remaining *int64) {
	m := selfTestStatusRe.FindStringSubmatch(text)
	if m == nil {
		return false, nil
	}
	v, _ := strconv.ParseInt(m[1], 10, 64)
	if v < 240 || v > 255 {
		return false, nil
	}
	if p := percentLeftRe.FindStringSubmatch(m[2]); p != nil {
		n, _ := strconv.ParseInt(p[1], 10, 64)
		return true, i64(n)
	}
	return true, i64((v & 0x0f) * 10)
}
