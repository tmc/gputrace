package counter

import (
	"encoding/csv"
	"fmt"
	"io"

	"github.com/tmc/gputrace/internal/trace"
)

// Type aliases
type (
	Trace          = trace.Trace
	ComputeEncoder = trace.ComputeEncoder
)

// CountersCSVExporter exports performance counter data in Xcode Counters.csv format.
type CountersCSVExporter struct {
	trace *Trace
}

// CountersCSVExportSummary reports the source of data rows written to Counters.csv.
type CountersCSVExportSummary struct {
	Rows                  int // Data rows written, excluding the header.
	ParsedCounterRows     int // Rows populated from parsed Counters_f_*.raw metrics.
	SyntheticFallbackRows int // Rows populated from synthetic fallback estimates.
}

// HasSyntheticFallback reports whether any exported rows used synthetic estimates.
func (s CountersCSVExportSummary) HasSyntheticFallback() bool {
	return s.SyntheticFallbackRows > 0
}

// NewCountersCSVExporter creates a new CSV exporter for the given trace.
func NewCountersCSVExporter(trace *Trace) *CountersCSVExporter {
	return &CountersCSVExporter{
		trace: trace,
	}
}

// ExportCountersCSV generates a Counters.csv file matching Xcode Instruments format.
// Attempts to use REAL counter data from .gpuprofiler_raw parsing (gputrace-44).
// Falls back to synthetic values if binary data unavailable.
func (e *CountersCSVExporter) ExportCountersCSV(w io.Writer) error {
	_, err := e.ExportCountersCSVWithSummary(w)
	return err
}

// ExportCountersCSVWithSummary generates Counters.csv and returns row source accounting.
func (e *CountersCSVExporter) ExportCountersCSVWithSummary(w io.Writer) (CountersCSVExportSummary, error) {
	writer := csv.NewWriter(w)
	defer writer.Flush()

	var summary CountersCSVExportSummary

	// Write header row
	if err := writer.Write(getCountersCSVHeader()); err != nil {
		return summary, fmt.Errorf("write header: %w", err)
	}

	// Try to get REAL counter data from binary parsing (gputrace-44)
	var encoderMetrics []EncoderCounterMetrics
	var useBinaryData bool
	if e.trace.HasPerfCounters() {
		metrics, err := PopulateEncoderMetricsFromBinaryParsing(e.trace)
		if err == nil && len(metrics) > 0 {
			encoderMetrics = metrics
			useBinaryData = true
		}
	}

	// Get encoder information
	computeEncoders, err := e.trace.ParseComputeEncoders()
	if err != nil {
		return summary, fmt.Errorf("parse compute encoders: %w", err)
	}

	// Generate rows for each encoder
	rowIndex := 1
	commandBufferIndex := 0

	for encIndex, encoder := range computeEncoders {
		// Create labels
		commandBufferLabel := fmt.Sprintf("Command Buffer %d", commandBufferIndex)
		encoderLabel := encoder.Label
		if encoderLabel == "" {
			encoderLabel = fmt.Sprintf("Compute Encoder %d 0x%x", encIndex, encoder.Address)
		}

		// Generate counter values for this encoder
		var row []string
		if useBinaryData && encIndex < len(encoderMetrics) {
			// Use REAL binary-parsed counter data
			row = e.generateCounterRowFromBinaryData(rowIndex, encIndex, commandBufferLabel, encoderLabel, &encoderMetrics[encIndex])
			summary.ParsedCounterRows++
		} else {
			// Fallback to synthetic estimates
			row = e.generateCounterRowSimple(rowIndex, encIndex, commandBufferLabel, encoderLabel, encoder)
			summary.SyntheticFallbackRows++
		}

		if err := writer.Write(row); err != nil {
			return summary, fmt.Errorf("write row %d: %w", rowIndex, err)
		}
		summary.Rows++
		rowIndex++
	}

	return summary, nil
}

