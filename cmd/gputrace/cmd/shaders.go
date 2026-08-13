package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/profilerraw"
)

var shadersCmd = newShadersCommand(&shadersOptions{
	format: "text",
})

type shadersOptions struct {
	verbose   bool
	estimate  bool
	format    string
	all       bool
	xcodeCost bool
}

func newShadersCommand(opts *shadersOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shaders <trace.gputrace>",
		Short: "Show shader performance statistics",
		Long: `Display shader/kernel performance statistics.

By default shows a simple two-column output:
  - Share % (SIMD-group share for full traces; dispatch-span share for profiler-only traces)
  - Shader name

Use --xcode-cost to run Xcode's private stream-data processor and show the
pipeline compute-time share from its All Shaders table. This is slower than the
default parser and requires the matching Xcode framework and GTLLVMHelper.

Use --all for full Xcode Instruments format with additional columns:
  - Type (Compute)
  - Pipeline State address
  - # SIMD Groups (SIMD wavefronts dispatched)
  - Temp Regs (temporary register count)
  - High Register, shown only when source-backed
  - Spilled Bytes (register spills to memory)
  - Dev Load / Dev Store (device memory load and store instruction counts)

Temp Regs, Spilled, Dev Load and Dev Store come from the shader compiler's
pipelinePerformanceStatistics and are available for profiler-only traces too.
They read "?" when the trace carries no statistics for that shader; zero is a
real count and is printed as 0.

Examples:
  gputrace shaders trace.gputrace                    # Simple cost + name output
  gputrace shaders trace.gputrace --all              # Full Xcode format
  gputrace shaders trace.gputrace --estimate         # Show estimates for unknown fields
  gputrace shaders trace.gputrace --xcode-cost       # Match Xcode's All Shaders Cost
  gputrace shaders trace.gputrace --format csv       # Export as CSV
  gputrace shaders trace.gputrace --format json      # Export as JSON`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShaders(cmd, args, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", opts.verbose, "Show verbose output")
	cmd.Flags().BoolVarP(&opts.estimate, "estimate", "e", opts.estimate, "Show estimated values for uncomputed fields")
	cmd.Flags().StringVarP(&opts.format, "format", "f", opts.format, "Output format: text, csv, or json")
	cmd.Flags().BoolVarP(&opts.all, "all", "a", opts.all, "Show all columns (full Xcode Instruments format)")
	cmd.Flags().BoolVar(&opts.xcodeCost, "xcode-cost", opts.xcodeCost, "Use Xcode's processed pipeline timing for Cost")
	return cmd
}

func init() {
	rootCmd.AddCommand(shadersCmd)
}

func runShaders(cmd *cobra.Command, args []string, opts *shadersOptions) error {
	if err := validateShadersFormat(opts.format); err != nil {
		return err
	}

	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Check if this is a full trace (has unsorted-capture for SIMD group data)
	hasUnsortedCapture := checkUnsortedCapture(tracePath)

	// Check if profiler data exists
	profilerDir := findProfilerDir(tracePath)

	// Require profiler data for accurate cost percentages
	if profilerDir == "" {
		// No profiler data - show shaders without cost, with hint
		if hasUnsortedCapture {
			return runShadersNoCost(tracePath, opts)
		}
		fmt.Fprintln(os.Stderr, "No profiler data found. To get shader timing:")
		fmt.Fprintf(os.Stderr, "  gputrace xp run %s -o profiled.gputrace\n\n", tracePath)
		return fmt.Errorf("profiler data required for shader timing")
	}
	if opts.xcodeCost {
		return runShadersXcodeCost(cmd, tracePath, opts)
	}

	if hasUnsortedCapture {
		// Full trace with profiler: use SIMD-based share.
		return runShadersFromFullTrace(tracePath, opts)
	}

	// Profiler-only: use dispatch duration for cost
	return runShadersFromProfiler(tracePath, opts)
}

func validateShadersFormat(format string) error {
	switch format {
	case "text", "csv", "json":
		return nil
	default:
		return invalidShadersFormatError(format)
	}
}

func invalidShadersFormatError(format string) error {
	return fmt.Errorf("invalid shaders format %q (must be text, csv, or json)", format)
}

// checkUnsortedCapture checks if unsorted-capture file or directory exists.
func checkUnsortedCapture(tracePath string) bool {
	unsortedPath := filepath.Join(tracePath, "unsorted-capture")
	if _, err := os.Stat(unsortedPath); err == nil {
		return true
	}
	return false
}

// runShadersNoCost shows shader names without cost percentages (no profiler data).
func runShadersNoCost(tracePath string, opts *shadersOptions) error {
	fmt.Fprint(os.Stderr, profileReplayHint(tracePath))
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}

	// Extract shader names from trace
	report, err := gputrace.ExtractShaderMetrics(trace)
	if err != nil {
		return fmt.Errorf("extract shader metrics: %w", err)
	}

	return writeShadersNoCost(report, tracePath, opts)
}

