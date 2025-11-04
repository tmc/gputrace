package counter

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// CSVCounterData represents performance counter data imported from Xcode Counters.csv.
type CSVCounterData struct {
	Encoders []CSVEncoderMetrics // Per-encoder metrics
}

// CSVEncoderMetrics represents metrics for a single encoder from CSV.
type CSVEncoderMetrics struct {
	Index                         int
	EncoderFunctionIndex          int
	CommandBufferLabel            string
	EncoderLabel                  string
	ALUUtilization                float64
	KernelInvocations             int
	KernelOccupancy               float64
	BytesReadFromDeviceMemory     uint64
	BytesWrittenToDeviceMemory    uint64
	BufferDeviceMemoryBytesRead   uint64
	BufferDeviceMemoryBytesWritten uint64
	DeviceMemoryBandwidth         float64 // GB/s
	GPUReadBandwidth              float64 // GB/s
	GPUWriteBandwidth             float64 // GB/s
	L1ReadBandwidth               float64 // GB/s
	L1WriteBandwidth              float64 // GB/s
	BufferL1ReadBandwidth         float64 // GB/s
	BufferL1WriteBandwidth        float64 // GB/s
}

// ImportCountersCSV imports performance counters from an Xcode Counters.csv file.
//
// The CSV file should be located adjacent to the trace or in the same directory.
// This function provides a reliable alternative to binary .gpuprofiler_raw parsing.
func ImportCountersCSV(t *trace.Trace) (*CSVCounterData, error) {
	// Try to find Counters.csv file
	csvPath, err := findCountersCSV(t.Path)
	if err != nil {
		return nil, err
	}

	return ParseCountersCSV(csvPath)
}

