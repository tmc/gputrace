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
)

var shadersCmd = newShadersCommand(&shadersOptions{
	format: "text",
})

type shadersOptions struct {
	verbose  bool
	estimate bool
	format   string
	all      bool
}

func newShadersCommand(opts *shadersOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shaders <trace.gputrace>",
		Short: "Show shader performance statistics",
		Long: `Display shader/kernel performance statistics.

By default shows a simple two-column output:
  - Cost % (percentage of total GPU time)
  - Shader name

Use --all for full Xcode Instruments format with additional columns:
  - Type (Compute)
  - Pipeline State address
  - # SIMD Groups (SIMD wavefronts dispatched)
  - # Allocated Registers
  - High Register, shown only when source-backed
  - Spilled Bytes (register spills to memory)

Examples:
  gputrace shaders trace.gputrace                    # Simple cost + name output
  gputrace shaders trace.gputrace --all              # Full Xcode format
  gputrace shaders trace.gputrace --estimate         # Show estimates for unknown fields
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

	if hasUnsortedCapture {
		// Full trace with profiler: use SIMD-based cost (matches Xcode)
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
	fmt.Fprintf(os.Stderr, "No profiler data. To get Cost %%, run:\n")
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
	fmt.Fprintf(w, "Cost      Name\n")
	for _, shader := range report.Shaders {
		fmt.Fprintf(w, "    ?     %s\n", shader.Name)
	}
	return nil
}

// runShadersFromFullTrace uses full trace parsing for SIMD-based cost calculation.
// This matches Xcode's Cost % = SIMD Groups / Total SIMD Groups × 100
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

	// Recalculate Cost % based on SIMD Groups (TotalThreadgroups) to match Xcode
	var totalSIMDGroups uint64
	for _, shader := range report.Shaders {
		totalSIMDGroups += shader.TotalThreadgroups
	}

	if totalSIMDGroups > 0 {
		for _, shader := range report.Shaders {
			shader.PercentOfTotal = float64(shader.TotalThreadgroups) / float64(totalSIMDGroups) * 100.0
		}
	}

	// Re-sort by SIMD-based cost
	sort.Slice(report.Shaders, func(i, j int) bool {
		return report.Shaders[i].PercentOfTotal > report.Shaders[j].PercentOfTotal
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
	dispatches, err := trace.ParseDispatchInRegion(trace.CaptureData, 0)
	if err != nil {
		return nil, fmt.Errorf("parse dispatch markers: %w", err)
	}

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

	const simdWidth uint64 = 32 // Apple Silicon SIMD width is 32 threads

	for i, dispatch := range dispatches {
		// Calculate threadgroups for this dispatch
		var tgX, tgY, tgZ uint64 = 1, 1, 1
		if dispatch.ThreadsPerGroupX > 0 {
			tgX = (dispatch.ThreadsX + dispatch.ThreadsPerGroupX - 1) / dispatch.ThreadsPerGroupX
		}
		if dispatch.ThreadsPerGroupY > 0 {
			tgY = (dispatch.ThreadsY + dispatch.ThreadsPerGroupY - 1) / dispatch.ThreadsPerGroupY
		}
		if dispatch.ThreadsPerGroupZ > 0 {
			tgZ = (dispatch.ThreadsZ + dispatch.ThreadsPerGroupZ - 1) / dispatch.ThreadsPerGroupZ
		}
		threadgroups := tgX * tgY * tgZ

		// Calculate SIMD groups (wavefronts)
		// Xcode's "# SIMD Groups" = Total Threads / SIMD Width (32)
		threadsPerGroup := dispatch.ThreadsPerGroupX * dispatch.ThreadsPerGroupY * dispatch.ThreadsPerGroupZ
		totalThreads := threadgroups * threadsPerGroup
		simdGroups := (totalThreads + simdWidth - 1) / simdWidth // Round up

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

		// Calculate SIMD-based cost percentage (matches Xcode)
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
		}

		report.Shaders = append(report.Shaders, m)
	}

	// Sort by SIMD-based cost (highest first)
	sort.Slice(report.Shaders, func(i, j int) bool {
		return report.Shaders[i].PercentOfTotal > report.Shaders[j].PercentOfTotal
	})

	report.TotalShaders = len(report.Shaders)

	return report, nil
}

// findProfilerDir finds the .gpuprofiler_raw directory if it exists.
func findProfilerDir(tracePath string) string {
	// Check if it's directly a .gpuprofiler_raw directory
	if filepath.Ext(tracePath) == ".gpuprofiler_raw" {
		if _, err := os.Stat(filepath.Join(tracePath, "streamData")); err == nil {
			return tracePath
		}
	}
	// Look inside for .gpuprofiler_raw
	entries, err := os.ReadDir(tracePath)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && filepath.Ext(e.Name()) == ".gpuprofiler_raw" {
			dir := filepath.Join(tracePath, e.Name())
			if _, err := os.Stat(filepath.Join(dir, "streamData")); err == nil {
				return dir
			}
		}
	}
	return ""
}

// runShadersFromProfiler extracts shader info from .gpuprofiler_raw when unsorted-capture is missing.
// Note: This uses dispatch duration for Cost %, NOT SIMD groups (Xcode uses SIMD groups).
// For Xcode-matching Cost %, use a full trace with unsorted-capture directory.
func runShadersFromProfiler(tracePath string, opts *shadersOptions) error {
	fmt.Fprintln(os.Stderr, "Note: Using dispatch duration for Cost % (profiler-only trace).")
	fmt.Fprintln(os.Stderr, "      Xcode uses SIMD Groups for Cost %. For matching values, use a full trace.")
	fmt.Fprintln(os.Stderr, "")
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
		fmt.Fprintf(os.Stderr, "Hint: To generate performance data, run:\n")
		fmt.Fprintf(os.Stderr, "  gputrace xcode-profile run %s\n\n", tracePath)
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

// convertPipelineStatsToShaderReport converts PipelineStats from streamData to ShaderMetricsReport.
// If execCosts is provided, uses statistical sampling cost for PercentOfTotal (matches Xcode).
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
			Bottlenecks:            make([]string, 0),
			OptimizationHints:      make([]string, 0),
		}

		if m.InvocationCount > 0 {
			m.AvgDurationNs = m.TotalDurationNs / uint64(m.InvocationCount)
		}

		// Use execution cost from statistical sampling if available (matches Xcode)
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
		return report.Shaders[i].PercentOfTotal > report.Shaders[j].PercentOfTotal
	})

	report.TotalShaders = len(report.Shaders)

	return report
}
