package cmd

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tmc/mlx-go/experiments/gputrace"
)

var perfcountersValidateCmd = &cobra.Command{
	Use:   "perfcounters-validate <trace.gputrace> <reference.csv>",
	Short: "Validate extracted perfcounter data against Xcode Instruments CSV",
	Long: `Compare extracted performance counter data against reference CSV from Xcode Instruments.

This command is critical for validating the binary parsing implementation:
- Extracts metrics from .gpuprofiler_raw binary files
- Compares against known-good Xcode Instruments data
- Reports accuracy for key metrics (Kernel Invocations, ALU Utilization, Occupancy)

Used to validate replay engine accuracy by cross-checking against ground truth.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		tracePath := args[0]
		csvPath := args[1]

		// Load trace
		trace, err := gputrace.Open(tracePath)
		if err != nil {
			return fmt.Errorf("open trace: %w", err)
		}

		// Parse performance counters
		stats, err := gputrace.ParsePerfCounters(trace)
		if err != nil {
			return fmt.Errorf("parse perfcounters: %w", err)
		}

		// Load reference CSV
		refData, err := loadReferenceCSV(csvPath)
		if err != nil {
			return fmt.Errorf("load reference CSV: %w", err)
		}

		// Validate metrics
		return validateMetrics(stats, refData)
	},
}

func init() {
	rootCmd.AddCommand(perfcountersValidateCmd)
}

// ReferenceCSVData holds parsed data from Xcode Instruments CSV
type ReferenceCSVData struct {
	Headers []string
	Rows    [][]string
	// Key metrics from first data row (index 0)
	KernelInvocations  int
	ALUUtilization     float64
	KernelOccupancy    float64
	MemoryBandwidthGBs float64
}

func loadReferenceCSV(path string) (*ReferenceCSVData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("CSV has no data rows")
	}

	data := &ReferenceCSVData{
		Headers: records[0],
		Rows:    records[1:],
	}

	// Find key metric columns
	colIndex := make(map[string]int)
	for i, header := range data.Headers {
		colIndex[header] = i
	}

	// Extract first row metrics (aggregate across all encoders)
	if len(data.Rows) > 0 {
		firstRow := data.Rows[0]

		// Kernel Invocations (usually column 106)
		if idx, ok := colIndex["Kernel Invocations"]; ok && idx < len(firstRow) {
			val := strings.ReplaceAll(firstRow[idx], ",", "")
			val = strings.TrimSpace(val)
			// Handle ".00" decimal notation
			if strings.Contains(val, ".") {
				floatVal, _ := strconv.ParseFloat(val, 64)
				data.KernelInvocations = int(floatVal)
			} else {
				data.KernelInvocations, _ = strconv.Atoi(val)
			}
		}

		// ALU Utilization (%) - column ~13
		if idx, ok := colIndex["ALU Utilization"]; ok && idx < len(firstRow) {
			val := strings.TrimSpace(firstRow[idx])
			data.ALUUtilization, _ = strconv.ParseFloat(val, 64)
		}

		// Kernel Occupancy (%) - column ~107
		if idx, ok := colIndex["Kernel Occupancy"]; ok && idx < len(firstRow) {
			val := strings.TrimSpace(firstRow[idx])
			data.KernelOccupancy, _ = strconv.ParseFloat(val, 64)
		}

		// Device Memory Bandwidth (GB/s) - column ~52
		if idx, ok := colIndex["Device Memory Bandwidth"]; ok && idx < len(firstRow) {
			val := strings.ReplaceAll(firstRow[idx], " GB/s", "")
			val = strings.TrimSpace(val)
			data.MemoryBandwidthGBs, _ = strconv.ParseFloat(val, 64)
		}
	}

	return data, nil
}

func validateMetrics(stats *gputrace.PerfCounterStats, ref *ReferenceCSVData) error {
	fmt.Printf("=== Performance Counter Validation ===\n\n")

	fmt.Printf("NOTE: CSV Row 1 represents ONE encoder, not aggregate.\n")
	fmt.Printf("Comparing first extracted encoder against CSV Row 1.\n\n")

	if len(stats.ShaderMetrics) == 0 {
		return fmt.Errorf("no shader metrics extracted")
	}

	// Use first encoder for comparison (CSV Row 1 = first encoder)
	firstEncoder := stats.ShaderMetrics[0]

	// Compare with reference
	fmt.Printf("Metric                          Extracted        Reference        Delta          Status\n")
	fmt.Printf("-------------------------------------------------------------------------------------------\n")

	// Kernel Invocations
	invocationsDelta := float64(firstEncoder.ExecutionCount-ref.KernelInvocations) / float64(ref.KernelInvocations) * 100
	invocationsStatus := "✅ PASS"
	if abs(invocationsDelta) > 5.0 {
		invocationsStatus = "❌ FAIL"
	}
	fmt.Printf("%-30s %15d  %15d  %+12.2f%%  %s\n",
		"Kernel Invocations",
		firstEncoder.ExecutionCount,
		ref.KernelInvocations,
		invocationsDelta,
		invocationsStatus)

	// ALU Utilization
	aluDelta := firstEncoder.ALUUtilization - ref.ALUUtilization
	aluStatus := "✅ PASS"
	if abs(aluDelta) > 5.0 {
		aluStatus = "❌ FAIL"
	}
	fmt.Printf("%-30s %14.2f%%  %14.2f%%  %+12.2f%%  %s\n",
		"ALU Utilization",
		firstEncoder.ALUUtilization,
		ref.ALUUtilization,
		aluDelta,
		aluStatus)

	// Kernel Occupancy
	occupancyDelta := firstEncoder.KernelOccupancy - ref.KernelOccupancy
	occupancyStatus := "✅ PASS"
	if abs(occupancyDelta) > 5.0 {
		occupancyStatus = "❌ FAIL"
	}
	fmt.Printf("%-30s %14.2f%%  %14.2f%%  %+12.2f%%  %s\n",
		"Kernel Occupancy",
		firstEncoder.KernelOccupancy,
		ref.KernelOccupancy,
		occupancyDelta,
		occupancyStatus)

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Total Encoders:      %d\n", len(stats.ShaderMetrics))
	fmt.Printf("Total Records:       %d\n", stats.TotalRecords)
	fmt.Printf("Files Processed:     %d\n", stats.FilesProcessed)
	fmt.Printf("Confidence Level:    %.1f%%\n", stats.ConfidenceLevel*100)

	// Show first 5 encoder invocation counts for debugging
	fmt.Printf("\nFirst 5 Encoder Invocation Counts:\n")
	for i := 0; i < 5 && i < len(stats.ShaderMetrics); i++ {
		fmt.Printf("  Encoder %d: %d invocations\n", i+1, stats.ShaderMetrics[i].ExecutionCount)
	}

	return nil
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
