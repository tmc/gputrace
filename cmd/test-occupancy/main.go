package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/tmc/mlx-go/experiments/gputrace"
	"github.com/tmc/mlx-go/experiments/gputrace/internal/counter"
)

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("Usage: %s <trace.gputrace> <counters.csv>", os.Args[0])
	}

	tracePath := os.Args[1]
	csvPath := os.Args[2]

	// Open trace
	t, err := gputrace.Open(tracePath)
	if err != nil {
		log.Fatalf("Failed to open trace: %v", err)
	}

	// Parse profiling metrics (where Kernel Occupancy is stored)
	profilingMetrics, err := counter.ParseProfilingFiles(t)
	if err != nil {
		log.Fatalf("Failed to parse profiling files: %v", err)
	}

	fmt.Printf("=== Extracted Kernel Occupancy from Binary ===\n\n")
	fmt.Printf("Found %d profiling metrics\n\n", len(profilingMetrics))

	// Load CSV for comparison
	csvData, err := loadKernelOccupancyFromCSV(csvPath)
	if err != nil {
		log.Printf("Warning: Could not load CSV: %v", err)
		csvData = nil
	}

	// Display results
	fmt.Printf("%-40s %15s %15s %15s %15s\n", "Encoder", "Binary", "CSV", "Diff", "Confidence")
	fmt.Printf("%s\n", repeatStr("-", 110))

	for i, metric := range profilingMetrics {
		csvValue := float64(-1)
		if csvData != nil && i < len(csvData) {
			csvValue = csvData[i]
		}

		diff := ""
		if csvValue >= 0 {
			diffVal := metric.KernelOccupancy - csvValue
			diff = fmt.Sprintf("%.2f%%", diffVal)
		} else {
			diff = "N/A"
		}

		csvStr := "N/A"
		if csvValue >= 0 {
			csvStr = fmt.Sprintf("%.2f%%", csvValue)
		}

		encoderName := fmt.Sprintf("Encoder_%d", metric.EncoderIndex)

		fmt.Printf("%-40s %14.2f%% %15s %15s %14.2f\n",
			truncate(encoderName, 40),
			metric.KernelOccupancy,
			csvStr,
			diff,
			metric.Confidence)
	}

	fmt.Printf("\n")

	// Summary statistics
	if csvData != nil && len(csvData) > 0 {
		fmt.Printf("=== Validation Summary ===\n\n")
		matches := 0
		totalDiff := 0.0
		count := 0

		for i := range profilingMetrics {
			if i < len(csvData) && csvData[i] >= 0 {
				diff := abs(profilingMetrics[i].KernelOccupancy - csvData[i])
				totalDiff += diff
				count++

				if diff < 1.0 { // Within 1% tolerance
					matches++
				}
			}
		}

		if count > 0 {
			avgDiff := totalDiff / float64(count)
			matchPercent := float64(matches) / float64(count) * 100

			fmt.Printf("Samples compared: %d\n", count)
			fmt.Printf("Average difference: %.2f%%\n", avgDiff)
			fmt.Printf("Matches (< 1%% diff): %d / %d (%.1f%%)\n", matches, count, matchPercent)

			if avgDiff < 1.0 {
				fmt.Printf("\n✅ Validation PASSED - Average diff < 1%%\n")
			} else if avgDiff < 5.0 {
				fmt.Printf("\n⚠️  Validation MARGINAL - Average diff < 5%%\n")
			} else {
				fmt.Printf("\n❌ Validation FAILED - Average diff >= 5%%\n")
			}
		}
	}
}

// loadKernelOccupancyFromCSV loads Kernel Occupancy values from Xcode CSV.
// Returns slice of occupancy values in row order.
func loadKernelOccupancyFromCSV(path string) ([]float64, error) {
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

	// Find "Kernel Occupancy" column
	header := records[0]
	occupancyCol := -1
	for i, col := range header {
		if col == "Kernel Occupancy" {
			occupancyCol = i
			break
		}
	}

	if occupancyCol == -1 {
		return nil, fmt.Errorf("Kernel Occupancy column not found")
	}

	// Extract occupancy values
	values := make([]float64, 0, len(records)-1)
	for i := 1; i < len(records); i++ {
		if occupancyCol >= len(records[i]) {
			values = append(values, -1)
			continue
		}

		val, err := strconv.ParseFloat(records[i][occupancyCol], 64)
		if err != nil {
			values = append(values, -1)
			continue
		}

		// CSV values are already percentages (e.g., 0.09 = 9%)
		// Multiply by 100 to match our binary extraction format
		values = append(values, val*100)
	}

	return values, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func repeatStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
