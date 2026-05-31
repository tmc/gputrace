package shader

import (
	"fmt"
	"sort"
	"time"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/timing"
	"github.com/tmc/gputrace/internal/trace"
)

// Type aliases
type (
	Trace                 = trace.Trace
	TimingMetrics         = timing.TimingMetrics
	KernelTiming          = timing.KernelTiming
	PerfCounterStats      = counter.PerfCounterStats
	ShaderHardwareMetrics = counter.ShaderHardwareMetrics
)

// CorrelatedShaderMetrics combines timing data with hardware performance metrics.
type CorrelatedShaderMetrics struct {
	ShaderName string `json:"shader_name"`

	// Timing data. TimingSource identifies whether duration fields came from
	// profiler streamData, a capture heuristic, or a synthetic estimate.
	ExecutionCount int           `json:"execution_count"`
	TotalDuration  time.Duration `json:"total_duration"`
	AvgDuration    time.Duration `json:"avg_duration"`
	MinDuration    time.Duration `json:"min_duration"`
	MaxDuration    time.Duration `json:"max_duration"`
	TimingSource   string        `json:"timing_source,omitempty"`
	TimingApprox   bool          `json:"timing_approximate,omitempty"`

	// Hardware Metrics (from .gpuprofiler_raw)
	ALUUtilization  float64 `json:"alu_utilization"`  // 0-100%
	KernelOccupancy float64 `json:"kernel_occupancy"` // 0-100%
	SIMDGroups      int     `json:"simd_groups"`
	AllocatedRegs   int     `json:"allocated_regs"`
	SpilledBytes    int     `json:"spilled_bytes"`
	MemoryBandwidth uint64  `json:"memory_bandwidth"`
	TotalCycles     uint64  `json:"total_cycles"`

	// Correlation Metadata
	CorrelationMethod     string  `json:"correlation_method"`     // "name", "execution-order", "timing-only"
	CorrelationConfidence float64 `json:"correlation_confidence"` // 0.0-1.0

	// Computed Metrics
	CyclesPerInvocation uint64  `json:"cycles_per_invocation"`
	EstimatedGPUFreqGHz float64 `json:"estimated_gpu_freq_ghz"`
}

// ShaderCorrelationReport contains all correlated shader metrics.
type ShaderCorrelationReport struct {
	Shaders           []*CorrelatedShaderMetrics `json:"shaders"`
	TotalShaders      int                        `json:"total_shaders"`
	CorrelatedShaders int                        `json:"correlated_shaders"`
	CorrelationRate   float64                    `json:"correlation_rate"` // Percentage

	// Summary Statistics
	AvgALUUtilization   float64 `json:"avg_alu_utilization"`
	AvgKernelOccupancy  float64 `json:"avg_kernel_occupancy"`
	TotalGPUCycles      uint64  `json:"total_gpu_cycles"`
	EstimatedGPUFreqGHz float64 `json:"estimated_gpu_freq_ghz"`

	// Data Sources
	TraceSource    string `json:"trace_source"`
	ProfilerSource string `json:"profiler_source"`
}

type correlationTiming struct {
	Name            string
	InvocationCount int
	TotalDuration   time.Duration
	AvgDuration     time.Duration
	MinDuration     time.Duration
	MaxDuration     time.Duration
	TimingSource    string
	TimingApprox    bool
}

// CorrelateShaderMetrics combines timing data from the trace with hardware
// metrics from the profiler data, matching shaders by name or execution order.
func CorrelateShaderMetrics(trace *Trace) (*ShaderCorrelationReport, error) {
	// Extract shader timings through ShaderMetrics so correlation inherits the
	// same timing-source and approximation model used by shader reports.
	shaderMetrics, err := ExtractShaderMetrics(trace)
	if err != nil {
		return nil, fmt.Errorf("extract shader metrics: %w", err)
	}
	timings := correlationTimingsFromShaderMetrics(shaderMetrics)

	// Parse performance counters from profiler data
	perfStats, err := counter.ParsePerfCounters(trace)
	if err != nil {
		// Profiler data not available - return timing-only report
		return createTimingOnlyReport(timings, trace.Path), nil
	}

	// Create correlation report
	report := &ShaderCorrelationReport{
		Shaders:        make([]*CorrelatedShaderMetrics, 0),
		TraceSource:    trace.Path,
		ProfilerSource: trace.Path + ".gpuprofiler_raw",
	}

	// Build maps for correlation
	timingMap := buildTimingMap(timings)
	hardwareMap := buildHardwareMap(perfStats)

	// Correlate by shader name (primary method)
	correlateByName(timingMap, hardwareMap, report)

	// Try to correlate remaining by execution order
	correlateByExecutionOrder(timingMap, hardwareMap, report)

	// Calculate summary statistics
	calculateCorrelationSummary(report)

	// Sort by total duration (descending)
	sort.Slice(report.Shaders, func(i, j int) bool {
		return report.Shaders[i].TotalDuration > report.Shaders[j].TotalDuration
	})

	return report, nil
}