// generateCounterRowSimple creates a single CSV row with all 247 columns.
func (e *CountersCSVExporter) generateCounterRowSimple(index, functionIndex int, cbLabel, encoderLabel string, encoder *ComputeEncoder) []string {
	row := make([]string, 247)

	// Get debug group for this encoder based on its label (sequence-based mapping)
	debugGroup := e.trace.GetDebugGroupForLabel(encoderLabel)

	// Columns 1-6: Metadata
	row[0] = fmt.Sprintf("%d", index)         // Index
	row[1] = fmt.Sprintf("%d", functionIndex) // Encoder FunctionIndex
	row[2] = cbLabel                          // CommandBuffer Label
	row[3] = debugGroup                       // Debug Group (hierarchical label)
	row[4] = encoderLabel                     // Encoder Label
	row[5] = ""                               // Empty column

	// Generate synthetic counter values (matching timeline's approach)
	values := e.generateSyntheticCountersSimple()

	// Columns 7-247: Performance metrics (241 metrics)
	// Map synthetic values to appropriate columns
	for i := 6; i < 247; i++ {
		metricName := getMetricNameForColumn(i)
		if val, exists := values[metricName]; exists {
			row[i] = fmt.Sprintf("%.2f", val)
		} else {
			row[i] = "0.00"
		}
	}

	return row
}

