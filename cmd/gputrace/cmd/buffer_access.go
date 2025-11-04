package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/mlx-go/experiments/gputrace"
)

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
	defer trace.Close()

	// Analyze buffer access patterns
	fmt.Println("Analyzing buffer access patterns...")
	analysis, err := trace.AnalyzeBufferAccess()
	if err != nil {
		return fmt.Errorf("failed to analyze buffer access: %w", err)
	}

	// Generate and print report
	report := trace.FormatBufferAccessReport(analysis)
	fmt.Print(report)

	if bufferAccessVerbose {
		// Show additional statistics
		fmt.Println("=== Additional Statistics ===\n")
		fmt.Printf("Aliased Addresses: %d\n", len(analysis.AliasedBuffers))

		// Calculate average access count
		totalAccesses := 0
		for _, pattern := range analysis.Patterns {
			totalAccesses += pattern.AccessCount
		}
		if len(analysis.Patterns) > 0 {
			avgAccesses := float64(totalAccesses) / float64(len(analysis.Patterns))
			fmt.Printf("Average Accesses per Buffer: %.1f\n", avgAccesses)
		}

		// Show access count distribution
		fmt.Println("\nAccess Count Distribution:")
		distribution := make(map[int]int) // count -> frequency
		for _, pattern := range analysis.Patterns {
			distribution[pattern.AccessCount]++
		}

		// Show histogram for access counts 1-10
		for i := 1; i <= 10; i++ {
			if freq, ok := distribution[i]; ok {
				fmt.Printf("  %2d accesses: %4d buffers\n", i, freq)
			}
		}

		// Show 11+ accesses
		manyAccesses := 0
		for count, freq := range distribution {
			if count > 10 {
				manyAccesses += freq
			}
		}
		if manyAccesses > 0 {
			fmt.Printf("  11+ accesses: %4d buffers\n", manyAccesses)
		}
	}

	return nil
}
