package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var bufferAccessJSON bool

var bufferAccessCmd = &cobra.Command{
	Use:   "buffer-access <trace.gputrace>",
	Short: "Analyze buffer access patterns",
	Long: `Analyze buffer access patterns to identify optimization opportunities.

This command analyzes Ct and Cul records to track:
- Which encoders access which buffers
- Buffer reuse frequency across encoders
- Memory aliasing (multiple buffer names for same address)
- Unused buffers (allocated but never accessed)
- Read-only vs read-write buffers (future enhancement)

The analysis helps identify:
- Buffers that could be reused to reduce memory usage
- Unused buffers that waste memory
- Memory aliasing issues that could cause bugs
- Access patterns for optimization

Examples:
  # Analyze buffer access patterns
  gputrace buffer-access trace.gputrace

  # Show detailed analysis
  gputrace buffer-access trace.gputrace -v`,
	Args: cobra.ExactArgs(1),
	RunE: runBufferAccess,
}

var bufferAccessVerbose bool

func init() {
	rootCmd.AddCommand(bufferAccessCmd)
	bufferAccessCmd.Flags().BoolVarP(&bufferAccessVerbose, "verbose", "v", false, "Show verbose output")
	bufferAccessCmd.Flags().BoolVar(&bufferAccessJSON, "json", false, "Output in JSON format")
}

func runBufferAccess(cmd *cobra.Command, args []string) error {
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

	// Analyze buffer access patterns
	analysis, err := gputrace.AnalyzeBufferAccess(trace)
	if err != nil {
		return fmt.Errorf("failed to analyze buffer access: %w", err)
	}

	if bufferAccessJSON {
		return writeBufferAccessJSON(cmd.OutOrStdout(), analysis)
	}

	// Format and display report
	report := gputrace.FormatBufferAccessReport(analysis, bufferAccessVerbose)
	fmt.Print(report)

	return nil
}

func writeBufferAccessJSON(w io.Writer, analysis *gputrace.BufferAccessAnalysis) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(analysis); err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	return nil
}