// generateCounterRowFromBinaryData creates a CSV row using REAL binary-parsed counter data.
// Maps EncoderCounterMetrics fields to the 247-column Xcode Counters.csv format.
// Uses data from PopulateEncoderMetricsFromBinaryParsing (validated 100% accurate on kernel invocations).
func (e *CountersCSVExporter) generateCounterRowFromBinaryData(index, functionIndex int, cbLabel, encoderLabel string, metrics *EncoderCounterMetrics) []string {
	row := make([]string, 247)

	// Get debug group for this encoder based on its label
	debugGroup := e.trace.GetDebugGroupForLabel(encoderLabel)

	// Columns 1-6: Metadata
	row[0] = fmt.Sprintf("%d", index)         // Index
	row[1] = fmt.Sprintf("%d", functionIndex) // Encoder FunctionIndex
	row[2] = cbLabel                          // CommandBuffer Label
	row[3] = debugGroup                       // Debug Group
	row[4] = encoderLabel                     // Encoder Label
	row[5] = ""                               // Empty column

	// Build map of counter values from binary parsing
	// Only use fields available in EncoderCounterMetrics (counter_sampling.go:143-167)
	values := make(map[string]float64)

	// Core metrics from binary parsing (validated 100% accurate)
	values["Kernel Invocations"] = float64(metrics.DispatchCount) // 100% accurate from gputrace-44
	values["ALU Utilization"] = metrics.ALUUtilization            // From CSV enhancement (gputrace-63)
	values["Kernel Occupancy"] = metrics.KernelOccupancy          // From CSV enhancement (gputrace-63)

	// Utilization metrics
	values["Compute Shader Utilization"] = metrics.ComputeUtilization
	values["Vertex Shader Utilization"] = metrics.VertexUtilization
	values["Fragment Shader Utilization"] = metrics.FragmentUtilization

	// Memory bandwidth - use real extracted values from gputrace-65
	if metrics.BytesReadFromDeviceMemory > 0 || metrics.BytesWrittenToDeviceMemory > 0 {
		values["Bytes Read From Device Memory"] = float64(metrics.BytesReadFromDeviceMemory)
		values["Bytes Written To Device Memory"] = float64(metrics.BytesWrittenToDeviceMemory)
	}
	if metrics.BufferDeviceMemoryBytesRead > 0 || metrics.BufferDeviceMemoryBytesWritten > 0 {
		values["Buffer Device Memory Bytes Read"] = float64(metrics.BufferDeviceMemoryBytesRead)
		values["Buffer Device Memory Bytes Written"] = float64(metrics.BufferDeviceMemoryBytesWritten)
	}
	if metrics.DeviceMemoryBandwidthGBps > 0 {
		values["Device Memory Bandwidth"] = metrics.DeviceMemoryBandwidthGBps
	}
	if metrics.GPUReadBandwidthGBps > 0 {
		values["GPU Read Bandwidth"] = metrics.GPUReadBandwidthGBps
	}
	if metrics.GPUWriteBandwidthGBps > 0 {
		values["GPU Write Bandwidth"] = metrics.GPUWriteBandwidthGBps
	}

	// Cache metrics
	if metrics.CacheHitRate > 0 {
		missRate := 100.0 - metrics.CacheHitRate
		values["Buffer L1 Miss Rate"] = missRate
		values["Texture Cache Miss Rate"] = missRate
		values["Kernel Texture Cache Miss Rate"] = missRate
	}

	// Buffer L1 Cache Metrics (gputrace-66)
	if metrics.BufferL1MissRate > 0 {
		values["Buffer L1 Miss Rate"] = metrics.BufferL1MissRate
	}
	if metrics.BufferL1ReadAccesses > 0 {
		values["Buffer L1 Read Accesses"] = metrics.BufferL1ReadAccesses
	}
	if metrics.BufferL1ReadBandwidth > 0 {
		values["L1 Read Bandwidth"] = metrics.BufferL1ReadBandwidth
	}
	if metrics.BufferL1WriteAccesses > 0 {
		values["Buffer L1 Write Accesses"] = metrics.BufferL1WriteAccesses
	}
	if metrics.BufferL1WriteBandwidth > 0 {
		values["L1 Write Bandwidth"] = metrics.BufferL1WriteBandwidth
	}

	// Shader Utilization Metrics (gputrace-67)
	if metrics.ComputeShaderUtilization > 0 {
		values["Compute Shader Utilization"] = metrics.ComputeShaderUtilization
	}
	if metrics.FragmentShaderUtilization > 0 {
		values["Fragment Shader Utilization"] = metrics.FragmentShaderUtilization
	}
	if metrics.VertexShaderUtilization > 0 {
		values["Vertex Shader Utilization"] = metrics.VertexShaderUtilization
	}
	if metrics.ControlFlowUtilization > 0 {
		values["Control Flow Utilization"] = metrics.ControlFlowUtilization
	}
	if metrics.InstructionThroughputUtil > 0 {
		values["Instruction Throughput Utilization"] = metrics.InstructionThroughputUtil
	}
	if metrics.IntegerAndComplexUtil > 0 {
		values["Integer And Complex Utilization"] = metrics.IntegerAndComplexUtil
	}
	if metrics.IntegerAndConditionalUtil > 0 {
		values["Integer And Conditional Utilization"] = metrics.IntegerAndConditionalUtil
	}
	if metrics.F16Utilization > 0 {
		values["F16 Utilization"] = metrics.F16Utilization
	}
	if metrics.F32Utilization > 0 {
		values["F32 Utilization"] = metrics.F32Utilization
	}

	// GPU time (convert ns to ms)
	if metrics.Duration > 0 {
		values["GPU Time"] = float64(metrics.Duration) / 1_000_000.0
	}

	// Draw counts
	if metrics.DrawCount > 0 {
		values["Primitives"] = float64(metrics.DrawCount)
	}

	// Fragment/Vertex shader metrics based on encoder type
	if metrics.EncoderType == "compute" {
		values["FS ALU Utilization"] = 0.0
		values["FS Occupancy"] = 0.0
		values["VS ALU Utilization"] = 0.0
		values["VS Occupancy"] = 0.0
	}

	// Map values to CSV columns (6-246)
	for i := 6; i < 247; i++ {
		metricName := getMetricNameForColumn(i)
		if val, exists := values[metricName]; exists {
			// Format based on metric type
			if metricName == "Kernel Invocations" ||
				metricName == "Primitives" ||
				metricName == "Threadgroups" ||
				metricName == "Threads" {
				// Integer values
				row[i] = fmt.Sprintf("%.0f", val)
			} else {
				// Float values (percentages, bandwidth, etc.)
				row[i] = fmt.Sprintf("%.2f", val)
			}
		} else {
			row[i] = "0.00"
		}
	}

	return row
}

