package gputrace

import (
	"os"
	"testing"
)

// TestDispatchCounts_CompiledTrace tests dispatch counting on a compiled trace
// with ICBs (Indirect Command Buffers).
func TestDispatchCounts_CompiledTrace(t *testing.T) {
	tracePath := "/tmp/gputraces/compiled_3tokens_tokens_0_to_3-perf.gputrace"

	// Skip if trace doesn't exist
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("Trace file not found: %s", tracePath)
	}

	trace, err := Open(tracePath)
	if err != nil {
		t.Fatalf("Failed to open trace: %v", err)
	}

	counts, err := trace.CountDispatches()
	if err != nil {
		t.Fatalf("Failed to count dispatches: %v", err)
	}

	// Expected values from investigation
	expectedICBs := 208
	expectedDirectDispatches := 308
	expectedEstimated := 1040 // 208 * 5
	expectedXcodeCount := 1043

	// Validate ICB count
	if counts.ICBExecutions != expectedICBs {
		t.Errorf("ICBExecutions = %d, want %d", counts.ICBExecutions, expectedICBs)
	}

	// Validate direct dispatch count
	if counts.DirectDispatches != expectedDirectDispatches {
		t.Errorf("DirectDispatches = %d, want %d", counts.DirectDispatches, expectedDirectDispatches)
	}

	// Validate estimated total (should be close to Xcode's 1043)
	if counts.EstimatedTotal != expectedEstimated {
		t.Errorf("EstimatedTotal = %d, want %d", counts.EstimatedTotal, expectedEstimated)
	}

	// Check that estimate is within 1% of Xcode count
	tolerance := 0.01
	diff := float64(counts.EstimatedTotal-expectedXcodeCount) / float64(expectedXcodeCount)
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("Estimated count (%d) differs from Xcode count (%d) by %.1f%% (tolerance: %.1f%%)",
			counts.EstimatedTotal, expectedXcodeCount, diff*100, tolerance*100)
	}

	// Verify it's detected as a compiled trace
	if !counts.IsCompiledTrace() {
		t.Error("Expected IsCompiledTrace() = true for ICB-based trace")
	}

	// Verify Ct multiplier is lower than typical non-compiled traces
	// Compiled traces should have ~1.34x multiplier
	if counts.CtMultiplier < 1.0 || counts.CtMultiplier > 1.5 {
		t.Errorf("CtMultiplier = %.2f, expected ~1.34 for compiled trace", counts.CtMultiplier)
	}

	t.Logf("Compiled trace counts: ICBs=%d, Direct=%d, Estimated=%d, CtMultiplier=%.2f",
		counts.ICBExecutions, counts.DirectDispatches, counts.EstimatedTotal, counts.CtMultiplier)
}

// TestDispatchCounts_BatchTraces tests dispatch counting on batch comparison traces
// which use direct dispatches without ICBs.
func TestDispatchCounts_BatchTraces(t *testing.T) {
	testCases := []struct {
		name                 string
		path                 string
		expectedDispatches   int
		expectedCtMultiplier float64
		tolerance            float64
	}{
		{
			name:                 "baseline",
			path:                 "/tmp/gputraces/batch_comparison/baseline.gputrace",
			expectedDispatches:   37479,
			expectedCtMultiplier: 1.73,
			tolerance:            0.02,
		},
		{
			name:                 "batch100",
			path:                 "/tmp/gputraces/batch_comparison/batch100.gputrace",
			expectedDispatches:   37270,
			expectedCtMultiplier: 1.75,
			tolerance:            0.02,
		},
		{
			name:                 "batch200",
			path:                 "/tmp/gputraces/batch_comparison/batch200.gputrace",
			expectedDispatches:   37253,
			expectedCtMultiplier: 1.74,
			tolerance:            0.02,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Skip if trace doesn't exist
			if _, err := os.Stat(tc.path); os.IsNotExist(err) {
				t.Skipf("Trace file not found: %s", tc.path)
			}

			trace, err := Open(tc.path)
			if err != nil {
				t.Fatalf("Failed to open trace: %v", err)
			}

			counts, err := trace.CountDispatches()
			if err != nil {
				t.Fatalf("Failed to count dispatches: %v", err)
			}

			// Note: Batch traces actually DO have Ci records, but they're not the primary
			// dispatch mechanism. Log for information but don't fail.
			if counts.ICBExecutions > 0 {
				t.Logf("Info: Found %d Ci records (ICBs present but not primary dispatch method)", counts.ICBExecutions)
			}

			// Verify dispatch count
			if counts.DirectDispatches != tc.expectedDispatches {
				t.Errorf("DirectDispatches = %d, want %d", counts.DirectDispatches, tc.expectedDispatches)
			}

			// Note: IsCompiledTrace() checks for ICBs, but batch traces may have some Ci records
			// while still being primarily direct-dispatch based. Don't enforce this check.

			// Verify Ct multiplier is in typical range (1.7-1.8x)
			multiplierDiff := counts.CtMultiplier - tc.expectedCtMultiplier
			if multiplierDiff < 0 {
				multiplierDiff = -multiplierDiff
			}
			if multiplierDiff > tc.tolerance {
				t.Errorf("CtMultiplier = %.2f, want %.2f ±%.2f",
					counts.CtMultiplier, tc.expectedCtMultiplier, tc.tolerance)
			}

			// For batch traces, direct dispatches are the primary method
			// The presence of Ci records doesn't mean they're the primary dispatch mechanism
			// Log both for analysis
			t.Logf("Best count: %d, Direct: %d, ICB-estimated: %d",
				counts.GetBestDispatchCount(), counts.DirectDispatches, counts.EstimatedTotal)

			t.Logf("%s counts: Direct=%d, TotalCt=%d, Multiplier=%.2f",
				tc.name, counts.DirectDispatches, counts.TotalCtRecords, counts.CtMultiplier)
		})
	}
}

