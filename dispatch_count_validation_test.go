package gputrace

import (
	"testing"
)

// TestDispatchCountsAgainstXcode validates that our dispatch counting
// matches Xcode's ground truth for known traces.
//
// This test will FAIL until we parse enough gputrace data to get accurate counts.
// Currently we only parse the 'capture' file, but we need to parse:
// - Large hash-named files (e.g., 78AF83698D5BA963 - 97MB)
// - Store files (store0, store1, etc.)
// - Performance counter files (Counters_f_*.raw)
//
// Expected behavior:
// - compiled_3tokens: 1,043 dispatches (208 Ci records)
// - bench_4tokens: 2,281 dispatches (90 Ci records)
//
// The varying ICB expansion factor (5× vs 25×) indicates we need to
// parse actual execution data, not just command structure.
func TestDispatchCountsAgainstXcode(t *testing.T) {
	tests := []struct {
		name               string
		tracePath          string
		expectedDispatches int
		notes              string
	}{
		{
			name:               "compiled_3tokens",
			tracePath:          "/tmp/gputraces/compiled_3tokens_tokens_0_to_3.gputrace",
			expectedDispatches: 1043,
			notes:              "Xcode: 1043 dispatches, 27.73ms, 32 command buffers, 28 encoders",
		},
		{
			name:               "bench_4tokens",
			tracePath:          "/tmp/gputraces/bench_4tokens.gputrace",
			expectedDispatches: 2281,
			notes:              "Xcode: 2281 dispatches - shows 25× ICB expansion vs 5× for compiled_3tokens",
		},
		{
			name:               "swift_first3tokens",
			tracePath:          "/tmp/gputraces/swift-first3tokens.gputrace",
			expectedDispatches: 269,
			notes:              "Xcode: 269 dispatches, 8 command buffers, 7 compute encoders",
		},
		{
			name:               "python_first3tokens_50total",
			tracePath:          "/tmp/gputraces/python-first3tokens-50total.gputrace",
			expectedDispatches: 1362,
			notes:              "Xcode: 1362 dispatches, 35 command buffers, 32 compute encoders",
		},
		{
			name:               "debug_3tokens",
			tracePath:          "/tmp/gputraces/debug_3tokens.gputrace",
			expectedDispatches: 2609,
			notes:              "Xcode: 2609 dispatches, 77 command buffers, 58 compute encoders",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace, err := Open(tt.tracePath)
			if err != nil {
				t.Skipf("Trace file not available: %v", err)
				return
			}

			// Count dispatches - this needs to parse ALL files in the gputrace
			count, err := trace.CountActualDispatches()
			if err != nil {
				t.Fatalf("Failed to count dispatches: %v", err)
			}

			if count != tt.expectedDispatches {
				t.Errorf("%s: got %d dispatches, want %d (Xcode ground truth)",
					tt.name, count, tt.expectedDispatches)
				t.Logf("Notes: %s", tt.notes)
				t.Logf("")
				t.Logf("This indicates we're not parsing enough gputrace data!")
				t.Logf("Currently parsing: 'capture' file only")
				t.Logf("Missing data sources:")
				t.Logf("  - Hash-named files (e.g., 78AF83698D5BA963 - 97MB)")
				t.Logf("  - Store files (store0, store1)")
				t.Logf("  - Performance counter files (Counters_f_*.raw)")
				t.Logf("")
				t.Logf("The varying ICB expansion factor (5× to 25×) proves we need")
				t.Logf("to parse actual execution data, not estimate from command structure.")
			} else {
				t.Logf("✅ %s: correctly counted %d dispatches", tt.name, count)
			}
		})
	}
}

// NOTE: CountActualDispatches() is now implemented in gputrace.go
// This test validates that implementation against Xcode ground truth.