// generateSyntheticCountersSimple creates synthetic counter values.
// Uses the same estimation approach as the timeline command.
func (e *CountersCSVExporter) generateSyntheticCountersSimple() map[string]float64 {
	values := make(map[string]float64)

	// Core metrics (matching timeline estimates)
	values["ALU Utilization"] = 65.0                  // 65% ALU utilization
	values["Kernel Occupancy"] = 75.0                 // 75% occupancy
	values["Buffer Device Memory Bytes Read"] = 25.15 // MB/s estimate
	values["Buffer Device Memory Bytes Written"] = 19.95
	values["Buffer L1 Miss Rate"] = 10.57 // 10.57% miss rate

	// Memory metrics
	values["Bytes Read From Device Memory"] = 45.10
	values["Bytes Written To Device Memory"] = 39.90
	values["Last Level Cache Bytes Read"] = 15.5
	values["Last Level Cache Bytes Written"] = 12.3

	// Kernel-specific metrics
	values["Kernel Invocations"] = 1.0
	values["Kernel ALU Instructions"] = 15000.0
	values["Kernel ALU Float Instructions"] = 8000.0
	values["Kernel ALU Half Instructions"] = 4000.0
	values["Kernel ALU Integer and Complex Instructions"] = 3000.0

	// Texture metrics
	values["Texture Cache Miss Rate"] = 5.23
	values["Texture Device Memory Bytes Read"] = 8.45
	values["Kernel Texture Cache Miss Rate"] = 5.23

	// Pipeline utilization metrics
	values["Fragment Shader Launch Limiter"] = 15.0
	values["Vertex Shader Launch Limiter"] = 12.0
	values["Compute Shader Launch Limiter"] = 25.0
	values["Texture Filtering Limiter"] = 8.0
	values["L1 Cache Limiter"] = 18.0
	values["Last Level Cache Limiter"] = 10.0

	// Assume compute encoder (most common in ML workloads)
	values["Compute Shader Utilization"] = 70.0
	values["Kernel Occupancy"] = 75.0

	// Fragment/Vertex shader metrics (set to 0 for compute, would be populated for render)
	values["FS ALU Utilization"] = 0.0
	values["FS Occupancy"] = 0.0
	values["FS Buffer Device Memory Bytes Read"] = 0.0
	values["FS Buffer Device Memory Bytes Written"] = 0.0
	values["VS ALU Utilization"] = 0.0
	values["VS Occupancy"] = 0.0

	// All other metrics default to 0.0 (already handled by the row generation)

	return values
}

// getCountersCSVHeader returns the header row for Counters.csv (247 columns).
// Uses the complete 241-metric list from file_mapping.go (gputrace-114).
func getCountersCSVHeader() []string {
	header := make([]string, 247)

	// Columns 1-6: Metadata
	header[0] = "Index"
	header[1] = "Encoder FunctionIndex"
	header[2] = "CommandBuffer Label"
	header[3] = "Debug Group"
	header[4] = "Encoder Label"
	header[5] = ""

	// Columns 7-247: Performance metrics (241 metrics)
	// Use the complete list from file_mapping.go (verified against Xcode Instruments)
	for i, metricName := range AllCounterNames {
		if i+6 < 247 {
			header[i+6] = metricName
		}
	}

	return header
}

// getMetricNameForColumn returns the metric name for a given column index.
func getMetricNameForColumn(colIndex int) string {
	if colIndex < 6 {
		return ""
	}

	header := getCountersCSVHeader()
	if colIndex < len(header) {
		return header[colIndex]
	}

	return ""
}
