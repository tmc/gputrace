package gputrace

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
func (t *Trace) ParseXcodeCountersCSV(csvPath string) (*XcodeCounterData, error) {
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

	// Parse header to get metric names
	// Format: Index, Encoder FunctionIndex, CommandBuffer Label, Encoder Label, <empty>, <metric1>, <metric2>, ...
	if len(headers) < 5 {
		return nil, fmt.Errorf("invalid header: expected at least 5 columns, got %d", len(headers))
	}

	// Metrics start at column 5 (index 5)
	metricNames := headers[5:]

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

		// Parse index (column 0)
		if idx, err := strconv.Atoi(row[0]); err == nil {
			encoder.Index = idx
		}

		// Parse function index (column 1)
		if fidx, err := strconv.Atoi(row[1]); err == nil {
			encoder.FunctionIndex = fidx
		}

		// Parse labels (columns 2-3)
		encoder.CommandBufferLabel = row[2]
		encoder.EncoderLabel = row[3]

		// Parse counter values (columns 5+)
		for i, metricName := range metricNames {
			colIdx := 5 + i
			if colIdx < len(row) && row[colIdx] != "" {
				if val, err := strconv.ParseFloat(row[colIdx], 64); err == nil {
					encoder.Counters[metricName] = val
				}
			}
		}

		data.Encoders = append(data.Encoders, encoder)
	}

	return data, nil
}

// HasXcodeCountersCSV returns true if an Xcode Counters.csv file can be found.
func (t *Trace) HasXcodeCountersCSV() bool {
	_, err := t.ParseXcodeCountersCSV("")
	return err == nil
}

// GetCounterValue retrieves a specific counter value for an encoder by index.
func (xcd *XcodeCounterData) GetCounterValue(encoderIndex int, counterName string) (float64, bool) {
	if encoderIndex < 0 || encoderIndex >= len(xcd.Encoders) {
		return 0, false
	}

	val, ok := xcd.Encoders[encoderIndex].Counters[counterName]
	return val, ok
}

// GetMetricNames returns all available metric names.
func (xcd *XcodeCounterData) GetMetricNames() []string {
	return xcd.Metrics
}

// GetEncoderByFunctionIndex finds an encoder by its function index.
func (xcd *XcodeCounterData) GetEncoderByFunctionIndex(functionIndex int) (*XcodeEncoderCounters, bool) {
	for i := range xcd.Encoders {
		if xcd.Encoders[i].FunctionIndex == functionIndex {
			return &xcd.Encoders[i], true
		}
	}
	return nil, false
}
