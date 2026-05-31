package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var (
	kernelsFilter  string
	kernelsVerbose bool
	kernelsStats   bool
	kernelsJSON    bool
)

var kernelsCmd = &cobra.Command{
	Use:   "kernels <trace.gputrace>",
	Short: "List kernel functions and their pipeline state mappings",
	Long: `List all kernel functions found in a GPU trace with their pipeline state addresses.

This command extracts the mapping between pipeline state objects and their
associated kernel functions, making it easy to understand which Metal functions
are being executed.

It can also display dispatch counts, timing information (if available), and associated debug groups/encoder labels.

Examples:
  # List all kernels with dispatch counts
  gputrace kernels trace.gputrace

  # Filter by kernel name (case-insensitive substring match)
  gputrace kernels trace.gputrace --filter copy
  gputrace kernels trace.gputrace --filter steel_gemm

  # Verbose output with detailed stats (debug groups, encoder labels)
  gputrace kernels trace.gputrace -v
  gputrace kernels trace.gputrace --stats`,
	Args: cobra.ExactArgs(1),
	RunE: runKernels,
}

func init() {
	rootCmd.AddCommand(kernelsCmd)

	kernelsCmd.Flags().StringVarP(&kernelsFilter, "filter", "f", "", "Filter kernels by name (case-insensitive substring match)")
	kernelsCmd.Flags().BoolVarP(&kernelsVerbose, "verbose", "v", false, "Show verbose output with additional details")
	kernelsCmd.Flags().BoolVar(&kernelsStats, "stats", false, "Show detailed statistics (debug groups, encoder labels)")
	kernelsCmd.Flags().BoolVar(&kernelsJSON, "json", false, "Output in JSON format")
}

