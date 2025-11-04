package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// TraceStatistics contains comprehensive statistics about a GPU trace.
type TraceStatistics struct {
	// Memory usage
	BufferUsageBytes uint64
	BufferUsageGB    float64
	UniqueBuffers    int

	// Kernels
	UniqueKernels int

	// Command buffers
	CommandBuffers int

	// Compute encoders
	ComputeEncoders int

	// Dispatch calls
	DispatchCalls int

	// MTSP records
	TotalRecords int
	RecordTypes  map[string]int
}

// ExtractStatistics analyzes a trace and extracts comprehensive statistics.
func ExtractStatistics(t *trace.Trace) (*TraceStatistics, error) {
	stats := &TraceStatistics{
		RecordTypes: make(map[string]int),
	}

	// Parse MTSP records
	records, err := t.ParseMTSPRecords()
	if err != nil {
		return nil, fmt.Errorf("parse MTSP records: %w", err)
	}

	stats.TotalRecords = len(records)

	// Count record types
	for _, record := range records {
		stats.RecordTypes[record.Type]++
	}

	// Extract buffer usage from device resources
	bufferUsage, uniqueBuffers := extractBufferUsage(t)
	stats.BufferUsageBytes = bufferUsage
	stats.BufferUsageGB = float64(bufferUsage) / (1024 * 1024 * 1024)
	stats.UniqueBuffers = uniqueBuffers

	// Kernel statistics
	stats.UniqueKernels = len(t.KernelNames)

	// Command buffer count
	cbCount, err := t.CountCommandBuffers()
	if err == nil {
		stats.CommandBuffers = cbCount
	}

	// Compute encoder count
	ceCount, err := t.CountComputeEncoders()
	if err == nil {
		stats.ComputeEncoders = ceCount
	}

	// Dispatch call count
	dispatchCount, err := t.CountDispatchCalls()
	if err == nil {
		stats.DispatchCalls = dispatchCount
	}

	return stats, nil
}

// extractBufferUsage calculates total buffer memory usage from MTLBuffer files in the trace directory.
func extractBufferUsage(t *trace.Trace) (totalBytes uint64, uniqueBuffers int) {
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

	// Memory usage
	report += "Memory Usage:\n"
	report += fmt.Sprintf("  Total Buffer Size: %.2f GiB (%d bytes)\n", stats.BufferUsageGB, stats.BufferUsageBytes)
	report += fmt.Sprintf("  Unique Buffers:    %d\n", stats.UniqueBuffers)
	report += "\n"

	// Kernel statistics
	report += "Kernel Statistics:\n"
	report += fmt.Sprintf("  Unique Kernels: %d\n", stats.UniqueKernels)
	report += "\n"

	// Command buffer and encoder statistics
	if stats.CommandBuffers > 0 || stats.ComputeEncoders > 0 || stats.DispatchCalls > 0 {
		report += "GPU Execution:\n"
		if stats.CommandBuffers > 0 {
			report += fmt.Sprintf("  Command Buffers:   %d\n", stats.CommandBuffers)
		}
		if stats.ComputeEncoders > 0 {
			report += fmt.Sprintf("  Compute Encoders:  %d\n", stats.ComputeEncoders)
		}
		if stats.DispatchCalls > 0 {
			report += fmt.Sprintf("  Dispatch Calls:    %d\n", stats.DispatchCalls)
		}
		report += "\n"
	}

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

	// Memory usage changes
	report += "Memory Usage:\n"
	report += fmt.Sprintf("  Buffer Size: %.2f GiB → %.2f GiB (%.2f GiB)\n",
		before.BufferUsageGB, after.BufferUsageGB,
		after.BufferUsageGB-before.BufferUsageGB)
	report += fmt.Sprintf("  Buffers:     %d → %d (%+d)\n",
		before.UniqueBuffers, after.UniqueBuffers,
		after.UniqueBuffers-before.UniqueBuffers)
	report += "\n"

	// Kernel changes
	report += "Kernels:\n"
	report += fmt.Sprintf("  Unique: %d → %d (%+d)\n",
		before.UniqueKernels, after.UniqueKernels,
		after.UniqueKernels-before.UniqueKernels)

	return report
}
