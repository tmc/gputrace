package counter

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// TestFindLimiterOffsets scans binary data systematically to locate exact CSV limiter values.
// This implements "Approach B: Systematic Offset Scanning" from GPUTRACE67_SHADER_LIMITER_STATUS.md
func TestFindLimiterOffsets(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	// Expected limiter values from CSV (01-single-encoder-run1 Counters.csv)
	expectedLimiters := map[string]float32{
		"Compute Shader Launch Limiter":   0.04,
		"Control Flow Limiter":            0.01,
		"Instruction Throughput Limiter":  0.06,
		"Integer and Conditional Limiter": 0.02,
		"L1 Cache Limiter":                0.01,
		"Last Level Cache Limiter":        0.04,
		"MMU Limiter":                     0.02,
		"Texture Write Limiter":           0.04,
	}

	t.Log("Expected limiter values from CSV:")
	for name, val := range expectedLimiters {
		t.Logf("  %s: %.2f", name, val)
	}

	// Find the .gpuprofiler_raw directory
	// The structure is: trace.gputrace/ contains <name>.gpuprofiler_raw/
	entries, err := os.ReadDir(tr.Path)
	if err != nil {
		t.Fatalf("Failed to read gputrace directory: %v", err)
	}

	var perfDir string
	for _, entry := range entries {
		if entry.IsDir() && filepath.Ext(entry.Name()) == ".gpuprofiler_raw" {
			perfDir = filepath.Join(tr.Path, entry.Name())
			break
		}
	}

	if perfDir == "" {
		t.Fatal("Could not find .gpuprofiler_raw directory")
	}

	t.Logf("\nScanning binary files in: %s\n", perfDir)

	// Scan first 5 counter files
	for fileIdx := 0; fileIdx < 5; fileIdx++ {
		counterPath := filepath.Join(perfDir, fmt.Sprintf("Counters_f_%d.raw", fileIdx))
		file, err := os.Open(counterPath)
		if err != nil {
			continue
		}

		t.Logf("\n=== Counters_f_%d.raw ===", fileIdx)

		// Read entire file
		stat, _ := file.Stat()
		data := make([]byte, stat.Size())
		n, err := file.Read(data)
		file.Close()
		if err != nil || n < len(data) {
			continue
		}

		// Scan for each expected value with tolerance
		for limiterName, expectedVal := range expectedLimiters {
			matches := findExactFloatMatches(data, expectedVal, 0.005)
			if len(matches) > 0 {
				t.Logf("  ✓ Found %s (%.2f) at %d offset(s):", limiterName, expectedVal, len(matches))
				for i, offset := range matches {
					if i < 5 { // Show first 5 matches
						t.Logf("    0x%04x (%d bytes)", offset, offset)
					}
				}
				if len(matches) > 5 {
					t.Logf("    ... and %d more", len(matches)-5)
				}
			}
		}
	}
}

// TestScanMetadataRecords specifically checks 2.3-2.9KB metadata records for limiter values.
// This implements "Approach A: Metadata Record Analysis" from GPUTRACE67_SHADER_LIMITER_STATUS.md
func TestScanMetadataRecords(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	expectedLimiters := map[string]float32{
		"Compute Shader Launch": 0.04,
		"Control Flow":          0.01,
		"Instruction Throughput": 0.06,
		"Integer Conditional":   0.02,
		"L1 Cache":              0.01,
		"Last Level Cache":      0.04,
		"MMU":                   0.02,
		"Texture Write":         0.04,
	}

	// Find .gpuprofiler_raw directory
	entries, err := os.ReadDir(tr.Path)
	if err != nil {
		t.Fatalf("Failed to read directory: %v", err)
	}

	var perfDir string
	for _, entry := range entries {
		if entry.IsDir() && filepath.Ext(entry.Name()) == ".gpuprofiler_raw" {
			perfDir = filepath.Join(tr.Path, entry.Name())
			break
		}
	}

	if perfDir == "" {
		t.Fatal("Could not find .gpuprofiler_raw directory")
	}

	// Scan counter files and identify metadata vs sample records
	counterPath := filepath.Join(perfDir, "Counters_f_0.raw")
	file, err := os.Open(counterPath)
	if err != nil {
		t.Fatalf("Failed to open counter file: %v", err)
	}
	defer file.Close()

	stat, _ := file.Stat()
	t.Logf("Total file size: %d bytes", stat.Size())

	// Read and classify records
	offset := int64(0)
	recordNum := 0

	for offset < stat.Size() {
		// Read record type and size
		header := make([]byte, 8)
		n, err := file.ReadAt(header, offset)
		if err != nil || n < 8 {
			break
		}

		recordType := binary.LittleEndian.Uint32(header[0:4])
		recordSize := binary.LittleEndian.Uint32(header[4:8])

		if recordSize < 8 || recordSize > 10000 {
			// Invalid size, try fixed sizes
			if offset+464 <= stat.Size() {
				recordSize = 464
			} else {
				break
			}
		}

		// Read full record
		recordData := make([]byte, recordSize)
		n, err = file.ReadAt(recordData, offset)
		if err != nil || n < int(recordSize) {
			break
		}

		// Classify by size
		isMetadata := recordSize >= 2300 && recordSize <= 2900
		isSample := recordSize == 464

		if isMetadata || (isSample && recordNum < 5) {
			recordTypeStr := "unknown"
			if isMetadata {
				recordTypeStr = "METADATA"
			} else if isSample {
				recordTypeStr = "SAMPLE"
			}

			t.Logf("\nRecord #%d at offset 0x%04x: type=0x%08x size=%d [%s]",
				recordNum, offset, recordType, recordSize, recordTypeStr)

			// Search for limiter values in this record
			foundAny := false
			for name, val := range expectedLimiters {
				matches := findExactFloatMatches(recordData, val, 0.005)
				if len(matches) > 0 {
					if !foundAny {
						t.Log("  Found limiter values:")
						foundAny = true
					}
					t.Logf("    %s (%.2f) at offsets: %v", name, val, matches[:min(3, len(matches))])
				}
			}
			if !foundAny && isMetadata {
				t.Log("  No limiter values found in this metadata record")
			}
		}

		offset += int64(recordSize)
		recordNum++

		// Limit to first 10 records for this test
		if recordNum >= 10 {
			break
		}
	}
}

// findExactFloatMatches finds all offsets where a float32 value matches the target within tolerance.
func findExactFloatMatches(data []byte, target float32, tolerance float32) []int {
	var matches []int

	for i := 0; i <= len(data)-4; i++ {
		bits := binary.LittleEndian.Uint32(data[i : i+4])
		val := math.Float32frombits(bits)

		// Check if value matches within tolerance and is not NaN/Inf
		if !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) {
			diff := val - target
			if diff < 0 {
				diff = -diff
			}
			if diff <= tolerance {
				matches = append(matches, i)
			}
		}
	}

	return matches
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
