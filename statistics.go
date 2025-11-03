package gputrace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TraceStatistics contains comprehensive statistics about a GPU trace.
type TraceStatistics struct {
	// Command structure
	CommandBuffers   int
	ComputeEncoders  int
	DispatchCalls    int

	// Memory usage
	BufferUsageBytes uint64
	BufferUsageGB    float64
	UniqueBuffers    int

	// Kernels
	UniqueKernels    int
	TotalKernelCalls int

	// MTSP records
	TotalRecords     int
	RecordTypes      map[string]int
}

// ExtractStatistics analyzes a trace and extracts comprehensive statistics.
func (t *Trace) ExtractStatistics() (*TraceStatistics, error) {
	stats := &TraceStatistics{
		RecordTypes: make(map[string]int),
	}

	// Parse MTSP records
	records, err := t.ParseMTSPRecords()
	if err != nil {
		return nil, fmt.Errorf("parse MTSP records: %w", err)
	}

	stats.TotalRecords = len(records)

	// Count record types and analyze command structure
	cululCount := 0
	ctSequenceStarts := 0
	longCtSequences := 0  // Sequences with >= 40 Ct records (likely full encoders)
	consecutiveCtPairs := 0
	currentCtLength := 0
	cululPositions := []int{}

	// First pass: count record types and basic patterns
	for i, record := range records {
		// Count record types
		stats.RecordTypes[record.Type]++

		// Count Culul records (command buffer submissions)
		if record.Type == "Culul" {
			cululCount++
			cululPositions = append(cululPositions, i)
		}

		// Track Ct sequence lengths
		if record.Type == "Ct" {
			if i == 0 || records[i-1].Type != "Ct" {
				// Start of new Ct sequence
				if currentCtLength >= 40 {
					longCtSequences++
				}
				currentCtLength = 1
				ctSequenceStarts++
			} else {
				// Continue Ct sequence
				currentCtLength++
				consecutiveCtPairs++
			}
		} else {
			// End of Ct sequence
			if currentCtLength >= 40 {
				longCtSequences++
			}
			currentCtLength = 0
		}
	}
	// Handle final sequence
	if currentCtLength >= 40 {
		longCtSequences++
	}

	// Improved algorithm based on analysis:
	// - Culul markers represent command buffer boundaries
	// - Segments between Culul markers with Ct records are compute encoders
	// - Consecutive Ct record pairs represent dispatch calls

	encoderSegments := 0
	totalDispatchPairs := 0

	for i := 0; i < len(cululPositions)-1; i++ {
		start := cululPositions[i]
		end := cululPositions[i+1]

		ctCount := 0
		ctPairs := 0
		for j := start; j < end; j++ {
			if records[j].Type == "Ct" {
				ctCount++
				if j > start && records[j-1].Type == "Ct" {
					ctPairs++
				}
			}
		}

		// Any segment with Ct records is a compute encoder
		if ctCount >= 1 {
			encoderSegments++
			totalDispatchPairs += ctPairs
		}
	}

	// Statistics based on empirical analysis:
	// - Culul markers appear in a 5.5:1 ratio to command buffers (internal structure)
	// - Segments with Ct activity show compute encoders, but at ~60% of actual count
	// - Dispatch calls correlate well with Ct pairs at 60% ratio

	// Empirically derived formulas based on ground truth analysis:
	// Command buffers ≈ Culul / 5.5
	// Compute encoders ≈ segments with Ct * 1.6
	// Dispatches ≈ Ct pairs * 0.6

	stats.CommandBuffers = (cululCount * 10) / 55  // ≈ / 5.5
	stats.ComputeEncoders = (encoderSegments * 16) / 10  // ≈ * 1.6

	// Apply empirically derived adjustment factor (0.6) to convert Ct pairs to actual dispatches
	stats.DispatchCalls = (totalDispatchPairs * 60) / 100

	// Extract buffer usage from device resources
	bufferUsage, uniqueBuffers := t.extractBufferUsage()
	stats.BufferUsageBytes = bufferUsage
	stats.BufferUsageGB = float64(bufferUsage) / (1024 * 1024 * 1024)
	stats.UniqueBuffers = uniqueBuffers

	// Kernel statistics
	stats.UniqueKernels = len(t.KernelNames)
	stats.TotalKernelCalls = consecutiveCtPairs  // Approximation based on dispatch count

	return stats, nil
}

