package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/gputrace/internal/shader"
	"github.com/tmc/gputrace/internal/trace"
)

// Type aliases
type (
	ShaderMetrics       = shader.ShaderMetrics
	ShaderMetricsReport = shader.ShaderMetricsReport
)

// Function aliases
var ExtractShaderMetrics = shader.ExtractShaderMetrics

// InsightType represents the type of performance insight.
type InsightType string

const (
	InsightBottleneck   InsightType = "BOTTLENECK"
	InsightOptimization InsightType = "OPTIMIZATION"
	InsightAntiPattern  InsightType = "ANTI-PATTERN"
	InsightInfo         InsightType = "INFO"
)

// InsightSeverity represents how critical an insight is.
type InsightSeverity string

const (
	SeverityCritical InsightSeverity = "CRITICAL"
	SeverityHigh     InsightSeverity = "HIGH"
	SeverityMedium   InsightSeverity = "MEDIUM"
	SeverityLow      InsightSeverity = "LOW"
	SeverityInfo     InsightSeverity = "INFO"
)

// PerformanceInsight represents a single actionable performance insight.
type PerformanceInsight struct {
	Type            InsightType            `json:"type"`
	Severity        InsightSeverity        `json:"severity"`
	ShaderName      string                 `json:"shader_name,omitempty"`
	TimingSource    string                 `json:"timing_source,omitempty"`
	TimingApprox    bool                   `json:"timing_approximate,omitempty"`
	Title           string                 `json:"title"`
	Description     string                 `json:"description"`
	Metrics         map[string]interface{} `json:"metrics,omitempty"`
	Recommendations []string               `json:"recommendations"`
	Impact          string                 `json:"impact,omitempty"`
}

// InsightsReport contains all performance insights from a trace.
type InsightsReport struct {
	Insights       []*PerformanceInsight `json:"insights"`
	CriticalCount  int                   `json:"critical_count"`
	HighCount      int                   `json:"high_count"`
	MediumCount    int                   `json:"medium_count"`
	LowCount       int                   `json:"low_count"`
	TotalGPUTimeMs float64               `json:"total_gpu_time_ms"`
	TimingSources  []string              `json:"timing_sources,omitempty"`
	TimingApprox   bool                  `json:"timing_approximate,omitempty"`
	TopBottlenecks []string              `json:"top_bottlenecks"`
}

// GenerateInsights analyzes trace data and generates actionable performance insights.
func GenerateInsights(t *trace.Trace) (*InsightsReport, error) {
	report := &InsightsReport{
		Insights: make([]*PerformanceInsight, 0),
	}

	// Extract shader metrics
	shaderMetrics, err := ExtractShaderMetrics(t)
	if err != nil {
		return nil, fmt.Errorf("extract shader metrics: %w", err)
	}

	report.TotalGPUTimeMs = shaderMetrics.TotalGPUTimeMs
	report.TimingSources, report.TimingApprox = insightTimingSources(shaderMetrics.Shaders)

	// Analyze each shader for insights
	for _, shader := range shaderMetrics.Shaders {
		// Bottleneck detection
		detectBottlenecks(shader, report)

		// Optimization opportunities
		detectOptimizations(t, shader, report)

		// Anti-pattern detection
		detectAntiPatterns(t, shader, report)
	}

	// Overall analysis
	detectOverallPatterns(t, shaderMetrics, report)

	// API usage insights (redundant bindings, etc.)
	detectRedundantBindings(t, report)

	// Calculate severity counts
	for _, insight := range report.Insights {
		switch insight.Severity {
		case SeverityCritical:
			report.CriticalCount++
		case SeverityHigh:
			report.HighCount++
		case SeverityMedium:
			report.MediumCount++
		case SeverityLow:
			report.LowCount++
		}
	}

	// Sort insights by severity
	sort.Slice(report.Insights, func(i, j int) bool {
		severityOrder := map[InsightSeverity]int{
			SeverityCritical: 0,
			SeverityHigh:     1,
			SeverityMedium:   2,
			SeverityLow:      3,
			SeverityInfo:     4,
		}
		return severityOrder[report.Insights[i].Severity] < severityOrder[report.Insights[j].Severity]
	})

	return report, nil
}

