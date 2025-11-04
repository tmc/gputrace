package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

var (
	correlateJSON   string
	correlateVerbose bool
)

var correlateCmd = &cobra.Command{
	Use:   "correlate <trace.gputrace>",
	Short: "Correlate timing data with hardware performance metrics",
	Long: `Correlate shader timing data from .gputrace files with hardware performance
metrics from .gpuprofiler_raw data.

This command combines:
  - Execution timing (duration, invocation count) from trace
  - Hardware metrics (ALU utilization, occupancy, registers) from profiler

The correlation algorithm matches shaders by:
  1. Exact name match (highest confidence)
  2. Execution order (fallback for mismatched names)

Requires both .gputrace and .gpuprofiler_raw data to be present.

Examples:
  # Show correlated metrics
  gputrace correlate trace.gputrace

  # Export to JSON
  gputrace correlate trace.gputrace --json metrics.json

  # Verbose output with detailed statistics
  gputrace correlate trace.gputrace -v`,
	Args: cobra.ExactArgs(1),
	RunE: runCorrelate,
}

func init() {
	rootCmd.AddCommand(correlateCmd)

	correlateCmd.Flags().StringVar(&correlateJSON, "json", "", "Export to JSON file")
	correlateCmd.Flags().BoolVarP(&correlateVerbose, "verbose", "v", false, "Show detailed statistics")
}

func runCorrelate(cmd *cobra.Command, args []string) error {
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

	// Check if profiler data exists
	if !trace.HasPerfCounters() {
		fmt.Fprintf(os.Stderr, "Warning: No .gpuprofiler_raw data found\n")
		fmt.Fprintf(os.Stderr, "Showing timing-only metrics\n\n")
	}

	// Correlate metrics
	report, err := gputrace.CorrelateShaderMetrics(trace)
	if err != nil {
		return fmt.Errorf("failed to correlate metrics: %w", err)
	}

	// Display report
	fmt.Println(gputrace.FormatCorrelationReport(report))

	// Show detailed statistics if verbose
	if correlateVerbose && len(report.Shaders) > 0 {
		fmt.Println("\n=== Detailed Hardware Metrics ===\n")
		fmt.Printf("%-40s %10s %10s %10s %10s\n",
			"Shader", "SIMD Grps", "Regs", "Spill(B)", "Cycles/Inv")
		fmt.Println(repeatChar('-', 85))

		for _, shader := range report.Shaders {
			if shader.SIMDGroups > 0 {
				fmt.Printf("%-40s %10d %10d %10d %10d\n",
					truncateStr(shader.ShaderName, 40),
					shader.SIMDGroups,
					shader.AllocatedRegs,
					shader.SpilledBytes,
					shader.CyclesPerInvocation)
			}
		}
	}

	// Export JSON if requested
	if correlateJSON != "" {
		f, err := os.Create(correlateJSON)
		if err != nil {
			return fmt.Errorf("failed to create JSON file: %w", err)
		}
		defer f.Close()

		encoder := json.NewEncoder(f)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("failed to write JSON: %w", err)
		}

		fmt.Fprintf(os.Stderr, "\n✓ Exported to: %s\n", correlateJSON)
	}

	return nil
}

func repeatChar(c rune, count int) string {
	result := make([]byte, count)
	for i := 0; i < count; i++ {
		result[i] = byte(c)
	}
	return string(result)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
