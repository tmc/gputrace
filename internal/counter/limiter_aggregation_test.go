package counter

import (
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// TestCompareAggregationStrategies tests whether limiter values need to be summed/averaged.
func TestCompareAggregationStrategies(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	// Read CSV to get expected values
	csvPath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1 Counters.csv")
	csvFile, err := os.Open(csvPath)
	if err != nil {
		t.Fatalf("Failed to open CSV: %v", err)
	}
	defer csvFile.Close()

	reader := csv.NewReader(csvFile)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("Failed to read CSV: %v", err)
	}

	// Find limiter columns
	headers := records[0]
	limiterColumns := make(map[string]int)
	for i, header := range headers {
		if header == "Compute Shader Launch Limiter" ||
			header == "Control Flow Limiter" ||
			header == "Instruction Throughput Limiter" ||
			header == "Integer and Conditional Limiter" ||
			header == "L1 Cache Limiter" ||
			header == "Last Level Cache Limiter" ||
			header == "MMU Limiter" ||
			header == "Texture Write Limiter" {
			limiterColumns[header] = i
		}
	}

	t.Log("Expected values from CSV:")
	dataRow := records[1] // First encoder's data
	expectedValues := make(map[string]float64)
	for name, colIdx := range limiterColumns {
		val, _ := strconv.ParseFloat(dataRow[colIdx], 64)
		expectedValues[name] = val
		if val > 0 {
			t.Logf("  %s: %.2f", name, val)
		}
	}

	// Now scan binary data and collect all instances of values close to expected
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

	// Collect all float values in limiter range from all records
	allFloats := make(map[float32]int) // value -> count

	for fileIdx := 0; fileIdx < 5; fileIdx++ {
		counterPath := filepath.Join(perfDir, fmt.Sprintf("Counters_f_%d.raw", fileIdx))
		data, err := os.ReadFile(counterPath)
		if err != nil {
			continue
		}

		// Scan for floats
		seen := make(map[int]bool)
		for i := 0; i <= len(data)-4; i += 4 {
			if seen[i] {
				continue
			}
			bits := binary.LittleEndian.Uint32(data[i : i+4])
			val := math.Float32frombits(bits)

			if !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) && val >= 0.001 && val <= 5.0 {
				allFloats[val]++
				seen[i] = true
			}
		}
	}

	t.Log("\n=== Float values found in binary (0.001-5.0 range) ===")
	// Sort by value
	type valueCount struct {
		value float32
		count int
	}
	var sorted []valueCount
	for val, count := range allFloats {
		sorted = append(sorted, valueCount{val, count})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].value < sorted[i].value {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	for _, vc := range sorted[:min(30, len(sorted))] {
		t.Logf("  %.6f: %d occurrences", vc.value, vc.count)
	}

	// Test aggregation hypothesis
	t.Log("\n=== Testing Aggregation Hypothesis ===")
	t.Log("If we sum/average individual sample values, do we get CSV values?")

	// Focus on Instruction Throughput (0.06 expected, but not found directly)
	// Maybe it's the sum of smaller values?
	target := 0.06
	t.Logf("\nLooking for combinations that sum to %.2f:", target)

	// Check if we have multiple small values that sum to 0.06
	for i, vc1 := range sorted {
		for j, vc2 := range sorted {
			if i != j {
				sum := vc1.value + vc2.value
				if math.Abs(float64(sum-float32(target))) < 0.005 {
					t.Logf("  %.4f + %.4f = %.4f (%d + %d occurrences)",
						vc1.value, vc2.value, sum, vc1.count, vc2.count)
				}
			}
		}
	}
}

// TestExamineRecordStructure dumps hex/float view of a sample record to understand structure.
func TestExamineRecordStructure(t *testing.T) {
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

	counterPath := filepath.Join(perfDir, "Counters_f_1.raw")
	data, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	// Find a record that contains limiter values
	// Record 44 had 0.04 at offset 449
	recordNum := 44
	if recordNum*464+464 > len(data) {
		recordNum = 0
	}

	recordStart := recordNum * 464
	recordData := data[recordStart : recordStart+464]

	t.Logf("=== Record %d Structure (464 bytes) ===\n", recordNum)

	// Show interesting regions with float interpretation
	regions := []struct {
		name   string
		offset int
		length int
	}{
		{"Header", 0, 16},
		{"Region around offset 72 (L1/Control Flow)", 64, 24},
		{"Region around offset 206 (MMU/Integer)", 200, 24},
		{"Region around offset 449-462 (Launch/MMU)", 440, 24},
	}

	for _, region := range regions {
		t.Logf("\n%s (offset %d-%d):", region.name, region.offset, region.offset+region.length)
		t.Log("Hex:")
		for i := 0; i < region.length; i += 16 {
			end := i + 16
			if end > region.length {
				end = region.length
			}
			offset := region.offset + i
			hexStr := ""
			for j := i; j < end; j++ {
				hexStr += fmt.Sprintf("%02x ", recordData[region.offset+j])
			}
			t.Logf("  +%03d: %s", offset, hexStr)
		}

		t.Log("Float32 values:")
		for i := 0; i < region.length-4; i += 4 {
			offset := region.offset + i
			bits := binary.LittleEndian.Uint32(recordData[offset : offset+4])
			val := math.Float32frombits(bits)
			if !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) {
				if val >= 0.001 && val <= 5.0 {
					t.Logf("  +%03d: %.6f ✓ (in limiter range)", offset, val)
				} else if val >= 0 && val < 1000000 {
					t.Logf("  +%03d: %.6f", offset, val)
				}
			}
		}
	}
}
