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

// TestExamineLimiterContext examines data surrounding known limiter offsets.
func TestExamineLimiterContext(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
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

	// Known offsets from TestSearchProfilingFiles:
	// L1 Cache (0.01): 0x001858 (6232)
	// Compute Launch (0.04): 0x0019b9 (6585)
	// MMU (0.02): 0x00298a (10634)
	// Instruction Throughput (0.06): 0x011f61 (73569)

	examineOffset := func(name string, offset int, expectedVal float32) {
		t.Logf("\n=== %s at offset 0x%06x (%d bytes) ===", name, offset, offset)

		// Show 128 bytes before and after
		start := offset - 64
		if start < 0 {
			start = 0
		}
		end := offset + 68
		if end > len(data) {
			end = len(data)
		}

		t.Log("\nHex dump (64 bytes before, value, 64 bytes after):")
		for i := start; i < end; i += 16 {
			hexStr := ""
			asciiStr := ""
			for j := 0; j < 16 && i+j < end; j++ {
				b := data[i+j]
				hexStr += fmt.Sprintf("%02x ", b)
				if b >= 32 && b < 127 {
					asciiStr += string(b)
				} else {
					asciiStr += "."
				}
			}

			marker := ""
			if i <= offset && offset < i+16 {
				marker = " ← TARGET"
			}
			t.Logf("  %06x: %-48s %s%s", i, hexStr, asciiStr, marker)
		}

		// Verify the value
		if offset+4 <= len(data) {
			bits := binary.LittleEndian.Uint32(data[offset : offset+4])
			val := math.Float32frombits(bits)
			t.Logf("\nFloat32 at offset: %.6f (expected %.2f)", val, expectedVal)
		}

		// Look for patterns - check if there are other float values nearby
		t.Log("\nNearby float32 values (±32 bytes):")
		scanStart := offset - 32
		if scanStart < 0 {
			scanStart = 0
		}
		scanEnd := offset + 36
		if scanEnd > len(data)-4 {
			scanEnd = len(data) - 4
		}

		for i := scanStart; i < scanEnd; i += 4 {
			bits := binary.LittleEndian.Uint32(data[i : i+4])
			val := math.Float32frombits(bits)
			if !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) {
				relOffset := i - offset
				marker := ""
				if i == offset {
					marker = " ← TARGET"
				}
				if val >= 0.001 && val <= 5.0 {
					t.Logf("  %+4d: %.6f ✓%s", relOffset, val, marker)
				} else if val >= 0 && val < 1000 {
					t.Logf("  %+4d: %.6f", relOffset, val)
				}
			}
		}

		// Check uint32/uint64 values nearby (might be counts, IDs)
		t.Log("\nNearby uint32/uint64 values:")
		for i := offset - 16; i < offset+20 && i+8 <= len(data); i += 4 {
			u32 := binary.LittleEndian.Uint32(data[i : i+4])
			if i+8 <= len(data) {
				u64 := binary.LittleEndian.Uint64(data[i : i+8])
				relOffset := i - offset
				marker := ""
				if i == offset {
					marker = " ← TARGET"
				}
				// Show values that look like counts, IDs, or sizes
				if u32 > 0 && u32 < 1000000 {
					t.Logf("  %+4d: u32=%10d (0x%08x)  u64=%16d (0x%016x)%s",
						relOffset, u32, u32, u64, u64, marker)
				}
			}
		}
	}

	// Examine multiple limiter locations
	examineOffset("L1 Cache Limiter (0.01)", 0x001858, 0.01)
	examineOffset("Compute Shader Launch (0.04)", 0x0019b9, 0.04)
	examineOffset("MMU Limiter (0.02)", 0x00298a, 0.02)
	examineOffset("Instruction Throughput (0.06)", 0x011f61, 0.06)
}

// TestIdentifyProfilingRecordSize attempts to find record boundaries.
func TestIdentifyProfilingRecordSize(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
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

	t.Logf("File size: %d bytes", len(data))

	// Known limiter offsets (from earlier tests)
	limiterOffsets := []int{
		0x001858, // L1 Cache (first occurrence)
		0x009522, // L1 Cache (second occurrence)
		0x00abb2, // L1 Cache (third occurrence)
	}

	t.Log("\nAnalyzing distance between limiter occurrences:")
	for i := 1; i < len(limiterOffsets); i++ {
		diff := limiterOffsets[i] - limiterOffsets[i-1]
		t.Logf("  Offset %d -> %d: distance = %d bytes (0x%x)",
			i-1, i, diff, diff)

		// Check common record sizes
		if diff%464 == 0 {
			t.Logf("    → Multiple of 464 bytes (%d records)", diff/464)
		}
		if diff%512 == 0 {
			t.Logf("    → Multiple of 512 bytes (%d records)", diff/512)
		}
		if diff%1024 == 0 {
			t.Logf("    → Multiple of 1024 bytes (%d records)", diff/1024)
		}
	}

	// Try to identify repeating patterns by looking at record headers
	t.Log("\nScanning for potential record headers:")

	// Common record header patterns: type + size fields
	type recordCandidate struct {
		offset uint32
		type_  uint32
		size   uint32
	}

	var candidates []recordCandidate
	for i := 0; i < len(data)-12; i += 4 {
		recordType := binary.LittleEndian.Uint32(data[i : i+4])
		recordSize := binary.LittleEndian.Uint32(data[i+4 : i+8])

		// Heuristic: size should be reasonable (100 bytes to 10KB)
		// and aligned, and type should have some pattern
		if recordSize >= 100 && recordSize <= 10000 && recordSize%4 == 0 {
			// Check if this could be a valid record by seeing if the next
			// potential record is at offset+recordSize
			if i+int(recordSize)+8 < len(data) {
				_ = binary.LittleEndian.Uint32(data[i+int(recordSize) : i+int(recordSize)+4])
				nextSize := binary.LittleEndian.Uint32(data[i+int(recordSize)+4 : i+int(recordSize)+8])

				// If next record also looks valid, this is promising
				if nextSize >= 100 && nextSize <= 10000 && nextSize%4 == 0 {
					candidates = append(candidates, recordCandidate{
						offset: uint32(i),
						type_:  recordType,
						size:   recordSize,
					})

					if len(candidates) <= 10 {
						t.Logf("  Offset 0x%06x: type=0x%08x size=%d", i, recordType, recordSize)
					}
				}
			}
		}
	}

	t.Logf("\nFound %d potential record headers", len(candidates))
	if len(candidates) > 10 {
		t.Log("  (showing first 10)")
	}
}