// createTimingOnlyReport creates a report with timing data only (no hardware metrics).
func createTimingOnlyReport(timings []*correlationTiming, tracePath string) *ShaderCorrelationReport {
	report := &ShaderCorrelationReport{
		Shaders:        make([]*CorrelatedShaderMetrics, 0),
		TraceSource:    tracePath,
		ProfilerSource: "(not available)",
	}

	for _, timing := range timings {
		correlated := &CorrelatedShaderMetrics{
			ShaderName:            timing.Name,
			ExecutionCount:        timing.InvocationCount,
			TotalDuration:         timing.TotalDuration,
			AvgDuration:           timing.AvgDuration,
			MinDuration:           timing.MinDuration,
			MaxDuration:           timing.MaxDuration,
			TimingSource:          timing.TimingSource,
			TimingApprox:          timing.TimingApprox,
			CorrelationMethod:     "timing-only",
			CorrelationConfidence: 1.0,
		}
		report.Shaders = append(report.Shaders, correlated)
	}

	report.TotalShaders = len(report.Shaders)
	report.CorrelatedShaders = len(report.Shaders)
	report.CorrelationRate = 100.0

	return report
}

func correlationTimingsFromShaderMetrics(report *ShaderMetricsReport) []*correlationTiming {
	if report == nil {
		return nil
	}

	timings := make([]*correlationTiming, 0, len(report.Shaders))
	for _, metrics := range report.Shaders {
		timing := correlationTimingFromShaderMetrics(metrics)
		if timing == nil {
			continue
		}
		timings = append(timings, timing)
	}
	return timings
}

func correlationTimingFromShaderMetrics(metrics *ShaderMetrics) *correlationTiming {
	if metrics == nil || metrics.Name == "" || metrics.TimingSource == "" {
		return nil
	}

	return &correlationTiming{
		Name:            metrics.Name,
		InvocationCount: metrics.InvocationCount,
		TotalDuration:   time.Duration(metrics.TotalDurationNs),
		AvgDuration:     time.Duration(metrics.AvgDurationNs),
		MinDuration:     time.Duration(metrics.MinDurationNs),
		MaxDuration:     time.Duration(metrics.MaxDurationNs),
		TimingSource:    metrics.TimingSource,
		TimingApprox:    metrics.TimingApprox,
	}
}

// buildTimingMap creates a map of shader name -> timing data.
func buildTimingMap(timings []*correlationTiming) map[string]*correlationTiming {
	timingMap := make(map[string]*correlationTiming)
	for _, timing := range timings {
		timingMap[timing.Name] = timing
	}
	return timingMap
}

// buildHardwareMap creates a map of shader name -> hardware metrics.
func buildHardwareMap(stats *PerfCounterStats) map[string]*ShaderHardwareMetrics {
	hardwareMap := make(map[string]*ShaderHardwareMetrics)
	for i := range stats.ShaderMetrics {
		metric := &stats.ShaderMetrics[i]
		hardwareMap[metric.ShaderName] = metric
	}
	return hardwareMap
}

// correlateByName matches shaders by exact name match.
func correlateByName(timingMap map[string]*correlationTiming, hardwareMap map[string]*ShaderHardwareMetrics, report *ShaderCorrelationReport) {
	correlated := make(map[string]bool)

	for name, timing := range timingMap {
		if hardware, ok := hardwareMap[name]; ok {
			correlated[name] = true

			merged := mergeTimingAndHardware(timing, hardware, name, "name", 1.0)

			report.Shaders = append(report.Shaders, merged)
		}
	}

	report.CorrelatedShaders = len(report.Shaders)
}

