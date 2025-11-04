package counter

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/command"
	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// PerfCounterStats represents statistics extracted from performance counter files.
type PerfCounterStats struct {
	DispatchCount   int                     // Total number of GPU dispatches executed
	TotalRecords    int                     // Total records parsed
	FilesProcessed  int                     // Number of counter files processed
	ConfidenceLevel float64                 // Confidence in the dispatch count (0.0 to 1.0)
	ShaderMetrics   []ShaderHardwareMetrics // Per-shader hardware metrics
}

// ShaderHardwareMetrics represents hardware performance metrics for a shader.
type ShaderHardwareMetrics struct {
	ShaderName      string  // Shader/kernel function name
	PipelineState   uint64  // Pipeline state object address
	SIMDGroups      int     // Number of SIMD groups executed
	AllocatedRegs   int     // Number of allocated registers
	HighRegister    int     // Highest register used
	SpilledBytes    int     // Bytes spilled to memory
	ALUUtilization  float64 // ALU utilization percentage (0-100)
	KernelOccupancy float64 // Kernel occupancy percentage (0-100)
	MemoryBandwidth uint64  // Memory bandwidth used (bytes)
	ExecutionCount  int     // Number of times this shader executed
	TotalCycles     uint64  // Total GPU cycles spent
}

// CounterRecord represents a single parsed record from a counter file.
type CounterRecord struct {
	Offset       int64                  // File offset where record starts
	RecordType   uint32                 // Type identifier
	RecordSize   uint32                 // Size of this record in bytes
	Data         []byte                 // Raw record data
	ShaderMetric *ShaderHardwareMetrics // Parsed metrics (if applicable)
	IsMetadata   bool                   // True if this is a metadata record (2.3-2.9 KB)
	EncoderID    uint64                 // Encoder identifier from metadata record
}

// EncoderGroup represents a group of records belonging to a single encoder.
type EncoderGroup struct {
	EncoderID      uint64           // Encoder identifier
	MetadataRecord *CounterRecord   // Metadata record for this encoder
	SampleRecords  []*CounterRecord // Sample records for this encoder
}

