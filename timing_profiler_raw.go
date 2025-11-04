package gputrace

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// ProfilerRawTiming represents timing data extracted from .gpuprofiler_raw files.
type ProfilerRawTiming struct {
	EncoderIndex   int
	EncoderLabel   string
	KernelName     string
	DurationNs     uint64
	DurationMs     float64
	GPUCycles      uint64
	Confidence     float64 // 0.0-1.0, based on data quality
}

// TimingExtractorProfilerRaw extracts timing from .gpuprofiler_raw performance counter files.
type TimingExtractorProfilerRaw struct {
	trace *Trace
}

// NewTimingExtractorProfilerRaw creates a new profiler raw timing extractor.
func NewTimingExtractorProfilerRaw(trace *Trace) *TimingExtractorProfilerRaw {
	return &TimingExtractorProfilerRaw{
		trace: trace,
	}
}

// findProfilerDir locates the .gpuprofiler_raw directory.
// It checks both adjacent to the trace and inside the trace bundle.
func (te *TimingExtractorProfilerRaw) findProfilerDir() (string, error) {
	// Check adjacent to trace
	profilerDir := te.trace.Path + ".gpuprofiler_raw"
	if info, err := os.Stat(profilerDir); err == nil && info.IsDir() {
		return profilerDir, nil
	}

	// Check inside trace bundle
	entries, err := os.ReadDir(te.trace.Path)
	if err != nil {
		return "", fmt.Errorf("failed to read trace directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() && filepath.Ext(entry.Name()) == ".gpuprofiler_raw" {
			return filepath.Join(te.trace.Path, entry.Name()), nil
		}
	}

	return "", fmt.Errorf(".gpuprofiler_raw directory not found")
}

// ExtractTimingFromProfilerRaw attempts to extract timing data from .gpuprofiler_raw files.
//
// The .gpuprofiler_raw directory contains hardware performance counter data captured
// by Xcode Instruments. Each Counters_f_*.raw file contains binary records with:
// - GPU execution cycles
// - Per-shader performance metrics
// - Dispatch timing information
//
// This is the same data source Xcode Instruments uses to calculate shader cost percentages.
func (te *TimingExtractorProfilerRaw) ExtractTimingFromProfilerRaw() ([]*EncoderTiming, error) {
	// Check if .gpuprofiler_raw directory exists
	if !te.trace.HasPerfCounters() {
		return nil, fmt.Errorf("no .gpuprofiler_raw directory found")
	}

	// Find the profiler directory
	profilerDir, err := te.findProfilerDir()
	if err != nil {
		return nil, err
	}

	// List all counter files
	counterFiles, err := filepath.Glob(filepath.Join(profilerDir, "Counters_f_*.raw"))
	if err != nil {
		return nil, fmt.Errorf("failed to list counter files: %w", err)
	}

	if len(counterFiles) == 0 {
		return nil, fmt.Errorf("no counter files found in %s", profilerDir)
	}

	// Parse counter files to extract timing
	var allTimings []*ProfilerRawTiming
	for _, counterFile := range counterFiles {
		timings, err := te.parseCounterFileForTiming(counterFile)
		if err != nil {
			// Continue with other files if one fails
			continue
		}
		allTimings = append(allTimings, timings...)
	}

	if len(allTimings) == 0 {
		return nil, fmt.Errorf("no timing data found in counter files")
	}

	// Convert to EncoderTiming format
	encoderTimings := te.convertToEncoderTiming(allTimings)

	// Calculate percentages
	if len(encoderTimings) > 0 {
		calculatePercentages(encoderTimings)
	}

	return encoderTimings, nil
}

// parseCounterFileForTiming parses a single counter file for timing data.
//
// Counter file format (based on reverse engineering):
// - Records start with 0x4E 0x00 0x00 0x00 marker
// - Variable record sizes (69 bytes to 40KB)
// - Contains GPU cycle counts and timing metrics
// - Format is binary and undocumented
//
// Current implementation uses heuristics to extract timing information.
func (te *TimingExtractorProfilerRaw) parseCounterFileForTiming(path string) ([]*ProfilerRawTiming, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open counter file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read counter file: %w", err)
	}

	// Find record boundaries
	records := te.findCounterRecords(data)

	// Parse each record for timing data
	var timings []*ProfilerRawTiming
	for _, record := range records {
		if timing := te.parseCounterRecord(record); timing != nil {
			timings = append(timings, timing)
		}
	}

	return timings, nil
}

// ProfilerCounterRecord represents a single record from a .gpuprofiler_raw counter file.
type ProfilerCounterRecord struct {
	Offset int64
	Data   []byte
}

// findCounterRecords locates all records in the counter data.
// Records start with 0x4E 0x00 0x00 0x00 marker.
func (te *TimingExtractorProfilerRaw) findCounterRecords(data []byte) []*ProfilerCounterRecord {
	var records []*ProfilerCounterRecord

	i := 0
	for i < len(data)-4 {
		// Look for record marker: 0x4E 0x00 0x00 0x00
		if data[i] == 0x4E && data[i+1] == 0x00 && data[i+2] == 0x00 && data[i+3] == 0x00 {
			// Try to determine record size
			// Heuristic: Read size field at offset+4 (if present)
			recordSize := te.estimateRecordSize(data, i)

			if i+recordSize <= len(data) {
				record := &ProfilerCounterRecord{
					Offset: int64(i),
					Data:   data[i : i+recordSize],
				}
				records = append(records, record)
				i += recordSize
			} else {
				i += 4
			}
		} else {
			i++
		}
	}

	return records
}

