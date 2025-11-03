package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

var (
	shaderMetricsVerbose bool
	shaderMetricsCSV     bool
	shaderMetricsJSON    bool
	shaderMetricsOutput  string
)

var shaderMetricsCmd = &cobra.Command{
	Use:   "shader-metrics <trace.gputrace>",
	Short: "Extract shader/kernel performance metrics from GPU traces",
	Long: `Analyze shader performance characteristics including:
- Per-shader execution time and invocation count
- Thread occupancy and utilization metrics
- Memory bandwidth usage estimates
- Compute vs memory-bound classification
- Performance bottlenecks and optimization hints

Output formats:
  - Human-readable report (default)
  - CSV export (--csv)
  - JSON export (--json)

Examples:
  gputrace shader-metrics trace.gputrace
  gputrace shader-metrics trace.gputrace -v
  gputrace shader-metrics trace.gputrace --csv -o metrics.csv
  gputrace shader-metrics trace.gputrace --json -o metrics.json`,
	Args: cobra.ExactArgs(1),
	RunE: runShaderMetrics,
}

func init() {
	rootCmd.AddCommand(shaderMetricsCmd)

	shaderMetricsCmd.Flags().BoolVarP(&shaderMetricsVerbose, "verbose", "v", false, "Show verbose output with detailed metrics")
	shaderMetricsCmd.Flags().BoolVar(&shaderMetricsCSV, "csv", false, "Export metrics to CSV format")
	shaderMetricsCmd.Flags().BoolVar(&shaderMetricsJSON, "json", false, "Export metrics to JSON format")
	shaderMetricsCmd.Flags().StringVarP(&shaderMetricsOutput, "output", "o", "", "Output file (stdout if not specified)")
}

func runShaderMetrics(cmd *cobra.Command, args []string) error {
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
	defer trace.Close()

	// Extract shader metrics
	fmt.Fprintf(os.Stderr, "Analyzing shader performance metrics...\n")
	report, err := trace.ExtractShaderMetrics()
	if err != nil {
		return fmt.Errorf("failed to extract shader metrics: %w", err)
	}

	// Determine output destination
	var output *os.File
	if shaderMetricsOutput != "" {
		f, err := os.Create(shaderMetricsOutput)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer f.Close()
		output = f
	} else {
		output = os.Stdout
	}

	// Export in requested format
	if shaderMetricsCSV {
		if err := gputrace.ExportShaderMetricsCSV(output, report); err != nil {
			return fmt.Errorf("failed to export CSV: %w", err)
		}
		if shaderMetricsOutput != "" {
			fmt.Fprintf(os.Stderr, "CSV metrics exported to %s\n", shaderMetricsOutput)
		}
	} else if shaderMetricsJSON {
		if err := gputrace.ExportShaderMetricsJSON(output, report); err != nil {
			return fmt.Errorf("failed to export JSON: %w", err)
		}
		if shaderMetricsOutput != "" {
			fmt.Fprintf(os.Stderr, "JSON metrics exported to %s\n", shaderMetricsOutput)
		}
	} else {
		// Human-readable format
		reportText := gputrace.FormatShaderMetricsReport(report)
		fmt.Fprint(output, reportText)
	}

	// Show summary on stderr if outputting to file
	if shaderMetricsOutput != "" {
		fmt.Fprintf(os.Stderr, "\nSummary:\n")
		fmt.Fprintf(os.Stderr, "  Analyzed %d shaders\n", report.TotalShaders)
		fmt.Fprintf(os.Stderr, "  Total GPU time: %.2f ms\n", report.TotalGPUTimeMs)
		fmt.Fprintf(os.Stderr, "  Compute-bound: %d, Memory-bound: %d, Balanced: %d\n",
			report.ComputeBoundCount, report.MemoryBoundCount, report.BalancedCount)
	}

	return nil
}