// detectBottlenecks identifies memory-bound vs compute-bound shaders.
func detectBottlenecks(shader *ShaderMetrics, report *InsightsReport) {
	if shader.TimingApprox {
		return
	}

	// A large attributed share identifies a place to investigate. It does not
	// establish where boundary or gap time belongs.
	if shader.PercentOfTotal > 20.0 {
		insight := &PerformanceInsight{
			Type:         InsightBottleneck,
			ShaderName:   shader.Name,
			TimingSource: shader.TimingSource,
			TimingApprox: shader.TimingApprox,
			Title:        fmt.Sprintf("%s is a major attributed-span contributor", shader.Name),
			Description: fmt.Sprintf("This shader accounts for %.1f%% of attributed dispatch span (%.2f ms). Cumulative-offset timing can include boundary or gap time.",
				shader.PercentOfTotal, float64(shader.TotalDurationNs)/1e6),
			Metrics: map[string]interface{}{
				"percent_of_total": shader.PercentOfTotal,
				"duration_ms":      float64(shader.TotalDurationNs) / 1e6,
				"invocations":      shader.InvocationCount,
			},
		}

		// Determine severity based on percentage
		if shader.PercentOfTotal > 50.0 {
			insight.Severity = SeverityCritical
			insight.Impact = "Highest-priority attribution hypothesis"
		} else if shader.PercentOfTotal > 30.0 {
			insight.Severity = SeverityHigh
			insight.Impact = "High-priority attribution hypothesis"
		} else {
			insight.Severity = SeverityMedium
			insight.Impact = "Attribution hypothesis worth investigating"
		}

		// Generate recommendations
		insight.Recommendations = []string{
			"Profile this shader in detail to identify hotspots",
			"Consider algorithmic optimizations or alternative approaches",
			"Evaluate if work can be distributed across multiple passes",
		}

		report.Insights = append(report.Insights, insight)
		report.TopBottlenecks = append(report.TopBottlenecks, shader.Name)
	}
}

// detectOptimizations identifies optimization opportunities.
func detectOptimizations(t *trace.Trace, shader *ShaderMetrics, report *InsightsReport) {
	// Low occupancy detection
	threadsPerGroup := shader.ThreadsPerGroupX * shader.ThreadsPerGroupY * shader.ThreadsPerGroupZ

	// Typical Metal GPU has 1024 threads per SIMD group max
	const maxThreadsPerGroup = 1024
	occupancy := float64(threadsPerGroup) / float64(maxThreadsPerGroup)

	if !shader.TimingApprox && threadsPerGroup > 0 && occupancy < 0.5 && shader.PercentOfTotal > 5.0 {
		insight := &PerformanceInsight{
			Type:         InsightOptimization,
			Severity:     SeverityMedium,
			ShaderName:   shader.Name,
			TimingSource: shader.TimingSource,
			TimingApprox: shader.TimingApprox,
			Title:        fmt.Sprintf("%s has suboptimal occupancy", shader.Name),
			Description: fmt.Sprintf("Threadgroup size is %d threads (%.0f%% occupancy). Low occupancy can limit GPU utilization.",
				threadsPerGroup, occupancy*100),
			Metrics: map[string]interface{}{
				"threads_per_group": threadsPerGroup,
				"occupancy_percent": occupancy * 100,
			},
			Recommendations: []string{
				fmt.Sprintf("Increase threadgroup size closer to %d threads", maxThreadsPerGroup),
				"Consider 2D threadgroup configuration for better occupancy",
				"Balance between occupancy and shared memory usage",
			},
			Impact: "Potential for improved GPU utilization",
		}
		report.Insights = append(report.Insights, insight)
	}

	// Many small invocations detection
	if !shader.TimingApprox && shader.InvocationCount > 100 && shader.PercentOfTotal > 5.0 {
		avgDurationUs := float64(shader.AvgDurationNs) / 1000.0
		if avgDurationUs < 50.0 { // Less than 50 microseconds per call
			insight := &PerformanceInsight{
				Type:         InsightOptimization,
				Severity:     SeverityHigh,
				ShaderName:   shader.Name,
				TimingSource: shader.TimingSource,
				TimingApprox: shader.TimingApprox,
				Title:        fmt.Sprintf("%s has excessive dispatch overhead", shader.Name),
				Description: fmt.Sprintf("Dispatched %d times with average duration %.1f μs. CPU dispatch overhead may be significant.",
					shader.InvocationCount, avgDurationUs),
				Metrics: map[string]interface{}{
					"invocations":     shader.InvocationCount,
					"avg_duration_us": avgDurationUs,
				},
				Recommendations: []string{
					"Batch multiple small dispatches into larger operations",
					"Consider kernel fusion to combine multiple passes",
					"Use persistent threadgroups pattern to reduce dispatch overhead",
				},
				Impact: "Could significantly reduce CPU-GPU synchronization overhead",
			}
			report.Insights = append(report.Insights, insight)
		}
	}

	// Large threadgroup count with low thread count (work imbalance)
	if shader.TotalThreadgroups > 1000 && threadsPerGroup > 0 && threadsPerGroup < 64 {
		insight := &PerformanceInsight{
			Type:       InsightOptimization,
			Severity:   SeverityMedium,
			ShaderName: shader.Name,
			Title:      fmt.Sprintf("%s may have work distribution imbalance", shader.Name),
			Description: fmt.Sprintf("Launching %d threadgroups with only %d threads each. Consider larger threadgroups with more work per group.",
				shader.TotalThreadgroups, threadsPerGroup),
			Metrics: map[string]interface{}{
				"total_threadgroups": shader.TotalThreadgroups,
				"threads_per_group":  threadsPerGroup,
			},
			Recommendations: []string{
				"Increase work per threadgroup to reduce scheduling overhead",
				"Consider tiling strategy with larger threadgroup sizes",
				"Profile scheduler overhead impact",
			},
			Impact: "Could reduce GPU scheduler overhead",
		}
		report.Insights = append(report.Insights, insight)
	}
}

