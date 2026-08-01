package counter

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

// TestParseCounterFileWithMetricsCountsRecords checks record accounting only.
// It used to assert ExecutionCount == 1024 from a synthetic record carrying
// 28,416 at offset 0x0064, which merely replayed the ÷27.75 divisor back at
// itself; both the divisor and the metrics it gated are gone.
func TestParseCounterFileWithMetricsCountsRecords(t *testing.T) {
	path := writeCounterRawFile(t, syntheticCounterRaw())

	stats, metrics, err := parseCounterFileWithMetrics(path)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRecords != 2 {
		t.Fatalf("TotalRecords = %d, want 2", stats.TotalRecords)
	}
	if len(metrics) != 0 {
		t.Fatalf("got %d metrics, want 0", len(metrics))
	}
}

func TestParseCounterFileWithMetricsRejectsMalformedRaw(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "empty",
			data: nil,
			want: "no counter record markers found",
		},
		{
			name: "no marker",
			data: []byte("not a counter file"),
			want: "no counter record markers found",
		},
		{
			name: "short marker",
			data: []byte{0x4e, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04},
			want: "no valid counter records found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseCounterFileWithMetrics(writeCounterRawFile(t, tt.data))
			if err == nil {
				t.Fatal("parseCounterFileWithMetrics succeeded")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestParsePerfCountersRejectsInvalidCounterFiles(t *testing.T) {
	tracePath, perfDir := makeTraceWithPerfDir(t)
	if err := os.WriteFile(filepath.Join(perfDir, "Counters_f_0.raw"), []byte("invalid"), 0o666); err != nil {
		t.Fatal(err)
	}

	_, err := ParsePerfCounters(&trace.Trace{Path: tracePath})
	if err == nil {
		t.Fatal("ParsePerfCounters succeeded")
	}
	if !strings.Contains(err.Error(), "no valid performance counter records") {
		t.Fatalf("error %q does not report invalid counter records", err)
	}
}

func TestParsePerfCountersProcessesSyntheticCounterFile(t *testing.T) {
	tracePath, perfDir := makeTraceWithPerfDir(t)
	if err := os.WriteFile(filepath.Join(perfDir, "Counters_f_0.raw"), syntheticCounterRaw(), 0o666); err != nil {
		t.Fatal(err)
	}

	stats, err := ParsePerfCounters(&trace.Trace{Path: tracePath})
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesProcessed != 1 {
		t.Fatalf("FilesProcessed = %d, want 1", stats.FilesProcessed)
	}
	if stats.TotalRecords != 2 {
		t.Fatalf("TotalRecords = %d, want 2", stats.TotalRecords)
	}
	if stats.ConfidenceLevel != 1 {
		t.Fatalf("ConfidenceLevel = %v, want 1", stats.ConfidenceLevel)
	}
	if len(stats.ShaderMetrics) != 0 {
		t.Fatalf("got %d shader metrics, want 0", len(stats.ShaderMetrics))
	}
}

// syntheticCounterRaw builds a two-record counter file: one metadata-sized
// record and one smaller record. Only the record framing is meaningful — no
// field inside either record is decoded any more.
func syntheticCounterRaw() []byte {
	const (
		metadataSize = 2300
		sampleSize   = 464
	)

	data := make([]byte, metadataSize+sampleSize)
	binary.LittleEndian.PutUint32(data[0:], 0x4e)

	sample := data[metadataSize:]
	binary.LittleEndian.PutUint32(sample[0:], 0x4e)
	return data
}

func writeCounterRawFile(t *testing.T, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "Counters_f_0.raw")
	if err := os.WriteFile(path, data, 0o666); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeTraceWithPerfDir(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()
	tracePath := filepath.Join(dir, "synthetic.gputrace")
	if err := os.Mkdir(tracePath, 0o777); err != nil {
		t.Fatal(err)
	}
	perfDir := tracePath + ".gpuprofiler_raw"
	if err := os.Mkdir(perfDir, 0o777); err != nil {
		t.Fatal(err)
	}
	return tracePath, perfDir
}