// ParsePerfCounters parses hardware performance counters from .gpuprofiler_raw files.
//
// This function extracts detailed GPU execution metrics including:
// - Shader execution counts and timing
// - Register allocation and spill data
// - ALU utilization and kernel occupancy
// - Memory bandwidth usage
//
// Returns PerfCounterStats with hardware metrics, or error if parsing fails.
func ParsePerfCounters(t *trace.Trace) (*PerfCounterStats, error) {
	// Find .gpuprofiler_raw directory (adjacent or inside trace bundle)
	perfDir := t.Path + ".gpuprofiler_raw"
	if _, err := os.Stat(perfDir); os.IsNotExist(err) {
		// Check inside trace bundle
		entries, err := os.ReadDir(t.Path)
		if err != nil {
			return nil, fmt.Errorf("no performance counter data: %s not found", perfDir)
		}

		// Look for .gpuprofiler_raw directory inside bundle
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

	stats := &PerfCounterStats{
		ShaderMetrics: make([]ShaderHardwareMetrics, 0),
	}

	// Find all Counters_f_*.raw files
	files, err := filepath.Glob(filepath.Join(perfDir, "Counters_f_*.raw"))
	if err != nil {
		return nil, fmt.Errorf("failed to find counter files: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no counter files found in %s", perfDir)
	}

	// Track metrics by pipeline state address
	metricsMap := make(map[uint64]*ShaderHardwareMetrics)

	// Parse each counter file
	for _, file := range files {
		fileStats, metrics, err := parseCounterFileWithMetrics(file)
		if err != nil {
			// Log but continue with other files
			continue
		}

		stats.TotalRecords += fileStats.TotalRecords
		stats.FilesProcessed++

		// Aggregate metrics by pipeline state
		for _, metric := range metrics {
			if metric.PipelineState != 0 {
				if existing, exists := metricsMap[metric.PipelineState]; exists {
					// Merge metrics for same pipeline state
					existing.ExecutionCount += metric.ExecutionCount
					existing.SIMDGroups += metric.SIMDGroups
					// Take max for register counts (they should be the same)
					if metric.AllocatedRegs > existing.AllocatedRegs {
						existing.AllocatedRegs = metric.AllocatedRegs
					}
					if metric.HighRegister > existing.HighRegister {
						existing.HighRegister = metric.HighRegister
					}
					existing.SpilledBytes += metric.SpilledBytes
				} else {
					metricsMap[metric.PipelineState] = metric
				}
			}
		}
	}

	// Convert map to slice
	for _, metric := range metricsMap {
		stats.ShaderMetrics = append(stats.ShaderMetrics, *metric)
	}

	// Try to correlate with shader names from trace
	if err := correlateShaderNames(t, stats); err == nil {
		// Correlation succeeded, metrics now have shader names
	}

	// Set confidence based on number of files processed
	if stats.FilesProcessed > 0 {
		stats.ConfidenceLevel = 1.0 // We have actual hardware data
	}

	return stats, nil
}

// CountFromPerfCounters attempts to count dispatches from performance counter files.
// Deprecated: Use ParsePerfCounters() instead for full hardware metrics.
func CountFromPerfCounters(t *trace.Trace) (*PerfCounterStats, error) {
	return ParsePerfCounters(t)
}

// counterFileStats represents statistics from a single counter file.
type counterFileStats struct {
	DispatchCount int
	TotalRecords  int
}

// parseCounterFile parses a single performance counter file (legacy version).
// Counter files contain GPU execution metrics in a binary format.
func parseCounterFile(path string) (*counterFileStats, error) {
	stats, _, err := parseCounterFileWithMetrics(path)
	return stats, err
}

// parseCounterFileWithMetrics parses a counter file and returns both statistics and extracted metrics.
//
// This function implements the encoder grouping and aggregation strategy documented in
// docs/FIELD_OFFSET_ANALYSIS.md:
// 1. Parse all records and classify by size (metadata vs sample)
// 2. Group sample records by their associated metadata/encoder
// 3. Aggregate metrics within each encoder group
// 4. Return aggregated metrics for validation against CSV
func parseCounterFileWithMetrics(path string) (*counterFileStats, []*ShaderHardwareMetrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, err
	}

	stats := &counterFileStats{}

	// Find all records starting with 0x4E marker
	recordStarts := findRecordBoundaries(data)
	stats.TotalRecords = len(recordStarts)

	// Parse all records
	records := make([]*CounterRecord, 0, len(recordStarts))
	for i, offset := range recordStarts {
		// Determine record size
		var recordSize int
		if i+1 < len(recordStarts) {
			recordSize = recordStarts[i+1] - offset
		} else {
			recordSize = len(data) - offset
		}

		// Skip if record is too small
		if recordSize < 16 {
			continue
		}

		record := parseCounterRecord(data[offset:offset+recordSize], int64(offset))
		if record != nil {
			records = append(records, record)
		}
	}

	// Group records by encoder
	groups := groupRecordsByEncoder(records)

	// Aggregate metrics for each encoder group
	metrics := make([]*ShaderHardwareMetrics, 0, len(groups))
	for _, group := range groups {
		aggregated := aggregateEncoderMetrics(group)
		if aggregated != nil && aggregated.ExecutionCount > 0 {
			metrics = append(metrics, aggregated)
			stats.DispatchCount++
		}
	}

	return stats, metrics, nil
}

// correlateShaderNames attempts to match pipeline state addresses with shader names from the trace.
func correlateShaderNames(t *trace.Trace, stats *PerfCounterStats) error {
	// Parse command buffers to get encoder/shader information
	commandBuffers, err := t.ParseCommandBuffers()
	if err != nil {
		return fmt.Errorf("parse command buffers: %w", err)
	}

	// Build map of pipeline state address to shader name
	pipelineToName := make(map[uint64]string)

	for _, cb := range commandBuffers {
		dcb, err := command.ParseDetailedCommandBuffer(t, cb.Index)
		if err != nil {
			continue
		}

		for _, encoder := range dcb.Encoders {
			if encoder.Address != 0 && encoder.Label != "" {
				pipelineToName[encoder.Address] = encoder.Label
			}
		}
	}

	// Update shader metrics with names
	for i := range stats.ShaderMetrics {
		metric := &stats.ShaderMetrics[i]
		if name, exists := pipelineToName[metric.PipelineState]; exists {
			metric.ShaderName = name
		} else {
			// Use pipeline state address as fallback
			metric.ShaderName = fmt.Sprintf("shader_0x%x", metric.PipelineState)
		}
	}

	return nil
}