func writeShadersNoCost(report *gputrace.ShaderMetricsReport, tracePath string, opts *shadersOptions) error {
	fmt.Fprintf(os.Stderr, "No profiler data. To get a measured shader share, run:\n")
	fmt.Fprintf(os.Stderr, "  gputrace xp run %s -o profiled.gputrace\n\n", tracePath)

	switch opts.format {
	case "csv":
		return gputrace.ExportShaderMetricsCSV(os.Stdout, report)
	case "json":
		return gputrace.ExportShaderMetricsJSON(os.Stdout, report)
	case "text":
		return formatShadersNoCostText(os.Stdout, report)
	default:
		return invalidShadersFormatError(opts.format)
	}
}

func formatShadersNoCostText(w io.Writer, report *gputrace.ShaderMetricsReport) error {
	fmt.Fprintf(w, "Share     Name\n")
	for _, shader := range report.Shaders {
		fmt.Fprintf(w, "    ?     %s\n", shader.Name)
	}
	return nil
}

// runShadersFromFullTrace uses full trace parsing for SIMD-based share.
func runShadersFromFullTrace(tracePath string, opts *shadersOptions) error {
	// Open trace for full parsing
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}

	// Try to get dispatch-level SIMD groups by joining capture data with profiler data
	profilerDir := findProfilerDir(tracePath)
	if profilerDir != "" {
		// Use combined approach: capture file dispatches + profiler function names
		report, err := extractSIMDBasedMetrics(trace, profilerDir)
		if err == nil && len(report.Shaders) > 0 {
			report.ShareBasis = "simd_groups"
			writeShaderShareBasis(opts.format, "SIMD groups")
			// Output based on format
			switch opts.format {
			case "csv":
				return gputrace.ExportShaderMetricsCSV(os.Stdout, report)
			case "json":
				return gputrace.ExportShaderMetricsJSON(os.Stdout, report)
			case "text":
				if opts.all {
					return gputrace.FormatShadersXcodeStyle(os.Stdout, report, trace, opts.estimate)
				}
				return gputrace.FormatShadersSimple(os.Stdout, report)
			default:
				return invalidShadersFormatError(opts.format)
			}
		}
		// Fall through to legacy method if combined approach fails
	}

	// Fallback: use legacy ExtractShaderMetrics (may not have accurate SIMD groups)
	report, err := gputrace.ExtractShaderMetrics(trace)
	if err != nil {
		return fmt.Errorf("extract shader metrics: %w", err)
	}

	// Recalculate share based on SIMD Groups (TotalThreadgroups).
	var totalSIMDGroups uint64
	for _, shader := range report.Shaders {
		totalSIMDGroups += shader.TotalThreadgroups
	}

	if totalSIMDGroups > 0 {
		for _, shader := range report.Shaders {
			shader.PercentOfTotal = float64(shader.TotalThreadgroups) / float64(totalSIMDGroups) * 100.0
		}
	}
	report.ShareBasis = "simd_groups"
	writeShaderShareBasis(opts.format, "SIMD groups")

	// Re-sort by SIMD-based cost
	sort.Slice(report.Shaders, func(i, j int) bool {
		if report.Shaders[i].PercentOfTotal != report.Shaders[j].PercentOfTotal {
			return report.Shaders[i].PercentOfTotal > report.Shaders[j].PercentOfTotal
		}
		return report.Shaders[i].Name < report.Shaders[j].Name
	})

	// Output based on format
	switch opts.format {
	case "csv":
		if err := gputrace.ExportShaderMetricsCSV(os.Stdout, report); err != nil {
			return fmt.Errorf("failed to export CSV: %w", err)
		}
	case "json":
		if err := gputrace.ExportShaderMetricsJSON(os.Stdout, report); err != nil {
			return fmt.Errorf("failed to export JSON: %w", err)
		}
	case "text":
		if opts.all {
			gputrace.FormatShadersXcodeStyle(os.Stdout, report, trace, opts.estimate)
		} else {
			gputrace.FormatShadersSimple(os.Stdout, report)
		}
	default:
		return invalidShadersFormatError(opts.format)
	}

	return nil
}

