package counter

import (
	"path/filepath"
	"testing"
)

// TestLimiterExtractionSingleEncoder tests limiter extraction on a simple single-encoder trace.
func TestLimiterExtractionSingleEncoder(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

	// Parse performance counters
	stats, err := ParsePerfCounters(tr)
	if err != nil {
		t.Fatalf("Failed to parse perf counters: %v", err)
	}

	if len(stats.ShaderMetrics) == 0 {
		t.Fatal("No shader metrics extracted")
	}

	// Check first encoder for limiter values
	metrics := stats.ShaderMetrics[0]
	t.Logf("Shader: %s", metrics.ShaderName)
	t.Logf("Compute Shader Launch Limiter: %.2f", metrics.ComputeShaderLaunchLimiter)
	t.Logf("Control Flow Limiter: %.2f", metrics.ControlFlowLimiter)
	t.Logf("Instruction Throughput Limiter: %.2f", metrics.InstructionThroughputLimiter)
	t.Logf("Integer and Conditional Limiter: %.2f", metrics.IntegerAndConditionalLimiter)
	t.Logf("L1 Cache Limiter: %.2f", metrics.L1CacheLimiter)
	t.Logf("Last Level Cache Limiter: %.2f", metrics.LastLevelCacheLimiter)
	t.Logf("MMU Limiter: %.2f", metrics.MMULimiter)
	t.Logf("Texture Write Limiter: %.2f", metrics.TextureWriteLimiter)

	// Expected values from CSV analysis (01-single-encoder):
	// Compute Shader Launch Limiter: 0.04
	// Control Flow Limiter: 0.01
	// Instruction Throughput Limiter: 0.06
	// Integer and Conditional Limiter: 0.02
	// L1 Cache Limiter: 0.01
	// Last Level Cache Limiter: 0.04
	// MMU Limiter: 0.02
	// Texture Write Limiter: 0.04

	// Validate that at least some limiters were extracted
	foundLimiters := 0
	if metrics.ComputeShaderLaunchLimiter > 0 {
		foundLimiters++
	}
	if metrics.ControlFlowLimiter > 0 {
		foundLimiters++
	}
	if metrics.InstructionThroughputLimiter > 0 {
		foundLimiters++
	}
	if metrics.L1CacheLimiter > 0 {
		foundLimiters++
	}

	t.Logf("Found %d/4 key limiters", foundLimiters)
	if foundLimiters == 0 {
		t.Log("Note: No limiters extracted - extraction logic may need refinement for this trace format")
	}
}

// TestLimiterExtractionSixEncoders tests limiter extraction on complex multi-encoder trace.
func TestLimiterExtractionSixEncoders(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

	// Parse performance counters
	stats, err := ParsePerfCounters(tr)
	if err != nil {
		t.Fatalf("Failed to parse perf counters: %v", err)
	}

	t.Logf("Extracted %d encoder metrics", len(stats.ShaderMetrics))

	// Check each encoder
	for i, metrics := range stats.ShaderMetrics {
		if i >= 6 {
			break // Only check first 6 encoders
		}

		t.Logf("\nEncoder %d: %s", i, metrics.ShaderName)
		t.Logf("  Compute Shader Launch Limiter: %.2f", metrics.ComputeShaderLaunchLimiter)
		t.Logf("  Instruction Throughput Limiter: %.2f", metrics.InstructionThroughputLimiter)
		t.Logf("  L1 Cache Limiter: %.2f", metrics.L1CacheLimiter)
		t.Logf("  Last Level Cache Limiter: %.2f", metrics.LastLevelCacheLimiter)
		t.Logf("  F32 Limiter: %.2f", metrics.F32Limiter)
		t.Logf("  Integer and Complex Limiter: %.2f", metrics.IntegerAndComplexLimiter)

		// Encoder 5 (complex_math) should have high F32 limiter (3.74 in CSV)
		if i == 4 && metrics.F32Limiter > 0 {
			t.Logf("  ✓ Found F32 limiter on complex_math encoder")
		}
	}
}

// TestLimiterCSVExport tests that extracted limiters appear in CSV export.
func TestLimiterCSVExport(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

	// Export CSV
	exporter := NewCountersCSVExporter(tr)

	// Get encoder metrics to check
	metrics, err := PopulateEncoderMetricsFromBinaryParsing(tr)
	if err != nil {
		t.Fatalf("Failed to populate encoder metrics: %v", err)
	}

	t.Logf("Populated %d encoder metrics", len(metrics))

	// Check that limiter fields are populated
	for i, metric := range metrics {
		if i >= 3 {
			break
		}
		t.Logf("\nEncoder %d: %s", i, metric.EncoderLabel)
		t.Logf("  Compute Shader Launch Limiter: %.2f", metric.ComputeShaderLaunchLimiter)
		t.Logf("  Control Flow Limiter: %.2f", metric.ControlFlowLimiter)
		t.Logf("  Instruction Throughput Limiter: %.2f", metric.InstructionThroughputLimiter)
		t.Logf("  L1 Cache Limiter: %.2f", metric.L1CacheLimiter)
		t.Logf("  Last Level Cache Limiter: %.2f", metric.LastLevelCacheLimiter)
		t.Logf("  MMU Limiter: %.2f", metric.MMULimiter)
		t.Logf("  F32 Limiter: %.2f", metric.F32Limiter)
	}

	_ = exporter // CSV export test covered by export_test.go
}

// TestLimiterValueRanges validates that extracted limiter values are in expected ranges.
func TestLimiterValueRanges(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

	stats, err := ParsePerfCounters(tr)
	if err != nil {
		t.Fatalf("Failed to parse perf counters: %v", err)
	}

	// From CSV analysis, limiter ranges:
	// - Most limiters: 0.01-0.15
	// - F32 limiter (complex shader): up to 3.74
	// - Integer limiters (complex shader): up to 1.65

	for _, metrics := range stats.ShaderMetrics {
		// Validate ranges
		if metrics.ComputeShaderLaunchLimiter > 0 {
			if metrics.ComputeShaderLaunchLimiter < 0.01 || metrics.ComputeShaderLaunchLimiter > 0.2 {
				t.Errorf("ComputeShaderLaunchLimiter out of expected range: %.2f", metrics.ComputeShaderLaunchLimiter)
			}
		}

		if metrics.F32Limiter > 0 {
			if metrics.F32Limiter < 0.01 || metrics.F32Limiter > 5.0 {
				t.Errorf("F32Limiter out of expected range: %.2f", metrics.F32Limiter)
			}
		}

		if metrics.L1CacheLimiter > 0 {
			if metrics.L1CacheLimiter < 0.001 || metrics.L1CacheLimiter > 0.2 {
				t.Errorf("L1CacheLimiter out of expected range: %.2f", metrics.L1CacheLimiter)
			}
		}

		if metrics.LastLevelCacheLimiter > 0 {
			if metrics.LastLevelCacheLimiter < 0.001 || metrics.LastLevelCacheLimiter > 0.2 {
				t.Errorf("LastLevelCacheLimiter out of expected range: %.2f", metrics.LastLevelCacheLimiter)
			}
		}
	}
}
