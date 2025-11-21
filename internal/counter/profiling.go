package counter

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// ProfilingMetrics represents metrics extracted from Profiling_f_*.raw files.
type ProfilingMetrics struct {
	EncoderIndex    int     // Index of encoder (from file number)
	KernelOccupancy float64 // Kernel occupancy percentage (0-100)
	SampleCount     int     // Number of samples found
	Confidence      float64 // Confidence in the measurement (0.0-1.0)
}

// ParseProfilingFiles extracts Kernel Occupancy and other metrics from Profiling_f_*.raw files.
//
// Key findings from investigation (docs/KERNEL_OCCUPANCY_LOCATION.md):
// - Kernel Occupancy is stored in Profiling_f_*.raw files (NOT Counters files)
// - Values are encoded as IEEE 754 float32 (little-endian)
// - Values are fractions 0.0-1.0 that must be multiplied by 100 (e.g., 0.0009 → 0.09%)
// - Multiple samples per encoder that need aggregation
//
// Strategy:
// 1. Scan each Profiling_f_N.raw file for float32 values in reasonable range
// 2. Group values by proximity (similar values likely same encoder)
// 3. Average multiple samples per encoder
// 4. Return one ProfilingMetrics per encoder
func ParseProfilingFiles(t *trace.Trace) ([]*ProfilingMetrics, error) {
	// Find .gpuprofiler_raw directory
	perfDir := t.Path + ".gpuprofiler_raw"
	if _, err := os.Stat(perfDir); os.IsNotExist(err) {
		// Check inside trace bundle
		entries, err := os.ReadDir(t.Path)
		if err != nil {
			return nil, fmt.Errorf("no performance counter data: %s not found", perfDir)
		}

		found := false
		for _, entry := range entries {
			if entry.IsDir() && filepath.Ext(entry.Name()) == ".gpuprofiler_raw" {
				perfDir = filepath.Join(t.Path, entry.Name())
				found = true
				break
			}
		}

		if !found {
			return nil, fmt.Errorf("no performance counter data: .gpuprofiler_raw not found")
		}
	}

	// Find all Profiling_f_*.raw files
	files, err := filepath.Glob(filepath.Join(perfDir, "Profiling_f_*.raw"))
	if err != nil {
		return nil, fmt.Errorf("failed to find profiling files: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no profiling files found in %s", perfDir)
	}

	// Sort files by index to maintain order
	sort.Strings(files)

	// Parse each profiling file
	var allMetrics []*ProfilingMetrics
	for i, file := range files {
		metrics, err := parseProfilingFile(file, i)
		if err != nil {
			// Continue with other files if one fails
			continue
		}
		if metrics != nil {
			allMetrics = append(allMetrics, metrics)
		}
	}

	if len(allMetrics) == 0 {
		return nil, fmt.Errorf("no profiling metrics extracted from %d files", len(files))
	}

	return allMetrics, nil
}

// parseProfilingFile extracts metrics from a single Profiling_f_N.raw file.
//
// Extraction strategy:
// 1. Read entire file into memory
// 2. Scan for float32 values in occupancy range (0.01-1.0)
// 3. Collect all candidate values
// 4. Use clustering/averaging to find most likely occupancy value
func parseProfilingFile(path string, encoderIndex int) (*ProfilingMetrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open profiling file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read profiling file: %w", err)
	}

	// Extract all float32 values in reasonable occupancy range
	candidateValues := extractOccupancyCandidates(data)

	if len(candidateValues) == 0 {
		// No valid occupancy values found
		return nil, fmt.Errorf("no occupancy candidates found")
	}

	// Calculate representative occupancy value
	// Strategy: Use median to be robust against outliers
	occupancy := calculateMedian(candidateValues)

	metrics := &ProfilingMetrics{
		EncoderIndex:    encoderIndex,
		KernelOccupancy: occupancy * 100, // Convert fraction to percentage (0.0009 → 0.09%)
		SampleCount:     len(candidateValues),
		Confidence:      calculateConfidence(candidateValues),
	}

	return metrics, nil
}