// correlateByExecutionOrder attempts to correlate remaining shaders by execution order.
// This is a fallback when shader names don't match (e.g., UUIDs vs readable names).
func correlateByExecutionOrder(timingMap map[string]*correlationTiming, hardwareMap map[string]*ShaderHardwareMetrics, report *ShaderCorrelationReport) {
	// Get lists of uncorrelated shaders
	correlatedNames := make(map[string]bool)
	for _, shader := range report.Shaders {
		correlatedNames[shader.ShaderName] = true
	}

	var uncorrelatedTiming []*correlationTiming
	for name, timing := range timingMap {
		if !correlatedNames[name] {
			uncorrelatedTiming = append(uncorrelatedTiming, timing)
		}
	}

	var uncorrelatedHardware []*ShaderHardwareMetrics
	for name, hardware := range hardwareMap {
		if !correlatedNames[name] {
			uncorrelatedHardware = append(uncorrelatedHardware, hardware)
		}
	}

	// Sort both by execution order (assuming they're in roughly the same order)
	sort.Slice(uncorrelatedTiming, func(i, j int) bool {
		return uncorrelatedTiming[i].Name < uncorrelatedTiming[j].Name
	})
	sort.Slice(uncorrelatedHardware, func(i, j int) bool {
		return uncorrelatedHardware[i].ShaderName < uncorrelatedHardware[j].ShaderName
	})

	// Match by position with lower confidence
	minLen := len(uncorrelatedTiming)
	if len(uncorrelatedHardware) < minLen {
		minLen = len(uncorrelatedHardware)
	}

	for i := 0; i < minLen; i++ {
		timing := uncorrelatedTiming[i]
		hardware := uncorrelatedHardware[i]

		merged := mergeTimingAndHardware(timing, hardware, timing.Name+" → "+hardware.ShaderName, "execution-order", 0.7)

		report.Shaders = append(report.Shaders, merged)
	}

	report.CorrelatedShaders = len(report.Shaders)
}

func mergeTimingAndHardware(timing *correlationTiming, hardware *ShaderHardwareMetrics, shaderName, method string, confidence float64) *CorrelatedShaderMetrics {
	merged := &CorrelatedShaderMetrics{
		ShaderName:            shaderName,
		ExecutionCount:        timing.InvocationCount,
		TotalDuration:         timing.TotalDuration,
		AvgDuration:           timing.AvgDuration,
		MinDuration:           timing.MinDuration,
		MaxDuration:           timing.MaxDuration,
		TimingSource:          timing.TimingSource,
		TimingApprox:          timing.TimingApprox,
		ALUUtilization:        hardware.ALUUtilization,
		KernelOccupancy:       hardware.KernelOccupancy,
		SIMDGroups:            hardware.SIMDGroups,
		AllocatedRegs:         hardware.AllocatedRegs,
		SpilledBytes:          hardware.SpilledBytes,
		MemoryBandwidth:       hardware.MemoryBandwidth,
		TotalCycles:           hardware.TotalCycles,
		CorrelationMethod:     method,
		CorrelationConfidence: confidence,
	}

	if merged.ExecutionCount > 0 {
		merged.CyclesPerInvocation = merged.TotalCycles / uint64(merged.ExecutionCount)
	}

	if !timing.TimingApprox && merged.ExecutionCount > 0 && timing.AvgDuration.Nanoseconds() > 0 {
		avgCycles := float64(merged.TotalCycles) / float64(merged.ExecutionCount)
		avgSeconds := timing.AvgDuration.Seconds()
		merged.EstimatedGPUFreqGHz = avgCycles / avgSeconds / 1e9
	}

	return merged
}

