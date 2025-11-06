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

// TestExamineSpecificOffsets examines the specific offsets where limiter values were found.
func TestExamineSpecificOffsets(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

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

	// Read first counter file
	counterPath := filepath.Join(perfDir, "Counters_f_1.raw")
	data, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	t.Logf("File size: %d bytes (%d complete 464-byte records)", len(data), len(data)/464)

	// Examine offset 0x01ce (462 bytes) - this is record 0, offset 462 within record
	// This offset appeared consistently with value 0.02
	t.Log("\n=== Offset 0x01ce (462 bytes from start = end of record 0) ===")
	if len(data) >= 462+4 {
		bits := binary.LittleEndian.Uint32(data[462 : 462+4])
		val := math.Float32frombits(bits)
		t.Logf("Float32 at offset 462: %.6f", val)
		t.Logf("This is %.2f bytes from end of first 464-byte record", 464.0-462.0)
	}

	// Check if this offset pattern repeats in other records
	t.Log("\n=== Checking offset pattern across multiple records ===")
	recordOffsetWithinRecord := 462 // relative offset within each record
	for recordNum := 0; recordNum < 5; recordNum++ {
		absoluteOffset := recordNum*464 + recordOffsetWithinRecord
		if absoluteOffset+4 <= len(data) {
			bits := binary.LittleEndian.Uint32(data[absoluteOffset : absoluteOffset+4])
			val := math.Float32frombits(bits)
			if !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) && val > 0.001 && val < 5.0 {
				t.Logf("Record %d, offset +%d (abs 0x%04x): %.6f", recordNum, recordOffsetWithinRecord, absoluteOffset, val)
			}
		}
	}

	// Examine offset 0x5181 (20865 bytes) where 0.04 was found
	// 20865 = 44 * 464 + 449, so this is record 44, offset 449 within record
	recordNum := 20865 / 464
	offsetWithinRecord := 20865 % 464
	t.Logf("\n=== Offset 0x5181 (20865 bytes) = Record %d, offset +%d within record ===", recordNum, offsetWithinRecord)
	if len(data) >= 20865+4 {
		bits := binary.LittleEndian.Uint32(data[20865 : 20865+4])
		val := math.Float32frombits(bits)
		t.Logf("Float32 value: %.6f", val)
	}

	// Let's scan for 0.06 (Instruction Throughput Limiter) which wasn't found
	t.Log("\n=== Searching for missing value 0.06 (Instruction Throughput) ===")
	target := float32(0.06)
	found := false
	for i := 0; i <= len(data)-4; i++ {
		bits := binary.LittleEndian.Uint32(data[i : i+4])
		val := math.Float32frombits(bits)
		if !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) {
			diff := val - target
			if diff < 0 {
				diff = -diff
			}
			if diff < 0.005 {
				recordNum := i / 464
				offsetInRecord := i % 464
				t.Logf("Found 0.06 at offset 0x%04x (record %d, +%d): %.6f", i, recordNum, offsetInRecord, val)
				found = true
				if !found {
					break // Show only first match initially
				}
			}
		}
	}
	if !found {
		t.Log("Value 0.06 not found in Counters_f_1.raw")
		// Try f_0.raw
		counterPath := filepath.Join(perfDir, "Counters_f_0.raw")
		data, err := os.ReadFile(counterPath)
		if err == nil {
			for i := 0; i <= len(data)-4; i++ {
				bits := binary.LittleEndian.Uint32(data[i : i+4])
				val := math.Float32frombits(bits)
				if !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) {
					diff := val - target
					if diff < 0 {
						diff = -diff
					}
					if diff < 0.005 {
						recordNum := i / 464
						offsetInRecord := i % 464
						t.Logf("Found 0.06 in f_0.raw at offset 0x%04x (record %d, +%d): %.6f", i, recordNum, offsetInRecord, val)
						break
					}
				}
			}
		}
	}
}

// TestLimiterOffsetsInRecords analyzes the specific in-record offsets for all limiters.
func TestLimiterOffsetsInRecords(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

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

	// Track which in-record offsets each limiter value appears at
	limiterOffsets := make(map[string]map[int]int) // limiter name -> {offset_in_record -> count}

	// Scan multiple counter files
	for fileIdx := 0; fileIdx < 5; fileIdx++ {
		counterPath := filepath.Join(perfDir, fmt.Sprintf("Counters_f_%d.raw", fileIdx))
		data, err := os.ReadFile(counterPath)
		if err != nil {
			continue
		}

		for limiterName, expectedVal := range expectedLimiters {
			if limiterOffsets[limiterName] == nil {
				limiterOffsets[limiterName] = make(map[int]int)
			}

			// Scan for this value
			for i := 0; i <= len(data)-4; i++ {
				bits := binary.LittleEndian.Uint32(data[i : i+4])
				val := math.Float32frombits(bits)

				if !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) {
					diff := val - expectedVal
					if diff < 0 {
						diff = -diff
					}
					if diff < 0.005 {
						offsetInRecord := i % 464
						limiterOffsets[limiterName][offsetInRecord]++
					}
				}
			}
		}
	}

	// Report findings
	t.Log("=== Limiter Value Offsets Within 464-byte Records ===\n")
	for limiterName, offsetCounts := range limiterOffsets {
		if len(offsetCounts) == 0 {
			t.Logf("%s: NOT FOUND", limiterName)
			continue
		}

		t.Logf("%s (%.2f):", limiterName, expectedLimiters[limiterName])

		// Sort by count (most common offsets first)
		type offsetCount struct {
			offset int
			count  int
		}
		var sorted []offsetCount
		for offset, count := range offsetCounts {
			sorted = append(sorted, offsetCount{offset, count})
		}

		// Simple bubble sort
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j].count > sorted[i].count {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}

		// Show top 5 offsets
		for i := 0; i < len(sorted) && i < 5; i++ {
			t.Logf("  Offset +%d (0x%03x): %d occurrences", sorted[i].offset, sorted[i].offset, sorted[i].count)
		}
		if len(sorted) > 5 {
			t.Logf("  ... and %d more unique offsets", len(sorted)-5)
		}
		t.Log("")
	}
}
