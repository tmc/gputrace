package counter

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tmc/gputrace/internal/trace"
)

// XcodeCounterData represents performance counter data imported from Xcode Counters.csv.
type XcodeCounterData struct {
	Encoders []XcodeEncoderCounters
	Metrics  []string // List of available metric names
}

// XcodeEncoderCounters represents counter data for a single encoder.
type XcodeEncoderCounters struct {
	Index              int                // Row index from CSV
	FunctionIndex      int                // Encoder function index
	CommandBufferLabel string             // Command buffer label
	EncoderLabel       string             // Encoder label
	Counters           map[string]float64 // Map of counter name to value
}

// ParseXcodeCountersCSV parses an Xcode Counters.csv file.
// The CSV file can be located in several places:
// - Alongside the .gputrace file (e.g., "trace Counters.csv")
// - Inside the .gputrace directory
// - As a standalone file specified by path
func ParseXcodeCountersCSV(t *trace.Trace, csvPath string) (*XcodeCounterData, error) {
	// If csvPath is empty, try to find it automatically
	if csvPath == "" {
		// Try common locations
		baseName := filepath.Base(t.Path)
		baseName = strings.TrimSuffix(baseName, ".gputrace")
		baseName = strings.TrimSuffix(baseName, "-perf")

		candidates := []string{
			filepath.Join(filepath.Dir(t.Path), baseName+" Counters.csv"),
			filepath.Join(filepath.Dir(t.Path), baseName+"-perf Counters.csv"),
			filepath.Join(filepath.Dir(t.Path), baseName+".gputrace Counters.csv"),
			filepath.Join(t.Path, "Counters.csv"),
		}

		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				csvPath = candidate
				break
			}
		}

		if csvPath == "" {
			return nil, fmt.Errorf("could not find Counters.csv file (tried %v)", candidates)
		}
	}

	// Open CSV file
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

	// Parse header to get metadata and metric column positions.
	if len(headers) < 5 {
		return nil, fmt.Errorf("invalid header: expected at least 5 columns, got %d", len(headers))
	}
	colIdx := make(map[string]int)
	for i, header := range headers {
		colIdx[header] = i
	}
	requiredCols := []string{
		"Index",
		"Encoder FunctionIndex",
		"CommandBuffer Label",
		"Encoder Label",
	}
	for _, col := range requiredCols {
		if _, ok := colIdx[col]; !ok {
			return nil, fmt.Errorf("missing required column: %s", col)
		}
	}

	metadataCols := map[string]bool{
		"":                      true,
		"Index":                 true,
		"Encoder FunctionIndex": true,
		"CommandBuffer Label":   true,
		"Debug Group":           true,
		"Encoder Label":         true,
	}
	metricNames := make([]string, 0, len(headers))
	metricCols := make([]int, 0, len(headers))
	for i, header := range headers {
		if metadataCols[header] {
			continue
		}
		metricNames = append(metricNames, header)
		metricCols = append(metricCols, i)
	}

	data := &XcodeCounterData{
		Encoders: make([]XcodeEncoderCounters, 0),
		Metrics:  metricNames,
	}

	// Read data rows
	for {
		row, err := reader.Read()
		if err != nil {
			break // End of file or error
		}

		if len(row) < 5 {
			continue // Skip malformed rows
		}

		encoder := XcodeEncoderCounters{
			Counters: make(map[string]float64),
		}

		// Parse index
		if idx, err := strconv.Atoi(csvColumn(row, colIdx["Index"])); err == nil {
			encoder.Index = idx
		}

		// Parse function index
		if fidx, err := strconv.Atoi(csvColumn(row, colIdx["Encoder FunctionIndex"])); err == nil {
			encoder.FunctionIndex = fidx
		}

		// Parse labels
		encoder.CommandBufferLabel = csvColumn(row, colIdx["CommandBuffer Label"])
		encoder.EncoderLabel = csvColumn(row, colIdx["Encoder Label"])

		// Parse counter values
		for i, colIdx := range metricCols {
			if colIdx < len(row) && row[colIdx] != "" {
				if val, err := strconv.ParseFloat(row[colIdx], 64); err == nil {
					metricName := metricNames[i]
					encoder.Counters[metricName] = val
				}
			}
		}

		data.Encoders = append(data.Encoders, encoder)
	}

	return data, nil
}

func csvColumn(row []string, col int) string {
	if col < 0 || col >= len(row) {
		return ""
	}
	return row[col]
}
