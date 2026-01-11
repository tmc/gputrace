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
	// High GPU time percentage indicates a bottleneck
	if shader.PercentOfTotal > 20.0 {
		insight := &PerformanceInsight{
			Type:       InsightBottleneck,
			ShaderName: shader.Name,
			Title:      fmt.Sprintf("%s is a major bottleneck", shader.Name),
			Description: fmt.Sprintf("This shader consumes %.1f%% of total GPU time (%.2f ms)",
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
			insight.Impact = "Dominates GPU execution time"
		} else if shader.PercentOfTotal > 30.0 {
			insight.Severity = SeverityHigh
			insight.Impact = "Major contributor to GPU time"
		} else {
			insight.Severity = SeverityMedium
			insight.Impact = "Significant contributor to GPU time"
		}

		// Generate recommendations
		insight.Recommendations = []string{
			"Profile this shader in detail to identify hotspots",
			"Consider algorithmic optimizations or alternative approaches",
			"Evaluate if work can be distributed across multiple passes",
		}

		// Try to determine if memory-bound or compute-bound
		totalThreads := shader.TotalThreadgroups * shader.ThreadsPerGroupX *
			shader.ThreadsPerGroupY * shader.ThreadsPerGroupZ
		if totalThreads > 0 {
			avgThreads := totalThreads / uint64(shader.InvocationCount)

			// Heuristic: Low thread count with high time = likely memory-bound
			if avgThreads < 1024 {
				insight.Description += "\n\nLikely MEMORY-BOUND: Low thread count suggests memory bandwidth limitation."
				insight.Recommendations = append([]string{
					"Consider reducing memory bandwidth via data tiling",
					"Explore data layout optimizations (structure of arrays vs array of structures)",
					"Use shared memory / threadgroup memory for data reuse",
				}, insight.Recommendations...)
			} else {
				insight.Description += "\n\nLikely COMPUTE-BOUND: High thread count suggests computational limitation."
				insight.Recommendations = append([]string{
					"Profile ALU utilization to identify compute inefficiencies",
					"Consider algorithmic optimizations to reduce arithmetic operations",
					"Evaluate vectorization opportunities",
				}, insight.Recommendations...)
			}
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

	if threadsPerGroup > 0 && occupancy < 0.5 && shader.PercentOfTotal > 5.0 {
		insight := &PerformanceInsight{
			Type:       InsightOptimization,
			Severity:   SeverityMedium,
			ShaderName: shader.Name,
			Title:      fmt.Sprintf("%s has suboptimal occupancy", shader.Name),
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
	if shader.InvocationCount > 100 && shader.PercentOfTotal > 5.0 {
		avgDurationUs := float64(shader.AvgDurationNs) / 1000.0
		if avgDurationUs < 50.0 { // Less than 50 microseconds per call
			insight := &PerformanceInsight{
				Type:       InsightOptimization,
				Severity:   SeverityHigh,
				ShaderName: shader.Name,
				Title:      fmt.Sprintf("%s has excessive dispatch overhead", shader.Name),
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

	// High variability in execution time (indicates branches or synchronization issues)
	if shader.InvocationCount > 1 {
		minMs := float64(shader.MinDurationNs) / 1e6
		maxMs := float64(shader.MaxDurationNs) / 1e6
		avgMs := float64(shader.AvgDurationNs) / 1e6

		if minMs > 0 && maxMs > minMs*3 { // Max is more than 3x min
			variability := ((maxMs - minMs) / avgMs) * 100
			insight := &PerformanceInsight{
				Type:       InsightAntiPattern,
				Severity:   SeverityMedium,
				ShaderName: shader.Name,
				Title:      fmt.Sprintf("%s has high execution time variability", shader.Name),
				Description: fmt.Sprintf("Execution time varies from %.2f ms to %.2f ms (%.0f%% variability). This suggests divergent branches or synchronization issues.",
					minMs, maxMs, variability),
				Metrics: map[string]interface{}{
					"min_ms":      minMs,
					"max_ms":      maxMs,
					"avg_ms":      avgMs,
					"variability": variability,
				},
				Recommendations: []string{
					"Profile for branch divergence and warp/SIMD lane stalls",
					"Consider restructuring conditionals to reduce divergence",
					"Check for synchronization primitives that may cause variation",
				},
				Impact: "Indicates potential SIMD efficiency issues",
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
	if len(metrics.Shaders) > 0 && metrics.Shaders[0].PercentOfTotal > 70.0 {
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
	sb.WriteString(fmt.Sprintf("Total GPU Time: %.2f ms\n", report.TotalGPUTimeMs))
	sb.WriteString(fmt.Sprintf("Insights Found: %d\n", len(report.Insights)))
	sb.WriteString(fmt.Sprintf("  Critical: %d, High: %d, Medium: %d, Low: %d\n\n",
		report.CriticalCount, report.HighCount, report.MediumCount, report.LowCount))

	if len(report.TopBottlenecks) > 0 {
		sb.WriteString("Top Bottlenecks:\n")
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

		sb.WriteString(fmt.Sprintf("    Type: %s\n", insight.Type))
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
