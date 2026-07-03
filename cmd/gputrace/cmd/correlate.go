package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var correlateCmd = newCorrelateCommand(&correlateOptions{})

type correlateOptions struct {
	json    bool
	verbose bool
}

func newCorrelateCommand(opts *correlateOptions) *cobra.Command {
	cmd := &cobra.Command{
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCorrelate(cmd, args, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output in JSON format")
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", opts.verbose, "Show verbose output with additional details")
	return cmd
}

func init() {
	rootCmd.AddCommand(correlateCmd)
}

func runCorrelate(cmd *cobra.Command, args []string, opts *correlateOptions) error {
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
	out := cmd.OutOrStdout()
	if opts.json {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("failed to encode JSON: %w", err)
		}
		return nil
	}

	// Print formatted report
	fmt.Fprint(out, gputrace.FormatCorrelationReport(report))

	// Print verbose details if requested
	if opts.verbose && len(report.Shaders) > 0 {
		fmt.Fprintln(out, "\n=== Detailed Shader Metrics ===")
		for i, shader := range report.Shaders {
			if i >= 10 {
				fmt.Fprintf(out, "... and %d more shaders (use --json for full data)\n", len(report.Shaders)-10)
				break
			}

			fmt.Fprintf(out, "[%d] %s\n", i+1, shader.ShaderName)
			fmt.Fprintf(out, "    Correlation: %s (confidence: %.1f%%)\n",
				shader.CorrelationMethod, shader.CorrelationConfidence*100)

			fmt.Fprintf(out, "    Timing:\n")
			fmt.Fprintf(out, "      Executions:  %d\n", shader.ExecutionCount)
			fmt.Fprintf(out, "      Total:       %v\n", shader.TotalDuration)
			fmt.Fprintf(out, "      Average:     %v\n", shader.AvgDuration)
			fmt.Fprintf(out, "      Min/Max:     %v / %v\n", shader.MinDuration, shader.MaxDuration)
			if shader.TimingSource != "" {
				source := shader.TimingSource
				if shader.TimingApprox {
					source += " (approximate)"
				}
				fmt.Fprintf(out, "      Source:      %s\n", source)
			}

			if shader.ALUUtilization > 0 {
				fmt.Fprintf(out, "    Hardware:\n")
				fmt.Fprintf(out, "      ALU Util:    %.1f%%\n", shader.ALUUtilization)
				fmt.Fprintf(out, "      Occupancy:   %.1f%%\n", shader.KernelOccupancy)
				fmt.Fprintf(out, "      SIMD Groups: %d\n", shader.SIMDGroups)
				fmt.Fprintf(out, "      Registers:   %d allocated, %d spilled bytes\n",
					shader.AllocatedRegs, shader.SpilledBytes)
				fmt.Fprintf(out, "      Bandwidth:   %d bytes\n", shader.MemoryBandwidth)
				fmt.Fprintf(out, "      Cycles:      %d total, %d per invocation\n",
					shader.TotalCycles, shader.CyclesPerInvocation)
				if shader.EstimatedGPUFreqGHz > 0 {
					fmt.Fprintf(out, "      Est. Freq:   %.2f GHz\n", shader.EstimatedGPUFreqGHz)
				}
			}
			fmt.Fprintln(out)
		}
	}

	return nil
}
