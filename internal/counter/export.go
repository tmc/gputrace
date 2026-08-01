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
	Rows              int // Data rows written, excluding the header.
	ParsedCounterRows int // Rows populated from parsed Counters_f_*.raw metrics.
	SkippedRows       int // Encoders with no parsed counter data, written as metadata only.
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
	computeEncoders := e.trace.ParseComputeEncoders()

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
			row = e.generateCounterRowFromBinaryData(rowIndex, encIndex, commandBufferLabel, encoderLabel, &encoderMetrics[encIndex])
			summary.ParsedCounterRows++
		} else {
			// No counter data for this encoder. Write the identifying columns
			// and leave every metric blank: a number here would be read as a
			// measurement, and a zero is indistinguishable from a measured zero.
			row = e.generateCounterRowMetadataOnly(rowIndex, encIndex, commandBufferLabel, encoderLabel)
			summary.SkippedRows++
		}

		if err := writer.Write(row); err != nil {
			return summary, fmt.Errorf("write row %d: %w", rowIndex, err)
		}
		summary.Rows++
		rowIndex++
	}

	return summary, nil
}

// generateCounterRowMetadataOnly creates a CSV row identifying an encoder for
// which no counter data was parsed. Every metric column is left blank.
func (e *CountersCSVExporter) generateCounterRowMetadataOnly(index, functionIndex int, cbLabel, encoderLabel string) []string {
	row := make([]string, 247)
	row[0] = fmt.Sprintf("%d", index)
	row[1] = fmt.Sprintf("%d", functionIndex)
	row[2] = cbLabel
	row[3] = e.trace.DebugGroupForLabel(encoderLabel)
	row[4] = encoderLabel
	return row
}

// generateCounterRowFromBinaryData creates a CSV row using REAL binary-parsed counter data.
// Maps EncoderCounterMetrics fields to the 247-column Xcode Counters.csv format.
// Uses data from PopulateEncoderMetricsFromBinaryParsing (validated 100% accurate on kernel invocations).
func (e *CountersCSVExporter) generateCounterRowFromBinaryData(index, functionIndex int, cbLabel, encoderLabel string, metrics *EncoderCounterMetrics) []string {
	row := make([]string, 247)

	// Get debug group for this encoder based on its label
	debugGroup := e.trace.DebugGroupForLabel(encoderLabel)

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

	// Map values to CSV columns (6-246). Columns gputrace does not know how to
	// derive are left blank rather than zeroed: roughly 240 of the 241 metric
	// columns fall in that bucket, and "0.00" in all of them is
	// indistinguishable from a measured zero.
	for i := 6; i < 247; i++ {
		metricName := getMetricNameForColumn(i)
		if unmeasurableCounters[metricName] {
			// Leave blank rather than 0.00: a zero here would read as a
			// measurement gputrace made.
			continue
		}
		val, exists := values[metricName]
		if !exists {
			continue
		}
		switch metricName {
		case "Kernel Invocations", "Primitives", "Threadgroups", "Threads":
			row[i] = fmt.Sprintf("%.0f", val)
		default:
			row[i] = fmt.Sprintf("%.2f", val)
		}
	}

	return row
}

// unmeasurableCounters names Xcode counter columns that gputrace has no way to
// produce from a trace bundle. Occupancy is a GPU counter sampled at capture
// time; it is not archived in streamData, and on Apple9 registers and
// threadgroup memory are allocated dynamically from L1, so no static residency
// model can supply one either.
var unmeasurableCounters = map[string]bool{
	"Kernel Occupancy": true,
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
