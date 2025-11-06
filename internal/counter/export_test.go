package counter

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

func TestExportWithBinaryData(t *testing.T) {
	// Test with single encoder perf trace that has binary counter data
	tracePath := filepath.Join("..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	// Create exporter
	exporter := NewCountersCSVExporter(tr)

	// Export to buffer
	var buf bytes.Buffer
	err = exporter.ExportCountersCSV(&buf)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Parse the generated CSV
	reader := csv.NewReader(strings.NewReader(buf.String()))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("Failed to parse CSV: %v", err)
	}

	if len(rows) < 2 {
		t.Fatal("Expected at least header + 1 data row")
	}

	// Check header
	header := rows[0]
	t.Logf("Generated %d columns", len(header))
	if len(header) != 246 {
		t.Errorf("Expected 246 columns, got %d", len(header))
	}

	// Check first data row
	dataRow := rows[1]
	t.Logf("First row has %d values", len(dataRow))

	// Find column indices
	colIdx := make(map[string]int)
	for i, col := range header {
		colIdx[col] = i
	}

	// Check that we have memory bandwidth data
	if idx, ok := colIdx["Bytes Read From Device Memory"]; ok && idx < len(dataRow) {
		t.Logf("Bytes Read From Device Memory: %s", dataRow[idx])
	}
	if idx, ok := colIdx["Bytes Written To Device Memory"]; ok && idx < len(dataRow) {
		t.Logf("Bytes Written To Device Memory: %s", dataRow[idx])
	}
	if idx, ok := colIdx["Device Memory Bandwidth"]; ok && idx < len(dataRow) {
		t.Logf("Device Memory Bandwidth: %s", dataRow[idx])
	}
	if idx, ok := colIdx["GPU Read Bandwidth"]; ok && idx < len(dataRow) {
		t.Logf("GPU Read Bandwidth: %s", dataRow[idx])
	}
	if idx, ok := colIdx["GPU Write Bandwidth"]; ok && idx < len(dataRow) {
		t.Logf("GPU Write Bandwidth: %s", dataRow[idx])
	}
	if idx, ok := colIdx["Kernel Invocations"]; ok && idx < len(dataRow) {
		t.Logf("Kernel Invocations: %s", dataRow[idx])
	}
}

func TestExportSixEncoders(t *testing.T) {
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	// Create exporter
	exporter := NewCountersCSVExporter(tr)

	// Export to buffer
	var buf bytes.Buffer
	err = exporter.ExportCountersCSV(&buf)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Write to file for manual inspection
	if err := os.WriteFile("/tmp/gputrace-81-export.csv", buf.Bytes(), 0644); err != nil {
		t.Logf("Warning: Could not write CSV to file: %v", err)
	} else {
		t.Logf("CSV written to /tmp/gputrace-81-export.csv")
	}

	// Parse the generated CSV
	reader := csv.NewReader(strings.NewReader(buf.String()))
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("Failed to parse CSV: %v", err)
	}

	t.Logf("Exported %d encoders", len(rows)-1) // -1 for header

	// Find column indices
	header := rows[0]
	colIdx := make(map[string]int)
	for i, col := range header {
		colIdx[col] = i
	}

	// Verify key columns from gputrace-68
	if header[13] != "ALU Utilization" {
		t.Errorf("Column 13 should be 'ALU Utilization', got '%s'", header[13])
	}
	if header[107] != "Kernel Invocations" {
		t.Errorf("Column 107 should be 'Kernel Invocations', got '%s'", header[107])
	}
	if header[108] != "Kernel Occupancy" {
		t.Errorf("Column 108 should be 'Kernel Occupancy', got '%s'", header[108])
	}

	// Check each encoder row
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		encoderLabel := row[colIdx["Encoder Label"]]
		t.Logf("\nEncoder %d: %s", i-1, encoderLabel)

		// gputrace-81: Check key validated metrics
		t.Logf("  ALU Utilization (col 13): %s", row[13])
		t.Logf("  Kernel Invocations (col 107): %s", row[107])
		t.Logf("  Kernel Occupancy (col 108): %s", row[108])

		if idx, ok := colIdx["Bytes Read From Device Memory"]; ok && idx < len(row) {
			t.Logf("  Bytes Read: %s", row[idx])
		}
		if idx, ok := colIdx["Bytes Written To Device Memory"]; ok && idx < len(row) {
			t.Logf("  Bytes Written: %s", row[idx])
		}
		if idx, ok := colIdx["Device Memory Bandwidth"]; ok && idx < len(row) {
			t.Logf("  Device BW: %s", row[idx])
		}
	}
}

func TestExportComparison(t *testing.T) {
	// Compare exported CSV with imported reference CSV
	tracePath := filepath.Join("..", "..", "testdata", "traces", "06-six-encoders", "06-six-encoders-run1-perf.gputrace")

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Skipf("Trace not available: %v", err)
	}
	defer tr.Close()

	// Export our CSV
	exporter := NewCountersCSVExporter(tr)
	var buf bytes.Buffer
	err = exporter.ExportCountersCSV(&buf)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Import reference CSV
	csvData, err := ImportCountersCSV(tr)
	if err != nil {
		t.Logf("Reference CSV not available: %v", err)
		return
	}

	// Parse our exported CSV
	reader := csv.NewReader(strings.NewReader(buf.String()))
	exportedRows, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("Failed to parse exported CSV: %v", err)
	}

	t.Logf("\nComparison:")
	t.Logf("Exported rows: %d", len(exportedRows)-1)
	t.Logf("Reference encoders: %d", len(csvData.Encoders))

	// We expect fewer rows in export since binary parsing may not find all encoders
	if len(exportedRows)-1 > len(csvData.Encoders) {
		t.Errorf("Exported more rows (%d) than reference (%d)", len(exportedRows)-1, len(csvData.Encoders))
	}
}