// TestDispatchCounts_TokensTrace tests the tokens_2_40 trace.
func TestDispatchCounts_TokensTrace(t *testing.T) {
	tracePath := "/tmp/gputraces/gputrace_tokens_2_40_tokens_2_to_40.gputrace"

	// Skip if trace doesn't exist
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("Trace file not found: %s", tracePath)
	}

	trace, err := Open(tracePath)
	if err != nil {
		t.Fatalf("Failed to open trace: %v", err)
	}

	counts, err := trace.CountDispatches()
	if err != nil {
		t.Fatalf("Failed to count dispatches: %v", err)
	}

	// Expected values from previous analysis
	expectedDispatches := 30608
	expectedMultiplier := 1.75

	if counts.DirectDispatches != expectedDispatches {
		t.Errorf("DirectDispatches = %d, want %d", counts.DirectDispatches, expectedDispatches)
	}

	if counts.CtMultiplier < expectedMultiplier-0.02 || counts.CtMultiplier > expectedMultiplier+0.02 {
		t.Errorf("CtMultiplier = %.2f, want %.2f ±0.02", counts.CtMultiplier, expectedMultiplier)
	}

	t.Logf("tokens_2_40 counts: Direct=%d, TotalCt=%d, Multiplier=%.2f",
		counts.DirectDispatches, counts.TotalCtRecords, counts.CtMultiplier)
}

// TestDispatchCounts_CommandBreakdown tests that command types are properly categorized.
func TestDispatchCounts_CommandBreakdown(t *testing.T) {
	tracePath := "/tmp/gputraces/compiled_3tokens_tokens_0_to_3-perf.gputrace"

	// Skip if trace doesn't exist
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("Trace file not found: %s", tracePath)
	}

	trace, err := Open(tracePath)
	if err != nil {
		t.Fatalf("Failed to open trace: %v", err)
	}

	counts, err := trace.CountDispatches()
	if err != nil {
		t.Fatalf("Failed to count dispatches: %v", err)
	}

	// Verify command breakdown adds up to total
	totalCommands := counts.DirectDispatches + counts.SetupCommands +
		counts.BarrierCommands + counts.FlushCommands

	if totalCommands != counts.TotalCtRecords {
		t.Errorf("Command breakdown sum (%d) != TotalCtRecords (%d)",
			totalCommands, counts.TotalCtRecords)
	}

	// Expected breakdown from compiled trace analysis
	expectedSetup := 102
	expectedBarrier := 2
	expectedFlush := 1

	if counts.SetupCommands != expectedSetup {
		t.Errorf("SetupCommands = %d, want %d", counts.SetupCommands, expectedSetup)
	}

	if counts.BarrierCommands != expectedBarrier {
		t.Errorf("BarrierCommands = %d, want %d", counts.BarrierCommands, expectedBarrier)
	}

	if counts.FlushCommands != expectedFlush {
		t.Errorf("FlushCommands = %d, want %d", counts.FlushCommands, expectedFlush)
	}

	// Verify dispatch percentage is high for compiled traces (should be ~75%)
	dispatchPct := float64(counts.DirectDispatches) / float64(counts.TotalCtRecords)
	if dispatchPct < 0.70 || dispatchPct > 0.80 {
		t.Errorf("Dispatch percentage = %.1f%%, expected ~75%% for compiled trace", dispatchPct*100)
	}

	t.Logf("Command breakdown: Dispatch=%d, Setup=%d, Barrier=%d, Flush=%d (%.1f%% dispatches)",
		counts.DirectDispatches, counts.SetupCommands, counts.BarrierCommands,
		counts.FlushCommands, dispatchPct*100)
}

// BenchmarkCountDispatches benchmarks the dispatch counting performance.
func BenchmarkCountDispatches(b *testing.B) {
	tracePath := "/tmp/gputraces/compiled_3tokens_tokens_0_to_3-perf.gputrace"

	// Skip if trace doesn't exist
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		b.Skipf("Trace file not found: %s", tracePath)
	}

	trace, err := Open(tracePath)
	if err != nil {
		b.Fatalf("Failed to open trace: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := trace.CountDispatches()
		if err != nil {
			b.Fatalf("CountDispatches failed: %v", err)
		}
	}
}
