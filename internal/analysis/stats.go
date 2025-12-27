package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tmc/gputrace/internal/trace"
)

// TraceStatistics contains comprehensive statistics about a GPU trace.
type TraceStatistics struct {
	// Memory usage
	BufferUsageBytes uint64
	BufferUsageGB    float64
	BufferSizeSum    uint64 // Sum of all buffer sizes (same as BufferUsageBytes for non-heap buffers)
	UniqueBuffers    int

	HeapUsageBytes uint64
	HeapUsageMB    float64
	UniqueHeaps    int

	UnusedMemoryBytes uint64
	UnusedMemoryMB    float64

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

	// MTLB Libraries
	MTLBLibraries int

	// Unused resources (from metadata)
	UnusedBuffers   int
	UnusedTextures  int
	UnusedFunctions int
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

	// MTLB Libraries
	stats.MTLBLibraries = len(t.MTLBLibraries)

	// Extract memory usage from device resources (Buffers and Heaps)
	bufferUsage, uniqueBuffers, heapUsage, uniqueHeaps, unusedUsage, unusedBuffers := extractMemoryUsage(t)
	stats.BufferUsageBytes = bufferUsage
	stats.BufferUsageGB = float64(bufferUsage) / (1024 * 1024 * 1024)
	stats.BufferSizeSum = bufferUsage
	stats.UniqueBuffers = uniqueBuffers

	stats.HeapUsageBytes = heapUsage
	stats.HeapUsageMB = float64(heapUsage) / (1024 * 1024)
	stats.UniqueHeaps = uniqueHeaps

	stats.UnusedMemoryBytes = unusedUsage
	stats.UnusedMemoryMB = float64(unusedUsage) / (1024 * 1024)
	if unusedBuffers > 0 {
		stats.UnusedBuffers = unusedBuffers
	}

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

	// Unused resource counts from metadata
	if t.Metadata != nil {
		stats.UnusedBuffers = t.Metadata.UnusedBufferCount
		stats.UnusedTextures = t.Metadata.UnusedTextureCount
		stats.UnusedFunctions = t.Metadata.UnusedFunctionCount
	}

	return stats, nil
}

// extractMemoryUsage calculates total buffer and heap memory usage from files in the trace directory.
func extractMemoryUsage(t *trace.Trace) (bufferBytes uint64, uniqueBuffers int, heapBytes uint64, uniqueHeaps int, unusedBytes uint64, unusedCount int) {
	// Scan for MTLBuffer-*-0 and MTLHeap-*-0 files
	entries, err := os.ReadDir(t.Path)
	if err != nil {
		return 0, 0, 0, 0, 0, 0
	}

	bufferSizes := make(map[string]uint64)
	heapSizes := make(map[string]uint64)

	for _, entry := range entries {
		name := entry.Name()

		// buffers
		if strings.HasPrefix(name, "MTLBuffer-") && strings.HasSuffix(name, "-0") {
			id := strings.TrimPrefix(name, "MTLBuffer-")
			id = strings.TrimSuffix(id, "-0")
			if size := getFileSize(t.Path, name); size > 0 {
				bufferSizes[id] = size
				bufferBytes += size
			}
			continue
		}

		// heaps
		if strings.HasPrefix(name, "MTLHeap-") && strings.HasSuffix(name, "-0") {
			id := strings.TrimPrefix(name, "MTLHeap-")
			id = strings.TrimSuffix(id, "-0")
			if size := getFileSize(t.Path, name); size > 0 {
				heapSizes[id] = size
				heapBytes += size
			}
		}
	}

	uniqueBuffers = len(bufferSizes)
	uniqueHeaps = len(heapSizes)

	// Scan for unused-device-resources to calculate unused memory
	// These files contain strings of filenames (e.g. "MTLBuffer-123-0") that are unused
	unusedFiles := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "unused-device-resources-") {
			content, err := os.ReadFile(filepath.Join(t.Path, name))
			if err == nil {
				// Simple heuristic: Regex find all MTLBuffer strings
				re := regexp.MustCompile(`MTLBuffer-\d+-0`)
				matches := re.FindAllString(string(content), -1)
				for _, match := range matches {
					id := strings.TrimPrefix(match, "MTLBuffer-")
					id = strings.TrimSuffix(id, "-0")
					unusedFiles[id] = struct{}{}
				}
			}
		}
	}

	for id := range unusedFiles {
		if size, ok := bufferSizes[id]; ok {
			unusedBytes += size
			unusedCount++
		}
	}

	return
}

func getFileSize(dir, name string) uint64 {
	info, err := os.Stat(filepath.Join(dir, name))
	if err == nil && !info.IsDir() {
		return uint64(info.Size())
	}
	return 0
}

// FormatStatistics returns a human-readable statistics report.
func (stats *TraceStatistics) FormatStatistics() string {
	report := "=== GPU Trace Statistics ===\n\n"

	// Memory usage
	report += "Memory Usage:\n"
	report += fmt.Sprintf("  Total Buffer Size: %.2f GiB (%d bytes)\n", stats.BufferUsageGB, stats.BufferUsageBytes)
	report += fmt.Sprintf("  Total Heap Size:   %.2f MiB (%d bytes)\n", stats.HeapUsageMB, stats.HeapUsageBytes)
	report += fmt.Sprintf("  Buffer Size Sum:   %d bytes\n", stats.BufferSizeSum)
	report += fmt.Sprintf("  Unique Buffers:    %d\n", stats.UniqueBuffers)
	report += fmt.Sprintf("  Unique Heaps:      %d\n", stats.UniqueHeaps)
	if stats.UnusedMemoryBytes > 0 {
		report += fmt.Sprintf("  Unused Memory:     %.2f MiB (%d bytes)\n", stats.UnusedMemoryMB, stats.UnusedMemoryBytes)
	}
	if stats.UnusedBuffers > 0 || stats.UnusedTextures > 0 || stats.UnusedFunctions > 0 {
		report += fmt.Sprintf("  Unused Resources:  %d buffers, %d textures, %d functions\n",
			stats.UnusedBuffers, stats.UnusedTextures, stats.UnusedFunctions)
	}
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
			report += fmt.Sprintf("  Compute Encoders:  %d (Raw Records: %d)\n", stats.ComputeEncoders, stats.RecordTypes["Cuw"])
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

	if stats.MTLBLibraries > 0 {
		report += fmt.Sprintf("\nMTLB Sidecars: %d integrated\n", stats.MTLBLibraries)
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
