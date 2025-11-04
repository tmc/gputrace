package counter

import (
	"path/filepath"
	"testing"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

func TestParseCountersCSV(t *testing.T) {
	// Test with single encoder CSV
	csvPath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1 Counters.csv")

	data, err := ParseCountersCSV(csvPath)
	if err != nil {
		t.Fatalf("ParseCountersCSV failed: %v", err)
	}

	if len(data.Encoders) == 0 {
		t.Fatal("Expected at least one encoder in CSV")
	}

	// Verify first encoder has expected metrics
	enc := data.Encoders[0]

	t.Logf("Encoder 0: %s", enc.EncoderLabel)
	t.Logf("  Kernel Invocations: %d", enc.KernelInvocations)
	t.Logf("  ALU Utilization: %.2f", enc.ALUUtilization)
	t.Logf("  Kernel Occupancy: %.2f", enc.KernelOccupancy)
	t.Logf("  Bytes Read From Device Memory: %d", enc.BytesReadFromDeviceMemory)
	t.Logf("  Bytes Written To Device Memory: %d", enc.BytesWrittenToDeviceMemory)
	t.Logf("  Device Memory Bandwidth: %.2f GB/s", enc.DeviceMemoryBandwidth)
	t.Logf("  GPU Read Bandwidth: %.2f GB/s", enc.GPUReadBandwidth)
	t.Logf("  GPU Write Bandwidth: %.2f GB/s", enc.GPUWriteBandwidth)

	// Verify some expected values (from CSV inspection)
	if enc.BytesReadFromDeviceMemory == 0 && enc.BytesWrittenToDeviceMemory == 0 {
		t.Error("Expected non-zero memory bandwidth values")
	}

	if enc.DeviceMemoryBandwidth == 0 {
		t.Error("Expected non-zero device memory bandwidth")
	}
}

func TestImportCountersCSV(t *testing.T) {
	// Test with six encoders trace
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	data, err := ImportCountersCSV(tr)
	if err != nil {
		t.Fatalf("ImportCountersCSV failed: %v", err)
	}

	if len(data.Encoders) != 6 {
		t.Errorf("Expected 6 encoders, got %d", len(data.Encoders))
	}

	// Verify all encoders have bandwidth data
	for i, enc := range data.Encoders {
		t.Logf("Encoder %d: %s", i, enc.EncoderLabel)
		t.Logf("  Device Memory Bandwidth: %.2f GB/s", enc.DeviceMemoryBandwidth)
		t.Logf("  GPU Read: %.2f GB/s, Write: %.2f GB/s", enc.GPUReadBandwidth, enc.GPUWriteBandwidth)

		// Check that at least some encoders have meaningful data
		if i < 3 && enc.DeviceMemoryBandwidth == 0 {
			t.Errorf("Encoder %d has zero bandwidth", i)
		}
	}
}

func TestEnhanceMetricsFromCSV(t *testing.T) {
	// Parse CSV
	csvPath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1 Counters.csv")
	csvData, err := ParseCountersCSV(csvPath)
	if err != nil {
		t.Fatalf("ParseCountersCSV failed: %v", err)
	}

	// Create test metrics
	stats := &PerfCounterStats{
		ShaderMetrics: []ShaderHardwareMetrics{
			{
				ExecutionCount: 1024,
				PipelineState:  0x12345,
			},
		},
	}

	// Enhance with CSV data
	err = EnhanceMetricsFromCSV(stats, csvData)
	if err != nil {
		t.Fatalf("EnhanceMetricsFromCSV failed: %v", err)
	}

	// Check if metrics were enhanced
	metric := stats.ShaderMetrics[0]
	t.Logf("Enhanced metric:")
	t.Logf("  BytesRead: %d", metric.BytesReadFromDeviceMemory)
	t.Logf("  BytesWritten: %d", metric.BytesWrittenToDeviceMemory)
	t.Logf("  Device BW: %.2f GB/s", metric.DeviceMemoryBandwidthGBps)
}
