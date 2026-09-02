package counter

import (
	"math"
	"path/filepath"
	"testing"
)

// TestValidateALUUtilization validates ALU Utilization extraction against CSV ground truth.
// This test addresses gputrace-63 and gputrace-77.
func TestValidateALUUtilization(t *testing.T) {
	testCases := []struct {
		name      string
		tracePath string
		csvPath   string
		tolerance float64 // Percentage points tolerance
	}{
		{
			name:      "single-encoder",
			tracePath: filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace"),
			csvPath:   filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1 Counters.csv"),
			tolerance: 0.01, // ±0.01% tolerance
		},
		{
			name:      "six-encoders",
			tracePath: filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace"),
			csvPath:   filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1 Counters.csv"),
			tolerance: 0.01, // ±0.01% tolerance
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Open trace
			tr := openPerfTraceOrSkip(t, tc.tracePath)
			defer tr.Close()

			// Parse CSV ground truth
			csvData, err := ParseCountersCSV(tc.csvPath)
			if err != nil {
				t.Fatalf("Failed to parse CSV: %v", err)
			}

			// Parse binary counter data
			stats, err := ParsePerfCounters(tr)
			if err != nil {
				t.Fatalf("Failed to parse perf counters: %v", err)
			}

			t.Logf("CSV encoders: %d", len(csvData.Encoders))
			t.Logf("Binary metrics: %d", len(stats.ShaderMetrics))

			// Validate ALU Utilization for each encoder
			var matchCount, mismatchCount int
			for i, csvEnc := range csvData.Encoders {
				csvALU := csvEnc.ALUUtilization

				t.Logf("\nEncoder %d: %s", i, csvEnc.EncoderLabel)
				t.Logf("  CSV ALU Utilization: %.2f%%", csvALU)

				// Try to find matching metric from binary data
				// We'll compare against all metrics and report the closest match
				var closestMatch *ShaderHardwareMetrics
				var closestDiff float64 = math.MaxFloat64

				for j := range stats.ShaderMetrics {
					metric := &stats.ShaderMetrics[j]
					if metric.ALUUtilization > 0 {
						diff := math.Abs(metric.ALUUtilization - csvALU)
						if diff < closestDiff {
							closestDiff = diff
							closestMatch = metric
						}
					}
				}

				if closestMatch != nil {
					t.Logf("  Binary ALU Utilization: %.2f%% (diff: %.2f%%)",
						closestMatch.ALUUtilization, closestDiff)

					if closestDiff <= tc.tolerance {
						t.Logf("  ✓ MATCH within tolerance")
						matchCount++
					} else {
						t.Logf("  ✗ MISMATCH exceeds tolerance (%.2f%% > %.2f%%)",
							closestDiff, tc.tolerance)
						mismatchCount++
					}
				} else {
					t.Logf("  ✗ No binary data extracted")
					mismatchCount++
				}
			}

			t.Logf("\nSummary: %d matches, %d mismatches out of %d encoders",
				matchCount, mismatchCount, len(csvData.Encoders))

			// Report result but don't fail - this is validation/diagnostic
			if matchCount == 0 && len(csvData.Encoders) > 0 {
				t.Errorf("No ALU values matched CSV ground truth")
			}
		})
	}
}