// calculateCorrelationSummary computes summary statistics for the correlation report.
func calculateCorrelationSummary(report *ShaderCorrelationReport) {
	if len(report.Shaders) == 0 {
		return
	}

	totalALU := 0.0
	totalOccupancy := 0.0
	totalCycles := uint64(0)
	totalFreq := 0.0
	countWithMetrics := 0

	for _, shader := range report.Shaders {
		if shader.ALUUtilization > 0 {
			totalALU += shader.ALUUtilization
			totalOccupancy += shader.KernelOccupancy
			totalCycles += shader.TotalCycles
			countWithMetrics++

			if shader.EstimatedGPUFreqGHz > 0 {
				totalFreq += shader.EstimatedGPUFreqGHz
			}
		}
	}

	if countWithMetrics > 0 {
		report.AvgALUUtilization = totalALU / float64(countWithMetrics)
		report.AvgKernelOccupancy = totalOccupancy / float64(countWithMetrics)
		report.EstimatedGPUFreqGHz = totalFreq / float64(countWithMetrics)
	}

	report.TotalGPUCycles = totalCycles
	report.TotalShaders = len(report.Shaders)

	if report.TotalShaders > 0 {
		report.CorrelationRate = float64(report.CorrelatedShaders) / float64(report.TotalShaders) * 100.0
	}
}

// FormatCorrelationReport generates a human-readable report of correlated shader metrics.
func FormatCorrelationReport(report *ShaderCorrelationReport) string {
	output := "=== Shader Correlation Report ===\n\n"
	output += fmt.Sprintf("Trace: %s\n", report.TraceSource)
	output += fmt.Sprintf("Profiler: %s\n", report.ProfilerSource)
	output += fmt.Sprintf("Correlated Shaders: %d/%d (%.1f%%)\n\n",
		report.CorrelatedShaders, report.TotalShaders, report.CorrelationRate)
	if len(report.Shaders) > 0 {
		output += fmt.Sprintf("Timing Sources: %s\n", formatCorrelationTimingSources(report.Shaders))
		if hasApproximateCorrelationTiming(report.Shaders) {
			output += "Timing Note: heuristic and synthetic timing is approximate; duration-derived frequency is omitted for those rows.\n"
		}
		output += "\n"
	}

	if report.AvgALUUtilization > 0 {
		output += "=== Summary Statistics ===\n"
		output += fmt.Sprintf("Average ALU Utilization: %.1f%%\n", report.AvgALUUtilization)
		output += fmt.Sprintf("Average Kernel Occupancy: %.1f%%\n", report.AvgKernelOccupancy)
		output += fmt.Sprintf("Total GPU Cycles: %d\n", report.TotalGPUCycles)
		output += fmt.Sprintf("Estimated GPU Frequency: %.2f GHz\n\n", report.EstimatedGPUFreqGHz)
	}

	output += "=== Per-Shader Metrics ===\n\n"
	output += fmt.Sprintf("%-40s %10s %10s %8s %8s %10s\n",
		"Shader", "Count", "Avg(µs)", "ALU%", "Occ%", "Method")
	output += repeatChar('-', 95) + "\n"

	for _, shader := range report.Shaders {
		avgUs := shader.AvgDuration.Microseconds()
		output += fmt.Sprintf("%-40s %10d %10d %7.1f%% %7.1f%% %10s\n",
			truncateString(shader.ShaderName, 40),
			shader.ExecutionCount,
			avgUs,
			shader.ALUUtilization,
			shader.KernelOccupancy,
			shader.CorrelationMethod)
	}

	return output
}

// Helper functions

func formatCorrelationTimingSources(shaders []*CorrelatedShaderMetrics) string {
	seen := make(map[string]bool)
	sources := make([]string, 0)
	for _, shader := range shaders {
		if shader.TimingSource == "" || seen[shader.TimingSource] {
			continue
		}
		seen[shader.TimingSource] = true
		source := shader.TimingSource
		if shader.TimingApprox {
			source += " (approximate)"
		}
		sources = append(sources, source)
	}
	if len(sources) == 0 {
		return "(not available)"
	}
	sort.Strings(sources)
	return joinStrings(sources, ", ")
}

func hasApproximateCorrelationTiming(shaders []*CorrelatedShaderMetrics) bool {
	for _, shader := range shaders {
		if shader.TimingApprox {
			return true
		}
	}
	return false
}

func joinStrings(values []string, sep string) string {
	if len(values) == 0 {
		return ""
	}
	out := values[0]
	for _, value := range values[1:] {
		out += sep + value
	}
	return out
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func repeatChar(c byte, n int) string {
	result := make([]byte, n)
	for i := 0; i < n; i++ {
		result[i] = c
	}
	return string(result)
}