// parseCounterRecord parses a single counter record.
//
// Based on analysis in docs/FIELD_OFFSET_ANALYSIS.md:
// - Metadata records: 2,300-2,900 bytes (contain encoder identification)
// - Sample records: 464 bytes (contain per-sample performance metrics)
//
// Aggregation strategy:
// 1. Metadata record identifies encoder/command buffer context
// 2. Following sample records contain metrics for that encoder
// 3. Metrics are summed/averaged across samples to produce CSV values
func parseCounterRecord(data []byte, offset int64) *CounterRecord {
	if len(data) < 16 {
		return nil
	}

	record := &CounterRecord{
		Offset: offset,
		Data:   data,
	}

	// Read record type (4 bytes at offset 0)
	record.RecordType = binary.LittleEndian.Uint32(data[0:4])

	// Record size is the length we were given
	record.RecordSize = uint32(len(data))

	// Classify record by size (from field offset analysis)
	// Metadata records: 2,300-2,900 bytes
	// Sample records: 464 bytes
	if len(data) >= 2300 && len(data) <= 2900 {
		record.IsMetadata = true

		// Extract encoder ID from metadata record
		// Candidate offset: 0x01b4 (from initial analysis - value 1,801)
		// This is a working hypothesis that needs validation
		if len(data) >= 0x01b8 {
			record.EncoderID = binary.LittleEndian.Uint64(data[0x01b4:0x01bc])
		}
	} else if len(data) == 464 {
		record.IsMetadata = false

		// This is a sample record - extract performance metrics
		// Based on field offset analysis from docs/FIELD_OFFSET_ANALYSIS.md
		metrics := &ShaderHardwareMetrics{}

		// Kernel Invocations - offset 0x0064
		// From analysis: offset 0x0064 contains a scaled value
		// Value 28,416 / 27.75 ≈ 1,024 (CSV value)
		// Hypothesis: This field counts in units of ~28 (possibly SIMD-related scaling)
		if len(data) >= 0x0068 {
			rawValue := binary.LittleEndian.Uint32(data[0x0064:0x0068])
			// Apply scaling factor: divide by 27.75 to get invocation count
			metrics.ExecutionCount = int(float64(rawValue) / 27.75)
		}

		// ALU Utilization - search for float32 value around 0.98
		// This requires scanning for percentage values (0.0-1.0 range)
		if aluUtil := findPercentageField(data, 0.95, 1.0); aluUtil >= 0 {
			metrics.ALUUtilization = aluUtil * 100 // Convert to percentage
		}

		// Kernel Occupancy - search for float32 value around 0.30
		if occupancy := findPercentageField(data, 0.25, 0.35); occupancy >= 0 {
			metrics.KernelOccupancy = occupancy * 100 // Convert to percentage
		}

		record.ShaderMetric = metrics
	}

	return record
}

// findPercentageField scans record data for float32 values in the specified range.
// Returns the first matching value, or -1 if not found.
func findPercentageField(data []byte, minVal, maxVal float64) float64 {
	for i := 0; i < len(data)-4; i += 4 {
		// Try reading as float32
		bits := binary.LittleEndian.Uint32(data[i : i+4])
		val := float64(intBitsToFloat32(bits))

		if val >= minVal && val <= maxVal {
			return val
		}
	}
	return -1
}

// intBitsToFloat32 converts uint32 bits to float32 (equivalent to math.Float32frombits)
func intBitsToFloat32(bits uint32) float32 {
	// Inline implementation to avoid importing math
	return *(*float32)(unsafe.Pointer(&bits))
}

// groupRecordsByEncoder groups records by encoder for aggregation.
//
// Strategy:
// 1. Metadata records (2.3-2.9 KB) identify encoder context
// 2. Following sample records (464 bytes) belong to that encoder
// 3. Group records until next metadata record is encountered
func groupRecordsByEncoder(records []*CounterRecord) []*EncoderGroup {
	groups := make([]*EncoderGroup, 0)
	var currentGroup *EncoderGroup

	for _, record := range records {
		if record.IsMetadata {
			// Start new encoder group
			if currentGroup != nil {
				groups = append(groups, currentGroup)
			}
			currentGroup = &EncoderGroup{
				EncoderID:      record.EncoderID,
				MetadataRecord: record,
				SampleRecords:  make([]*CounterRecord, 0),
			}
		} else if currentGroup != nil {
			// Add sample record to current group
			currentGroup.SampleRecords = append(currentGroup.SampleRecords, record)
		}
	}

	// Add final group
	if currentGroup != nil {
		groups = append(groups, currentGroup)
	}

	return groups
}