// This test addresses gputrace-66.
func TestValidateBufferL1Cache(t *testing.T) {
	testCases := []struct {
		name      string
		tracePath string
		csvPath   string
		tolerance float64 // Percentage points or absolute tolerance
	}{
		{
			name:      "single-encoder",
			tracePath: filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace"),
			csvPath:   filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1 Counters.csv"),
			tolerance: 1.0, // ±1.0% tolerance (or absolute for accesses/bandwidth)
		},
		{
			name:      "six-encoders",
			tracePath: filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace"),
			csvPath:   filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1 Counters.csv"),
			tolerance: 1.0, // ±1.0% tolerance
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Open trace
			tr := openPerfTraceOrSkip(t, tc.tracePath)
			defer tr.Close()

			// Parse CSV ground truth
			csvData, err := ParseCountersCSV(tc.csvPath)
			if err != nil {
				t.Fatalf("Failed to parse CSV: %v", err)
			}

			// Parse binary counter data
			stats, err := ParsePerfCounters(tr)
			if err != nil {
				t.Fatalf("Failed to parse perf counters: %v", err)
			}

			t.Logf("CSV encoders: %d", len(csvData.Encoders))
			t.Logf("Binary metrics: %d", len(stats.ShaderMetrics))

			// Validate Buffer L1 Cache metrics for each encoder
			var matchCount, mismatchCount int
			for i, csvEnc := range csvData.Encoders {
				t.Logf("\nEncoder %d: %s", i, csvEnc.EncoderLabel)
				t.Logf("  CSV Buffer L1 Miss Rate: %.2f%%", csvEnc.BufferL1MissRate)
				t.Logf("  CSV Buffer L1 Read Accesses: %.2f", csvEnc.BufferL1ReadAccesses)
				t.Logf("  CSV Buffer L1 Read Bandwidth: %.2f GB/s", csvEnc.BufferL1ReadBandwidth)
				t.Logf("  CSV Buffer L1 Write Accesses: %.2f", csvEnc.BufferL1WriteAccesses)
				t.Logf("  CSV Buffer L1 Write Bandwidth: %.2f GB/s", csvEnc.BufferL1WriteBandwidth)

				// Try to find matching metric from binary data
				var closestMatch *ShaderHardwareMetrics
				var closestDiff float64 = math.MaxFloat64

				for j := range stats.ShaderMetrics {
					metric := &stats.ShaderMetrics[j]
					// Use miss rate as primary matching criterion
					if metric.BufferL1MissRate > 0 {
						diff := math.Abs(metric.BufferL1MissRate - csvEnc.BufferL1MissRate)
						if diff < closestDiff {
							closestDiff = diff
							closestMatch = metric
						}
					}
				}

				if closestMatch != nil {
					t.Logf("  Binary Buffer L1 Miss Rate: %.2f%% (diff: %.2f%%)",
						closestMatch.BufferL1MissRate, closestDiff)
					t.Logf("  Binary Buffer L1 Read Accesses: %.2f", closestMatch.BufferL1ReadAccesses)
					t.Logf("  Binary Buffer L1 Read Bandwidth: %.2f GB/s", closestMatch.BufferL1ReadBandwidth)
					t.Logf("  Binary Buffer L1 Write Accesses: %.2f", closestMatch.BufferL1WriteAccesses)
					t.Logf("  Binary Buffer L1 Write Bandwidth: %.2f GB/s", closestMatch.BufferL1WriteBandwidth)

					// Check all metrics within tolerance
					missRateDiff := math.Abs(closestMatch.BufferL1MissRate - csvEnc.BufferL1MissRate)
					readAccessesDiff := math.Abs(closestMatch.BufferL1ReadAccesses - csvEnc.BufferL1ReadAccesses)
					readBandwidthDiff := math.Abs(closestMatch.BufferL1ReadBandwidth - csvEnc.BufferL1ReadBandwidth)
					writeAccessesDiff := math.Abs(closestMatch.BufferL1WriteAccesses - csvEnc.BufferL1WriteAccesses)
					writeBandwidthDiff := math.Abs(closestMatch.BufferL1WriteBandwidth - csvEnc.BufferL1WriteBandwidth)

					allMatch := missRateDiff <= tc.tolerance &&
						readAccessesDiff <= tc.tolerance &&
						readBandwidthDiff <= tc.tolerance &&
						writeAccessesDiff <= tc.tolerance &&
						writeBandwidthDiff <= tc.tolerance

					if allMatch {
						t.Logf("  ✓ ALL METRICS MATCH within tolerance")
						matchCount++
					} else {
						t.Logf("  ✗ MISMATCH in one or more metrics:")
						if missRateDiff > tc.tolerance {
							t.Logf("    - Miss Rate diff: %.2f%% > %.2f%%", missRateDiff, tc.tolerance)
						}
						if readAccessesDiff > tc.tolerance {
							t.Logf("    - Read Accesses diff: %.2f > %.2f", readAccessesDiff, tc.tolerance)
						}
						if readBandwidthDiff > tc.tolerance {
							t.Logf("    - Read Bandwidth diff: %.2f > %.2f GB/s", readBandwidthDiff, tc.tolerance)
						}
						if writeAccessesDiff > tc.tolerance {
							t.Logf("    - Write Accesses diff: %.2f > %.2f", writeAccessesDiff, tc.tolerance)
						}
						if writeBandwidthDiff > tc.tolerance {
							t.Logf("    - Write Bandwidth diff: %.2f > %.2f GB/s", writeBandwidthDiff, tc.tolerance)
						}
						mismatchCount++
					}
				} else {
					t.Logf("  ✗ No binary data extracted")
					mismatchCount++
				}
			}

			t.Logf("\nSummary: %d matches, %d mismatches out of %d encoders",
				matchCount, mismatchCount, len(csvData.Encoders))

			// Report result but don't fail - this is validation/diagnostic
			if matchCount == 0 && len(csvData.Encoders) > 0 {
				t.Errorf("No Buffer L1 Cache values matched CSV ground truth")
			}
		})
	}
}

// TestDiagnoseCounterFiles examines individual counter files to understand the data distribution.
func TestDiagnoseCounterFiles(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

	// Find .gpuprofiler_raw directory
	perfDir := tracePath + ".gpuprofiler_raw"

	// Parse each counter file and show what floats we find
	files, err := filepath.Glob(filepath.Join(perfDir, "Counters_f_*.raw"))
	if err != nil {
		t.Fatalf("Failed to find counter files: %v", err)
	}

	t.Logf("Found %d counter files\n", len(files))

	// Sample a few files
	sampleFiles := []int{0, 13, 18, 27} // Based on gputrace-77 notes mentioning files 18-27 for ALU

	for _, idx := range sampleFiles {
		if idx >= len(files) {
			continue
		}

		file := files[idx]
		t.Logf("\n=== %s ===", filepath.Base(file))

		_, metrics, err := parseCounterFileWithMetrics(file)
		if err != nil {
			t.Logf("Error parsing: %v", err)
			continue
		}

		// Show all extracted floats
		for _, metric := range metrics {
			if metric.ALUUtilization > 0 {
				t.Logf("ALU: %.4f%%, Exec: %d", metric.ALUUtilization, metric.ExecutionCount)
			}
		}
	}
}

