package cmd

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/counter"
)

// ProfilerOutputStats extends StreamDataStats with execution cost.
type ProfilerOutputStats struct {
	*counter.StreamDataStats
	ExecutionCost []counter.ExecutionCostByFunction `json:"execution_cost,omitempty"`
	// TimelineInfo is explicitly included to ensure it appears in JSON output
	// (StreamDataStats.Timeline is already included via embedding, but this ensures visibility)
}

var (
	profilerJSON     bool
	profilerLimiters bool
)

var profilerCmd = &cobra.Command{
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
	RunE: runProfiler,
}

func init() {
	rootCmd.AddCommand(profilerCmd)
	profilerCmd.Flags().BoolVar(&profilerJSON, "json", false, "Output in JSON format")
	profilerCmd.Flags().BoolVar(&profilerLimiters, "limiters", false, "Show performance limiter data from Counter files")
}

func runProfiler(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Find .gpuprofiler_raw directory
	profilerDir := ""

	// Check if it's directly a .gpuprofiler_raw directory
	if filepath.Ext(tracePath) == ".gpuprofiler_raw" {
		profilerDir = tracePath
	} else {
		// Look inside for .gpuprofiler_raw
		entries, err := os.ReadDir(tracePath)
		if err != nil {
			return fmt.Errorf("read directory: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() && filepath.Ext(e.Name()) == ".gpuprofiler_raw" {
				profilerDir = filepath.Join(tracePath, e.Name())
				break
			}
		}
	}

	if profilerDir == "" {
		return fmt.Errorf("no .gpuprofiler_raw directory found in %s", tracePath)
	}

	// Parse streamData
	stats, err := counter.ParseStreamData(profilerDir)
	if err != nil {
		return fmt.Errorf("parse streamData: %w", err)
	}

	// Correlate GPRWCNTR samples with dispatches for per-dispatch GPU utilization
	counter.CorrelateDispatchSamples(stats)

	// Parse execution cost from Profiling_f_*.raw files
	var execCost []counter.ExecutionCostByFunction
	if costMetrics, err := counter.ExtractExecutionCostFromDir(profilerDir); err == nil {
		execCost = counter.AggregateExecutionCostByFunction(costMetrics, stats.Pipelines)
	}

	if profilerJSON {
		output := ProfilerOutputStats{
			StreamDataStats: stats,
			ExecutionCost:   execCost,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	}

	// Print human-readable output
	fmt.Printf("=== GPU Profiler Data ===\n\n")
	fmt.Printf("Source: %s\n\n", profilerDir)

	// Function names
	fmt.Printf("Functions (%d):\n", len(stats.FunctionNames))
	for i, name := range stats.FunctionNames {
		if name != "" {
			fmt.Printf("  [%d] %s\n", i, name)
		}
	}

	// Pipelines with addresses (similar to Xcode's ComputePipelineState view)
	if len(stats.Pipelines) > 0 {
		fmt.Printf("\nPipelines (%d):\n", len(stats.Pipelines))
		for i, p := range stats.Pipelines {
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
			// Memory operations summary
			memOps := p.DeviceLoadCount + p.DeviceStoreCount + p.ThreadgroupLoadCount + p.ThreadgroupStoreCount
			if memOps > 0 {
				fmt.Printf("      Memory Ops: device(load=%d store=%d) threadgroup(load=%d store=%d)\n",
					p.DeviceLoadCount, p.DeviceStoreCount, p.ThreadgroupLoadCount, p.ThreadgroupStoreCount)
			}
		}
	}

	// Encoder timing
	if len(stats.EncoderTimings) > 0 {
		fmt.Printf("\nEncoder Timings (%d encoders, total %d µs):\n", len(stats.EncoderTimings), stats.TotalTimeUs)
		for _, e := range stats.EncoderTimings {
			pct := 0.0
			if stats.TotalTimeUs > 0 {
				pct = float64(e.DurationMicros) / float64(stats.TotalTimeUs) * 100
			}
			label := e.Label
			if label == "" {
				label = fmt.Sprintf("encoder_%d", e.Index)
			}
			fmt.Printf("  [%d] %s: %d µs (%.2f%%)\n", e.Index, label, e.DurationMicros, pct)
		}
	}

	// Dispatches with GPRWCNTR sample correlation
	if len(stats.Dispatches) > 0 {
		// Calculate total dispatch time and samples
		var totalDispatchTime, totalSamples int
		for _, d := range stats.Dispatches {
			totalDispatchTime += d.DurationUs
			totalSamples += d.SampleCount
		}

		fmt.Printf("\nDispatches (%d commands, total %d µs, %d samples):\n",
			len(stats.Dispatches), totalDispatchTime, totalSamples)

		// Print first 25 dispatches with sample info
		for i, d := range stats.Dispatches {
			if i >= 25 {
				fmt.Printf("  ... (%d more)\n", len(stats.Dispatches)-25)
				break
			}
			pct := 0.0
			if totalDispatchTime > 0 {
				pct = float64(d.DurationUs) / float64(totalDispatchTime) * 100
			}
			// Show sample count and density if available
			if d.SampleCount > 0 {
				fmt.Printf("  [%2d] %5d µs (%5.2f%%) %3d samp (%.2f/µs) %s\n",
					d.Index, d.DurationUs, pct, d.SampleCount, d.SamplingDensity, d.FunctionName)
			} else {
				fmt.Printf("  [%2d] %5d µs (%5.2f%%) %s\n",
					d.Index, d.DurationUs, pct, d.FunctionName)
			}
		}

		// Aggregate by function
		fmt.Printf("\n=== Aggregated by Function (Execution Cost) ===\n")
		funcTotals := make(map[string]int)
		funcCounts := make(map[string]int)
		for _, d := range stats.Dispatches {
			name := d.FunctionName
			if name == "" {
				name = fmt.Sprintf("(pipeline_%d)", d.PipelineIndex)
			}
			funcTotals[name] += d.DurationUs
			funcCounts[name]++
		}

		type funcStat struct {
			name  string
			time  int
			count int
		}
		var sorted []funcStat
		for name, time := range funcTotals {
			sorted = append(sorted, funcStat{name, time, funcCounts[name]})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].time > sorted[j].time
		})

		fmt.Printf("\n%-50s %8s %8s %8s\n", "Function", "Time(µs)", "Count", "Cost%")
		fmt.Printf("%s\n", "--------------------------------------------------------------------------------")
		for _, fs := range sorted {
			pct := 0.0
			if totalDispatchTime > 0 {
				pct = float64(fs.time) / float64(totalDispatchTime) * 100
			}
			fmt.Printf("%-50s %8d %8d %7.2f%%\n", fs.name, fs.time, fs.count, pct)
		}
	}

	// Display statistical execution cost if available
	if len(execCost) > 0 {
		fmt.Printf("\n=== Statistical Execution Cost (from Profiling_f_*.raw) ===\n")
		fmt.Printf("%-50s %8s %8s\n", "Function", "Samples", "Cost%")
		fmt.Printf("%s\n", "----------------------------------------------------------------------")
		for _, ec := range execCost {
			fmt.Printf("%-50s %8d %7.2f%%\n", ec.FunctionName, ec.SampleCount, ec.CostPercent)
		}
	}

	// Display GPRWCNTR sample-based cost analysis if we have sample data
	var totalSamples int
	for _, d := range stats.Dispatches {
		totalSamples += d.SampleCount
	}
	if totalSamples > 0 {
		sampleStats := counter.AggregateDispatchSamples(stats.Dispatches)
		if len(sampleStats) > 0 {
			fmt.Printf("\n=== Sample vs Time Cost Analysis (GPRWCNTR) ===\n")
			fmt.Printf("%-40s %8s %10s %10s %8s\n", "Function", "Samples", "SampleCost", "TimeCost", "Delta")
			fmt.Printf("%s\n", "-----------------------------------------------------------------------------------------")
			for _, s := range sampleStats {
				name := s.FunctionName
				if len(name) > 40 {
					name = name[:37] + "..."
				}
				fmt.Printf("%-40s %8d %9.1f%% %9.1f%% %+7.1f%%\n",
					name, s.TotalSamples, s.SampleCostPct, s.TimeCostPct, s.CostDelta)
			}
			fmt.Println("\n  Note: Positive delta = higher GPU utilization per µs")
		}
	}

	// Display command buffer timeline from APSTimelineData with ASCII bars
	if stats.Timeline != nil && len(stats.Timeline.CommandBufferTimestamps) > 0 {
		ti := stats.Timeline
		fmt.Printf("\n=== Command Buffer Timeline (from APSTimelineData) ===\n")
		fmt.Printf("Timebase: %d/%d (%.2f ns/tick)\n",
			ti.TimebaseNumer, ti.TimebaseDenom,
			float64(ti.TimebaseNumer)/float64(ti.TimebaseDenom))
		fmt.Printf("Capture Start: %d ticks\n\n", ti.AbsoluteTime)

		// Calculate min/max for ASCII bar scaling
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
		fmt.Printf("%s\n", "----------------------------------------------------------------")
		for _, cb := range ti.CommandBufferTimestamps {
			durationNs := cb.DurationNs(ti.TimebaseNumer, ti.TimebaseDenom)
			durationUs := float64(durationNs) / 1000

			// Calculate bar position
			relStart := float64(cb.StartTicks-minStart) / float64(totalTicks)
			relEnd := float64(cb.EndTicks-minStart) / float64(totalTicks)
			barStart := int(relStart * float64(barWidth))
			barEnd := int(relEnd * float64(barWidth))
			if barEnd <= barStart {
				barEnd = barStart + 1
			}

			// Build ASCII bar
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

	// Display encoder profiler data from GPRWCNTR blobs
	if stats.Timeline != nil && len(stats.Timeline.EncoderProfiles) > 0 {
		ti := stats.Timeline
		fmt.Printf("\n=== Encoder Timeline (from GPRWCNTR ShaderProfilerData) ===\n")
		fmt.Printf("Encoders: %d\n\n", len(ti.EncoderProfiles))

		// Calculate min/max for ASCII bar scaling
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
		fmt.Printf("%s\n", "-------------------------------------------------------------------------------")
		for _, ep := range ti.EncoderProfiles {
			durationUs := float64(ep.DurationNs) / 1000

			// Calculate bar position
			relStart := float64(ep.StartTicks-minStart) / float64(totalTicks)
			relEnd := float64(ep.EndTicks-minStart) / float64(totalTicks)
			barStart := int(relStart * float64(barWidth))
			barEnd := int(relEnd * float64(barWidth))
			if barEnd <= barStart {
				barEnd = barStart + 1
			}

			// Build ASCII bar
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

	// Display frame/encoder statistics (similar to Xcode's summary)
	fmt.Printf("\n=== Frame Statistics ===\n")
	numCBs := 0
	if stats.Timeline != nil {
		numCBs = len(stats.Timeline.CommandBufferTimestamps)
	}
	// Count dispatches per encoder
	encoderDispatches := make(map[int]int)
	for _, d := range stats.Dispatches {
		encoderDispatches[d.EncoderIndex]++
	}
	fmt.Printf("  Command Buffers: %d\n", numCBs)
	fmt.Printf("  Compute Encoders: %d\n", stats.NumEncoders)
	fmt.Printf("  Total Dispatches: %d\n", stats.NumGPUCommands)
	fmt.Printf("  Unique Pipelines: %d\n", stats.NumPipelines)
	if len(encoderDispatches) > 0 {
		fmt.Printf("  Dispatches per Encoder:\n")
		for enc := 0; enc < stats.NumEncoders; enc++ {
			if count, ok := encoderDispatches[enc]; ok {
				fmt.Printf("    Encoder %d: %d dispatches\n", enc, count)
			}
		}
	}

	// Display performance limiters if requested
	if profilerLimiters {
		limiterData := extractLimiterData(profilerDir)
		if len(limiterData) > 0 {
			fmt.Printf("\n=== Performance Limiters (from Counter files) ===\n")
			fmt.Printf("%-5s %-16s %-18s %-16s %-16s %-16s\n",
				"Enc", "Occupancy Mgr", "Instr Throughput", "Int & Complex", "F32 Limiter", "L1 Cache")
			fmt.Printf("%s\n", "--------------------------------------------------------------------------------------------")
			for _, ld := range limiterData {
				fmt.Printf("%-5d %15.2f%% %17.2f%% %15.2f%% %15.2f%% %15.2f%%\n",
					ld.EncoderIndex, ld.OccupancyManager, ld.InstructionThroughput,
					ld.IntegerComplex, ld.F32Limiter, ld.L1Cache)
			}
			fmt.Println("\nNote: Limiter percentages indicate bottleneck sources (higher = more constrained)")
		}
	}

	return nil
}

// limiterMetrics holds extracted performance limiter values per encoder.
type limiterMetrics struct {
	EncoderIndex          int
	OccupancyManager      float64
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
				case val >= 50 && val <= 100 && ld.OccupancyManager == 0:
					// Occupancy Manager typically 50-80%
					ld.OccupancyManager = val
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