func runKernels(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Analyze kernels to get stats
	stats, err := trace.AnalyzeKernels()
	if err != nil {
		return fmt.Errorf("analyze kernels: %w", err)
	}

	// Try to get timing stats
	var timingStats map[string]*gputrace.TimingStat
	// We check for perf counters availability
	if trace.HasPerfCounters() {
		// Use extracted timing data
		// Note: We need to bridge internal/timing to something usable here without import cycles in core packages.
		// Since cmd can import anything, we can implement extraction here or use a helper.
		// But gputrace package re-exports ExtractTimingData.

		timings, err := gputrace.ExtractTimingData(trace)
		if err == nil {
			timingStats = make(map[string]*gputrace.TimingStat)
			for _, t := range timings {
				name := t.Label
				// Normalize name to match kernel stats if possible
				// Encoder timing labels usually match encoder labels

				// Clean up name if it's an "Encoder_X_kernel" style
				if strings.Contains(name, "_") {
					parts := strings.SplitN(name, "_", 3)
					if len(parts) >= 3 && parts[0] == "Encoder" {
						name = parts[2]
					}
				}

				if _, exists := timingStats[name]; !exists {
					timingStats[name] = &gputrace.TimingStat{
						MinTime: 1e9,
					}
				}

				s := timingStats[name]
				s.TotalTime += t.DurationMs
				if t.DurationMs < s.MinTime {
					s.MinTime = t.DurationMs
				}
				if t.DurationMs > s.MaxTime {
					s.MaxTime = t.DurationMs
				}
			}
		}
	}

	// Filter and sort
	var kernels []*gputrace.KernelStat
	filterLower := strings.ToLower(kernelsFilter)

	for _, k := range stats {
		if kernelsFilter != "" && !strings.Contains(strings.ToLower(k.Name), filterLower) {
			continue
		}
		kernels = append(kernels, k)
	}

	// Sort by dispatch count (descending), then name
	sort.Slice(kernels, func(i, j int) bool {
		if kernels[i].DispatchCount != kernels[j].DispatchCount {
			return kernels[i].DispatchCount > kernels[j].DispatchCount
		}
		return kernels[i].Name < kernels[j].Name
	})

	if kernelsJSON {
		return writeKernelsJSON(cmd.OutOrStdout(), kernels, timingStats)
	}

	// Count unique kernels
	uniqueKernels := len(kernels)

	// Output header
	if kernelsFilter != "" {
		fmt.Printf("%d %s matching %q:\n", uniqueKernels, Pluralize(uniqueKernels, "kernel", "kernels"), kernelsFilter)
	} else {
		fmt.Printf("%d %s:\n", uniqueKernels, Pluralize(uniqueKernels, "kernel", "kernels"))
	}
	fmt.Println()

	if uniqueKernels == 0 {
		return nil
	}

	// Determine column widths
	maxNameLen := 30
	for _, k := range kernels {
		if len(k.Name) > maxNameLen {
			maxNameLen = len(k.Name)
		}
	}
	// Cap max length to reasonable value to prevent wrapping issues
	if maxNameLen > 60 {
		maxNameLen = 60
	}

	// Print table header
	nameFmt := fmt.Sprintf("%%-%ds", maxNameLen)

	// Adjust columns if we have timing
	hasTiming := len(timingStats) > 0

	fmt.Printf(nameFmt+"  %-18s  %-10s", "Name", "Pipeline State", "Dispatches")
	if hasTiming {
		fmt.Printf("  %-10s  %-10s", "Total Time", "Avg Time")
	}
	if kernelsVerbose || kernelsStats {
		fmt.Printf("  %s", "Debug Groups / Labels")
	}
	fmt.Println()

	sepWidth := maxNameLen + 2 + 18 + 2 + 10
	if hasTiming {
		sepWidth += 2 + 10 + 2 + 10
	}
	if kernelsVerbose || kernelsStats {
		sepWidth += 2 + 30
	}
	fmt.Println(TableSeparator(sepWidth))

	// Print rows
	for _, k := range kernels {
		name := k.Name
		displayName := name
		if len(displayName) > maxNameLen {
			displayName = displayName[:maxNameLen-3] + "..."
		}

		fmt.Printf(nameFmt+"  0x%-16x  %-10d", displayName, k.PipelineAddr, k.DispatchCount)

		if hasTiming {
			if tStat, ok := timingStats[name]; ok {
				avg := tStat.TotalTime
				if k.DispatchCount > 0 {
					avg = tStat.TotalTime / float64(k.DispatchCount)
				}
				// Note: Timing extraction might not match 1:1 with dispatch counts if aggregation is different.
				// But we display what we have.
				fmt.Printf("  %7.2f ms  %7.3f ms", tStat.TotalTime, avg)
			} else {
				// Try looking up via encoder labels if direct name match failed
				var found bool
				for label := range k.EncoderLabels {
					if tStat, ok := timingStats[label]; ok {
						// Found a match via encoder label
						// Aggregating multiple matches is complex, just show first found for now
						// or maybe we should have aggregated timingStats differently
						fmt.Printf("  %7.2f ms  %7.3f ms", tStat.TotalTime, tStat.TotalTime/float64(k.DispatchCount)) // approx
						found = true
						break
					}
				}
				if !found {
					fmt.Printf("  %10s  %10s", "-", "-")
				}
			}
		}

		if kernelsVerbose || kernelsStats {
			var details []string

			// Add debug groups
			for group, count := range k.DebugGroups {
				details = append(details, fmt.Sprintf("%s (%d)", group, count))
			}

			// If no debug groups, show encoder labels (if different from kernel name)
			if len(details) == 0 {
				for label, count := range k.EncoderLabels {
					if label != k.Name && label != "" {
						details = append(details, fmt.Sprintf("%s (%d)", label, count))
					}
				}
			}

			// If we have details, print them
			if len(details) > 0 {
				// Sort details for consistency
				sort.Strings(details)

				// Print first few inline
				str := strings.Join(details, ", ")
				if len(str) > 60 {
					str = str[:57] + "..."
				}
				fmt.Printf("  %s", str)
			}
		}
		fmt.Println()
	}

	// Print summary of unknown pipelines if any
	if k, ok := stats["unknown"]; ok && k.DispatchCount > 0 {
		fmt.Printf("\nUnknown Pipelines: %d dispatches (encoder: %v)\n", k.DispatchCount, k.EncoderLabels)
	}

	return nil
}

func writeKernelsJSON(w io.Writer, kernels []*gputrace.KernelStat, timingStats map[string]*gputrace.TimingStat) error {
	type kernelJSON struct {
		Name          string         `json:"name"`
		PipelineAddr  string         `json:"pipeline_addr"`
		DispatchCount int            `json:"dispatch_count"`
		DebugGroups   map[string]int `json:"debug_groups,omitempty"`
		EncoderLabels map[string]int `json:"encoder_labels,omitempty"`
		TotalTimeMs   float64        `json:"total_time_ms,omitempty"`
		AvgTimeMs     float64        `json:"avg_time_ms,omitempty"`
	}

	out := make([]kernelJSON, len(kernels))
	for i, k := range kernels {
		kj := kernelJSON{
			Name:          k.Name,
			PipelineAddr:  fmt.Sprintf("0x%x", k.PipelineAddr),
			DispatchCount: k.DispatchCount,
			DebugGroups:   k.DebugGroups,
			EncoderLabels: k.EncoderLabels,
		}
		if tStat, ok := timingStats[k.Name]; ok {
			kj.TotalTimeMs = tStat.TotalTime
			if k.DispatchCount > 0 {
				kj.AvgTimeMs = tStat.TotalTime / float64(k.DispatchCount)
			}
		}
		out[i] = kj
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}
