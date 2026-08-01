package counter

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/gputrace/internal/command"
	"github.com/tmc/gputrace/internal/profilerraw"
	"github.com/tmc/gputrace/internal/trace"
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
	ShaderName                     string  // Shader/kernel function name
	PipelineState                  uint64  // Pipeline state object address
	SIMDGroups                     int     // Number of SIMD groups executed
	AllocatedRegs                  int     // Temporary register count
	HighRegister                   int     // Highest register used
	SpilledBytes                   int     // Bytes spilled to memory
	DeviceLoadCount                int     // Device memory load instructions
	DeviceStoreCount               int     // Device memory store instructions
	HasPipelineStats               bool    // Compiler statistics were found for this shader
	ALUUtilization                 float64 // ALU utilization percentage (0-100)
	MemoryBandwidth                uint64  // Memory bandwidth used (bytes)
	ExecutionCount                 int     // Number of times this shader executed
	TotalCycles                    uint64  // Total GPU cycles spent
	BytesReadFromDeviceMemory      uint64  // Total bytes read from device memory
	BytesWrittenToDeviceMemory     uint64  // Total bytes written to device memory
	BufferDeviceMemoryBytesRead    uint64  // Buffer bytes read from device memory
	BufferDeviceMemoryBytesWritten uint64  // Buffer bytes written to device memory
	DeviceMemoryBandwidthGBps      float64 // Device memory bandwidth in GB/s
	GPUReadBandwidthGBps           float64 // GPU read bandwidth in GB/s
	GPUWriteBandwidthGBps          float64 // GPU write bandwidth in GB/s

	// Shader Launch Limiters (0-100% range, typically 0.03-0.08)
	ComputeShaderLaunchLimiter  float64 // Compute shader launch limiter percentage
	FragmentShaderLaunchLimiter float64 // Fragment shader launch limiter percentage
	VertexShaderLaunchLimiter   float64 // Vertex shader launch limiter percentage

	// Pipeline Limiters (0-100% range, typically 0.01-3.74 for complex shaders)
	ControlFlowLimiter           float64 // Control flow limiter percentage
	InstructionThroughputLimiter float64 // Instruction throughput limiter percentage
	IntegerAndComplexLimiter     float64 // Integer and complex instruction limiter percentage
	IntegerAndConditionalLimiter float64 // Integer and conditional instruction limiter percentage
	F16Limiter                   float64 // FP16 instruction limiter percentage
	F32Limiter                   float64 // FP32 instruction limiter percentage

	// Memory Limiters (0-100% range, typically 0.01-0.15)
	L1CacheLimiter        float64 // L1 cache limiter percentage
	LastLevelCacheLimiter float64 // Last level cache limiter percentage
	MMULimiter            float64 // MMU limiter percentage

	// Texture Limiters (0-100% range, typically 0.01-0.04)
	TextureFilteringLimiter float64 // Texture filtering limiter percentage
	TextureWriteLimiter     float64 // Texture write limiter percentage
	TextureReadLimiter      float64 // Texture read limiter percentage

	// Buffer L1 Cache Metrics (gputrace-66)
	BufferL1MissRate       float64 // Buffer L1 cache miss rate percentage (0-100)
	BufferL1ReadAccesses   float64 // Buffer L1 read accesses count
	BufferL1ReadBandwidth  float64 // Buffer L1 read bandwidth (GB/s)
	BufferL1WriteAccesses  float64 // Buffer L1 write accesses count
	BufferL1WriteBandwidth float64 // Buffer L1 write bandwidth (GB/s)

	// Shader Utilization Metrics (gputrace-67)
	ComputeShaderUtilization  float64 // Compute shader utilization percentage (0-100)
	FragmentShaderUtilization float64 // Fragment shader utilization percentage (0-100)
	VertexShaderUtilization   float64 // Vertex shader utilization percentage (0-100)
	ControlFlowUtilization    float64 // Control flow utilization percentage (0-100)
	InstructionThroughputUtil float64 // Instruction throughput utilization percentage (0-100)
	IntegerAndComplexUtil     float64 // Integer and complex instruction utilization percentage (0-100)
	IntegerAndConditionalUtil float64 // Integer and conditional instruction utilization percentage (0-100)
	F16Utilization            float64 // FP16 instruction utilization percentage (0-100)
	F32Utilization            float64 // FP32 instruction utilization percentage (0-100)

	// Instruction Counts from PipelineStats (streamData)
	InstructionCount       int // Total instruction count from shader compilation
	ALUInstructionCount    int // ALU instruction count
	FP32InstructionCount   int // FP32 instruction count
	FP16InstructionCount   int // FP16 instruction count
	INT32InstructionCount  int // INT32 instruction count
	INT16InstructionCount  int // INT16 instruction count
	BranchInstructionCount int // Branch instruction count
	ThreadgroupMemory      int // Threadgroup memory usage in bytes
}