// detectAntiPatterns identifies common performance anti-patterns.
func detectAntiPatterns(t *trace.Trace, shader *ShaderMetrics, report *InsightsReport) {
	// Unbalanced threadgroups (not using all dimensions effectively)
	threadsX := shader.ThreadsPerGroupX
	threadsY := shader.ThreadsPerGroupY
	threadsZ := shader.ThreadsPerGroupZ

	if (threadsX == 1 && (threadsY > 1 || threadsZ > 1)) ||
		(threadsY == 1 && (threadsX > 1 || threadsZ > 1)) {
		insight := &PerformanceInsight{
			Type:       InsightAntiPattern,
			Severity:   SeverityLow,
			ShaderName: shader.Name,
			Title:      fmt.Sprintf("%s has unusual threadgroup configuration", shader.Name),
			Description: fmt.Sprintf("Threadgroup dimensions: %d x %d x %d. Consider more balanced configurations.",
				threadsX, threadsY, threadsZ),
			Metrics: map[string]interface{}{
				"threads_x": threadsX,
				"threads_y": threadsY,
				"threads_z": threadsZ,
			},
			Recommendations: []string{
				"Use balanced threadgroup dimensions (e.g., 32x32, 16x16x4)",
				"Align threadgroup size with memory access patterns",
			},
			Impact: "May cause suboptimal SIMD lane utilization",
		}
		report.Insights = append(report.Insights, insight)
	}

	// High variability is a triage signal. Dispatch durations recovered from
	// streamData are cumulative offsets, so boundary or gap time may be charged
	// to the following dispatch.
	if !shader.TimingApprox && shader.InvocationCount > 1 {
		minMs := float64(shader.MinDurationNs) / 1e6
		maxMs := float64(shader.MaxDurationNs) / 1e6
		avgMs := float64(shader.AvgDurationNs) / 1e6

		if minMs > 0 && maxMs > minMs*3 { // Max is more than 3x min
			variability := ((maxMs - minMs) / avgMs) * 100
			insight := &PerformanceInsight{
				Type:         InsightAntiPattern,
				Severity:     SeverityMedium,
				ShaderName:   shader.Name,
				TimingSource: shader.TimingSource,
				TimingApprox: shader.TimingApprox,
				Title:        fmt.Sprintf("%s has high observed timing variability", shader.Name),
				Description: fmt.Sprintf("Observed duration varies from %.2f ms to %.2f ms (%.0f%% variability). Cumulative-offset timing can include boundary or gap time, so this does not by itself establish branch divergence or synchronization overhead.",
					minMs, maxMs, variability),
				Metrics: map[string]interface{}{
					"min_ms":      minMs,
					"max_ms":      maxMs,
					"avg_ms":      avgMs,
					"variability": variability,
				},
				Recommendations: []string{
					"Inspect neighboring dispatches and command-buffer boundaries",
					"Corroborate with source-backed counters before attributing the variation",
					"Repeat the capture to distinguish stable workload variation from a boundary artifact",
				},
				Impact: "Triage signal; the cause is not established",
			}
			report.Insights = append(report.Insights, insight)
		}
	}
}

