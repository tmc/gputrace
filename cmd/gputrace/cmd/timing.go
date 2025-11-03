package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

var (
	timingJSON    string
	timingCSV     string
	timingCompare string
	timingTable   bool
)

var timingCmd = &cobra.Command{
	Use:   "timing <trace.gputrace>",
	Short: "Extract and export comprehensive timing metrics from GPU traces",
	Long: `Extract comprehensive timing metrics including per-kernel execution times,
command buffer timings, and statistical analysis.

This command extracts timing data from GPU traces and provides:
  - Per-kernel execution statistics (min/max/avg/percentiles)
  - Command buffer and encoder timing
  - Memory transfer timing (when available)
  - Export formats: JSON, CSV, and human-readable tables
  - Trace comparison for regression detection

Examples:
  # Show timing table
  gputrace timing trace.gputrace

  # Export to JSON and CSV
  gputrace timing -json timing.json -csv timing.csv trace.gputrace

  # Compare two traces for regressions
  gputrace timing -compare baseline.gputrace current.gputrace

Note: Timing data depends on trace capture method. Traces without profiling
      data will use synthetic/estimated timing for visualization.`,
	Args: cobra.ExactArgs(1),
	RunE: runTiming,
}

func init() {
	rootCmd.AddCommand(timingCmd)

	timingCmd.Flags().StringVar(&timingJSON, "json", "", "Export timing metrics to JSON file")
	timingCmd.Flags().StringVar(&timingCSV, "csv", "", "Export timing metrics to CSV file")
	timingCmd.Flags().StringVar(&timingCompare, "compare", "", "Compare with baseline trace for regression detection")
	timingCmd.Flags().BoolVar(&timingTable, "table", true, "Show human-readable table output")
}

func runTiming(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Extract timing metrics
	extractor := gputrace.NewTimingMetricsExtractor(trace)
	metrics, err := extractor.Extract()
	if err != nil {
		return fmt.Errorf("failed to extract timing metrics: %w", err)
	}

	// Show table if requested
	if timingTable {
		report := gputrace.FormatTimingMetrics(metrics)
		fmt.Println(report)
	}

	// Export JSON if requested
	if timingJSON != "" {
		f, err := os.Create(timingJSON)
		if err != nil {
			return fmt.Errorf("failed to create JSON file: %w", err)
		}
		defer f.Close()

		if err := gputrace.ExportTimingMetricsJSON(f, metrics); err != nil {
			return fmt.Errorf("failed to write JSON: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Exported JSON to %s\n", timingJSON)
	}

	// Export CSV if requested
	if timingCSV != "" {
		f, err := os.Create(timingCSV)
		if err != nil {
			return fmt.Errorf("failed to create CSV file: %w", err)
		}
		defer f.Close()

		if err := gputrace.ExportTimingMetricsCSV(f, metrics); err != nil {
			return fmt.Errorf("failed to write CSV: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Exported CSV to %s\n", timingCSV)
	}

	// Compare traces if requested
	if timingCompare != "" {
		if err := checkTraceFile(timingCompare); err != nil {
			return fmt.Errorf("baseline trace: %w", err)
		}

		baselineTrace, err := gputrace.Open(timingCompare)
		if err != nil {
			return fmt.Errorf("failed to open baseline trace: %w", err)
		}

		baselineExtractor := gputrace.NewTimingMetricsExtractor(baselineTrace)
		baselineMetrics, err := baselineExtractor.Extract()
		if err != nil {
			return fmt.Errorf("failed to extract baseline metrics: %w", err)
		}

		comparison := gputrace.CompareTraces(baselineMetrics, metrics)
		fmt.Println("\n" + gputrace.FormatTimingComparison(comparison))

		if comparison.RegressionCount > 0 {
			// Return error to indicate regressions found
			return fmt.Errorf("found %d performance regressions", comparison.RegressionCount)
		}
	}

	return nil
}