// estimateRecordSize attempts to determine the size of a counter record.
// The format is not fully understood, so we use heuristics.
func (te *TimingExtractorProfilerRaw) estimateRecordSize(data []byte, offset int) int {
	// Check if there's a size field after the marker
	if offset+8 <= len(data) {
		// Try reading uint32 at offset+4
		potentialSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])

		// Sanity check: size should be reasonable (< 100KB)
		if potentialSize > 0 && potentialSize < 100000 {
			return int(potentialSize) + 8 // Include header
		}
	}

	// Fallback: Look for next record marker
	searchStart := offset + 4
	for i := searchStart; i < len(data)-4 && i < offset+100000; i++ {
		if data[i] == 0x4E && data[i+1] == 0x00 && data[i+2] == 0x00 && data[i+3] == 0x00 {
			return i - offset
		}
	}

	// Default minimum size
	return 69
}

// parseCounterRecord attempts to extract timing data from a counter record.
//
// Strategy:
// 1. Look for GPU cycle counts (uint64 values in reasonable ranges)
// 2. Look for timestamp-like values (mach_absolute_time format)
// 3. Correlate with known encoder/kernel structure from trace
//
// Returns nil if no timing data found in this record.
func (te *TimingExtractorProfilerRaw) parseCounterRecord(record *ProfilerCounterRecord) *ProfilerRawTiming {
	data := record.Data

	if len(data) < 32 {
		return nil
	}

	// Search for potential GPU cycle counts or timing values
	// These are typically uint64 values in the range:
	// - GPU cycles: 1000 to 100,000,000 (for reasonable kernel duration)
	// - Timestamps: 1e15 to 1e18 (mach_absolute_time)

	var gpuCycles uint64
	var durationNs uint64

	// Scan through record looking for timing values
	for i := 8; i < len(data)-8; i += 8 {
		val := binary.LittleEndian.Uint64(data[i : i+8])

		// Check if this looks like GPU cycles (1K - 100M range)
		if val >= 1000 && val <= 100000000 {
			if gpuCycles == 0 || val > gpuCycles {
				gpuCycles = val
			}
		}

		// Check if this looks like a duration in nanoseconds
		// Reasonable range: 100ns to 10 seconds
		if val >= 100 && val <= 10000000000 {
			if durationNs == 0 || (val > durationNs && val < 10000000000) {
				durationNs = val
			}
		}
	}

	// Need at least one valid value to create timing
	if gpuCycles == 0 && durationNs == 0 {
		return nil
	}

	// If we have GPU cycles but no duration, estimate duration
	// Typical GPU frequency: ~1.4 GHz for Apple Silicon
	// Duration (ns) = (GPU cycles / frequency) * 1e9
	if durationNs == 0 && gpuCycles > 0 {
		const estimatedGPUFrequencyHz = 1_400_000_000 // 1.4 GHz
		durationNs = (gpuCycles * 1_000_000_000) / estimatedGPUFrequencyHz
	}

	return &ProfilerRawTiming{
		GPUCycles:  gpuCycles,
		DurationNs: durationNs,
		DurationMs: float64(durationNs) / 1e6,
		Confidence: 0.5, // Medium confidence - needs validation
	}
}

// convertToEncoderTiming converts ProfilerRawTiming to EncoderTiming format.
//
// Matches timing data with encoder labels from the trace file.
// Uses best-effort matching based on execution order.
func (te *TimingExtractorProfilerRaw) convertToEncoderTiming(profilerTimings []*ProfilerRawTiming) []*EncoderTiming {
	// Sort by offset/index
	sort.Slice(profilerTimings, func(i, j int) bool {
		return profilerTimings[i].EncoderIndex < profilerTimings[j].EncoderIndex
	})

	// Match with encoder labels
	encoderLabels := te.trace.EncoderLabels
	if len(encoderLabels) == 0 {
		encoderLabels = te.trace.KernelNames
	}

	var encoderTimings []*EncoderTiming

	// Simple strategy: Match timing records to encoders by position
	for i, profTiming := range profilerTimings {
		label := "unknown"
		if i < len(encoderLabels) {
			label = encoderLabels[i]
		}

		timing := &EncoderTiming{
			Label:      label,
			DurationNs: profTiming.DurationNs,
			DurationMs: profTiming.DurationMs,
			// Start/End timestamps not available from counter files
			StartTimestamp: 0,
			EndTimestamp:   0,
		}

		encoderTimings = append(encoderTimings, timing)
	}

	return encoderTimings
}

// ProfilerRawTimingReport generates a detailed report of timing from .gpuprofiler_raw files.
func (te *TimingExtractorProfilerRaw) ProfilerRawTimingReport(timings []*EncoderTiming) string {
	if len(timings) == 0 {
		return "No timing data available from .gpuprofiler_raw\n"
	}

	report := "=== GPU Timing from .gpuprofiler_raw ===\n\n"

	// Calculate total time
	var totalNs uint64
	for _, t := range timings {
		totalNs += t.DurationNs
	}
	totalMs := float64(totalNs) / 1e6

	report += fmt.Sprintf("Total GPU Time: %.2f ms\n", totalMs)
	report += fmt.Sprintf("Number of Encoders: %d\n", len(timings))
	report += fmt.Sprintf("Data Source: Hardware Performance Counters\n\n")

	// Sort by duration (descending)
	sorted := make([]*EncoderTiming, len(timings))
	copy(sorted, timings)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].DurationNs > sorted[j].DurationNs
	})

	// Show timing breakdown
	report += "Timing Breakdown:\n"
	report += fmt.Sprintf("%-40s %12s %8s\n", "Label", "Duration", "Percent")
	report += fmt.Sprintf("%s\n", repeatChar('-', 65))

	for _, t := range sorted {
		report += fmt.Sprintf("%-40s %9.2f ms %7.1f%%\n",
			truncateString(t.Label, 40),
			t.DurationMs,
			t.Percentage)
	}

	return report
}
