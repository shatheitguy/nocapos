package docker

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func frame(stream byte, payload string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(payload)))
	return append(h, payload...)
}

func TestDemuxReassemblesLinesAcrossFrames(t *testing.T) {
	var in bytes.Buffer
	in.Write(frame(1, "2024-01-01T00:00:00Z hel"))
	in.Write(frame(2, "err line\r\n"))
	in.Write(frame(1, "lo\nsecond"))

	var got []LogLine
	collect := func(stream string) *lineWriter {
		return &lineWriter{fn: func(b []byte) error { got = append(got, parseLogLine(stream, b)); return nil }}
	}
	out, errw := collect("stdout"), collect("stderr")
	if err := demux(&in, out, errw); err != nil {
		t.Fatal(err)
	}
	out.Close()
	errw.Close()

	want := []LogLine{
		{Stream: "stderr", Text: "err line"},
		{Stream: "stdout", Text: "hello"},
		{Stream: "stdout", Text: "second"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Stream != want[i].Stream || got[i].Text != want[i].Text {
			t.Errorf("line %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	if got[1].Time.IsZero() {
		t.Error("timestamp prefix should be parsed")
	}
}

func TestLineWriterTruncates(t *testing.T) {
	var n int
	w := &lineWriter{fn: func(b []byte) error { n = len(b); return nil }}
	w.Write(bytes.Repeat([]byte("x"), maxLogLine*2))
	w.Write([]byte("\n"))
	if n != maxLogLine {
		t.Fatalf("line length %d, want %d", n, maxLogLine)
	}
}

func TestCPUPercent(t *testing.T) {
	var s statsJSON
	s.PreCPUStats.CPUUsage.TotalUsage = 1_000
	s.CPUStats.CPUUsage.TotalUsage = 3_000
	s.PreCPUStats.SystemUsage = 10_000
	s.CPUStats.SystemUsage = 18_000
	s.CPUStats.OnlineCPUs = 4
	// 2000/8000 of all CPU time on a 4-core box = 1 full core = 100%.
	if got := cpuPercent(&s); math.Abs(got-100) > 1e-9 {
		t.Fatalf("cpuPercent = %v, want 100", got)
	}
}

func TestMemUsageSubtractsCache(t *testing.T) {
	var s statsJSON
	s.MemoryStats.Usage = 1000
	s.MemoryStats.Stats = map[string]uint64{"inactive_file": 300}
	if got := memUsage(&s); got != 700 {
		t.Fatalf("memUsage = %d, want 700", got)
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions("1.41", "1.47") >= 0 || compareVersions("1.51", "1.9") <= 0 || compareVersions("1.44", "1.44") != 0 {
		t.Fatal("version comparison is not numeric")
	}
}