// extractOccupancyCandidates scans binary data for float32 values that could be occupancy.
//
// Valid occupancy range: 0.0001 to 1.0 (before multiplying by 100)
// - Below 0.0001 (0.01%): Too low to be meaningful kernel occupancy
// - Above 1.0: Invalid (occupancy is a fraction that gets converted to %)
//
// Key insight from frequency analysis (docs/KERNEL_OCCUPANCY_EXTRACTION_STATUS.md):
// - Actual occupancy values are RARE (1-5 occurrences)
// - Noise values are FREQUENT (50-100 occurrences, e.g., 0.125)
// - Use frequency filtering to exclude noise before selection
//
// Returns only rare candidate values (likely actual occupancy).
func extractOccupancyCandidates(data []byte) []float64 {
	const (
		minOccupancy   = 0.0001 // 0.01% minimum (CSV shows values like 0.08%)
		maxOccupancy   = 1.0    // 100% maximum (but typically < 1%)
		noiseThreshold = 20     // Values appearing >20 times are likely noise
		minOccurrences = 1      // Must appear at least once (obviously)
	)

	// First pass: Count frequency of each value
	valueFrequency := make(map[float32]int)

	for i := 0; i < len(data)-4; i += 4 {
		bits := binary.LittleEndian.Uint32(data[i : i+4])
		val := math.Float32frombits(bits)

		// Check if value is in valid occupancy range
		if val >= minOccupancy && val <= maxOccupancy {
			// Additional validation: check for NaN and Inf
			if !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0) {
				valueFrequency[val]++
			}
		}
	}

	// Second pass: Filter out noise (frequent values)
	// Keep only rare values that are likely actual occupancy measurements
	var rareValues []float64
	for val, count := range valueFrequency {
		// Keep values that appear rarely (signal, not noise)
		if count >= minOccurrences && count <= noiseThreshold {
			rareValues = append(rareValues, float64(val))
		}
	}

	return rareValues
}

// calculateMedian computes the median value from a slice of floats.
// Using median instead of mean to be robust against outliers.
func calculateMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Sort values
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	// Return median
	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

// calculateConfidence estimates confidence in the occupancy measurement.
//
// Confidence factors:
// - Sample count: More samples = higher confidence
// - Consistency: Low variance = higher confidence
//
// Returns value between 0.0 (no confidence) and 1.0 (high confidence)
func calculateConfidence(values []float64) float64 {
	if len(values) == 0 {
		return 0.0
	}

	// Factor 1: Sample count (more samples = higher confidence)
	sampleConfidence := math.Min(float64(len(values))/10.0, 1.0)

	// Factor 2: Consistency (calculate variance)
	mean := calculateMean(values)
	variance := 0.0
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(values))

	// Low variance = high confidence
	// Assume variance > 0.01 is inconsistent
	varianceConfidence := 1.0 - math.Min(variance/0.01, 1.0)

	// Combine factors (weighted average)
	confidence := 0.7*sampleConfidence + 0.3*varianceConfidence

	return confidence
}

// calculateMean computes the arithmetic mean of a slice of floats.
func calculateMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// MergeProfilingMetricsWithShaderMetrics merges profiling metrics into shader metrics.
//
// Strategy:
// - Match by encoder index/order
// - Update KernelOccupancy field in ShaderHardwareMetrics
// - Preserve existing shader metric data
func MergeProfilingMetricsWithShaderMetrics(
	shaderMetrics []ShaderHardwareMetrics,
	profilingMetrics []*ProfilingMetrics,
) []ShaderHardwareMetrics {
	// Create a copy to avoid modifying original
	merged := make([]ShaderHardwareMetrics, len(shaderMetrics))
	copy(merged, shaderMetrics)

	// Match by index (assuming order corresponds)
	for i := range merged {
		if i < len(profilingMetrics) && profilingMetrics[i] != nil {
			merged[i].KernelOccupancy = profilingMetrics[i].KernelOccupancy
		}
	}

	return merged
}