// ParseCountersCSV parses a Counters.csv file exported by Xcode Instruments.
func ParseCountersCSV(csvPath string) (*CSVCounterData, error) {
	f, err := os.Open(csvPath)
	if err != nil {
		return nil, fmt.Errorf("open CSV: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)

	// Read header row
	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	// Build column index map
	colIdx := make(map[string]int)
	for i, header := range headers {
		colIdx[header] = i
	}

	// Verify required columns exist
	requiredCols := []string{
		"Index", "Encoder FunctionIndex", "CommandBuffer Label", "Encoder Label",
	}
	for _, col := range requiredCols {
		if _, exists := colIdx[col]; !exists {
			return nil, fmt.Errorf("missing required column: %s", col)
		}
	}

	data := &CSVCounterData{
		Encoders: make([]CSVEncoderMetrics, 0),
	}

	// Read data rows
	for {
		row, err := reader.Read()
		if err != nil {
			break // EOF or error
		}

		if len(row) < 5 {
			continue // Skip invalid rows
		}

		encoder := CSVEncoderMetrics{}

		// Parse required fields
		if idx, err := strconv.Atoi(row[colIdx["Index"]]); err == nil {
			encoder.Index = idx
		}
		if funcIdx, err := strconv.Atoi(row[colIdx["Encoder FunctionIndex"]]); err == nil {
			encoder.EncoderFunctionIndex = funcIdx
		}
		encoder.CommandBufferLabel = row[colIdx["CommandBuffer Label"]]
		encoder.EncoderLabel = row[colIdx["Encoder Label"]]

		// Parse optional performance metrics
		encoder.ALUUtilization = parseFloat(row, colIdx, "ALU Utilization")
		encoder.KernelInvocations = parseInt(row, colIdx, "Kernel Invocations")
		encoder.KernelOccupancy = parseFloat(row, colIdx, "Kernel Occupancy")

		// Memory bandwidth fields
		encoder.BytesReadFromDeviceMemory = parseUint64(row, colIdx, "Bytes Read From Device Memory")
		encoder.BytesWrittenToDeviceMemory = parseUint64(row, colIdx, "Bytes Written To Device Memory")
		encoder.BufferDeviceMemoryBytesRead = parseUint64(row, colIdx, "Buffer Device Memory Bytes Read")
		encoder.BufferDeviceMemoryBytesWritten = parseUint64(row, colIdx, "Buffer Device Memory Bytes Written")
		encoder.DeviceMemoryBandwidth = parseFloat(row, colIdx, "Device Memory Bandwidth")
		encoder.GPUReadBandwidth = parseFloat(row, colIdx, "GPU Read Bandwidth")
		encoder.GPUWriteBandwidth = parseFloat(row, colIdx, "GPU Write Bandwidth")
		encoder.L1ReadBandwidth = parseFloat(row, colIdx, "L1 Read Bandwidth")
		encoder.L1WriteBandwidth = parseFloat(row, colIdx, "L1 Write Bandwidth")
		encoder.BufferL1ReadBandwidth = parseFloat(row, colIdx, "Buffer L1 Read Bandwidth")
		encoder.BufferL1WriteBandwidth = parseFloat(row, colIdx, "Buffer L1 Write Bandwidth")

		data.Encoders = append(data.Encoders, encoder)
	}

	return data, nil
}

// findCountersCSV locates the Counters.csv file for a trace.
func findCountersCSV(tracePath string) (string, error) {
	baseName := filepath.Base(tracePath)
	baseName = strings.TrimSuffix(baseName, ".gputrace")

	// Remove common suffixes like "-perf", "-perf2"
	baseName = strings.TrimSuffix(baseName, "-perf")
	baseName = strings.TrimSuffix(baseName, "-perf2")
	baseName = strings.TrimSuffix(baseName, "-run1")
	baseName = strings.TrimSuffix(baseName, "-run2")
	baseName = strings.TrimSuffix(baseName, "-run3")

	// Try common locations
	candidates := []string{
		// Same directory as trace with full original name
		filepath.Join(filepath.Dir(tracePath), filepath.Base(tracePath)+" Counters.csv"),
		// Same directory with stripped suffix
		filepath.Join(filepath.Dir(tracePath), baseName+" Counters.csv"),
		// Parent directory
		filepath.Join(filepath.Dir(filepath.Dir(tracePath)), filepath.Base(tracePath)+" Counters.csv"),
		filepath.Join(filepath.Dir(filepath.Dir(tracePath)), baseName+" Counters.csv"),
	}

	// Also try wildcards in same directory
	pattern := filepath.Join(filepath.Dir(tracePath), "*Counters.csv")
	matches, err := filepath.Glob(pattern)
	if err == nil && len(matches) > 0 {
		// Return first match
		return matches[0], nil
	}

	// Try exact matches
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("Counters.csv not found near %s (tried %d locations)", tracePath, len(candidates))
}

// Helper functions to safely parse CSV fields

func parseFloat(row []string, colIdx map[string]int, colName string) float64 {
	if idx, exists := colIdx[colName]; exists && idx < len(row) {
		if val, err := strconv.ParseFloat(row[idx], 64); err == nil {
			return val
		}
	}
	return 0
}

func parseInt(row []string, colIdx map[string]int, colName string) int {
	if idx, exists := colIdx[colName]; exists && idx < len(row) {
		// Try as float first (CSV may have "1024.00")
		if val, err := strconv.ParseFloat(row[idx], 64); err == nil {
			return int(val)
		}
	}
	return 0
}

func parseUint64(row []string, colIdx map[string]int, colName string) uint64 {
	if idx, exists := colIdx[colName]; exists && idx < len(row) {
		if val, err := strconv.ParseFloat(row[idx], 64); err == nil {
			return uint64(val)
		}
	}
	return 0
}

// EnhanceMetricsFromCSV enhances hardware metrics with data from CSV import.
func EnhanceMetricsFromCSV(stats *PerfCounterStats, csvData *CSVCounterData) error {
	// Build map of encoder labels to CSV metrics
	csvByEncoder := make(map[string]*CSVEncoderMetrics)
	for i := range csvData.Encoders {
		enc := &csvData.Encoders[i]
		// Use encoder function index as key
		key := fmt.Sprintf("encoder_%d", enc.EncoderFunctionIndex)
		csvByEncoder[key] = enc
	}

	// Enhance shader metrics with CSV data
	for i := range stats.ShaderMetrics {
		metric := &stats.ShaderMetrics[i]

		// Try to find matching CSV entry
		// (This is a simple matching strategy - could be improved)
		for _, csvEnc := range csvData.Encoders {
			// Match by checking if execution counts are similar
			if metric.ExecutionCount > 0 && csvEnc.KernelInvocations > 0 {
				// If within 10% of each other, consider it a match
				ratio := float64(metric.ExecutionCount) / float64(csvEnc.KernelInvocations)
				if ratio >= 0.9 && ratio <= 1.1 {
					// Enhance with CSV data
					metric.BytesReadFromDeviceMemory = csvEnc.BytesReadFromDeviceMemory
					metric.BytesWrittenToDeviceMemory = csvEnc.BytesWrittenToDeviceMemory
					metric.BufferDeviceMemoryBytesRead = csvEnc.BufferDeviceMemoryBytesRead
					metric.BufferDeviceMemoryBytesWritten = csvEnc.BufferDeviceMemoryBytesWritten
					metric.DeviceMemoryBandwidthGBps = csvEnc.DeviceMemoryBandwidth
					metric.GPUReadBandwidthGBps = csvEnc.GPUReadBandwidth
					metric.GPUWriteBandwidthGBps = csvEnc.GPUWriteBandwidth

					// Update shader name if empty
					if metric.ShaderName == "" {
						metric.ShaderName = csvEnc.EncoderLabel
					}
					break
				}
			}
		}
	}

	return nil
}