// detectRedundantBindings detects redundant buffer binding calls.
// A redundant binding occurs when the same buffer index is bound multiple times
// before a dispatch, meaning the earlier binding(s) are wasted.
func detectRedundantBindings(t *trace.Trace, report *InsightsReport) {
	// Try to parse API call list - this requires unsorted-capture
	apiList, err := t.ParseAPICallList()
	if err != nil {
		// No API call data available (profiler-only trace)
		return
	}

	totalRedundant := 0
	redundantByEncoder := make(map[string]int)

	// Process each command buffer
	for _, cb := range apiList.CommandBuffers {
		// Track current bindings within an encoder: index -> (bufferAddr, callNum)
		currentBindings := make(map[int]struct {
			bufferAddr uint64
			callNum    int
		})
		currentEncoderLabel := ""

		for _, call := range cb.Calls {
			switch call.Type {
			case "encoder":
				// New encoder - reset tracking
				currentBindings = make(map[int]struct {
					bufferAddr uint64
					callNum    int
				})
				currentEncoderLabel = call.Label
				if currentEncoderLabel == "" {
					currentEncoderLabel = fmt.Sprintf("0x%x", call.Address)
				}

			case "setBuffer":
				// Parse buffer address and index from Details
				// Format: "setBuffer:0x... offset:0 atIndex:N"
				var bufAddr uint64
				var offset int
				var index int
				n, _ := fmt.Sscanf(call.Details, "setBuffer:0x%x offset:%d atIndex:%d", &bufAddr, &offset, &index)
				if n < 3 {
					continue
				}

				// Check if this index was already bound
				if prev, exists := currentBindings[index]; exists {
					// This is a redundant binding - the previous binding at this index is wasted
					totalRedundant++
					if currentEncoderLabel != "" {
						redundantByEncoder[currentEncoderLabel]++
					}
					_ = prev // previous binding was wasted
				}

				// Update current binding for this index
				currentBindings[index] = struct {
					bufferAddr uint64
					callNum    int
				}{bufAddr, call.CallNumber}

			case "dispatch":
				// Dispatch clears the "pending" bindings - they've been used
				// Reset tracking for next round of bindings
				currentBindings = make(map[int]struct {
					bufferAddr uint64
					callNum    int
				})

			case "endEncoding":
				// End of encoder - reset for next encoder
				currentBindings = make(map[int]struct {
					bufferAddr uint64
					callNum    int
				})
			}
		}
	}

	// Generate insight if redundant bindings found
	if totalRedundant > 0 {
		insight := &PerformanceInsight{
			Type:     InsightAntiPattern,
			Severity: SeverityMedium,
			Title:    fmt.Sprintf("Redundant Binding x %d", totalRedundant),
			Description: fmt.Sprintf("Found %d redundant buffer binding calls. "+
				"A buffer index was bound multiple times before dispatch, wasting the earlier binding(s).",
				totalRedundant),
			Metrics: map[string]interface{}{
				"total_redundant": totalRedundant,
			},
			Recommendations: []string{
				"Review buffer binding logic to avoid binding the same index twice",
				"Consider caching binding state to skip redundant setBuffer calls",
				"Check if conditional binding logic could be simplified",
			},
			Impact: "Reduces API call overhead and improves CPU efficiency",
		}

		// Add per-encoder breakdown if multiple encoders affected
		if len(redundantByEncoder) > 1 {
			insight.Metrics["by_encoder"] = redundantByEncoder
		}

		report.Insights = append(report.Insights, insight)
	}
}

// detectOverallPatterns identifies patterns across all shaders.
func detectOverallPatterns(t *trace.Trace, metrics *ShaderMetricsReport, report *InsightsReport) {
	// Too many unique shaders (might indicate poor kernel reuse)
	if metrics.TotalShaders > 50 {
		insight := &PerformanceInsight{
			Type:     InsightInfo,
			Severity: SeverityLow,
			Title:    "High number of unique shaders",
			Description: fmt.Sprintf("Trace contains %d unique shaders. This may indicate limited kernel reuse opportunities.",
				metrics.TotalShaders),
			Metrics: map[string]interface{}{
				"total_shaders": metrics.TotalShaders,
			},
			Recommendations: []string{
				"Review shader generation to identify consolidation opportunities",
				"Consider template-based kernel generation with specialization",
			},
			Impact: "May increase compilation time and memory overhead",
		}
		report.Insights = append(report.Insights, insight)
	}

	// Check for highly concentrated GPU time (one shader dominates)
	if len(metrics.Shaders) > 0 && !metrics.Shaders[0].TimingApprox && metrics.Shaders[0].PercentOfTotal > 70.0 {
		insight := &PerformanceInsight{
			Type:     InsightInfo,
			Severity: SeverityInfo,
			Title:    "GPU time highly concentrated in one shader",
			Description: fmt.Sprintf("%s consumes %.1f%% of GPU time. Optimization efforts should focus here.",
				metrics.Shaders[0].Name, metrics.Shaders[0].PercentOfTotal),
			Metrics: map[string]interface{}{
				"dominant_shader": metrics.Shaders[0].Name,
				"percent":         metrics.Shaders[0].PercentOfTotal,
			},
			Recommendations: []string{
				"Focus optimization efforts on this single shader",
				"Consider algorithmic improvements rather than micro-optimizations",
			},
			Impact: "Optimization focus is clear",
		}
		report.Insights = append(report.Insights, insight)
	}
}