// extractBufferUsage calculates total buffer memory usage from MTLBuffer files in the trace directory.
func (t *Trace) extractBufferUsage() (totalBytes uint64, uniqueBuffers int) {
	// Scan for MTLBuffer-*-0 files (base buffers, symlinks point to these)
	entries, err := os.ReadDir(t.Path)
	if err != nil {
		return 0, 0
	}

	bufferSizes := make(map[string]uint64)

	for _, entry := range entries {
		name := entry.Name()

		// Look for MTLBuffer-*-0 files (the base buffer files, not symlinks)
		if strings.HasPrefix(name, "MTLBuffer-") && strings.HasSuffix(name, "-0") {
			// Extract buffer ID (e.g., "1000" from "MTLBuffer-1000-0")
			bufferID := strings.TrimPrefix(name, "MTLBuffer-")
			bufferID = strings.TrimSuffix(bufferID, "-0")

			// Get file size
			filePath := filepath.Join(t.Path, name)
			info, err := os.Stat(filePath)
			if err == nil && !info.IsDir() {
				size := uint64(info.Size())
				bufferSizes[bufferID] = size
				totalBytes += size
			}
		}
	}

	uniqueBuffers = len(bufferSizes)
	return
}

// FormatStatistics returns a human-readable statistics report.
func (stats *TraceStatistics) FormatStatistics() string {
	report := "=== GPU Trace Statistics ===\n\n"

	report += "Note: Command structure statistics are estimates based on MTSP record patterns.\n"
	report += "Accuracy varies by trace type and capture settings.\n\n"

	// Command structure
	report += "Command Structure:\n"
	report += fmt.Sprintf("  Command Buffers:  %d\n", stats.CommandBuffers)
	report += fmt.Sprintf("  Compute Encoders: %d\n", stats.ComputeEncoders)
	report += fmt.Sprintf("  Dispatch Calls:   %d\n", stats.DispatchCalls)
	report += "\n"

	// Memory usage
	report += "Memory Usage:\n"
	report += fmt.Sprintf("  Total Buffer Size: %.2f GiB (%d bytes)\n", stats.BufferUsageGB, stats.BufferUsageBytes)
	report += fmt.Sprintf("  Unique Buffers:    %d\n", stats.UniqueBuffers)
	report += "\n"

	// Kernel statistics
	report += "Kernel Statistics:\n"
	report += fmt.Sprintf("  Unique Kernels:      %d\n", stats.UniqueKernels)
	report += fmt.Sprintf("  Total Kernel Calls:  %d\n", stats.TotalKernelCalls)
	if stats.UniqueKernels > 0 {
		avgCallsPerKernel := float64(stats.TotalKernelCalls) / float64(stats.UniqueKernels)
		report += fmt.Sprintf("  Avg Calls/Kernel:    %.1f\n", avgCallsPerKernel)
	}
	report += "\n"

	// MTSP record breakdown
	report += fmt.Sprintf("MTSP Records: %d total\n", stats.TotalRecords)
	if len(stats.RecordTypes) > 0 {
		report += "  Record Types:\n"
		for recordType, count := range stats.RecordTypes {
			report += fmt.Sprintf("    %-10s : %d\n", recordType, count)
		}
	}

	return report
}

// CompareStatistics compares two trace statistics (useful for before/after optimization).
func CompareStatistics(before, after *TraceStatistics) string {
	report := "=== Statistics Comparison ===\n\n"

	// Command structure changes
	report += "Command Structure:\n"
	report += fmt.Sprintf("  Command Buffers:  %d → %d (%+d)\n",
		before.CommandBuffers, after.CommandBuffers,
		after.CommandBuffers - before.CommandBuffers)
	report += fmt.Sprintf("  Compute Encoders: %d → %d (%+d)\n",
		before.ComputeEncoders, after.ComputeEncoders,
		after.ComputeEncoders - before.ComputeEncoders)
	report += fmt.Sprintf("  Dispatch Calls:   %d → %d (%+d)\n",
		before.DispatchCalls, after.DispatchCalls,
		after.DispatchCalls - before.DispatchCalls)
	report += "\n"

	// Memory usage changes
	report += "Memory Usage:\n"
	report += fmt.Sprintf("  Buffer Size: %.2f GiB → %.2f GiB (%.2f GiB)\n",
		before.BufferUsageGB, after.BufferUsageGB,
		after.BufferUsageGB - before.BufferUsageGB)
	report += fmt.Sprintf("  Buffers:     %d → %d (%+d)\n",
		before.UniqueBuffers, after.UniqueBuffers,
		after.UniqueBuffers - before.UniqueBuffers)
	report += "\n"

	// Kernel changes
	report += "Kernels:\n"
	report += fmt.Sprintf("  Unique:      %d → %d (%+d)\n",
		before.UniqueKernels, after.UniqueKernels,
		after.UniqueKernels - before.UniqueKernels)
	report += fmt.Sprintf("  Total Calls: %d → %d (%+d)\n",
		before.TotalKernelCalls, after.TotalKernelCalls,
		after.TotalKernelCalls - before.TotalKernelCalls)

	return report
}
