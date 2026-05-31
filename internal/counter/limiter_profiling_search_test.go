package counter

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestSearchProfilingFiles searches Profiling_f_*.raw files for limiter values.
func TestSearchProfilingFiles(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

	expectedLimiters := map[string]float32{
		"Compute Shader Launch":  0.04,
		"Control Flow":           0.01,
		"Instruction Throughput": 0.06,
		"Integer Conditional":    0.02,
		"L1 Cache":               0.01,
		"Last Level Cache":       0.04,
		"MMU":                    0.02,
		"Texture Write":          0.04,
	}

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

	t.Log("Searching Profiling_f_*.raw files for limiter values...\n")

	// Search first 3 profiling files
	for fileIdx := 0; fileIdx < 3; fileIdx++ {
		profilingPath := filepath.Join(perfDir, fmt.Sprintf("Profiling_f_%d.raw", fileIdx))
		data, err := os.ReadFile(profilingPath)
		if err != nil {
			continue
		}

		t.Logf("=== Profiling_f_%d.raw (%d bytes) ===", fileIdx, len(data))

		// Search for each expected value
		foundAny := false
		for limiterName, expectedVal := range expectedLimiters {
			matches := findFloat32Matches(data, expectedVal, 0.005)
			if len(matches) > 0 {
				if !foundAny {
					foundAny = true
				}
				t.Logf("  ✓ Found %s (%.2f) at %d offset(s):", limiterName, expectedVal, len(matches))
				for i, offset := range matches {
					if i < 3 {
						t.Logf("    0x%06x (%d bytes)", offset, offset)
					}
				}
				if len(matches) > 3 {
					t.Logf("    ... and %d more", len(matches)-3)
				}
			}
		}

		if !foundAny {
			t.Log("  No limiter values found")
		}
		t.Log("")
	}
}

// TestProfilingFileStructure examines the structure of Profiling files.
func TestProfilingFileStructure(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr := openPerfTraceOrSkip(t, tracePath)
	defer tr.Close()

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

	profilingPath := filepath.Join(perfDir, "Profiling_f_0.raw")
	data, err := os.ReadFile(profilingPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	t.Logf("Profiling_f_0.raw: %d bytes", len(data))

	// Try to identify record structure
	if len(data) >= 16 {
		t.Log("\nFirst 64 bytes (hex):")
		for i := 0; i < min(64, len(data)); i += 16 {
			hexStr := ""
			for j := 0; j < 16 && i+j < len(data); j++ {
				hexStr += fmt.Sprintf("%02x ", data[i+j])
			}
			t.Logf("  %04x: %s", i, hexStr)
		}

		t.Log("\nFirst 16 uint32 values:")
		for i := 0; i < min(64, len(data)); i += 4 {
			val := binary.LittleEndian.Uint32(data[i : i+4])
			t.Logf("  +%04x: %10d (0x%08x)", i, val, val)
		}

		t.Log("\nScanning for float32 values in limiter range (first 1KB):")
		count := 0
		for i := 0; i < min(1024, len(data)-4); i += 4 {
			bits := binary.LittleEndian.Uint32(data[i : i+4])
			val := math.Float32frombits(bits)
			if !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) && val >= 0.001 && val <= 5.0 {
				t.Logf("  +%04x: %.6f", i, val)
				count++
				if count >= 20 {
					break
				}
			}
		}
	}
}

func findFloat32Matches(data []byte, target float32, tolerance float32) []int {
	var matches []int

	for i := 0; i <= len(data)-4; i++ {
		bits := binary.LittleEndian.Uint32(data[i : i+4])
		val := math.Float32frombits(bits)

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