// CounterRecord represents a single parsed record from a counter file.
type CounterRecord struct {
	Offset     int64  // File offset where record starts
	RecordType uint32 // Type identifier
	RecordSize uint32 // Size of this record in bytes
	Data       []byte // Raw record data
	IsMetadata bool   // True if this is a metadata record (2.3-2.9 KB)
	EncoderID  uint64 // Encoder identifier from metadata record
}

// ParsePerfCounters parses hardware performance counters from .gpuprofiler_raw files.
//
// This function extracts detailed GPU execution metrics including:
// - Shader execution counts and timing
// - Register allocation and spill data
// - ALU utilization
// - Memory bandwidth usage
//
// Returns PerfCounterStats with hardware metrics, or error if parsing fails.
func ParsePerfCounters(t *trace.Trace) (*PerfCounterStats, error) {
	// Find .gpuprofiler_raw directory (adjacent to, inside, or equal to the bundle)
	perfDir := profilerraw.FindDir(t.Path)
	if perfDir == "" {
		return nil, fmt.Errorf("no performance counter data: .gpuprofiler_raw not found")
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
	var parseErrors []string
	for _, file := range files {
		fileStats, metrics, err := parseCounterFileWithMetrics(file)
		if err != nil {
			parseErrors = append(parseErrors, fmt.Sprintf("%s: %v", filepath.Base(file), err))
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
	if stats.FilesProcessed == 0 {
		if len(parseErrors) > 0 {
			return nil, fmt.Errorf("no valid performance counter records in %s: %s", perfDir, strings.Join(parseErrors, "; "))
		}
		return nil, fmt.Errorf("no valid performance counter records in %s", perfDir)
	}

	// Convert map to slice
	for _, metric := range metricsMap {
		stats.ShaderMetrics = append(stats.ShaderMetrics, *metric)
	}

	// Try CSV enhancement (gputrace-63): Use Xcode-exported CSV as ground truth
	// CSV is the most accurate source, so apply it first
	csvApplied := false
	if csvData, err := ImportCountersCSV(t); err == nil {
		if err := EnhanceMetricsFromCSV(stats, csvData); err == nil {
			csvApplied = true
			// Successfully enhanced metrics with CSV data
		}
	}

	// Apply deterministic metric extraction (gputrace-115)
	// Only if CSV wasn't applied, as CSV is more reliable
	if !csvApplied {
		if err := extractDeterministicMetrics(perfDir, stats); err == nil {
			// Successfully enhanced metrics with deterministic extraction
		}
	}

	// Try to correlate with shader names from trace
	if err := correlateShaderNames(t, stats); err == nil {
		// Correlation succeeded, metrics now have shader names
	}

	// Enhance with native streamData (pipeline compilation stats)
	// This provides register counts, instruction counts without CSV export
	if err := enhanceFromStreamData(t, stats); err == nil {
		// Successfully enhanced metrics with native streamData
	}

	// Set confidence based on number of files processed
	if stats.FilesProcessed > 0 {
		stats.ConfidenceLevel = 1.0 // We have actual hardware data
	}

	return stats, nil
}

// counterFileStats represents statistics from a single counter file.
type counterFileStats struct {
	DispatchCount int
	TotalRecords  int
}

// parseCounterFileWithMetrics parses a counter file and returns record statistics.
//
// It no longer derives per-encoder shader metrics. The former sample-record
// path keyed off a 464-byte record size and an unsourced ÷27.75 scale on offset
// 0x0064; neither survived contact with real archives (see the removal note on
// parseCounterRecord), so nothing it produced was ever emitted. Deterministic
// metrics come from extractDeterministicMetrics and the streamData path instead.
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

	rawRecords := profilerraw.Records(data)
	stats.TotalRecords = len(rawRecords)
	if len(rawRecords) == 0 {
		return nil, nil, fmt.Errorf("no counter record markers found")
	}

	// Parse all records
	records := make([]*CounterRecord, 0, len(rawRecords))
	for _, raw := range rawRecords {
		// Skip if record is too small
		if len(raw.Data) < 16 {
			continue
		}

		record := parseCounterRecord(raw.Data, raw.Offset)
		if record != nil {
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		return nil, nil, fmt.Errorf("no valid counter records found")
	}

	return stats, nil, nil
}

// correlateShaderNames attempts to match pipeline state addresses with shader names from the trace.
func correlateShaderNames(t *trace.Trace, stats *PerfCounterStats) error {
	// Parse command buffers to get encoder/shader information
	capture, err := command.OpenCapture(t)
	if err != nil {
		return fmt.Errorf("parse command buffers: %w", err)
	}
	commandBuffers := capture.CommandBuffers()

	// Build map of pipeline state address to shader name
	pipelineToName := make(map[uint64]string)

	for _, cb := range commandBuffers {
		dcb, err := capture.Detailed(cb.Index)
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
// It classifies a record as encoder metadata by size (2,300-2,900 bytes) and
// pulls a candidate encoder ID from offset 0x01b4. [?] Both come from one
// archive and neither has been checked against a second.
//
// A "sample record" path used to sit alongside this: records of exactly 464
// bytes, from which Kernel Invocations was read at offset 0x0064 and divided by
// 27.75, with the remaining metrics recovered by scanning for float32 values in
// hand-tuned ranges. It was removed because the divisor could not be sourced.
// 27.75 was back-fitted from a single pair, 28,416 raw against 1,024 in one
// Xcode CSV export (28,416/1,024 = 27.75 exactly); no hardware quantity
// explains 111/4, and no second observation was ever recorded. Measuring the
// records in a real archive settled it: over the first five Counters_f_*.raw of
// /tmp/qwen25-05b-staticmask-warm-tokens2-4-rep1-perfdata3.gputrace, zero of
// ~30,000 records are 464 bytes (the common sizes are 1742, 612, 671, 8192),
// so the branch produced no metrics at all and, because emission was gated on
// a non-zero invocation count, nothing downstream of it was ever displayed.
// Removing it changes no user-visible value.
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
	}

	return record
}

// findAllFloatsInRange scans record data for all float32 values in the specified range.
// Returns up to maxCount matching values, sorted by offset order.
//
// [?] This is a scan, not a field read. It is only sound because its callers
// have already narrowed the search to a single Counters_f_N.raw file that
// GPUCounterGraph.plist names as holding one counter, so the range filter is
// picking among values of a known quantity rather than guessing which quantity
// a word represents. Do not reuse it on a file whose counter is unknown.
func findAllFloatsInRange(data []byte, minVal, maxVal float64, maxCount int) []float64 {
	results := make([]float64, 0, maxCount)
	seen := make(map[float64]bool)

	for i := 0; i < len(data)-4 && len(results) < maxCount; i += 4 {
		bits := binary.LittleEndian.Uint32(data[i : i+4])
		val := float64(intBitsToFloat32(bits))
		if val >= minVal && val <= maxVal && !isNaNOrInf(val) && !seen[val] {
			results = append(results, val)
			seen[val] = true
		}
	}

	return results
}

// isNaNOrInf checks if a float is NaN or Infinity
func isNaNOrInf(val float64) bool {
	return val != val || val > 1e308 || val < -1e308
}

func intBitsToFloat32(bits uint32) float32 {
	return math.Float32frombits(bits)
}

// CounterType indicates the data type and aggregation method for a counter.
type CounterType int

const (
	// CounterTypePercentage is for percentage values (0-100), aggregated by AVERAGE
	CounterTypePercentage CounterType = iota
	// CounterTypeBytes is for byte counts (uint64), aggregated by SUM
	CounterTypeBytes
	// CounterTypeCount is for counts/accesses (float64), aggregated by AVERAGE
	CounterTypeCount
	// CounterTypeBandwidth is for bandwidth in GB/s (float64), aggregated by AVERAGE
	CounterTypeBandwidth
)

// counterConfig defines extraction parameters for a specific counter.
type counterConfig struct {
	FileIndex   int         // Counters_f_N.raw file index
	Name        string      // Counter name (for logging/debugging)
	Type        CounterType // Data type and aggregation method
	MinValue    float64     // Minimum valid value
	MaxValue    float64     // Maximum valid value
	ApplyFunc   func(*ShaderHardwareMetrics, float64)
	ApplyUint64 func(*ShaderHardwareMetrics, uint64) // For byte counters
}

// counterConfigs defines all supported counter extractions.
// These configs are enhanced with metadata from GPUCounterGraph.plist via plist_mapping.go.
var counterConfigs = []counterConfig{
	// ALU Utilization (file 12) - percentage 0-100
	{12, "ALU Utilization", CounterTypePercentage, 0.0, 100.0,
		func(m *ShaderHardwareMetrics, v float64) { m.ALUUtilization = v }, nil},

	// Memory byte counters (uint64)
	{21, "Buffer Device Memory Bytes Read", CounterTypeBytes, 0, 0,
		nil, func(m *ShaderHardwareMetrics, v uint64) { m.BufferDeviceMemoryBytesRead = v }},
	{22, "Buffer Device Memory Bytes Written", CounterTypeBytes, 0, 0,
		nil, func(m *ShaderHardwareMetrics, v uint64) { m.BufferDeviceMemoryBytesWritten = v }},
	{28, "Bytes Read From Device Memory", CounterTypeBytes, 0, 0,
		nil, func(m *ShaderHardwareMetrics, v uint64) { m.BytesReadFromDeviceMemory = v }},
	{29, "Bytes Written To Device Memory", CounterTypeBytes, 0, 0,
		nil, func(m *ShaderHardwareMetrics, v uint64) { m.BytesWrittenToDeviceMemory = v }},

	// Buffer L1 Cache metrics (files 23-27)
	{23, "Buffer L1 Miss Rate", CounterTypePercentage, 0.0, 100.0,
		func(m *ShaderHardwareMetrics, v float64) { m.BufferL1MissRate = v }, nil},
	{24, "Buffer L1 Read Accesses", CounterTypeCount, 0.0, 10000.0,
		func(m *ShaderHardwareMetrics, v float64) { m.BufferL1ReadAccesses = v }, nil},
	{25, "Buffer L1 Read Bandwidth", CounterTypeBandwidth, 0.0, 1000.0,
		func(m *ShaderHardwareMetrics, v float64) { m.BufferL1ReadBandwidth = v }, nil},
	{26, "Buffer L1 Write Accesses", CounterTypeCount, 0.0, 10000.0,
		func(m *ShaderHardwareMetrics, v float64) { m.BufferL1WriteAccesses = v }, nil},
	{27, "Buffer L1 Write Bandwidth", CounterTypeBandwidth, 0.0, 1000.0,
		func(m *ShaderHardwareMetrics, v float64) { m.BufferL1WriteBandwidth = v }, nil},

	// Shader launch limiters and utilization (files 33-36)
	{33, "Compute Shader Launch Limiter", CounterTypePercentage, 0.0, 100.0,
		func(m *ShaderHardwareMetrics, v float64) { m.ComputeShaderLaunchLimiter = v }, nil},
	{34, "Compute Shader Launch Utilization", CounterTypePercentage, 0.0, 100.0,
		func(m *ShaderHardwareMetrics, v float64) { m.ComputeShaderUtilization = v }, nil},
	{35, "Control Flow Limiter", CounterTypePercentage, 0.0, 100.0,
		func(m *ShaderHardwareMetrics, v float64) { m.ControlFlowLimiter = v }, nil},
	{36, "Control Flow Utilization", CounterTypePercentage, 0.0, 100.0,
		func(m *ShaderHardwareMetrics, v float64) { m.ControlFlowUtilization = v }, nil},
}

// extractDeterministicMetrics extracts metrics deterministically using file-to-counter mapping.
//
// This function implements gputrace-115: Replace heuristic extraction with deterministic
// approach. For each metric, we:
// 1. Look up which Counters_f_X file contains it
// 2. Parse that specific file
// 3. Aggregate samples correctly (AVERAGE for percentages, SUM for counts)
func extractDeterministicMetrics(perfDir string, stats *PerfCounterStats) error {
	// Build map from encoder index to metrics (for targeted updates)
	encoderMetrics := make([]*ShaderHardwareMetrics, len(stats.ShaderMetrics))
	for i := range stats.ShaderMetrics {
		encoderMetrics[i] = &stats.ShaderMetrics[i]
	}

	// Extract all configured counters
	for _, cfg := range counterConfigs {
		if cfg.Type == CounterTypeBytes {
			if err := extractByteCounterFromFile(perfDir, cfg, encoderMetrics); err != nil {
				// Continue with other metrics even if one fails
				continue
			}
		} else {
			if err := extractFloatMetricFromFile(perfDir, cfg, encoderMetrics); err != nil {
				// Continue with other metrics even if one fails
				continue
			}
		}
	}

	return nil
}

// extractFloatMetricFromFile extracts a float64 metric from a specific counter file.
//
// Strategy for gputrace-115:
// 1. Read the designated Counters_f_N.raw file
// 2. Group records by encoder (metadata + sample records)
// 3. Extract float32 values from sample records
// 4. Aggregate per-encoder (AVERAGE for percentages/bandwidth)
func extractFloatMetricFromFile(perfDir string, cfg counterConfig, encoderMetrics []*ShaderHardwareMetrics) error {
	if cfg.ApplyFunc == nil {
		return fmt.Errorf("no apply function for %s", cfg.Name)
	}

	// Construct file path
	filePath := filepath.Join(perfDir, fmt.Sprintf("Counters_f_%d.raw", cfg.FileIndex))

	// Read file
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	records := profilerraw.Records(data)

	// Group records by encoder and extract per-encoder values
	encoderValues := extractEncoderFloatValues(records, cfg.MinValue, cfg.MaxValue)

	// Apply aggregated values to encoder metrics
	for i, metric := range encoderMetrics {
		if metric == nil {
			continue
		}
		if i < len(encoderValues) && len(encoderValues[i]) > 0 {
			// Aggregate values for this encoder (AVERAGE for float metrics)
			aggregated := averageValues(encoderValues[i])
			cfg.ApplyFunc(metric, aggregated)
		}
	}

	return nil
}

// extractByteCounterFromFile extracts a uint64 byte counter from a specific counter file.
//
// Strategy:
// 1. Read the designated Counters_f_N.raw file
// 2. Group records by encoder (metadata + sample records)
// 3. Extract uint64 values from sample records
// 4. SUM per-encoder (byte counters accumulate)
func extractByteCounterFromFile(perfDir string, cfg counterConfig, encoderMetrics []*ShaderHardwareMetrics) error {
	if cfg.ApplyUint64 == nil {
		return fmt.Errorf("no apply function for %s", cfg.Name)
	}

	// Construct file path
	filePath := filepath.Join(perfDir, fmt.Sprintf("Counters_f_%d.raw", cfg.FileIndex))

	// Read file
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	records := profilerraw.Records(data)

	// Group records by encoder and extract per-encoder byte values
	encoderValues := extractEncoderByteValues(records)

	// Apply summed values to encoder metrics
	for i, metric := range encoderMetrics {
		if metric == nil {
			continue
		}
		if i < len(encoderValues) && len(encoderValues[i]) > 0 {
			// SUM byte counter values for this encoder
			var sum uint64
			for _, v := range encoderValues[i] {
				sum += v
			}
			cfg.ApplyUint64(metric, sum)
		}
	}

	return nil
}

// extractEncoderFloatValues groups float values by encoder from counter file data.
//
// Returns slice of slices: encoderValues[encoderIdx][sampleIdx] = value
func extractEncoderFloatValues(records []profilerraw.Record, minVal, maxVal float64) [][]float64 {
	encoderValues := make([][]float64, 0)
	var currentEncoderValues []float64

	for _, record := range records {
		// Metadata records (2300-2900 bytes) mark encoder boundaries
		if len(record.Data) >= 2300 && len(record.Data) <= 2900 {
			// Save previous encoder's values
			if len(currentEncoderValues) > 0 {
				encoderValues = append(encoderValues, currentEncoderValues)
			}
			currentEncoderValues = make([]float64, 0)
			continue
		}

		// Sample records (464 bytes) contain metric values
		if len(record.Data) != 464 {
			continue
		}

		// Extract float32 values in the valid range
		candidates := findAllFloatsInRange(record.Data, minVal, maxVal, 5)
		for _, val := range candidates {
			if val > 0.0001 { // Filter near-zero noise
				currentEncoderValues = append(currentEncoderValues, val)
			}
		}
	}

	// Don't forget the last encoder
	if len(currentEncoderValues) > 0 {
		encoderValues = append(encoderValues, currentEncoderValues)
	}

	return encoderValues
}

// extractEncoderByteValues groups uint64 byte values by encoder from counter file data.
//
// Returns slice of slices: encoderValues[encoderIdx][sampleIdx] = value
func extractEncoderByteValues(records []profilerraw.Record) [][]uint64 {
	encoderValues := make([][]uint64, 0)
	var currentEncoderValues []uint64

	for _, record := range records {
		// Metadata records (2300-2900 bytes) mark encoder boundaries
		if len(record.Data) >= 2300 && len(record.Data) <= 2900 {
			// Save previous encoder's values
			if len(currentEncoderValues) > 0 {
				encoderValues = append(encoderValues, currentEncoderValues)
			}
			currentEncoderValues = make([]uint64, 0)
			continue
		}

		// Sample records (464 bytes) contain metric values
		if len(record.Data) != 464 {
			continue
		}

		// Extract uint64 values that look like byte counts
		// Look for values in reasonable byte count range (1KB - 1GB per sample)
		byteVals := extractByteCountCandidates(record.Data)
		currentEncoderValues = append(currentEncoderValues, byteVals...)
	}

	// Don't forget the last encoder
	if len(currentEncoderValues) > 0 {
		encoderValues = append(encoderValues, currentEncoderValues)
	}

	return encoderValues
}

// extractByteCountCandidates extracts uint64 values that look like byte counts from record data.
//
// Byte counts for GPU memory operations are typically:
// - Minimum: 1000 bytes (1KB - small buffer access)
// - Maximum: 100,000,000 bytes (100MB - large buffer access per sample)
func extractByteCountCandidates(data []byte) []uint64 {
	const (
		minBytes = 1000        // 1KB minimum
		maxBytes = 100_000_000 // 100MB maximum per sample
	)

	var values []uint64
	seen := make(map[uint64]bool)

	// Scan for uint64 values at 8-byte aligned offsets
	for i := 0; i < len(data)-8; i += 8 {
		val := binary.LittleEndian.Uint64(data[i : i+8])

		// Check if value is in reasonable byte count range
		if val >= minBytes && val <= maxBytes && !seen[val] {
			values = append(values, val)
			seen[val] = true
		}
	}

	// Also check 4-byte aligned uint64 values (may not be 8-byte aligned)
	for i := 4; i < len(data)-8; i += 8 {
		val := binary.LittleEndian.Uint64(data[i : i+8])

		if val >= minBytes && val <= maxBytes && !seen[val] {
			values = append(values, val)
			seen[val] = true
		}
	}

	return values
}

// averageValues computes the arithmetic mean of a slice of float64.
func averageValues(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// enhanceFromStreamData enhances metrics with native pipeline compilation stats from streamData.
// streamData is an NSKeyedArchiver plist in .gpuprofiler_raw that contains:
// - Register counts (Temporary register count, Uniform register count)
// - Spilled bytes
// - Instruction counts (ALU, FP32, FP16, etc.)
// - Function names
//
// This provides metadata without requiring CSV export from Xcode.
func enhanceFromStreamData(t *trace.Trace, stats *PerfCounterStats) error {
	// Find .gpuprofiler_raw directory
	perfDir := t.Path + ".gpuprofiler_raw"
	if _, err := os.Stat(perfDir); os.IsNotExist(err) {
		// Check inside trace bundle
		entries, err := os.ReadDir(t.Path)
		if err != nil {
			return fmt.Errorf("no performance counter data: %w", err)
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
			return fmt.Errorf("no .gpuprofiler_raw directory found")
		}
	}

	// Parse streamData
	streamStats, err := ParseStreamData(perfDir, nil)
	if err != nil {
		return fmt.Errorf("parse streamData: %w", err)
	}

	// Build function name to pipeline stats map
	// Match by function name substring (kernel names may be mangled differently)
	pipelineByFunc := make(map[string]*PipelineStats)
	for i := range streamStats.Pipelines {
		p := &streamStats.Pipelines[i]
		// Also index by pipeline ID for direct lookup
		pipelineByFunc[fmt.Sprintf("pipeline_%d", p.PipelineID)] = p
	}

	// Index function names for fuzzy matching
	for i, funcName := range streamStats.FunctionNames {
		// Associate function name with nearest pipeline (heuristic)
		// Pipeline IDs and function names aren't directly linked in streamData,
		// but they often appear in related order
		if i < len(streamStats.Pipelines) {
			pipelineByFunc[funcName] = &streamStats.Pipelines[i]
		}
	}

	// Enhance shader metrics with pipeline stats
	enhanced := 0
	for i := range stats.ShaderMetrics {
		metric := &stats.ShaderMetrics[i]

		// Try exact match first
		if p, ok := pipelineByFunc[metric.ShaderName]; ok {
			applyPipelineStats(metric, p)
			enhanced++
			continue
		}

		// Try substring match (kernel names may have prefixes/suffixes).
		// An empty name on either side must not match everything.
		shaderName := strings.ToLower(metric.ShaderName)
		for funcName, p := range pipelineByFunc {
			funcName := strings.ToLower(funcName)
			if shaderName == "" || funcName == "" {
				continue
			}
			if strings.Contains(shaderName, funcName) || strings.Contains(funcName, shaderName) {
				applyPipelineStats(metric, p)
				enhanced++
				break
			}
		}
	}

	// If we have more pipelines than shader metrics, add them as new entries
	// This handles cases where binary parsing missed some encoders
	if len(streamStats.Pipelines) > len(stats.ShaderMetrics) {
		for i := len(stats.ShaderMetrics); i < len(streamStats.Pipelines); i++ {
			p := &streamStats.Pipelines[i]
			funcName := ""
			if i < len(streamStats.FunctionNames) {
				funcName = streamStats.FunctionNames[i]
			}

			newMetric := ShaderHardwareMetrics{
				ShaderName:       funcName,
				PipelineState:    uint64(p.PipelineID),
				AllocatedRegs:    p.TemporaryRegisterCount,
				SpilledBytes:     p.SpilledBytes,
				DeviceLoadCount:  p.DeviceLoadCount,
				DeviceStoreCount: p.DeviceStoreCount,
				HasPipelineStats: true,
			}
			stats.ShaderMetrics = append(stats.ShaderMetrics, newMetric)
		}
	}

	return nil
}

// applyPipelineStats applies pipeline compilation stats to shader metrics.
func applyPipelineStats(metric *ShaderHardwareMetrics, p *PipelineStats) {
	// Only update if we don't already have values (prefer CSV data if present)
	if metric.AllocatedRegs == 0 {
		metric.AllocatedRegs = p.TemporaryRegisterCount
	}
	if metric.SpilledBytes == 0 {
		metric.SpilledBytes = p.SpilledBytes
	}

	// Apply instruction counts from PipelineStats (streamData)
	metric.InstructionCount = p.InstructionCount
	metric.ALUInstructionCount = p.ALUInstructionCount
	metric.FP32InstructionCount = p.FP32InstructionCount
	metric.FP16InstructionCount = p.FP16InstructionCount
	metric.INT32InstructionCount = p.INT32InstructionCount
	metric.INT16InstructionCount = p.INT16InstructionCount
	metric.BranchInstructionCount = p.BranchInstructionCount
	metric.ThreadgroupMemory = p.ThreadgroupMemory
	metric.DeviceLoadCount = p.DeviceLoadCount
	metric.DeviceStoreCount = p.DeviceStoreCount
	metric.HasPipelineStats = true
}
