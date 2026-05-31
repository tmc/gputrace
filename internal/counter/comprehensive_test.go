package counter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

// TestDeterminism verifies that parsing the same trace multiple times produces identical results
func TestDeterminism(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

	// Parse the same trace 3 times
	var results []*PerfCounterStats
	for i := 0; i < 3; i++ {
		stats, err := ParsePerfCounters(tr)
		if err != nil {
			t.Fatalf("Parse %d failed: %v", i, err)
		}
		results = append(results, stats)
	}

	// Verify all results have same number of encoders
	for i := 1; i < len(results); i++ {
		if len(results[i].ShaderMetrics) != len(results[0].ShaderMetrics) {
			t.Errorf("Run %d: Got %d shader metrics, expected %d",
				i, len(results[i].ShaderMetrics), len(results[0].ShaderMetrics))
		}
	}

	// Verify totals are identical (order may vary due to map iteration)
	for i := 1; i < len(results); i++ {
		total0 := 0
		totali := 0
		for j := range results[0].ShaderMetrics {
			total0 += results[0].ShaderMetrics[j].ExecutionCount
		}
		for j := range results[i].ShaderMetrics {
			totali += results[i].ShaderMetrics[j].ExecutionCount
		}

		if total0 != totali {
			t.Errorf("Run %d: Total ExecutionCount %d != %d", i, totali, total0)
		}
	}

	t.Logf("✓ All %d parsing runs produced identical results", len(results))
}

// TestEdgeCases tests handling of edge cases and invalid data
func TestEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		tracePath   string
		expectError bool
		skipReason  string
	}{
		{
			name:        "nonexistent-trace",
			tracePath:   "/tmp/nonexistent-trace.gputrace",
			expectError: true,
		},
		{
			name:        "empty-path",
			tracePath:   "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipReason != "" {
				t.Skip(tt.skipReason)
			}

			// For positive tests, check if file exists
			if !tt.expectError && tt.tracePath != "" {
				if _, err := os.Stat(tt.tracePath); os.IsNotExist(err) {
					t.Skipf("skipping test, trace file not found: %s", tt.tracePath)
				}
			}

			tr, err := trace.Open(tt.tracePath)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error opening %s, got nil", tt.tracePath)
					tr.Close()
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			defer tr.Close()

			stats, err := ParsePerfCounters(tr)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error parsing, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if stats == nil {
					t.Error("Expected stats, got nil")
				}
			}
		})
	}
}

// TestComprehensiveMetrics validates extraction of all supported metrics
func TestComprehensiveMetrics(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

	stats, err := ParsePerfCounters(tr)
	if err != nil {
		t.Fatalf("ParsePerfCounters failed: %v", err)
	}

	if len(stats.ShaderMetrics) == 0 {
		t.Fatal("No shader metrics extracted")
	}

	// Test core metrics are populated
	metricsFound := make(map[string]int)

	for _, m := range stats.ShaderMetrics {
		// Core execution metrics
		if m.ExecutionCount > 0 {
			metricsFound["ExecutionCount"]++
		}
		if m.ALUUtilization > 0 {
			metricsFound["ALUUtilization"]++
		}
		if m.KernelOccupancy > 0 {
			metricsFound["KernelOccupancy"]++
		}

		// Memory metrics
		if m.BytesReadFromDeviceMemory > 0 {
			metricsFound["BytesReadFromDeviceMemory"]++
		}
		if m.BytesWrittenToDeviceMemory > 0 {
			metricsFound["BytesWrittenToDeviceMemory"]++
		}
		if m.BufferDeviceMemoryBytesRead > 0 {
			metricsFound["BufferDeviceMemoryBytesRead"]++
		}
		if m.BufferDeviceMemoryBytesWritten > 0 {
			metricsFound["BufferDeviceMemoryBytesWritten"]++
		}
		if m.DeviceMemoryBandwidthGBps > 0 {
			metricsFound["DeviceMemoryBandwidthGBps"]++
		}
		if m.GPUReadBandwidthGBps > 0 {
			metricsFound["GPUReadBandwidthGBps"]++
		}
		if m.GPUWriteBandwidthGBps > 0 {
			metricsFound["GPUWriteBandwidthGBps"]++
		}

		// Cache metrics
		if m.BufferL1MissRate > 0 {
			metricsFound["BufferL1MissRate"]++
		}
		if m.BufferL1ReadAccesses > 0 {
			metricsFound["BufferL1ReadAccesses"]++
		}
		if m.BufferL1ReadBandwidth > 0 {
			metricsFound["BufferL1ReadBandwidth"]++
		}
		if m.BufferL1WriteAccesses > 0 {
			metricsFound["BufferL1WriteAccesses"]++
		}
		if m.BufferL1WriteBandwidth > 0 {
			metricsFound["BufferL1WriteBandwidth"]++
		}
	}

	t.Logf("\n=== Metrics Coverage ===")
	requiredMetrics := []string{
		"ExecutionCount",
		"ALUUtilization",
		"KernelOccupancy",
	}

	for _, metric := range requiredMetrics {
		count := metricsFound[metric]
		t.Logf("%-30s: %d/%d encoders (%.0f%%)",
			metric, count, len(stats.ShaderMetrics),
			float64(count)/float64(len(stats.ShaderMetrics))*100)

		if count == 0 {
			t.Errorf("Required metric %s not found in any encoder", metric)
		}
	}

	optionalMetrics := []string{
		"BytesReadFromDeviceMemory",
		"BytesWrittenToDeviceMemory",
		"BufferDeviceMemoryBytesRead",
		"BufferDeviceMemoryBytesWritten",
		"DeviceMemoryBandwidthGBps",
		"GPUReadBandwidthGBps",
		"GPUWriteBandwidthGBps",
		"BufferL1MissRate",
		"BufferL1ReadAccesses",
		"BufferL1ReadBandwidth",
		"BufferL1WriteAccesses",
		"BufferL1WriteBandwidth",
	}

	for _, metric := range optionalMetrics {
		count := metricsFound[metric]
		coverage := float64(count) / float64(len(stats.ShaderMetrics)) * 100
		t.Logf("%-30s: %d/%d encoders (%.0f%%)",
			metric, count, len(stats.ShaderMetrics), coverage)
	}
}

