package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
)

// ProfilerOutputStats extends StreamDataStats with execution cost.
type ProfilerOutputStats struct {
	*counter.StreamDataStats
	ExecutionCost []counter.ExecutionCostByFunction `json:"execution_cost,omitempty"`
	EncoderCost   []counter.EncoderCost             `json:"encoder_execution_cost,omitempty"`
	// TimelineInfo is explicitly included to ensure it appears in JSON output
	// (StreamDataStats.Timeline is already included via embedding, but this ensures visibility)
}

var profilerCmd = newProfilerCommand(&profilerOptions{limit: 20})

type profilerOptions struct {
	json        bool
	limiters    bool
	kernels     bool
	limit       int
	minCalls    int
	benchfmt    bool
	benchConfig benchfmtConfigFlags
}

func newProfilerCommand(opts *profilerOptions) *cobra.Command {
	if opts.limit == 0 {
		opts.limit = 20
	}
	cmd := &cobra.Command{
		Use:   "profiler <trace.gputrace>",
		Short: "Extract GPU profiler data (timing, dispatches, pipelines) from trace",
		Long: `Extract GPU profiler timing and performance data from a .gputrace bundle.

This command parses the streamData file from .gpuprofiler_raw to extract:
- Per-dispatch timing with function names
- Pipeline compilation statistics (instruction counts, register usage)
- Encoder timing information
- Aggregated cost percentages by function

Works with both full traces and profiler-only traces (no unsorted-capture required).

Example:
  gputrace profiler /path/to/trace.gputrace
  gputrace profiler /path/to/trace.gpuprofiler_raw
  gputrace profiler /path/to/trace.gputrace --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProfiler(cmd, args, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output in JSON format")
	cmd.Flags().BoolVar(&opts.limiters, "limiters", opts.limiters, "Show performance limiter data from Counter files")
	cmd.Flags().BoolVar(&opts.kernels, "kernels", opts.kernels, "Show kernel/function names and per-dispatch details")
	cmd.Flags().IntVar(&opts.limit, "limit", opts.limit, "Maximum non-zero limiter rows to show")
	cmd.Flags().IntVar(&opts.minCalls, "min-calls", opts.minCalls, "Only table rows for functions dispatched at least N times (off by default; reports what it drops; JSON and benchfmt are never filtered)")
	addBenchfmtFlags(cmd, &opts.benchfmt, &opts.benchConfig)
	return cmd
}

func init() {
	rootCmd.AddCommand(profilerCmd)
}

func runProfiler(cmd *cobra.Command, args []string, opts *profilerOptions) error {
	if opts.limit <= 0 {
		return fmt.Errorf("--limit must be > 0")
	}
	if opts.minCalls < 0 {
		return fmt.Errorf("--min-calls must be >= 0")
	}
	if err := validateBenchfmtFlags(opts.benchfmt, opts.benchConfig); err != nil {
		return err
	}
	if opts.benchfmt && opts.json {
		return fmt.Errorf("--benchfmt and --json are mutually exclusive")
	}
	tracePath := args[0]

	profilerDir, stats, err := loadProfilerStats(tracePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Hint: To generate performance data, run:\n")
		fmt.Fprintf(os.Stderr, "  gputrace xcode-profile run %s\n\n", tracePath)
		return err
	}
	// Parse execution cost from Profiling_f_*.raw files
	execCost := aggregateExecutionCost(profilerDir, stats)
	if opts.benchfmt {
		return writeProfilerBenchfmt(cmd.OutOrStdout(), tracePath, stats, execCost, opts.benchConfig)
	}

	if opts.json {
		output := ProfilerOutputStats{
			StreamDataStats: stats,
			ExecutionCost:   execCost,
			EncoderCost:     stats.CounterArchive.EncoderCosts(),
		}
		return writeProfilerJSON(cmd.OutOrStdout(), output)
	}

	// Print human-readable output

	// Calculate summary stats first
	numCBs := 0
	if stats.Timeline != nil {
		numCBs = len(stats.Timeline.CommandBufferTimestamps)
	}
	var totalDispatchTime int
	for _, d := range stats.Dispatches {
		totalDispatchTime += d.DurationUs
	}

	// Calculate memory stats from pipelines
	var totalThreadgroupMem, totalDeviceLoads, totalDeviceStores int
	for _, p := range stats.Pipelines {
		totalThreadgroupMem += p.ThreadgroupMemory
		totalDeviceLoads += p.DeviceLoadCount
		totalDeviceStores += p.DeviceStoreCount
	}

	sortedFuncs := profilerFunctionRows(stats.Dispatches, totalDispatchTime)

	// === MAIN SUMMARY OUTPUT ===
	// One-line summary
	parts := []string{
		fmt.Sprintf("%d %s", numCBs, Pluralize(numCBs, "CB", "CBs")),
		fmt.Sprintf("%d %s", stats.NumEncoders, Pluralize(stats.NumEncoders, "encoder", "encoders")),
		fmt.Sprintf("%d %s", stats.NumGPUCommands, Pluralize(stats.NumGPUCommands, "dispatch", "dispatches")),
	}
	fmt.Printf("%s (%s dispatch span)\n\n", strings.Join(parts, ", "), FormatDuration(totalDispatchTime))

	fmt.Println(Colorize("Summary", ColorBold))
	fmt.Println(TableSeparator(40))
	fmt.Printf("  Command Buffers:   %s\n", FormatCount(numCBs))
	fmt.Printf("  Compute Encoders:  %s\n", FormatCount(stats.NumEncoders))
	fmt.Printf("  Dispatch Calls:    %s\n", FormatCount(stats.NumGPUCommands))
	fmt.Printf("  Unique Pipelines:  %s\n", FormatCount(stats.NumPipelines))
	fmt.Printf("  Encoder Span Time: %s\n", FormatDuration(stats.TotalEncoderTimeUs))
	fmt.Printf("  Dispatch Span Time: %s\n", FormatDuration(totalDispatchTime))
	if stats.EffectiveGPUTimeNs != nil {
		fmt.Printf("  Effective GPU Time: %s\n", FormatDurationNs(*stats.EffectiveGPUTimeNs))
	} else {
		fmt.Println("  Effective GPU Time: (not present in streamData)")
	}
	if stats.CommandBufferActiveNs > 0 {
		fmt.Printf("  CB Active Time:    %s\n", FormatDurationNs(stats.CommandBufferActiveNs))
	}
	if stats.CommandBufferWallNs > 0 {
		fmt.Printf("  CB Wall Time:      %s\n", FormatDurationNs(stats.CommandBufferWallNs))
	}
	if stats.TimingSource != "" {
		fmt.Printf("  Timing Source:     %s\n", stats.TimingSource)
	}
	if totalThreadgroupMem > 0 {
		fmt.Printf("  Threadgroup Mem:   %s (max per pipeline)\n", FormatBytes(uint64(totalThreadgroupMem)))
	}
	if totalDeviceLoads > 0 || totalDeviceStores > 0 {
		fmt.Printf("  Memory Ops:        %s loads, %s stores\n", FormatCount(totalDeviceLoads), FormatCount(totalDeviceStores))
	}

	// Show function call counts (always)
	if len(sortedFuncs) > 0 {
		fmt.Println()
		fmt.Print(formatProfilerFunctionCalls(sortedFuncs, opts.minCalls,
			strings.Contains(stats.TimingSource, "gpuCommandInfoData")))
	}

	// Per-encoder execution cost, which is how Xcode groups the column.
	if encCost := stats.CounterArchive.EncoderCosts(); len(encCost) > 0 {
		fmt.Println()
		fmt.Println(Colorize("Execution Cost by Encoder (from APSCounterData GRC_GPU_CYCLES)", ColorBold))
		fmt.Println(TableSeparator(60))
		fmt.Printf("%-10s %10s %14s %10s\n", "Encoder", "Cost", "GPU Cycles", "Reads")
		fmt.Println(TableSeparator(60))
		for _, c := range encCost {
			mark := ""
			if c.Sparse() {
				mark = "  (few reads)"
			}
			fmt.Printf("%-10d %9s %14s %10d%s\n",
				c.Ordinal, FormatPercent(c.CostPercent), FormatCount(int(c.GPUCycles)), c.EndRecords, mark)
		}
		fmt.Println("Differs from Xcode's Execution Cost column by up to ~0.9 pp; see internal/counter/encodercost.go.")
	}

	// Detailed kernel info only with --kernels flag
	if opts.kernels {
		functionNames := dispatchedFunctionNames(stats.Dispatches)
		pipelines := dispatchedPipelines(stats.Pipelines, stats.Dispatches)

		// Function names
		fmt.Println()
		fmt.Println(Colorize("Kernel Details", ColorBold))
		fmt.Println(TableSeparator(40))
		fmt.Printf("%d dispatched %s:\n", len(functionNames), Pluralize(len(functionNames), "function", "functions"))
		for i, name := range functionNames {
			fmt.Printf("  [%d] %s\n", i, name)
		}

		// Pipelines with addresses
		if len(pipelines) > 0 {
			fmt.Printf("\n%d dispatched %s:\n", len(pipelines), Pluralize(len(pipelines), "pipeline", "pipelines"))
			for i, p := range pipelines {
				if p.PipelineAddress != 0 {
					fmt.Printf("  [%d] 0x%x ID=%d %s\n", i, p.PipelineAddress, p.PipelineID, p.FunctionName)
				} else {
					fmt.Printf("  [%d] ID=%d %s\n", i, p.PipelineID, p.FunctionName)
				}
				fmt.Printf("      Instructions: %d (ALU=%d, FP32=%d, FP16=%d, INT=%d, Branch=%d)\n",
					p.InstructionCount, p.ALUInstructionCount, p.FP32InstructionCount,
					p.FP16InstructionCount, p.INT32InstructionCount+p.INT16InstructionCount,
					p.BranchInstructionCount)
				fmt.Printf("      Registers: temp=%d uniform=%d spilled=%d bytes\n",
					p.TemporaryRegisterCount, p.UniformRegisterCount, p.SpilledBytes)
				if p.ThreadgroupMemory > 0 {
					fmt.Printf("      Threadgroup Memory: %d bytes\n", p.ThreadgroupMemory)
				}
				memOps := p.DeviceLoadCount + p.DeviceStoreCount + p.ThreadgroupLoadCount + p.ThreadgroupStoreCount
				if memOps > 0 {
					fmt.Printf("      Memory Ops: device(load=%d store=%d) threadgroup(load=%d store=%d)\n",
						p.DeviceLoadCount, p.DeviceStoreCount, p.ThreadgroupLoadCount, p.ThreadgroupStoreCount)
				}
			}
		}

		// Encoder timing
		if len(stats.EncoderTimings) > 0 {
			fmt.Printf("\n%d %s (%s total):\n",
				len(stats.EncoderTimings),
				Pluralize(len(stats.EncoderTimings), "encoder", "encoders"),
				FormatDuration(stats.TotalEncoderTimeUs))
			for _, e := range stats.EncoderTimings {
				pct := 0.0
				if stats.TotalEncoderTimeUs > 0 {
					pct = float64(e.DurationMicros) / float64(stats.TotalEncoderTimeUs) * 100
				}
				label := e.Label
				if label == "" {
					label = fmt.Sprintf("encoder_%d", e.Index)
				}
				fmt.Printf("  [%d] %s: %d µs (%.2f%%)\n", e.Index, label, e.DurationMicros, pct)
			}
		}

		// Dispatches with sample info
		if len(stats.Dispatches) > 0 {
			var totalSamples int
			for _, d := range stats.Dispatches {
				totalSamples += d.SampleCount
			}
			fmt.Printf("\nDispatches (%d commands, total %d µs, %d samples):\n",
				len(stats.Dispatches), totalDispatchTime, totalSamples)

			for i, d := range stats.Dispatches {
				if i >= 25 {
					fmt.Printf("  ... (%d more)\n", len(stats.Dispatches)-25)
					break
				}
				pct := 0.0
				if totalDispatchTime > 0 {
					pct = float64(d.DurationUs) / float64(totalDispatchTime) * 100
				}
				if d.SampleCount > 0 {
					fmt.Printf("  [%2d] %5d µs (%5.2f%%) %3d samp (%.2f/µs) %s\n",
						d.Index, d.DurationUs, pct, d.SampleCount, d.SamplingDensity, d.DisplayName())
				} else {
					fmt.Printf("  [%2d] %5d µs (%5.2f%%) %s\n",
						d.Index, d.DurationUs, pct, d.DisplayName())
				}
			}
		}

		// Statistical execution cost
		if len(execCost) > 0 {
			fmt.Println()
			fmt.Println(Colorize("Statistical Execution Cost (from Profiling_f_*.raw)", ColorBold))
			fmt.Println(TableSeparator(70))
			fmt.Printf("%-50s %8s %8s\n", "Function", "Samples", "Cost")
			fmt.Println(TableSeparator(70))
			for _, ec := range execCost {
				fmt.Printf("%-50s %8s %7s\n", ec.FunctionName, FormatCount(ec.SampleCount), FormatPercent(ec.CostPercent))
			}
		}

		// GPRWCNTR sample analysis
		var totalSamples int
		for _, d := range stats.Dispatches {
			totalSamples += d.SampleCount
		}
		if totalSamples > 0 {
			sampleStats := counter.AggregateDispatchSamples(stats.Dispatches)
			if len(sampleStats) > 0 {
				fmt.Println()
				fmt.Println(Colorize("Sample vs Time Cost Analysis (GPRWCNTR)", ColorBold))
				fmt.Println(TableSeparator(85))
				fmt.Printf("%-40s %8s %10s %10s %8s\n", "Function", "Samples", "SampleCost", "TimeCost", "Delta")
				fmt.Println(TableSeparator(85))
				for _, s := range sampleStats {
					name := s.FunctionName
					if len(name) > 40 {
						name = name[:37] + "..."
					}
					fmt.Printf("%-40s %8s %9s %9s %+7.1f%%\n",
						name, FormatCount(s.TotalSamples), FormatPercent(s.SampleCostPct), FormatPercent(s.TimeCostPct), s.CostDelta)
				}
				fmt.Println("\n  Note: Positive delta = higher GPU utilization per us")
			}
		}

		// Command buffer timeline
		if stats.Timeline != nil && len(stats.Timeline.CommandBufferTimestamps) > 0 {
			ti := stats.Timeline
			fmt.Println()
			fmt.Println(Colorize("Command Buffer Timeline", ColorBold))
			fmt.Println(TableSeparator(65))
			fmt.Printf("Timebase: %d/%d (%.2f ns/tick)\n\n",
				ti.TimebaseNumer, ti.TimebaseDenom,
				float64(ti.TimebaseNumer)/float64(ti.TimebaseDenom))

			var minStart, maxEnd uint64 = ^uint64(0), 0
			for _, cb := range ti.CommandBufferTimestamps {
				if cb.StartTicks < minStart {
					minStart = cb.StartTicks
				}
				if cb.EndTicks > maxEnd {
					maxEnd = cb.EndTicks
				}
			}
			totalTicks := maxEnd - minStart
			if totalTicks == 0 {
				totalTicks = 1
			}

			barWidth := 40
			fmt.Printf("%-8s |%-*s| %12s\n", "CB", barWidth, " Timeline", "Duration")
			fmt.Println(TableSeparator(65))
			for _, cb := range ti.CommandBufferTimestamps {
				durationNs := cb.DurationNs(ti.TimebaseNumer, ti.TimebaseDenom)
				durationUs := float64(durationNs) / 1000

				relStart := float64(cb.StartTicks-minStart) / float64(totalTicks)
				relEnd := float64(cb.EndTicks-minStart) / float64(totalTicks)
				barStart := int(relStart * float64(barWidth))
				barEnd := int(relEnd * float64(barWidth))
				if barEnd <= barStart {
					barEnd = barStart + 1
				}

				bar := make([]byte, barWidth)
				for i := range bar {
					bar[i] = ' '
				}
				for i := barStart; i < barEnd && i < barWidth; i++ {
					bar[i] = '='
				}

				fmt.Printf("CB#%-5d |%s| %10.2f µs\n", cb.Index, string(bar), durationUs)
			}
		}

		// Encoder timeline
		if stats.Timeline != nil && len(stats.Timeline.EncoderProfiles) > 0 {
			ti := stats.Timeline
			fmt.Println()
			fmt.Println(Colorize("Encoder Timeline", ColorBold))
			fmt.Println(TableSeparator(80))
			fmt.Printf("%d %s\n\n", len(ti.EncoderProfiles), Pluralize(len(ti.EncoderProfiles), "encoder", "encoders"))

			var minStart, maxEnd uint64 = ^uint64(0), 0
			for _, ep := range ti.EncoderProfiles {
				if ep.StartTicks > 0 && ep.StartTicks < minStart {
					minStart = ep.StartTicks
				}
				if ep.EndTicks > maxEnd {
					maxEnd = ep.EndTicks
				}
			}
			totalTicks := maxEnd - minStart
			if totalTicks == 0 {
				totalTicks = 1
			}

			barWidth := 40
			fmt.Printf("%-10s %8s |%-*s| %12s\n", "Encoder", "Samples", barWidth, " Timeline", "Duration")
			fmt.Println(TableSeparator(80))
			for _, ep := range ti.EncoderProfiles {
				durationUs := float64(ep.DurationNs) / 1000

				relStart := float64(ep.StartTicks-minStart) / float64(totalTicks)
				relEnd := float64(ep.EndTicks-minStart) / float64(totalTicks)
				barStart := int(relStart * float64(barWidth))
				barEnd := int(relEnd * float64(barWidth))
				if barEnd <= barStart {
					barEnd = barStart + 1
				}

				bar := make([]byte, barWidth)
				for i := range bar {
					bar[i] = ' '
				}
				for i := barStart; i < barEnd && i < barWidth; i++ {
					bar[i] = '#'
				}

				fmt.Printf("Enc#%-6d %8d |%s| %10.2f µs\n", ep.Index, ep.SampleCount, string(bar), durationUs)
			}
		}
	}

	// Display performance limiters if requested
	if opts.limiters {
		limiterData := extractLimiterData(profilerDir)
		if len(limiterData) > 0 {
			rows, nonzero, zero := selectLimiterRows(limiterData, opts.limit)
			fmt.Println()
			fmt.Println(Colorize("Candidate Performance Limiters (heuristic Counter-file decoder)", ColorBold))
			fmt.Printf("Showing %d of %d non-zero rows", len(rows), nonzero)
			if zero > 0 {
				fmt.Printf(" (%d zero rows omitted)", zero)
			}
			fmt.Println()
			fmt.Println(TableSeparator(78))
			fmt.Printf("%-5s %-18s %-16s %-16s %-16s\n",
				"Record", "Instr Throughput", "Int & Complex", "F32 Limiter", "L1 Cache")
			fmt.Println(TableSeparator(78))
			for _, ld := range rows {
				fmt.Printf("%-5d %17s %15s %15s %15s\n",
					ld.EncoderIndex, FormatPercent(ld.InstructionThroughput),
					FormatPercent(ld.IntegerComplex), FormatPercent(ld.F32Limiter), FormatPercent(ld.L1Cache))
			}
			fmt.Println("\nNote: Values are heuristic candidates, not source-backed bottleneck measurements.")
			fmt.Println("Higher values mean more constrained only if the candidate field mapping is correct.")
			if nonzero > len(rows) {
				fmt.Printf("Use --limit %d or higher to show all non-zero rows.\n", nonzero)
			}
		}
	}

	return nil
}

// profilerFunctionRows aggregates dispatches by function name and ranks them by
// span, descending. The rows are KernelTiming values so that this table shares
// the low-sample marker and the --min-calls filter with the timing command
// instead of restating either.
func profilerFunctionRows(dispatches []counter.DispatchInfo, totalSpanUs int) []*gputrace.KernelTiming {
	byName := make(map[string]*gputrace.KernelTiming)
	var rows []*gputrace.KernelTiming
	for _, d := range dispatches {
		name := d.DisplayName()
		kt, ok := byName[name]
		if !ok {
			kt = &gputrace.KernelTiming{Name: name}
			byName[name] = kt
			rows = append(rows, kt)
		}
		kt.InvocationCount++
		kt.TotalDuration += time.Duration(d.DurationUs) * time.Microsecond
	}
	for _, kt := range rows {
		if totalSpanUs > 0 {
			kt.PercentOfTotal = float64(kt.TotalDuration.Microseconds()) / float64(totalSpanUs) * 100
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].TotalDuration > rows[j].TotalDuration
	})
	return rows
}

// formatProfilerFunctionCalls renders the cost-ranked function table. minCalls
// filters the table only; the JSON and benchfmt outputs are built from the
// unfiltered dispatches, because a filtered export is a partial file that reads
// as a complete one once the flag is forgotten. Filtering never reorders: the
// surviving rows keep their ranking, and the note says the shares no longer sum
// to the whole.
func formatProfilerFunctionCalls(rows []*gputrace.KernelTiming, minCalls int, cumulativeOffsets bool) string {
	shown, dropped := gputrace.FilterMinCalls(rows, minCalls)

	var out strings.Builder
	out.WriteString(Colorize("Function Calls", ColorBold) + "\n")
	out.WriteString(TableSeparator(80) + "\n")
	fmt.Fprintf(&out, "%-50s %8s %10s %10s\n", "Function", "Calls", "Span(us)", "Span Share")
	out.WriteString(TableSeparator(80) + "\n")
	for _, kt := range shown {
		marker := ""
		if kt.IsLowSample() {
			marker = gputrace.LowSampleMarker
		}
		fmt.Fprintf(&out, "%-50s %8s %10s %7s%s\n",
			kt.Name,
			FormatCount(kt.InvocationCount),
			FormatCount(int(kt.TotalDuration.Microseconds())),
			FormatPercent(kt.PercentOfTotal),
			marker)
	}
	out.WriteString(gputrace.LowSampleFootnote(shown))
	out.WriteString(gputrace.MinCallsNote(minCalls, dropped, len(rows)))
	if cumulativeOffsets {
		out.WriteString("Attribution note: span values are cumulative-offset deltas and may include boundary or gap time.\n")
	}
	return out.String()
}

func selectLimiterRows(all []limiterMetrics, limit int) (rows []limiterMetrics, nonzero, zero int) {
	for _, row := range all {
		if limiterPeak(row) < 0.05 {
			zero++
			continue
		}
		rows = append(rows, row)
	}
	nonzero = len(rows)
	sort.SliceStable(rows, func(i, j int) bool {
		return limiterPeak(rows[i]) > limiterPeak(rows[j])
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nonzero, zero
}

func limiterPeak(row limiterMetrics) float64 {
	return max(row.InstructionThroughput, row.IntegerComplex, row.F32Limiter, row.L1Cache)
}

func dispatchedFunctionNames(dispatches []counter.DispatchInfo) []string {
	seen := make(map[string]bool)
	var names []string
	for _, dispatch := range dispatches {
		name := dispatch.DisplayName()
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

func dispatchedPipelines(pipelines []counter.PipelineStats, dispatches []counter.DispatchInfo) []counter.PipelineStats {
	ids := make(map[int]bool)
	names := make(map[string]bool)
	for _, dispatch := range dispatches {
		if dispatch.PipelineID != 0 {
			ids[dispatch.PipelineID] = true
		}
		if dispatch.FunctionName != "" {
			names[dispatch.FunctionName] = true
		}
	}
	var dispatched []counter.PipelineStats
	for _, pipeline := range pipelines {
		if ids[pipeline.PipelineID] || names[pipeline.FunctionName] {
			dispatched = append(dispatched, pipeline)
		}
	}
	return dispatched
}

func writeProfilerJSON(w io.Writer, output ProfilerOutputStats) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

// limiterMetrics holds extracted performance limiter values per encoder.
type limiterMetrics struct {
	EncoderIndex          int
	InstructionThroughput float64
	IntegerComplex        float64
	F32Limiter            float64
	L1Cache               float64
}

// extractLimiterData extracts performance limiter values from Counter files.
func extractLimiterData(profilerDir string) []limiterMetrics {
	// Read and parse counter files for limiter data
	var results []limiterMetrics

	// Parse all counter files and extract limiter metrics per encoder
	counterFiles, err := filepath.Glob(filepath.Join(profilerDir, "Counters_f_*.raw"))
	if err != nil || len(counterFiles) == 0 {
		return nil
	}

	// Parse first file to get encoder count
	encoderLimiters := make(map[int]*limiterMetrics)

	for _, file := range counterFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		// Find record boundaries (0x4E marker)
		records := findRecordStarts(data)
		encoderIdx := 0

		for i, offset := range records {
			// Determine record size
			var recordSize int
			if i+1 < len(records) {
				recordSize = records[i+1] - offset
			} else {
				recordSize = len(data) - offset
			}

			// Metadata records mark encoder boundaries
			if recordSize >= 2300 && recordSize <= 2900 {
				encoderIdx++
				if _, exists := encoderLimiters[encoderIdx]; !exists {
					encoderLimiters[encoderIdx] = &limiterMetrics{EncoderIndex: encoderIdx}
				}
				continue
			}

			// Sample records (464 bytes) contain limiter values
			if recordSize != 464 {
				continue
			}

			recordData := data[offset : offset+recordSize]

			// Extract float32 limiter values
			limiters := extractFloatValues(recordData, 0.001, 100.0, 10)
			if len(limiters) == 0 {
				continue
			}

			// Initialize encoder entry if needed
			if _, exists := encoderLimiters[encoderIdx]; !exists {
				encoderLimiters[encoderIdx] = &limiterMetrics{EncoderIndex: encoderIdx}
			}
			ld := encoderLimiters[encoderIdx]

			// Map extracted values to limiter types (heuristic based on value ranges)
			for _, val := range limiters {
				switch {
				case val >= 0.01 && val <= 5 && ld.InstructionThroughput == 0:
					// Instruction throughput limiter (small %)
					ld.InstructionThroughput = val
				case val >= 0.01 && val <= 5 && ld.IntegerComplex == 0:
					// Integer/complex limiter
					ld.IntegerComplex = val
				case val >= 0.01 && val <= 10 && ld.F32Limiter == 0:
					// F32 limiter
					ld.F32Limiter = val
				case val >= 0.01 && val <= 5 && ld.L1Cache == 0:
					// L1 cache limiter
					ld.L1Cache = val
				}
			}
		}
	}

	// Convert map to sorted slice
	for i := 1; i <= len(encoderLimiters); i++ {
		if ld, exists := encoderLimiters[i]; exists {
			results = append(results, *ld)
		}
	}

	return results
}

// findRecordStarts finds 0x4E record markers in counter file data.
func findRecordStarts(data []byte) []int {
	var starts []int
	for i := 0; i < len(data)-4; i++ {
		if data[i] == 0x4E && data[i+1] == 0x00 && data[i+2] == 0x00 && data[i+3] == 0x00 {
			starts = append(starts, i)
		}
	}
	return starts
}

// extractFloatValues extracts float32 values in the given range from record data.
func extractFloatValues(data []byte, minVal, maxVal float64, maxCount int) []float64 {
	var values []float64
	seen := make(map[float64]bool)

	for i := 0; i < len(data)-4 && len(values) < maxCount; i += 4 {
		bits := uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16 | uint32(data[i+3])<<24
		val := float64(math.Float32frombits(bits))

		// Check for valid float in range (val != val is NaN check)
		if val >= minVal && val <= maxVal && val == val && !seen[val] {
			values = append(values, val)
			seen[val] = true
		}
	}
	return values
}
