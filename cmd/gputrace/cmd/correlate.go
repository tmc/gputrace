package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var (
	correlateJSON    bool
	correlateVerbose bool
)

var correlateCmd = &cobra.Command{
	Use:   "correlate <trace.gputrace>",
	Short: "Correlate timing and hardware metrics for shaders",
	Long: `Correlate shader timing data with hardware performance metrics.

This command combines timing information from the trace with hardware metrics
from the profiler data (.gpuprofiler_raw), providing a comprehensive view of
shader performance including:
  - Execution timing (count, duration, min/max/avg, source, approximation flag)
  - Hardware metrics (ALU utilization, kernel occupancy)
  - Memory metrics (bandwidth, total cycles)
  - Derived metrics (cycles per invocation, GPU frequency)

Correlation methods:
  - By name: Exact shader name matching (highest confidence)
  - By execution order: Fallback when names don't match (lower confidence)

Examples:
  # Show correlation report
  gputrace correlate trace.gputrace

  # Show verbose correlation with all details
  gputrace correlate trace.gputrace -v

  # Export correlation data as JSON
  gputrace correlate trace.gputrace --json > correlation.json`,
	Args: cobra.ExactArgs(1),
	RunE: runCorrelate,
}

func init() {
	rootCmd.AddCommand(correlateCmd)

	correlateCmd.Flags().BoolVar(&correlateJSON, "json", false, "Output in JSON format")
	correlateCmd.Flags().BoolVarP(&correlateVerbose, "verbose", "v", false, "Show verbose output with additional details")
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
	defer trace.Close()

	// Correlate shader metrics
	report, err := gputrace.CorrelateShaderMetrics(trace)
	if err != nil {
		return fmt.Errorf("failed to correlate shader metrics: %w", err)
	}

	// Output based on format
	if correlateJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("failed to encode JSON: %w", err)
		}
		return nil
	}

	// Print formatted report
	fmt.Print(gputrace.FormatCorrelationReport(report))

	// Print verbose details if requested
	if correlateVerbose && len(report.Shaders) > 0 {
		fmt.Println("\n=== Detailed Shader Metrics ===")
		for i, shader := range report.Shaders {
			if i >= 10 {
				fmt.Printf("... and %d more shaders (use --json for full data)\n", len(report.Shaders)-10)
				break
			}

			fmt.Printf("[%d] %s\n", i+1, shader.ShaderName)
			fmt.Printf("    Correlation: %s (confidence: %.1f%%)\n",
				shader.CorrelationMethod, shader.CorrelationConfidence*100)

			fmt.Printf("    Timing:\n")
			fmt.Printf("      Executions:  %d\n", shader.ExecutionCount)
			fmt.Printf("      Total:       %v\n", shader.TotalDuration)
			fmt.Printf("      Average:     %v\n", shader.AvgDuration)
			fmt.Printf("      Min/Max:     %v / %v\n", shader.MinDuration, shader.MaxDuration)
			if shader.TimingSource != "" {
				source := shader.TimingSource
				if shader.TimingApprox {
					source += " (approximate)"
				}
				fmt.Printf("      Source:      %s\n", source)
			}

			if shader.ALUUtilization > 0 {
				fmt.Printf("    Hardware:\n")
				fmt.Printf("      ALU Util:    %.1f%%\n", shader.ALUUtilization)
				fmt.Printf("      Occupancy:   %.1f%%\n", shader.KernelOccupancy)
				fmt.Printf("      SIMD Groups: %d\n", shader.SIMDGroups)
				fmt.Printf("      Registers:   %d allocated, %d spilled bytes\n",
					shader.AllocatedRegs, shader.SpilledBytes)
				fmt.Printf("      Bandwidth:   %d bytes\n", shader.MemoryBandwidth)
				fmt.Printf("      Cycles:      %d total, %d per invocation\n",
					shader.TotalCycles, shader.CyclesPerInvocation)
				if shader.EstimatedGPUFreqGHz > 0 {
					fmt.Printf("      Est. Freq:   %.2f GHz\n", shader.EstimatedGPUFreqGHz)
				}
			}
			fmt.Println()
		}
	}

	return nil
}