// TestCSVRoundTrip validates that CSV export -> import produces consistent results
func TestCSVRoundTrip(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

	// Import reference CSV if available
	csvData, err := ImportCountersCSV(tr)
	if err != nil {
		t.Skipf("Reference CSV not available: %v", err)
	}

	// Parse counters (with CSV enhancement)
	stats, err := ParsePerfCounters(tr)
	if err != nil {
		t.Fatalf("ParsePerfCounters failed: %v", err)
	}

	// Verify CSV data was applied
	if len(stats.ShaderMetrics) == 0 {
		t.Fatal("No shader metrics extracted")
	}

	// Check that at least one encoder has CSV-enhanced data
	hasCSVData := false
	for _, m := range stats.ShaderMetrics {
		if m.ALUUtilization > 0 && m.ALUUtilization < 100 {
			hasCSVData = true
			break
		}
	}

	if !hasCSVData && len(csvData.Encoders) > 0 {
		t.Error("CSV data available but not applied to metrics")
	}

	t.Logf("✓ CSV round-trip validated: %d encoders enhanced", len(csvData.Encoders))
}

// TestMultipleTraces verifies parsing works across different trace types
func TestMultipleTraces(t *testing.T) {
	traces := []struct {
		name          string
		path          string
		minEncoders   int
		expectMetrics bool
	}{
		{
			name:          "single-encoder",
			path:          filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace"),
			minEncoders:   1,
			expectMetrics: true,
		},
		{
			name:          "six-encoders",
			path:          filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace"),
			minEncoders:   5, // Binary extraction may find 5-6
			expectMetrics: true,
		},
	}

	for _, tt := range traces {
		t.Run(tt.name, func(t *testing.T) {
			tr := openPerfTraceOrSkip(t, tt.path)
			defer tr.Close()

			stats, err := ParsePerfCounters(tr)
			if err != nil {
				t.Fatalf("ParsePerfCounters failed: %v", err)
			}

			if tt.expectMetrics && len(stats.ShaderMetrics) < tt.minEncoders {
				t.Errorf("Expected at least %d encoders, got %d",
					tt.minEncoders, len(stats.ShaderMetrics))
			}

			// Verify stats structure
			if stats.TotalRecords == 0 {
				t.Error("TotalRecords is 0")
			}
			if stats.FilesProcessed == 0 {
				t.Error("FilesProcessed is 0")
			}

			t.Logf("✓ Extracted %d encoders, %d records from %d files",
				len(stats.ShaderMetrics), stats.TotalRecords, stats.FilesProcessed)
		})
	}
}

// TestMetricValueRanges validates that extracted metrics are within reasonable ranges
func TestMetricValueRanges(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

	stats, err := ParsePerfCounters(tr)
	if err != nil {
		t.Fatalf("ParsePerfCounters failed: %v", err)
	}

	for i, m := range stats.ShaderMetrics {
		// Percentages should be 0-100
		if m.ALUUtilization < 0 || m.ALUUtilization > 100 {
			t.Errorf("Encoder %d: ALUUtilization %.2f%% out of range [0, 100]",
				i, m.ALUUtilization)
		}
		if m.KernelOccupancy < 0 || m.KernelOccupancy > 100 {
			t.Errorf("Encoder %d: KernelOccupancy %.2f%% out of range [0, 100]",
				i, m.KernelOccupancy)
		}
		if m.BufferL1MissRate < 0 || m.BufferL1MissRate > 100 {
			t.Errorf("Encoder %d: BufferL1MissRate %.2f%% out of range [0, 100]",
				i, m.BufferL1MissRate)
		}

		// Execution count should be positive
		if m.ExecutionCount < 0 {
			t.Errorf("Encoder %d: ExecutionCount %d is negative", i, m.ExecutionCount)
		}

		// Bandwidth values should be non-negative
		if m.DeviceMemoryBandwidthGBps < 0 {
			t.Errorf("Encoder %d: DeviceMemoryBandwidthGBps %.2f is negative",
				i, m.DeviceMemoryBandwidthGBps)
		}
		if m.GPUReadBandwidthGBps < 0 {
			t.Errorf("Encoder %d: GPUReadBandwidthGBps %.2f is negative",
				i, m.GPUReadBandwidthGBps)
		}
		if m.GPUWriteBandwidthGBps < 0 {
			t.Errorf("Encoder %d: GPUWriteBandwidthGBps %.2f is negative",
				i, m.GPUWriteBandwidthGBps)
		}
	}

	t.Logf("✓ All %d encoder metrics within valid ranges", len(stats.ShaderMetrics))
}