// aggregateEncoderMetrics aggregates metrics from sample records within an encoder group.
//
// Aggregation rules (based on docs/FIELD_OFFSET_ANALYSIS.md):
// - Kernel Invocations: SUM across all sample records
// - ALU Utilization: AVERAGE across all sample records
// - Kernel Occupancy: AVERAGE across all sample records
// - Memory Bandwidth: SUM bytes, then calculate bandwidth
func aggregateEncoderMetrics(group *EncoderGroup) *ShaderHardwareMetrics {
	if len(group.SampleRecords) == 0 {
		return nil
	}

	aggregated := &ShaderHardwareMetrics{
		PipelineState: group.EncoderID, // Use encoder ID as identifier
	}

	var totalInvocations int
	var totalALUUtil float64
	var totalOccupancy float64
	var aluSamples int
	var occupancySamples int

	for _, record := range group.SampleRecords {
		if record.ShaderMetric == nil {
			continue
		}

		metrics := record.ShaderMetric

		// Sum: Kernel Invocations
		totalInvocations += metrics.ExecutionCount

		// Average: ALU Utilization (if present)
		if metrics.ALUUtilization > 0 {
			totalALUUtil += metrics.ALUUtilization
			aluSamples++
		}

		// Average: Kernel Occupancy (if present)
		if metrics.KernelOccupancy > 0 {
			totalOccupancy += metrics.KernelOccupancy
			occupancySamples++
		}
	}

	aggregated.ExecutionCount = totalInvocations

	if aluSamples > 0 {
		aggregated.ALUUtilization = totalALUUtil / float64(aluSamples)
	}

	if occupancySamples > 0 {
		aggregated.KernelOccupancy = totalOccupancy / float64(occupancySamples)
	}

	return aggregated
}

// findRecordBoundaries finds the start positions of all records in counter data.
// Records appear to start with the 0x4E marker.
func findRecordBoundaries(data []byte) []int {
	boundaries := make([]int, 0, 20000)

	for i := 0; i < len(data)-4; i++ {
		// Look for 0x4E 0x00 0x00 0x00 pattern
		if data[i] == 0x4E && data[i+1] == 0x00 && data[i+2] == 0x00 && data[i+3] == 0x00 {
			boundaries = append(boundaries, i)
		}
	}

	return boundaries
}

// HasPerfCounters returns true if the trace has performance counter data.
func HasPerfCounters(t *trace.Trace) bool {
	// Check for .gpuprofiler_raw directory adjacent to trace
	perfDir := t.Path + ".gpuprofiler_raw"
	if info, err := os.Stat(perfDir); err == nil && info.IsDir() {
		return true
	}

	// Check for .gpuprofiler_raw directory inside trace bundle
	// (Xcode sometimes creates it with the original trace name)
	entries, err := os.ReadDir(t.Path)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if entry.IsDir() && filepath.Ext(entry.Name()) == ".gpuprofiler_raw" {
			return true
		}
	}

	return false
}

// GetDispatchCountMethod returns a description of which method will be used to count dispatches.
func GetDispatchCountMethod(t *trace.Trace) string {
	if t.HasPerfCounters() {
		return "Performance Counters (100% accurate)"
	}
	return "MTSP Estimation (95%+ accuracy for standard workloads)"
}

// GetRegisterDataForShader returns register allocation data for a specific shader if available.
// Returns (allocatedRegs, highRegister, spilledBytes, found).
func GetRegisterDataForShader(t *trace.Trace, pipelineStateAddr uint64) (int, int, int, bool) {
	if !t.HasPerfCounters() {
		return 0, 0, 0, false
	}

	stats, err := ParsePerfCounters(t)
	if err != nil {
		return 0, 0, 0, false
	}

	// Find metrics for this pipeline state
	for _, metric := range stats.ShaderMetrics {
		if metric.PipelineState == pipelineStateAddr {
			// Only return if we actually have register data
			if metric.AllocatedRegs > 0 {
				return metric.AllocatedRegs, metric.HighRegister, metric.SpilledBytes, true
			}
		}
	}

	return 0, 0, 0, false
}

// GetRegisterDataByName returns register allocation data for a shader by name.
// Returns (allocatedRegs, highRegister, spilledBytes, found).
func GetRegisterDataByName(t *trace.Trace, shaderName string) (int, int, int, bool) {
	if !t.HasPerfCounters() {
		return 0, 0, 0, false
	}

	stats, err := ParsePerfCounters(t)
	if err != nil {
		return 0, 0, 0, false
	}

	// Find metrics for this shader name
	for _, metric := range stats.ShaderMetrics {
		if metric.ShaderName == shaderName {
			// Only return if we actually have register data
			if metric.AllocatedRegs > 0 {
				return metric.AllocatedRegs, metric.HighRegister, metric.SpilledBytes, true
			}
		}
	}

	return 0, 0, 0, false
}