// extractSIMDBasedMetrics extracts shader metrics with accurate SIMD group counts
// by joining dispatch threadgroup data from capture file with function names from profiler.
func extractSIMDBasedMetrics(trace *gputrace.Trace, profilerDir string) (*gputrace.ShaderMetricsReport, error) {
	// Parse dispatch markers from capture data to get threadgroup dimensions
	dispatches := trace.ParseDispatchInRegion(trace.CaptureData, 0)

	// Parse profiler streamData to get function names per dispatch index
	stats, err := counter.ParseStreamData(profilerDir, nil)
	if err != nil {
		return nil, fmt.Errorf("parse streamData: %w", err)
	}

	// Join by index: each profiler dispatch corresponds to a capture dispatch
	// Both should be in the same order
	if len(dispatches) != len(stats.Dispatches) {
		// If counts don't match, fall back - the data might not align
		return nil, fmt.Errorf("dispatch count mismatch: capture=%d, profiler=%d", len(dispatches), len(stats.Dispatches))
	}

	// Calculate SIMD groups per function
	// SIMD Groups = threadgroups = ceil(threads / threadsPerGroup) in each dimension
	funcSIMDGroups := make(map[string]uint64)
	funcDurations := make(map[string]uint64)
	funcPipelineStats := make(map[string]*counter.PipelineStats)

	// Build pipeline stats lookup
	for i := range stats.Pipelines {
		p := &stats.Pipelines[i]
		if p.FunctionName != "" {
			funcPipelineStats[p.FunctionName] = p
		}
	}

	for i, dispatch := range dispatches {
		simdGroups := dispatch.SIMDGroups()

		// Get function name from profiler data
		funcName := ""
		if i < len(stats.Dispatches) {
			funcName = stats.Dispatches[i].FunctionName
			funcDurations[funcName] += uint64(stats.Dispatches[i].DurationUs) * 1000 // Convert to ns
		}
		if funcName == "" {
			funcName = fmt.Sprintf("(dispatch_%d)", i)
		}

		funcSIMDGroups[funcName] += simdGroups
	}

	// Calculate total SIMD groups
	var totalSIMDGroups uint64
	for _, groups := range funcSIMDGroups {
		totalSIMDGroups += groups
	}

	// Build report
	report := &gputrace.ShaderMetricsReport{
		Shaders:          make([]*gputrace.ShaderMetrics, 0, len(funcSIMDGroups)),
		TotalGPUTimeNs:   uint64(stats.TotalTimeUs) * 1000,
		TotalGPUTimeMs:   float64(stats.TotalTimeUs) / 1000.0,
		TotalInvocations: len(dispatches),
	}

	for funcName, simdGroups := range funcSIMDGroups {
		m := &gputrace.ShaderMetrics{
			Name:              funcName,
			TotalThreadgroups: simdGroups,
			TotalDurationNs:   funcDurations[funcName],
		}

		// Calculate SIMD-based share percentage.
		if totalSIMDGroups > 0 {
			m.PercentOfTotal = float64(simdGroups) / float64(totalSIMDGroups) * 100.0
		}

		// Add pipeline stats if available
		if ps := funcPipelineStats[funcName]; ps != nil {
			m.Address = ps.PipelineAddress
			m.InstructionCount = ps.InstructionCount
			m.ALUInstructionCount = ps.ALUInstructionCount
			m.FP32InstructionCount = ps.FP32InstructionCount
			m.FP16InstructionCount = ps.FP16InstructionCount
			m.INT32InstructionCount = ps.INT32InstructionCount
			m.INT16InstructionCount = ps.INT16InstructionCount
			m.BranchInstructionCount = ps.BranchInstructionCount
			m.ThreadgroupMemory = ps.ThreadgroupMemory
			m.AllocatedRegisters = ps.TemporaryRegisterCount
			m.SpilledBytes = ps.SpilledBytes
			m.DeviceLoadCount = ps.DeviceLoadCount
			m.DeviceStoreCount = ps.DeviceStoreCount
			m.HasPipelineStats = true
		}

		report.Shaders = append(report.Shaders, m)
	}

	// Sort by SIMD-based cost (highest first)
	sort.Slice(report.Shaders, func(i, j int) bool {
		if report.Shaders[i].PercentOfTotal != report.Shaders[j].PercentOfTotal {
			return report.Shaders[i].PercentOfTotal > report.Shaders[j].PercentOfTotal
		}
		return report.Shaders[i].Name < report.Shaders[j].Name
	})

	report.TotalShaders = len(report.Shaders)

	return report, nil
}

