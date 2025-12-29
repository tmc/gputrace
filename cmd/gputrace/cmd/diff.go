package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/analysis"
	"github.com/tmc/gputrace/internal/trace"
)

var diffCmd = &cobra.Command{
	Use:   "diff <trace1> <trace2>",
	Short: "Compare two GPU traces",
	Long: `Compare two GPU traces to identify divergences in execution structure and resource usage.

This command compares:
- Metadata (Device, API version)
- High-level statistics (Record counts, memory usage)
- Execution structure (Kernel launches, Debug groups)

Example:
  gputrace diff base.gputrace candidate.gputrace`,
	Args: cobra.ExactArgs(2),
	RunE: runDiff,
}

func init() {
	rootCmd.AddCommand(diffCmd)
}

func runDiff(cmd *cobra.Command, args []string) error {
	path1, path2 := args[0], args[1]

	// Open traces
	t1, err := trace.Open(path1)
	if err != nil {
		return fmt.Errorf("failed to open trace 1 (%s): %w", path1, err)
	}
	defer t1.Close()

	t2, err := trace.Open(path2)
	if err != nil {
		return fmt.Errorf("failed to open trace 2 (%s): %w", path2, err)
	}
	defer t2.Close()

	fmt.Printf("Comparing %s vs %s\n\n", Colorize(path1, ColorBold), Colorize(path2, ColorBold))

	// 1. Compare Metadata
	compareMetadata(t1, t2)

	// 2. Compare Statistics
	if err := compareStats(t1, t2); err != nil {
		return err
	}

	// 3. Compare Execution Structure
	if err := compareStructure(t1, t2); err != nil {
		return err
	}

	return nil
}

func compareMetadata(t1, t2 *trace.Trace) {
	fmt.Println(Colorize("=== Metadata Comparison ===", ColorBold))

	printDiff("Device ID", fmt.Sprintf("%d", t1.Metadata.DeviceID), fmt.Sprintf("%d", t2.Metadata.DeviceID))
	printDiff("Capture Version", fmt.Sprintf("%d", t1.Metadata.CaptureVersion), fmt.Sprintf("%d", t2.Metadata.CaptureVersion))
	printDiff("Graphics API", fmt.Sprintf("%d", t1.Metadata.GraphicsAPI), fmt.Sprintf("%d", t2.Metadata.GraphicsAPI))
	fmt.Println()
}

func compareStats(t1, t2 *trace.Trace) error {
	stats1, err := analysis.ExtractStatistics(t1)
	if err != nil {
		return fmt.Errorf("stats extract trace 1: %w", err)
	}
	stats2, err := analysis.ExtractStatistics(t2)
	if err != nil {
		return fmt.Errorf("stats extract trace 2: %w", err)
	}

	fmt.Println(Colorize("=== Statistics Comparison ===", ColorBold))

	printDiff("Total Records", fmt.Sprintf("%d", stats1.TotalRecords), fmt.Sprintf("%d", stats2.TotalRecords))
	printDiff("Compute Encoders", fmt.Sprintf("%d", stats1.ComputeEncoders), fmt.Sprintf("%d", stats2.ComputeEncoders))
	printDiff("Dispatch Calls", fmt.Sprintf("%d", stats1.DispatchCalls), fmt.Sprintf("%d", stats2.DispatchCalls))
	printDiff("Unique Kernels", fmt.Sprintf("%d", stats1.UniqueKernels), fmt.Sprintf("%d", stats2.UniqueKernels))

	// Set difference for Kernel Names
	set1 := make(map[string]bool)
	for _, n := range t1.KernelNames {
		set1[n] = true
	}
	set2 := make(map[string]bool)
	for _, n := range t2.KernelNames {
		set2[n] = true
	}

	var onlyIn1 []string
	for n := range set1 {
		if !set2[n] {
			onlyIn1 = append(onlyIn1, n)
		}
	}
	var onlyIn2 []string
	for n := range set2 {
		if !set1[n] {
			onlyIn2 = append(onlyIn2, n)
		}
	}

	if len(onlyIn1) > 0 {
		fmt.Printf("Kernels only in trace 1 (%d):\n", len(onlyIn1))
		for _, n := range onlyIn1 {
			fmt.Printf("  - %s\n", n)
		}
	}
	if len(onlyIn2) > 0 {
		fmt.Printf("Kernels only in trace 2 (%d):\n", len(onlyIn2))
		for _, n := range onlyIn2 {
			fmt.Printf("  - %s\n", n)
		}
	}
	if len(onlyIn1) == 0 && len(onlyIn2) == 0 {
		fmt.Println("Kernels: Identical set")
	}

	fmt.Println()
	return nil
}

func compareStructure(t1, t2 *trace.Trace) error {
	fmt.Println(Colorize("=== Structure Comparison (Top-level) ===", ColorBold))

	recs1, err := t1.ParseMTSPRecords()
	if err != nil {
		return err
	}
	recs2, err := t2.ParseMTSPRecords()
	if err != nil {
		return err
	}

	// Filter for significant events (CS labels)
	evs1 := extractStructuralEvents(recs1)
	evs2 := extractStructuralEvents(recs2)

	limit := len(evs1)
	if len(evs2) < limit {
		limit = len(evs2)
	}

	diffCount := 0
	maxDiffs := 10

	for i := 0; i < limit; i++ {
		e1 := evs1[i]
		e2 := evs2[i]

		if e1 != e2 {
			fmt.Printf("Difference at index %d:\n", i)
			fmt.Printf("  1: %s\n", e1)
			fmt.Printf("  2: %s\n", e2)
			diffCount++
			if diffCount >= maxDiffs {
				fmt.Println("... (max diffs reached)")
				break
			}
		}
	}

	if len(evs1) != len(evs2) {
		fmt.Printf("Length mismatch: Trace 1 has %d events, Trace 2 has %d events.\n", len(evs1), len(evs2))
	} else if diffCount == 0 {
		fmt.Println("Execution structure matches exactly (for captured top-level labels).")
	}

	return nil
}

func extractStructuralEvents(recs []trace.MTSPRecord) []string {
	var events []string
	for _, r := range recs {
		if r.Type == trace.RecordTypeCS && r.Label != "" {
			events = append(events, r.Label)
		}
		// We could recurse, but top-level structure is a good start
	}
	return events
}

func printDiff(label, v1, v2 string) {
	if v1 == v2 {
		fmt.Printf("%-20s: %s (Match)\n", label, v1)
	} else {
		fmt.Printf("%-20s: %s vs %s\n", label, Colorize(v1, ColorRed), Colorize(v2, ColorGreen))
	}
}