// FormatInsightsReport generates a human-readable insights report.
func FormatInsightsReport(report *InsightsReport) string {
	var sb strings.Builder

	sb.WriteString("=== GPU Performance Insights ===\n\n")
	timeLabel := "Total GPU Time"
	if report.TimingApprox {
		timeLabel = "Estimated GPU Time"
	}
	sb.WriteString(fmt.Sprintf("%s: %.2f ms\n", timeLabel, report.TotalGPUTimeMs))
	if len(report.TimingSources) > 0 {
		kind := "measured"
		if report.TimingApprox {
			kind = "approximate"
		}
		sb.WriteString(fmt.Sprintf("Timing Source: %s (%s)\n", strings.Join(report.TimingSources, "; "), kind))
		for _, source := range report.TimingSources {
			if strings.Contains(source, "gpuCommandInfoData") {
				sb.WriteString("Attribution Note: per-dispatch values are cumulative-offset deltas and may include boundary or gap time.\n")
				break
			}
		}
	}
	sb.WriteString(fmt.Sprintf("Insights Found: %d\n", len(report.Insights)))
	sb.WriteString(fmt.Sprintf("  Critical: %d, High: %d, Medium: %d, Low: %d\n\n",
		report.CriticalCount, report.HighCount, report.MediumCount, report.LowCount))

	attributionLimited := false
	for _, source := range report.TimingSources {
		if strings.Contains(source, "gpuCommandInfoData") {
			attributionLimited = true
			break
		}
	}
	if len(report.TopBottlenecks) > 0 {
		if attributionLimited {
			sb.WriteString("Top Attributed-Span Contributors:\n")
		} else {
			sb.WriteString("Top Bottlenecks:\n")
		}
		for i, name := range report.TopBottlenecks {
			if i >= 5 {
				break
			}
			sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, name))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("=== Detailed Insights ===\n\n")

	for i, insight := range report.Insights {
		// Severity icon
		icon := ""
		switch insight.Severity {
		case SeverityCritical:
			icon = "🔴"
		case SeverityHigh:
			icon = "🟠"
		case SeverityMedium:
			icon = "🟡"
		case SeverityLow:
			icon = "🔵"
		case SeverityInfo:
			icon = "ℹ️"
		}

		sb.WriteString(fmt.Sprintf("[%d] %s [%s] %s\n", i+1, icon, insight.Severity, insight.Title))

		if insight.ShaderName != "" {
			sb.WriteString(fmt.Sprintf("    Shader: %s\n", insight.ShaderName))
		}
		if insight.TimingSource != "" {
			kind := "measured"
			if insight.TimingApprox {
				kind = "approximate"
			}
			sb.WriteString(fmt.Sprintf("    Timing Source: %s (%s)\n", insight.TimingSource, kind))
		}

		if attributionLimited && !insight.TimingApprox {
			sb.WriteString("    Finding Class: TRIAGE HYPOTHESIS\n")
		} else {
			sb.WriteString(fmt.Sprintf("    Type: %s\n", insight.Type))
		}
		sb.WriteString(fmt.Sprintf("\n    %s\n\n", insight.Description))

		if insight.Impact != "" {
			sb.WriteString(fmt.Sprintf("    Impact: %s\n\n", insight.Impact))
		}

		if len(insight.Recommendations) > 0 {
			sb.WriteString("    Recommendations:\n")
			for _, rec := range insight.Recommendations {
				sb.WriteString(fmt.Sprintf("      • %s\n", rec))
			}
			sb.WriteString("\n")
		}

		sb.WriteString("    " + strings.Repeat("-", 70) + "\n\n")
	}

	return sb.String()
}

func insightTimingSources(shaders []*ShaderMetrics) ([]string, bool) {
	seen := make(map[string]bool)
	var sources []string
	approximate := false
	for _, shader := range shaders {
		if shader.TimingApprox {
			approximate = true
		}
		if shader.TimingSource == "" || seen[shader.TimingSource] {
			continue
		}
		seen[shader.TimingSource] = true
		sources = append(sources, shader.TimingSource)
	}
	sort.Strings(sources)
	return sources, approximate
}