// findProfilerDir finds the .gpuprofiler_raw directory if it exists.
// Shader metrics come from streamData, so a directory without it is absent.
func findProfilerDir(tracePath string) string {
	return profilerraw.FindDirWithStreamData(tracePath)
}

// runShadersFromProfiler extracts shader info from .gpuprofiler_raw when unsorted-capture is missing.
// Note: This uses dispatch duration for Share %, not Xcode pipeline timing.
func runShadersFromProfiler(tracePath string, opts *shadersOptions) error {
	fmt.Fprintln(os.Stderr, "Note: Share is based on cumulative dispatch span for this profiler-only trace.")
	fmt.Fprintln(os.Stderr, "      Use --xcode-cost for Xcode's processed pipeline timing.")
	fmt.Fprintln(os.Stderr, "")
	profilerDir := profilerraw.FindDir(tracePath)

	if profilerDir == "" {
		fmt.Fprint(os.Stderr, profileReplayHint(tracePath))
		return fmt.Errorf("no .gpuprofiler_raw directory found in %s (and unsorted-capture is missing)", tracePath)
	}

	// Parse streamData for pipeline stats
	stats, err := counter.ParseStreamData(profilerDir, nil)
	if err != nil {
		return fmt.Errorf("parse streamData: %w", err)
	}

	// Convert PipelineStats to shader metrics format
	// Note: Uses dispatch duration for Cost %. Statistical sampling from Profiling_f_*.raw
	// has a complex format that needs further reverse engineering to match Xcode exactly.
	report := convertPipelineStatsToShaderReport(stats, nil)
	report.ShareBasis = "dispatch_span"
	if err := applySourceBackedShaderMetrics(filepath.Join(profilerDir, "streamData"), stats, report); err != nil {
		fmt.Fprintf(os.Stderr, "Note: source-backed high-register metrics unavailable: %v\n", err)
	}

	// Output based on format
	switch opts.format {
	case "csv":
		if err := gputrace.ExportShaderMetricsCSV(os.Stdout, report); err != nil {
			return fmt.Errorf("failed to export CSV: %w", err)
		}
	case "json":
		if err := gputrace.ExportShaderMetricsJSON(os.Stdout, report); err != nil {
			return fmt.Errorf("failed to export JSON: %w", err)
		}
	case "text":
		writeShaderShareBasis(opts.format, "dispatch cumulative-offset span")
		if opts.all {
			// Format as Xcode Instruments style output (no trace available)
			gputrace.FormatShadersXcodeStyle(os.Stdout, report, nil, opts.estimate)
		} else {
			gputrace.FormatShadersSimple(os.Stdout, report)
		}
	default:
		return invalidShadersFormatError(opts.format)
	}

	return nil
}

func writeShaderShareBasis(format, basis string) {
	if format == "text" {
		fmt.Fprintf(os.Stdout, "Share basis: %s\n", basis)
	}
}

