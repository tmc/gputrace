package timing

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/tmc/gputrace/internal/fmtutil"
	"github.com/tmc/gputrace/internal/profilerraw"
	"github.com/tmc/gputrace/internal/trace"
)

// ProfilerRawTiming represents timing data extracted from .gpuprofiler_raw files.
type ProfilerRawTiming struct {
	EncoderIndex int
	EncoderLabel string
	KernelName   string
	DurationNs   uint64
	DurationMs   float64
	GPUCycles    uint64
	Confidence   float64 // 0.0-1.0, based on data quality
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
//
// Timing extraction strategy (gputrace-108):
// 1. Try kdebug events first (highest accuracy ~0.95)
// 2. Fall back to shader limiter heuristic (lower accuracy ~0.3)
func (te *TimingExtractorProfilerRaw) ExtractTimingFromProfilerRaw() ([]*EncoderTiming, error) {
	// Check if .gpuprofiler_raw directory exists
	if !te.trace.HasPerfCounters() {
		return nil, fmt.Errorf("no .gpuprofiler_raw directory found")
	}

	// STRATEGY 1: Try kdebug timing first (gputrace-108 improvement)
	// This provides actual GPU execution timestamps with high confidence
	kdebugTimings, kdebugErr := te.extractTimingFromKDebug()
	if kdebugErr == nil && len(kdebugTimings) > 0 {
		// Successfully extracted kdebug timing
		if len(kdebugTimings) > 0 {
			calculatePercentages(kdebugTimings)
		}
		return kdebugTimings, nil
	}

	// STRATEGY 2: Fall back to counter file limiter heuristic
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

	records := profilerraw.Records(data)

	// Parse each record for timing data
	var timings []*ProfilerRawTiming
	for i := range records {
		if timing := te.parseCounterRecord(&records[i]); timing != nil {
			timings = append(timings, timing)
		}
	}

	return timings, nil
}

// parseCounterRecord attempts to extract timing data from a counter record.
//
// NOTE: This is a FALLBACK method used when kdebug timing is unavailable (gputrace-108).
//
// The .gpuprofiler_raw files contain performance counter samples, not direct timing data.
// Xcode Instruments calculates shader timing by analyzing:
// 1. GPU cycle counts across multiple counter samples
// 2. Timestamp correlation with command buffer execution (kdebug events)
// 3. Shader limiter percentages (which indicate relative cost)
//
// This fallback implementation uses shader limiter data as a proxy for relative timing,
// since absolute timing from counter files alone is not directly accessible.
// Confidence level: Low (~0.3)
//
// For better accuracy, use kdebug events when available (confidence ~0.95).
//
// Returns nil if no usable timing proxy data found in this record.
func (te *TimingExtractorProfilerRaw) parseCounterRecord(record *profilerraw.Record) *ProfilerRawTiming {
	data := record.Data

	// Only process sample records (464 bytes) which contain performance metrics
	// Metadata records (2300-2900 bytes) don't have timing data
	if len(data) != 464 {
		return nil
	}

	// Extract shader limiter values as proxy for relative cost
	// Higher limiter values indicate the shader spent more time in that bottleneck
	// We sum all limiters to get a relative "cost" metric
	limiters := findAllFloatsInRange(data, 0.001, 10.0, 30) // Find up to 30 limiter values

	if len(limiters) == 0 {
		return nil
	}

	// Sum all limiter percentages to get relative cost
	// This is a heuristic: shaders with more bottlenecks take longer
	var totalLimiterCost float64
	for _, val := range limiters {
		totalLimiterCost += val
	}

	// Convert limiter cost to relative duration (arbitrary units)
	// We'll normalize these later based on total GPU time
	relativeDuration := uint64(totalLimiterCost * 1_000_000) // Scale to microseconds

	return &ProfilerRawTiming{
		GPUCycles:  0, // Not extractable from counter files
		DurationNs: relativeDuration,
		DurationMs: float64(relativeDuration) / 1e6,
		Confidence: 0.3, // Low confidence - this is an approximation
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

	// Determine data source and confidence based on timestamps
	dataSource := "Hardware Performance Counters (Limiter Heuristic)"
	confidence := "Low (~0.3)"
	if len(timings) > 0 && timings[0].StartTimestamp != 0 {
		// Has actual timestamps - came from kdebug
		dataSource = "KDebug GPU Execution Events"
		confidence = "High (~0.95)"
	}

	report += fmt.Sprintf("Total GPU Time: %.2f ms\n", totalMs)
	report += fmt.Sprintf("Number of Encoders: %d\n", len(timings))
	report += fmt.Sprintf("Data Source: %s\n", dataSource)
	report += fmt.Sprintf("Confidence: %s\n\n", confidence)

	// Sort by duration (descending)
	sorted := make([]*EncoderTiming, len(timings))
	copy(sorted, timings)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].DurationNs > sorted[j].DurationNs
	})

	// Show timing breakdown
	report += "Timing Breakdown:\n"
	report += fmt.Sprintf("%-40s %12s %8s\n", "Label", "Duration", "Percent")
	report += fmt.Sprintf("%s\n", fmtutil.RepeatChar('-', 65))

	for _, t := range sorted {
		report += fmt.Sprintf("%-40s %9.2f ms %7.1f%%\n",
			fmtutil.TruncateString(t.Label, 40),
			t.DurationMs,
			t.Percentage)
	}

	return report
}