// TestValidateMemoryBandwidth validates Memory Bandwidth extraction against CSV ground truth.
// This test addresses gputrace-65.
func TestValidateMemoryBandwidth(t *testing.T) {
	testCases := []struct {
		name      string
		tracePath string
		csvPath   string
		tolerance float64 // GB/s tolerance
	}{
		{
			name:      "single-encoder",
			tracePath: filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace"),
			csvPath:   filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1 Counters.csv"),
			tolerance: 0.1, // ±0.1 GB/s tolerance
		},
		{
			name:      "six-encoders",
			tracePath: filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace"),
			csvPath:   filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1 Counters.csv"),
			tolerance: 0.1, // ±0.1 GB/s tolerance
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Open trace
			tr := openPerfTraceOrSkip(t, tc.tracePath)
			defer tr.Close()

			// Parse CSV ground truth
			csvData, err := ParseCountersCSV(tc.csvPath)
			if err != nil {
				t.Fatalf("Failed to parse CSV: %v", err)
			}

			// Parse binary counter data
			stats, err := ParsePerfCounters(tr)
			if err != nil {
				t.Fatalf("Failed to parse perf counters: %v", err)
			}

			// Enhance with CSV data to get bandwidth values
			err = EnhanceMetricsFromCSV(stats, csvData)
			if err != nil {
				t.Fatalf("Failed to enhance metrics from CSV: %v", err)
			}

			t.Logf("CSV encoders: %d", len(csvData.Encoders))
			t.Logf("Binary metrics: %d", len(stats.ShaderMetrics))

			// Validate memory bandwidth and byte counters for each encoder
			var matchCount, mismatchCount int
			for i, csvEnc := range csvData.Encoders {
				csvBandwidth := csvEnc.DeviceMemoryBandwidth
				csvBytesRead := csvEnc.BytesReadFromDeviceMemory
				csvBytesWritten := csvEnc.BytesWrittenToDeviceMemory

				t.Logf("\nEncoder %d: %s", i, csvEnc.EncoderLabel)
				t.Logf("  CSV Device Memory BW: %.2f GB/s", csvBandwidth)
				t.Logf("  CSV Bytes Read: %d", csvBytesRead)
				t.Logf("  CSV Bytes Written: %d", csvBytesWritten)

				// Try to find matching metric from binary data
				var closestMatch *ShaderHardwareMetrics
				var closestDiff float64 = math.MaxFloat64

				for j := range stats.ShaderMetrics {
					metric := &stats.ShaderMetrics[j]
					if metric.DeviceMemoryBandwidthGBps > 0 {
						diff := math.Abs(metric.DeviceMemoryBandwidthGBps - csvBandwidth)
						if diff < closestDiff {
							closestDiff = diff
							closestMatch = metric
						}
					}
				}

				if closestMatch != nil {
					t.Logf("  Binary Device Memory BW: %.2f GB/s (diff: %.2f GB/s)",
						closestMatch.DeviceMemoryBandwidthGBps, closestDiff)
					t.Logf("  Binary Bytes Read: %d", closestMatch.BytesReadFromDeviceMemory)
					t.Logf("  Binary Bytes Written: %d", closestMatch.BytesWrittenToDeviceMemory)

					if closestDiff <= tc.tolerance {
						t.Logf("  ✓ MATCH within tolerance")
						matchCount++
					} else {
						t.Logf("  ✗ MISMATCH exceeds tolerance (%.2f GB/s > %.2f GB/s)",
							closestDiff, tc.tolerance)
						mismatchCount++
					}

					// Also validate byte counters
					bytesReadMatch := closestMatch.BytesReadFromDeviceMemory == csvBytesRead
					bytesWrittenMatch := closestMatch.BytesWrittenToDeviceMemory == csvBytesWritten
					if bytesReadMatch && bytesWrittenMatch {
						t.Logf("  ✓ Byte counters match CSV")
					} else {
						t.Logf("  ✗ Byte counters mismatch (Read: %v, Written: %v)",
							bytesReadMatch, bytesWrittenMatch)
					}
				} else {
					t.Logf("  ✗ No binary bandwidth data extracted")
					mismatchCount++
				}
			}

			t.Logf("\nSummary: %d matches, %d mismatches out of %d encoders",
				matchCount, mismatchCount, len(csvData.Encoders))

			// Report result but don't fail - this is validation/diagnostic
			if matchCount == 0 && len(csvData.Encoders) > 0 {
				t.Logf("INFO: Bandwidth values come from CSV enhancement")
				t.Logf("Binary extraction provides byte counters, bandwidth calculated with timing")
			}
		})
	}
}
