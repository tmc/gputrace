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

var kernelsCmd = newKernelsCommand(&kernelsOptions{})

type kernelsOptions struct {
	filter  string
	verbose bool
	stats   bool
	json    bool
	limit   int
	all     bool
}

func newKernelsCommand(opts *kernelsOptions) *cobra.Command {
	cmd := &cobra.Command{
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKernels(cmd, args, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.filter, "filter", "f", opts.filter, "Filter kernels by name (case-insensitive substring match)")
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", opts.verbose, "Show verbose output with additional details")
	cmd.Flags().BoolVar(&opts.stats, "stats", opts.stats, "Show detailed statistics (debug groups, encoder labels)")
	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output in JSON format")
	cmd.Flags().IntVar(&opts.limit, "limit", 50, "Maximum rows to show")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Show every row")
	return cmd
}

func init() {
	rootCmd.AddCommand(kernelsCmd)
}

func runKernels(cmd *cobra.Command, args []string, opts *kernelsOptions) error {
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

	var timingStats map[string]*gputrace.TimingStat
	source := "capture records"
	if _, profilerStats, err := loadProfilerStats(tracePath); err == nil && len(profilerStats.Dispatches) > 0 {
		source = "profiler streamData dispatches"
		stats = make(map[string]*gputrace.KernelStat)
		timingStats = make(map[string]*gputrace.TimingStat)
		for _, dispatch := range profilerStats.Dispatches {
			name := dispatch.FunctionName
			if name == "" {
				name = fmt.Sprintf("(pipeline_%d)", dispatch.PipelineIndex)
			}
			k := stats[name]
			if k == nil {
				k = &gputrace.KernelStat{
					Name:          name,
					DebugGroups:   make(map[string]int),
					EncoderLabels: make(map[string]int),
				}
				stats[name] = k
			}
			k.DispatchCount++
			s := timingStats[name]
			if s == nil {
				s = &gputrace.TimingStat{}
				timingStats[name] = s
			}
			s.TotalTime += float64(dispatch.DurationUs) / 1000
		}
	}

	// Filter and sort
	var kernels []*gputrace.KernelStat
	filterLower := strings.ToLower(opts.filter)

	for _, k := range stats {
		if opts.filter != "" && !strings.Contains(strings.ToLower(k.Name), filterLower) {
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

	if opts.json {
		return writeKernelsJSON(cmd.OutOrStdout(), kernels, timingStats)
	}

	out := cmd.OutOrStdout()
	hasTiming := len(timingStats) > 0
	totalDispatches := 0
	attributedDispatches := 0
	for _, k := range stats {
		totalDispatches += k.DispatchCount
		if k.Name != "unknown" && k.Name != "" {
			attributedDispatches += k.DispatchCount
		}
	}

	rows := splitKernelRows(kernels)
	namedKernels, unknownBucket := rows.Executed, rows.Unknown
	uniqueKernels := len(namedKernels)

	// Output header. Count only the kernels that ran: a created-but-unrun
	// pipeline and a library UUID are both in the inventory and neither is
	// evidence of a dispatch.
	rowSingular, rowPlural := "dispatched kernel", "dispatched kernels"
	if hasTiming {
		rowSingular, rowPlural = "timed function", "timed functions"
	}
	if opts.filter != "" {
		fmt.Fprintf(out, "%d %s matching %q:\n", uniqueKernels, Pluralize(uniqueKernels, rowSingular, rowPlural), opts.filter)
	} else {
		fmt.Fprintf(out, "%d %s:\n", uniqueKernels, Pluralize(uniqueKernels, rowSingular, rowPlural))
	}
	fmt.Fprintf(out, "Source: %s\n", source)
	fmt.Fprintf(out, "Dispatch attribution: %d/%d", attributedDispatches, totalDispatches)
	if attributedDispatches < totalDispatches {
		fmt.Fprint(out, " (unattributed dispatches are reported as unknown)")
	}
	fmt.Fprintln(out)
	if unattributedInventory(attributedDispatches, totalDispatches) {
		fmt.Fprint(out, unattributedInventoryNote)
	}
	if hasTiming {
		fmt.Fprintln(out, "Timing: cumulative dispatch offsets; spans may include boundary or gap time")
	}
	fmt.Fprintln(out)

	if uniqueKernels == 0 && unknownBucket == nil {
		writeInactiveKernelRows(out, rows)
		return nil
	}

	// Determine column widths
	maxNameLen := 30
	shown := namedKernels
	if !opts.all && opts.limit >= 0 && len(shown) > opts.limit {
		shown = shown[:opts.limit]
	}
	for _, k := range shown {
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

	fmt.Fprintf(out, nameFmt+"  %-18s  %-10s", "Name", "Pipeline State", "Dispatches")
	if hasTiming {
		fmt.Fprintf(out, "  %-10s  %-10s", "Total Time", "Avg Time")
	}
	if opts.verbose || opts.stats {
		fmt.Fprintf(out, "  %s", "Debug Groups / Labels")
	}
	fmt.Fprintln(out)

	sepWidth := maxNameLen + 2 + 18 + 2 + 10
	if hasTiming {
		sepWidth += 2 + 10 + 2 + 10
	}
	if opts.verbose || opts.stats {
		sepWidth += 2 + 30
	}
	fmt.Fprintln(out, TableSeparator(sepWidth))

	// Print rows
	for _, k := range shown {
		name := k.Name
		displayName := name
		if len(displayName) > maxNameLen {
			displayName = displayName[:maxNameLen-3] + "..."
		}

		pipeline := "—"
		if k.PipelineAddr != 0 {
			pipeline = fmt.Sprintf("0x%x", k.PipelineAddr)
		}
		fmt.Fprintf(out, nameFmt+"  %-18s  %-10s", displayName, pipeline,
			formatDispatchCount(k.DispatchCount, attributedDispatches, totalDispatches))

		if hasTiming {
			if tStat, ok := timingStats[name]; ok {
				avg := tStat.TotalTime
				if k.DispatchCount > 0 {
					avg = tStat.TotalTime / float64(k.DispatchCount)
				}
				// Note: Timing extraction might not match 1:1 with dispatch counts if aggregation is different.
				// But we display what we have.
				fmt.Fprintf(out, "  %7.2f ms  %7.3f ms", tStat.TotalTime, avg)
			} else {
				// Try looking up via encoder labels if direct name match failed
				var found bool
				for label := range k.EncoderLabels {
					if tStat, ok := timingStats[label]; ok {
						// Found a match via encoder label
						// Aggregating multiple matches is complex, just show first found for now
						// or maybe we should have aggregated timingStats differently
						fmt.Fprintf(out, "  %7.2f ms  %7.3f ms", tStat.TotalTime, tStat.TotalTime/float64(k.DispatchCount)) // approx
						found = true
						break
					}
				}
				if !found {
					fmt.Fprintf(out, "  %10s  %10s", "-", "-")
				}
			}
		}

		if opts.verbose || opts.stats {
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
				fmt.Fprintf(out, "  %s", str)
			}
		}
		fmt.Fprintln(out)
	}
	if len(shown) < len(namedKernels) {
		fmt.Fprintf(out, "... %d more; use --all to show every row\n", len(namedKernels)-len(shown))
	}

	writeInactiveKernelRows(out, rows)

	if unknownBucket != nil {
		writeUnknownKernelBucket(out, unknownBucket)
	}

	return nil
}

// An inventory row can mean three different things, and presenting them
// identically invites the reader to count labels as if they were kernels that
// ran. A pipeline is created before it is used, and MLX creates several it
// then fuses away, so the inventory lists kernels that dispatch zero times.
// The library records share the label field with function records, so it also
// lists UUIDs. Reading four zero-dispatch rows as "a whole kernel family"
// happened, and the header saying "named inventory kernel labels" over all
// three kinds is what made it a reasonable reading.
type kernelRows struct {
	Executed  []*gputrace.KernelStat // dispatched at least once
	Unrun     []*gputrace.KernelStat // pipeline created, never dispatched
	Libraries []*gputrace.KernelStat // library UUIDs, never function names
	Unknown   *gputrace.KernelStat   // the synthetic unattributed bucket
}

func splitKernelRows(kernels []*gputrace.KernelStat) kernelRows {
	var rows kernelRows
	for _, k := range kernels {
		switch {
		case k.Name == "unknown":
			rows.Unknown = k
		case gputrace.IsLibraryUUID(k.Name):
			rows.Libraries = append(rows.Libraries, k)
		case k.DispatchCount == 0:
			rows.Unrun = append(rows.Unrun, k)
		default:
			rows.Executed = append(rows.Executed, k)
		}
	}
	return rows
}

// writeInactiveKernelRows lists the rows that are not evidence a kernel ran,
// under headers that say what they are.
func writeInactiveKernelRows(w io.Writer, rows kernelRows) {
	if len(rows.Unrun) > 0 {
		fmt.Fprintf(w, "\n%d %s created but never dispatched (a pipeline is created before use, "+
			"and fused-away kernels are created and then not used):\n",
			len(rows.Unrun), Pluralize(len(rows.Unrun), "pipeline", "pipelines"))
		for _, k := range rows.Unrun {
			fmt.Fprintf(w, "  %s\n", k.Name)
		}
	}
	if len(rows.Libraries) > 0 {
		fmt.Fprintf(w, "\n%d library %s (not kernel names):\n",
			len(rows.Libraries), Pluralize(len(rows.Libraries), "UUID", "UUIDs"))
		for _, k := range rows.Libraries {
			fmt.Fprintf(w, "  %s\n", k.Name)
		}
	}
}

func writeUnknownKernelBucket(w io.Writer, unknown *gputrace.KernelStat) {
	fmt.Fprintf(w, "\nSynthetic unattributed bucket: %d dispatches", unknown.DispatchCount)
	if len(unknown.EncoderLabels) > 0 {
		fmt.Fprintf(w, " (encoder labels: %v)", unknown.EncoderLabels)
	}
	fmt.Fprintln(w)
}

func writeKernelsJSON(w io.Writer, kernels []*gputrace.KernelStat, timingStats map[string]*gputrace.TimingStat) error {
	type kernelJSON struct {
		Name          string         `json:"name"`
		RowKind       string         `json:"row_kind"`
		PipelineAddr  string         `json:"pipeline_addr"`
		DispatchCount int            `json:"dispatch_count"`
		DebugGroups   map[string]int `json:"debug_groups,omitempty"`
		EncoderLabels map[string]int `json:"encoder_labels,omitempty"`
		TotalTimeMs   float64        `json:"total_time_ms,omitempty"`
		AvgTimeMs     float64        `json:"avg_time_ms,omitempty"`
	}

	out := make([]kernelJSON, len(kernels))
	for i, k := range kernels {
		rowKind := "named_inventory"
		if k.Name == "unknown" {
			rowKind = "synthetic_unattributed_bucket"
		}
		kj := kernelJSON{
			Name:          k.Name,
			RowKind:       rowKind,
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