// extractTimingFromKDebug extracts timing data from kdebug events (gputrace-108).
//
// KDebug events provide actual GPU execution timestamps from the kernel,
// which are much more accurate than shader limiter heuristics.
//
// Returns high-confidence timing data (confidence ~0.95) when kdebug events are available.
func (te *TimingExtractorProfilerRaw) extractTimingFromKDebug() ([]*EncoderTiming, error) {
	// Import trace package for kdebug parsing
	// Note: We need to add the import at the top of the file

	// Try to parse kdebug events from trace
	kdebugParser := trace.NewKDebugParser(te.trace)
	events, err := kdebugParser.ParseKDebugEvents()
	if err != nil {
		return nil, fmt.Errorf("kdebug events not available: %w", err)
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("no kdebug events found")
	}

	// Correlate events into GPU execution intervals
	intervals := trace.CorrelateGPUExecution(events)
	if len(intervals) == 0 {
		return nil, fmt.Errorf("no GPU execution intervals found")
	}

	// Convert intervals to EncoderTiming
	var encoderTimings []*EncoderTiming

	// Match intervals with encoder labels
	encoderLabels := te.trace.EncoderLabels
	if len(encoderLabels) == 0 {
		encoderLabels = te.trace.KernelNames
	}

	for i, interval := range intervals {
		duration := interval.Duration()
		if duration == 0 {
			continue
		}

		// Match to encoder label by position
		label := "unknown"
		if i < len(encoderLabels) {
			label = encoderLabels[i]
		}

		timing := &EncoderTiming{
			Label:          label,
			StartTimestamp: interval.StartEvent.Timestamp,
			EndTimestamp:   interval.EndEvent.Timestamp,
			DurationNs:     duration,
			DurationMs:     float64(duration) / 1e6,
		}

		encoderTimings = append(encoderTimings, timing)
	}

	if len(encoderTimings) == 0 {
		return nil, fmt.Errorf("no valid timing intervals extracted")
	}

	return encoderTimings, nil
}

// findAllFloatsInRange scans record data for all float32 values in the specified range.
// Returns up to maxCount matching values, sorted by offset order.
func findAllFloatsInRange(data []byte, minVal, maxVal float64, maxCount int) []float64 {
	results := make([]float64, 0, maxCount)
	seen := make(map[float64]bool) // Avoid duplicates

	for i := 0; i < len(data)-4; i += 4 {
		// Read as float32
		bits := binary.LittleEndian.Uint32(data[i : i+4])
		val := float64(math.Float32frombits(bits))

		// Check if in range and not already seen
		if val >= minVal && val <= maxVal && !seen[val] {
			results = append(results, val)
			seen[val] = true

			if len(results) >= maxCount {
				break
			}
		}
	}

	return results
}
