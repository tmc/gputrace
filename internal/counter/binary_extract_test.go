package counter

import (
	"path/filepath"
	"testing"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

func TestExtractFromBinary(t *testing.T) {
	// Test with single encoder perf trace
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	// Parse performance counters from binary
	stats, err := ParsePerfCounters(tr)
	if err != nil {
		t.Fatalf("ParsePerfCounters failed: %v", err)
	}

	t.Logf("Found %d shader metrics", len(stats.ShaderMetrics))
	t.Logf("Total records: %d", stats.TotalRecords)
	t.Logf("Files processed: %d", stats.FilesProcessed)

	// Show what we extracted
	for i, metric := range stats.ShaderMetrics {
		t.Logf("Metric %d: %s", i, metric.ShaderName)
		t.Logf("  Execution Count: %d", metric.ExecutionCount)
		t.Logf("  ALU Utilization: %.2f%%", metric.ALUUtilization)
		t.Logf("  Kernel Occupancy: %.2f%%", metric.KernelOccupancy)
		t.Logf("  Bytes Read: %d", metric.BytesReadFromDeviceMemory)
		t.Logf("  Bytes Written: %d", metric.BytesWrittenToDeviceMemory)
		t.Logf("  Total Bandwidth: %d bytes", metric.MemoryBandwidth)
	}
}

func TestExtractFromBinarySixEncoders(t *testing.T) {
	// Test with six encoders perf trace
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	// Parse performance counters from binary
	stats, err := ParsePerfCounters(tr)
	if err != nil {
		t.Fatalf("ParsePerfCounters failed: %v", err)
	}

	t.Logf("Found %d shader metrics", len(stats.ShaderMetrics))
	t.Logf("Total records: %d", stats.TotalRecords)
	t.Logf("Files processed: %d", stats.FilesProcessed)

	// Compare with CSV data
	csvData, err := ImportCountersCSV(tr)
	if err != nil {
		t.Logf("CSV not available: %v", err)
	} else {
		t.Logf("\nComparison with CSV:")
		for i, metric := range stats.ShaderMetrics {
			if i < len(csvData.Encoders) {
				csv := csvData.Encoders[i]
				t.Logf("\nEncoder %d: %s", i, metric.ShaderName)
				t.Logf("  Binary - Invocations: %d, ALU: %.2f%%, Bytes: %d",
					metric.ExecutionCount, metric.ALUUtilization, metric.MemoryBandwidth)
				t.Logf("  CSV    - Invocations: %d, ALU: %.2f%%, Bytes: %d",
					csv.KernelInvocations, csv.ALUUtilization,
					csv.BytesReadFromDeviceMemory+csv.BytesWrittenToDeviceMemory)
			}
		}
	}
}
