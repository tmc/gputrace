package counter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestXcodeCountersCSVParsing(t *testing.T) {
	tr := openCountersCSVTrace(t)

	// Try to parse the Xcode Counters.csv
	csvData, err := ImportCountersCSV(tr)
	if err != nil {
		t.Fatalf("Failed to parse Counters.csv: %v", err)
	}

	t.Logf("Parsed %d encoders", len(csvData.Encoders))

	if len(csvData.Encoders) == 0 {
		t.Fatal("Expected at least one encoder")
	}

	// Check first encoder
	enc := csvData.Encoders[0]
	t.Logf("Encoder 0:")
	t.Logf("  Index: %d", enc.Index)
	t.Logf("  FunctionIndex: %d", enc.EncoderFunctionIndex)
	t.Logf("  CommandBuffer: %s", enc.CommandBufferLabel)
	t.Logf("  Encoder: %s", enc.EncoderLabel)
	t.Logf("  ALU Utilization: %.2f", enc.ALUUtilization)
	t.Logf("  Kernel Occupancy: %.2f", enc.KernelOccupancy)
	t.Logf("  Kernel Invocations: %d", enc.KernelInvocations)
	t.Logf("  Device Memory Bandwidth: %.2f GB/s", enc.DeviceMemoryBandwidth)
}

func TestXcodeCSVMemoryBandwidth(t *testing.T) {
	tr := openCountersCSVTrace(t)

	csvData, err := ImportCountersCSV(tr)
	if err != nil {
		t.Skipf("Counters.csv not available: %v", err)
	}

	// Check memory bandwidth fields
	for i, enc := range csvData.Encoders {
		t.Logf("Encoder %d: %s", i, enc.EncoderLabel)
		t.Logf("  Bytes Read: %d", enc.BytesReadFromDeviceMemory)
		t.Logf("  Bytes Written: %d", enc.BytesWrittenToDeviceMemory)
		t.Logf("  Device Memory BW: %.2f GB/s", enc.DeviceMemoryBandwidth)
		t.Logf("  GPU Read BW: %.2f GB/s", enc.GPUReadBandwidth)
		t.Logf("  GPU Write BW: %.2f GB/s", enc.GPUWriteBandwidth)
	}
}

func openCountersCSVTrace(t *testing.T) *trace.Trace {
	t.Helper()

	tracePath := os.Getenv("GPUTRACE_COUNTERS_CSV_TRACE")
	if tracePath == "" {
		t.Skip("set GPUTRACE_COUNTERS_CSV_TRACE to run Xcode Counters.csv fixture tests")
	}
	tracePath = filepath.Clean(tracePath)

	info, err := os.Stat(tracePath)
	if err != nil {
		t.Fatalf("GPUTRACE_COUNTERS_CSV_TRACE=%q is not accessible: %v", tracePath, err)
	}
	if !info.IsDir() {
		t.Fatalf("GPUTRACE_COUNTERS_CSV_TRACE=%q must point to a .gputrace directory", tracePath)
	}
	if filepath.Ext(tracePath) != ".gputrace" {
		t.Fatalf("GPUTRACE_COUNTERS_CSV_TRACE=%q must point to a .gputrace directory", tracePath)
	}

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Fatalf("open trace from GPUTRACE_COUNTERS_CSV_TRACE=%q: %v", tracePath, err)
	}
	return tr
}
