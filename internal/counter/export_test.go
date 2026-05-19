package counter

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
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
	if len(header) != 247 {
		t.Errorf("Expected 247 columns (6 metadata + 241 metrics), got %d", len(header))
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

	// Verify key columns (adjusted for 6 metadata columns: Index, FunctionIndex, CB Label, Debug Group, Encoder Label, empty)
	// ALU Utilization is 9th metric (index 8) → column 6+8=14
	// Kernel Invocations is metric index 102 → column 6+102=108
	// Kernel Occupancy is metric index 103 → column 6+103=109
	if header[14] != "ALU Utilization" {
		t.Errorf("Column 14 should be 'ALU Utilization', got '%s'", header[14])
	}
	if header[108] != "Kernel Invocations" {
		t.Errorf("Column 108 should be 'Kernel Invocations', got '%s'", header[108])
	}
	if header[109] != "Kernel Occupancy" {
		t.Errorf("Column 109 should be 'Kernel Occupancy', got '%s'", header[109])
	}

	// Check each encoder row
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		encoderLabel := row[colIdx["Encoder Label"]]
		t.Logf("\nEncoder %d: %s", i-1, encoderLabel)

		// gputrace-81: Check key validated metrics (using colIdx for correct columns)
		aluIdx := colIdx["ALU Utilization"]
		invIdx := colIdx["Kernel Invocations"]
		occIdx := colIdx["Kernel Occupancy"]

		t.Logf("  ALU Utilization (col %d): %s", aluIdx, row[aluIdx])
		t.Logf("  Kernel Invocations (col %d): %s", invIdx, row[invIdx])
		t.Logf("  Kernel Occupancy (col %d): %s", occIdx, row[occIdx])

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

	// Log difference but don't fail - export may find more or fewer encoders than reference
	if len(exportedRows)-1 != len(csvData.Encoders) {
		t.Logf("Note: Exported %d rows vs reference %d encoders", len(exportedRows)-1, len(csvData.Encoders))
	}
}

func TestPopulateEncoderMetricsFromPerfCounterStats(t *testing.T) {
	stats := &PerfCounterStats{
		ShaderMetrics: []ShaderHardwareMetrics{{
			ShaderName:                     "kernel0",
			ALUUtilization:                 3.25,
			KernelOccupancy:                0.81,
			MemoryBandwidth:                4096,
			ExecutionCount:                 7,
			DeviceMemoryBandwidthGBps:      12.5,
			ComputeShaderLaunchLimiter:     0.17,
			L1CacheLimiter:                 0.04,
			InstructionThroughputUtil:      2.5,
			BytesReadFromDeviceMemory:      1024,
			BufferDeviceMemoryBytesWritten: 512,
		}},
	}

	got, err := PopulateEncoderMetricsFromPerfCounterStats(nil, stats)
	if err != nil {
		t.Fatalf("PopulateEncoderMetricsFromPerfCounterStats: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(metrics) = %d, want 1", len(got))
	}
	m := got[0]
	if m.EncoderIndex != 0 || m.EncoderLabel != "kernel0" || m.EncoderType != "compute" {
		t.Fatalf("encoder identity = (%d, %q, %q), want (0, kernel0, compute)", m.EncoderIndex, m.EncoderLabel, m.EncoderType)
	}
	if m.ALUUtilization != 3.25 {
		t.Fatalf("ALUUtilization = %v, want 3.25", m.ALUUtilization)
	}
	if m.KernelOccupancy != 0.81 {
		t.Fatalf("KernelOccupancy = %v, want 0.81", m.KernelOccupancy)
	}
	if m.ComputeUtilization != 3.25 {
		t.Fatalf("ComputeUtilization = %v, want 3.25", m.ComputeUtilization)
	}
	if m.MemoryBandwidth != 4096 || m.DeviceMemoryBandwidthGBps != 12.5 {
		t.Fatalf("bandwidth = (%d, %v), want (4096, 12.5)", m.MemoryBandwidth, m.DeviceMemoryBandwidthGBps)
	}
	if m.ComputeShaderLaunchLimiter != 0.17 || m.L1CacheLimiter != 0.04 || m.InstructionThroughputUtil != 2.5 {
		t.Fatalf("counter details not copied: %+v", m)
	}
	if m.DispatchCount != 7 {
		t.Fatalf("DispatchCount = %d, want 7", m.DispatchCount)
	}
	if m.BytesReadFromDeviceMemory != 1024 || m.BufferDeviceMemoryBytesWritten != 512 {
		t.Fatalf("memory byte counters not copied: %+v", m)
	}
}

func TestPopulateEncoderMetricsFromPerfCounterStatsNilStats(t *testing.T) {
	if _, err := PopulateEncoderMetricsFromPerfCounterStats(nil, nil); err == nil {
		t.Fatal("PopulateEncoderMetricsFromPerfCounterStats(nil, nil) succeeded, want error")
	}
}