// convertPipelineStatsToShaderReport converts PipelineStats from streamData to ShaderMetricsReport.
// If execCosts is provided, uses statistical sampling cost for PercentOfTotal.
// Otherwise falls back to dispatch duration-based cost.
func convertPipelineStatsToShaderReport(stats *counter.StreamDataStats, execCosts *counter.ExecutionCostMetrics) *gputrace.ShaderMetricsReport {
	report := &gputrace.ShaderMetricsReport{
		Shaders:          make([]*gputrace.ShaderMetrics, 0, len(stats.Pipelines)),
		TotalGPUTimeNs:   uint64(stats.TotalTimeUs) * 1000,
		TotalGPUTimeMs:   float64(stats.TotalTimeUs) / 1000.0,
		TotalInvocations: len(stats.Dispatches),
	}

	// Calculate total dispatch time for duration-based percentages (fallback)
	var totalDispatchTime int
	for _, d := range stats.Dispatches {
		totalDispatchTime += d.DurationUs
	}

	// Build per-function aggregates from dispatch data
	funcTotals := make(map[string]int)    // function name -> total duration µs
	funcCounts := make(map[string]int)    // function name -> invocation count
	funcPipeIDs := make(map[string][]int) // function name -> pipeline IDs
	for _, d := range stats.Dispatches {
		name := d.FunctionName
		if name == "" {
			name = fmt.Sprintf("(pipeline_%d)", d.PipelineIndex)
		}
		funcTotals[name] += d.DurationUs
		funcCounts[name]++
	}

	// Map function names to pipeline IDs for execution cost lookup
	for _, p := range stats.Pipelines {
		name := p.FunctionName
		if name == "" {
			continue
		}
		funcPipeIDs[name] = append(funcPipeIDs[name], p.PipelineID)
	}

	// Convert pipelines to shader metrics
	for _, p := range stats.Pipelines {
		name := p.FunctionName
		if name == "" {
			continue
		}

		m := &gputrace.ShaderMetrics{
			Name:                   name,
			Address:                p.PipelineAddress,
			InvocationCount:        funcCounts[name],
			TotalDurationNs:        uint64(funcTotals[name]) * 1000,
			AvgDurationNs:          0,
			InstructionCount:       p.InstructionCount,
			ALUInstructionCount:    p.ALUInstructionCount,
			FP32InstructionCount:   p.FP32InstructionCount,
			FP16InstructionCount:   p.FP16InstructionCount,
			INT32InstructionCount:  p.INT32InstructionCount,
			INT16InstructionCount:  p.INT16InstructionCount,
			BranchInstructionCount: p.BranchInstructionCount,
			ThreadgroupMemory:      p.ThreadgroupMemory,
			AllocatedRegisters:     p.TemporaryRegisterCount,
			SpilledBytes:           p.SpilledBytes,
			DeviceLoadCount:        p.DeviceLoadCount,
			DeviceStoreCount:       p.DeviceStoreCount,
			HasPipelineStats:       true,
			Bottlenecks:            make([]string, 0),
			OptimizationHints:      make([]string, 0),
		}

		if m.InvocationCount > 0 {
			m.AvgDurationNs = m.TotalDurationNs / uint64(m.InvocationCount)
		}

		// Use execution cost from statistical sampling if available.
		if execCosts != nil {
			// Sum cost across all pipeline IDs for this function
			var totalCost float64
			for _, pid := range funcPipeIDs[name] {
				totalCost += execCosts.PipelineCosts[pid]
			}
			m.PercentOfTotal = totalCost
		} else if totalDispatchTime > 0 {
			// Fallback to duration-based cost
			m.PercentOfTotal = float64(funcTotals[name]) / float64(totalDispatchTime) * 100.0
		}

		report.Shaders = append(report.Shaders, m)
	}

	// Sort by cost (highest first) like Xcode does
	sort.Slice(report.Shaders, func(i, j int) bool {
		if report.Shaders[i].PercentOfTotal != report.Shaders[j].PercentOfTotal {
			return report.Shaders[i].PercentOfTotal > report.Shaders[j].PercentOfTotal
		}
		return report.Shaders[i].Name < report.Shaders[j].Name
	})

	report.TotalShaders = len(report.Shaders)

	return report
}
