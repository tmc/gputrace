package counter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// TestDebugFloatSearch scans for all float values in expected limiter ranges.
func TestDebugFloatSearch(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	// Find .gpuprofiler_raw directory
	perfDir := tr.Path + ".gpuprofiler_raw"

	// Read first counter file
	counterPath := filepath.Join(perfDir, "Counters_f_0.raw")
	file, err := os.Open(counterPath)
	if err != nil {
		t.Fatalf("Failed to open counter file: %v", err)
	}
	defer file.Close()

	// Read first 464-byte record
	data := make([]byte, 464)
	n, err := file.Read(data)
	if err != nil || n < 464 {
		t.Fatalf("Failed to read 464 bytes: %v", err)
	}

	t.Log("Scanning for float32 values in expected limiter range (0.001 - 5.0):")

	// Find all floats in limiter range
	floats := findAllFloatsInRange(data, 0.001, 5.0, 50)

	t.Logf("Found %d float values:", len(floats))
	for i, val := range floats {
		t.Logf("  [%d] %.4f", i, val)
	}

	// Also scan for exact expected values from CSV
	expectedValues := []float64{0.01, 0.02, 0.03, 0.04, 0.06, 0.08}
	t.Log("\nSearching for exact CSV values:")
	for _, expected := range expectedValues {
		found := false
		for _, val := range floats {
			if val >= expected-0.001 && val <= expected+0.001 {
				t.Logf("  ✓ Found value near %.2f: %.4f", expected, val)
				found = true
				break
			}
		}
		if !found {
			t.Logf("  ✗ Did not find value near %.2f", expected)
		}
	}
}

// TestDebugAllCounterFiles scans all counter files for float patterns.
func TestDebugAllCounterFiles(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	perfDir := tr.Path + ".gpuprofiler_raw"

	// Check first 5 counter files
	for i := 0; i < 5; i++ {
		counterPath := filepath.Join(perfDir, "Counters_f_"+string(rune('0'+i))+".raw")
		file, err := os.Open(counterPath)
		if err != nil {
			continue
		}

		// Read first record
		data := make([]byte, 464)
		n, err := file.Read(data)
		file.Close()

		if err != nil || n < 464 {
			continue
		}

		// Find floats
		floats := findAllFloatsInRange(data, 0.001, 5.0, 20)
		t.Logf("Counters_f_%d.raw: found %d floats", i, len(floats))
		if len(floats) > 0 {
			t.Logf("  First 5: %.4f %.4f %.4f %.4f %.4f",
				floats[0],
				getOrZero(floats, 1),
				getOrZero(floats, 2),
				getOrZero(floats, 3),
				getOrZero(floats, 4))
		}
	}
}

func getOrZero(floats []float64, index int) float64 {
	if index < len(floats) {
		return floats[index]
	}
	return 0.0
}
