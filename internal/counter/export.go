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

const (
	// countersCSVColumns is the total column count in Xcode's Counters.csv.
	countersCSVColumns = 247
	// countersCSVMetricStart is the first metric column. Columns 0-3 identify
	// the encoder and column 4 is blank, matching Xcode's export exactly.
	countersCSVMetricStart = 5
)

// CountersCSVExporter exports performance counter data in Xcode Counters.csv format.
type CountersCSVExporter struct {
	trace *Trace
}

// CountersCSVExportSummary reports the source of data rows written to Counters.csv.
type CountersCSVExportSummary struct {
	Rows int // Data rows written, excluding the header.
	// ParsedCounterRows counts rows carrying measured counter values. It is
	// always zero: parsed rows are pipeline-scoped and no encoder join exists.
	// The field and its test are a tripwire for reintroducing that path.
	ParsedCounterRows int
	SkippedRows       int // Encoders written as metadata only.
}

// NewCountersCSVExporter creates a new CSV exporter for the given trace.
func NewCountersCSVExporter(trace *Trace) *CountersCSVExporter {
	return &CountersCSVExporter{
		trace: trace,
	}
}

// ExportCountersCSV generates a Counters.csv-shaped export. Metric columns are
// blank until a capture-backed join maps their source rows to encoders.
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

		// Parsed Counters_f rows are pipeline-scoped. Indexing them by encoder
		// position mislabels data whenever the two cardinalities happen to agree,
		// so no counter value is exported until a stable join exists.
		row := e.generateCounterRowMetadataOnly(rowIndex, encIndex, commandBufferLabel, encoderLabel)
		summary.SkippedRows++

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
	row := make([]string, countersCSVColumns)
	row[0] = fmt.Sprintf("%d", index)
	row[1] = fmt.Sprintf("%d", functionIndex)
	row[2] = cbLabel
	row[3] = encoderLabel
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

// getCountersCSVHeader returns the header row for Counters.csv. The layout is
// byte-identical to Xcode's own export: four identifying columns, one blank
// column, then the 242 metric names from AllCounterNames.
func getCountersCSVHeader() []string {
	header := make([]string, countersCSVColumns)

	// Columns 1-6: Metadata
	header[0] = "Index"
	header[1] = "Encoder FunctionIndex"
	header[2] = "CommandBuffer Label"
	header[3] = "Encoder Label"
	header[4] = ""

	// Columns 6-247: the 242 metric names, in Xcode's order.
	for i, metricName := range AllCounterNames {
		if i+countersCSVMetricStart < countersCSVColumns {
			header[i+countersCSVMetricStart] = metricName
		}
	}

	return header
}

// getMetricNameForColumn returns the metric name for a given column index.
func getMetricNameForColumn(colIndex int) string {
	if colIndex < countersCSVMetricStart {
		return ""
	}

	header := getCountersCSVHeader()
	if colIndex < len(header) {
		return header[colIndex]
	}

	return ""
}
