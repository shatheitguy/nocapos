package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

const smartRunning = `{"smartctl":{"exit_status":0},"smart_status":{"passed":true},
 "ata_smart_data":{"self_test":{"status":{"value":249,"string":"in progress, 90% remaining"},
  "polling_minutes":{"short":2,"extended":154}}},
 "ata_smart_self_test_log":{"standard":{"table":[]}}}`

const smartRunningOld = `{"smartctl":{"exit_status":0},"smart_status":{"passed":true},
 "ata_smart_data":{"self_test":{"status":{"value":244,"string":"in progress"}}}}`

const smartDone = `{"smartctl":{"exit_status":0},"smart_status":{"passed":true},
 "ata_smart_data":{"self_test":{"status":{"value":0,"string":"completed without error","passed":true},
  "polling_minutes":{"short":2,"extended":154}}},
 "ata_smart_self_test_log":{"standard":{"table":[
  {"type":{"string":"Short offline"},"status":{"string":"Completed without error"},"lifetime_hours":28710},
  {"type":{"string":"Extended offline"},"status":{"string":"Completed: read failure"},"lifetime_hours":28000}]}}}`

func TestParseSmartTestProgress(t *testing.T) {
	r, err := ParseSmart([]byte(smartRunning))
	if err != nil {
		t.Fatal(err)
	}
	s := r.Summary
	if !s.TestRunning || s.TestRemaining == nil || *s.TestRemaining != 90 || *s.ShortTestMin != 2 || *s.LongTestMin != 154 {
		t.Fatalf("running: %+v", s)
	}
	// Without remaining_percent the status byte's low nibble says it (0xF4 = 40%).
	r, _ = ParseSmart([]byte(smartRunningOld))
	if !r.Summary.TestRunning || *r.Summary.TestRemaining != 40 {
		t.Fatalf("status byte: %+v", r.Summary)
	}
}

func TestParseSelfTestStatusText(t *testing.T) {
	text := `General SMART Values:
Offline data collection status:  (0x00)	Offline data collection activity
					was never started.
Self-test execution status:      ( 249)	Self-test routine in progress...
					90% of test remaining.
Total time to complete Offline
data collection: 		(  600) seconds.
`
	running, left := ParseSelfTestStatus(text)
	if !running || left == nil || *left != 90 {
		t.Fatalf("in progress: %v %v", running, left)
	}
	done := "Self-test execution status:      (   0)	The previous self-test routine completed\n\t\t\t\t\twithout error or no self-test has ever \n\t\t\t\t\tbeen run.\n"
	if running, _ := ParseSelfTestStatus(done); running {
		t.Error("finished test reported running")
	}
	if running, _ := ParseSelfTestStatus("nothing here"); running {
		t.Error("no status reported running")
	}
}

func TestTestETA(t *testing.T) {
	s := &SmartSummary{TestRunning: true, TestRemaining: i64(90), ShortTestMin: i64(2), LongTestMin: i64(154)}
	if eta := testETA(s, "long", time.Minute); eta == nil || *eta < 154*60*90/100 {
		t.Errorf("long at start: %v", eta)
	}
	if eta := testETA(s, "short", 0); eta == nil || *eta != 108 {
		t.Errorf("short from the disk's estimate: %v", eta)
	}
	s.TestRemaining = i64(50)
	if eta := testETA(s, "long", 40*time.Minute); eta == nil || *eta != 40*60 {
		t.Errorf("long from its pace: %v", eta)
	}
	if eta := testETA(&SmartSummary{TestRunning: true, TestRemaining: i64(90)}, "", 0); eta != nil {
		t.Errorf("unknown test: %v", *eta)
	}
	if eta := testETA(&SmartSummary{}, "short", time.Minute); eta != nil {
		t.Error("ETA for a disk that isn't testing")
	}
}

func TestTestResult(t *testing.T) {
	d := Disk{Name: "sdb", Model: "WD Red"}
	if r := testResult(d, "short", []SelfTest{{Status: "Completed without error"}}); !r.Passed || r.Model != "WD Red" {
		t.Errorf("passed: %+v", r)
	}
	if r := testResult(d, "long", []SelfTest{{Status: "Completed: read failure"}}); r.Passed || r.Aborted {
		t.Errorf("failed: %+v", r)
	}
	if r := testResult(d, "long", []SelfTest{{Status: "Aborted by host"}}); r.Passed || !r.Aborted {
		t.Errorf("aborted: %+v", r)
	}
	if r := testResult(d, "short", nil); r.Passed {
		t.Errorf("empty log: %+v", r)
	}
}

func TestSmartTestFollowsProgress(t *testing.T) {
	m, fr, _, _ := newTestManager(t)
	m.testPoll = 20 * time.Millisecond
	ctx := context.Background()
	const read = "smartctl -j -a -n standby /dev/sdb"
	fr.out[read] = smartRunning
	fr.out["smartctl -t short /dev/sdb"] = "Testing has begun."
	results := make(chan TestResult, 1)
	m.OnTestDone = func(r TestResult) { results <- r }

	j, err := m.SmartTest(ctx, "sdb", "short", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, j)
	// While it runs, the disk list carries the progress and time left.
	deadline := time.Now().Add(2 * time.Second)
	var s *SmartSummary
	for time.Now().Before(deadline) {
		disks, err := m.Disks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if d, ok := findDisk(disks, "sdb"); ok && d.Smart != nil && d.Smart.TestRunning {
			s = d.Smart
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s == nil || s.TestKind != "short" || s.TestETASec == nil || *s.TestRemaining != 90 {
		t.Fatalf("progress in disk list: %+v", s)
	}

	fr.mu.Lock()
	fr.out[read] = smartDone
	fr.mu.Unlock()
	select {
	case r := <-results:
		if r.Disk != "sdb" || r.Kind != "short" || !r.Passed || r.Status != "Completed without error" {
			t.Fatalf("result: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no result")
	}
	// Only disk tools ran: nothing touched networking or services.
	fr.mu.Lock()
	defer fr.mu.Unlock()
	for _, c := range fr.calls {
		switch c[0] {
		case "smartctl", "lsblk", "zpool", "zfs", "findmnt":
		default:
			t.Errorf("unexpected command during a self-test: %s", strings.Join(c, " "))
		}
	}
}
