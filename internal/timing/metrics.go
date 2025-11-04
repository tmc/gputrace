package timing

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/command"
)

// TimingMetrics represents comprehensive timing data for a trace.
type TimingMetrics struct {
	// Summary Statistics
	TotalDuration       time.Duration `json:"total_duration"`
	TotalEncoders       int           `json:"total_encoders"`
	TotalCommandBuffers int           `json:"total_command_buffers"`

	// Per-Kernel Metrics
	KernelTimings []*KernelTiming `json:"kernel_timings"`

	// Per-Encoder Metrics
	EncoderTimings []*EncoderTiming `json:"encoder_timings"`

	// Per-Command Buffer Metrics
	CommandBufferTimings []*CommandBufferTiming `json:"command_buffer_timings"`

	// Trace Metadata
	TracePath   string    `json:"trace_path"`
	CaptureTime time.Time `json:"capture_time,omitempty"`
}

// KernelTiming represents timing data for a specific kernel/shader.
type KernelTiming struct {
	Name string `json:"name"`

	// Execution Statistics
	InvocationCount int           `json:"invocation_count"`
	TotalDuration   time.Duration `json:"total_duration"`
	MinDuration     time.Duration `json:"min_duration"`
	MaxDuration     time.Duration `json:"max_duration"`
	AvgDuration     time.Duration `json:"avg_duration"`

	// Percentiles
	P50Duration time.Duration `json:"p50_duration"` // Median
	P95Duration time.Duration `json:"p95_duration"`
	P99Duration time.Duration `json:"p99_duration"`

	// Percentage of total GPU time
	PercentOfTotal float64 `json:"percent_of_total"`

	// Individual invocations for percentile calculation
	Durations []time.Duration `json:"-"` // Omit from JSON (too verbose)
}

// CommandBufferTiming represents timing for a command buffer.
type CommandBufferTiming struct {
	Index int    `json:"index"`
	Label string `json:"label,omitempty"`

	// Timing
	SubmissionTime time.Time     `json:"submission_time,omitempty"`
	CompletionTime time.Time     `json:"completion_time,omitempty"`
	Duration       time.Duration `json:"duration"`

	// Queue latency (time from submission to execution)
	QueueLatency time.Duration `json:"queue_latency,omitempty"`

	// Encoders in this command buffer
	EncoderCount int `json:"encoder_count"`
}

// TimingMetricsExtractor extracts comprehensive timing metrics.
type TimingMetricsExtractor struct {
	trace *Trace
}

// NewTimingMetricsExtractor creates a new metrics extractor.
func NewTimingMetricsExtractor(trace *Trace) *TimingMetricsExtractor {
	return &TimingMetricsExtractor{trace: trace}
}

// Extract extracts all timing metrics from the trace.
func (tme *TimingMetricsExtractor) Extract() (*TimingMetrics, error) {
	metrics := &TimingMetrics{
		TracePath:            tme.trace.Path,
		KernelTimings:        make([]*KernelTiming, 0),
		EncoderTimings:       make([]*EncoderTiming, 0),
		CommandBufferTimings: make([]*CommandBufferTiming, 0),
	}

	// Extract encoder timings first
	// TODO: Re-enable when TimingExtractorV2 is properly implemented
	var encoderTimings []*EncoderTiming
	// extractor := NewTimingExtractor(tme.trace)
	// encoderTimings, err := extractor.ExtractTimingV2()
	// if err != nil {
	// 	return nil, fmt.Errorf("extract encoder timing: %w", err)
	// }
	metrics.EncoderTimings = encoderTimings
	metrics.TotalEncoders = len(encoderTimings)

	// Aggregate by kernel name
	kernelMap := make(map[string]*KernelTiming)
	for _, et := range encoderTimings {
		duration := time.Duration(et.DurationNs)

		kt, exists := kernelMap[et.Label]
		if !exists {
			kt = &KernelTiming{
				Name:        et.Label,
				MinDuration: duration,
				MaxDuration: duration,
				Durations:   make([]time.Duration, 0),
			}
			kernelMap[et.Label] = kt
		}

		kt.InvocationCount++
		kt.TotalDuration += duration
		kt.Durations = append(kt.Durations, duration)

		if duration < kt.MinDuration {
			kt.MinDuration = duration
		}
		if duration > kt.MaxDuration {
			kt.MaxDuration = duration
		}
	}

	// Calculate derived statistics for each kernel
	var totalDuration time.Duration
	for _, kt := range kernelMap {
		if kt.InvocationCount > 0 {
			kt.AvgDuration = kt.TotalDuration / time.Duration(kt.InvocationCount)
		}

		// Calculate percentiles
		kt.calculatePercentiles()

		totalDuration += kt.TotalDuration
		metrics.KernelTimings = append(metrics.KernelTimings, kt)
	}

	// Calculate percentages
	for _, kt := range metrics.KernelTimings {
		if totalDuration > 0 {
			kt.PercentOfTotal = float64(kt.TotalDuration) / float64(totalDuration) * 100.0
		}
	}

	// Sort kernels by total duration (descending)
	sort.Slice(metrics.KernelTimings, func(i, j int) bool {
		return metrics.KernelTimings[i].TotalDuration > metrics.KernelTimings[j].TotalDuration
	})

	metrics.TotalDuration = totalDuration

	// Extract command buffer timings
	if err := tme.extractCommandBufferTimings(metrics); err != nil {
		// Non-fatal - command buffer timing is optional
		fmt.Printf("Warning: could not extract command buffer timing: %v\n", err)
	}

	return metrics, nil
}

// calculatePercentiles calculates percentile values for kernel timing.
func (kt *KernelTiming) calculatePercentiles() {
	if len(kt.Durations) == 0 {
		return
	}

	// Sort durations for percentile calculation
	sorted := make([]time.Duration, len(kt.Durations))
	copy(sorted, kt.Durations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	// Calculate percentiles
	p50Idx := len(sorted) * 50 / 100
	p95Idx := len(sorted) * 95 / 100
	p99Idx := len(sorted) * 99 / 100

	if p50Idx >= len(sorted) {
		p50Idx = len(sorted) - 1
	}
	if p95Idx >= len(sorted) {
		p95Idx = len(sorted) - 1
	}
	if p99Idx >= len(sorted) {
		p99Idx = len(sorted) - 1
	}

	kt.P50Duration = sorted[p50Idx]
	kt.P95Duration = sorted[p95Idx]
	kt.P99Duration = sorted[p99Idx]
}

// extractCommandBufferTimings extracts command buffer level timing.
func (tme *TimingMetricsExtractor) extractCommandBufferTimings(metrics *TimingMetrics) error {
	commandBuffers, err := tme.trace.ParseCommandBuffers()
	if err != nil {
		return fmt.Errorf("parse command buffers: %w", err)
	}

	metrics.TotalCommandBuffers = len(commandBuffers)

	for _, cb := range commandBuffers {
		// Parse detailed command buffer to get encoders
		dcb, err := command.ParseDetailedCommandBuffer(tme.trace, cb.Index)
		if err != nil {
			// Skip command buffers we can't parse
			continue
		}

		cbt := &CommandBufferTiming{
			Index:        cb.Index,
			EncoderCount: len(dcb.Encoders),
		}

		// Try to find timing for encoders in this command buffer
		var cbDuration time.Duration
		for range dcb.Encoders {
			// Find matching encoder timing by address
			for _, et := range metrics.EncoderTimings {
				// Match by label for now (address matching would be more precise)
				// TODO: Add encoder address tracking to EncoderTiming
				cbDuration += time.Duration(et.DurationNs)
				break
			}
		}

		cbt.Duration = cbDuration
		metrics.CommandBufferTimings = append(metrics.CommandBufferTimings, cbt)
	}

	return nil
}

// FormatTimingMetrics formats timing metrics as a human-readable report.
func FormatTimingMetrics(metrics *TimingMetrics) string {
	var out string

	out += "=== GPU Timing Metrics ===\n\n"
	out += fmt.Sprintf("Trace: %s\n", metrics.TracePath)
	out += fmt.Sprintf("Total Duration: %v (%.2f ms)\n", metrics.TotalDuration, float64(metrics.TotalDuration)/float64(time.Millisecond))
	out += fmt.Sprintf("Command Buffers: %d\n", metrics.TotalCommandBuffers)
	out += fmt.Sprintf("Encoders: %d\n", metrics.TotalEncoders)
	out += fmt.Sprintf("Unique Kernels: %d\n\n", len(metrics.KernelTimings))

	out += "=== Top Kernels by Time ===\n\n"
	out += fmt.Sprintf("%-40s %8s %10s %10s %10s %10s %10s %10s %8s\n",
		"Kernel Name", "Invokes", "Total(ms)", "Avg(µs)", "Min(µs)", "Max(µs)", "P50(µs)", "P95(µs)", "% Total")
	out += fmt.Sprintf("%s\n", repeatStr("-", 140))

	for _, kt := range metrics.KernelTimings {
		name := kt.Name
		if len(name) > 40 {
			name = name[:37] + "..."
		}

		totalMs := float64(kt.TotalDuration) / float64(time.Millisecond)
		avgUs := float64(kt.AvgDuration) / float64(time.Microsecond)
		minUs := float64(kt.MinDuration) / float64(time.Microsecond)
		maxUs := float64(kt.MaxDuration) / float64(time.Microsecond)
		p50Us := float64(kt.P50Duration) / float64(time.Microsecond)
		p95Us := float64(kt.P95Duration) / float64(time.Microsecond)

		out += fmt.Sprintf("%-40s %8d %10.2f %10.1f %10.1f %10.1f %10.1f %10.1f %7.1f%%\n",
			name,
			kt.InvocationCount,
			totalMs,
			avgUs,
			minUs,
			maxUs,
			p50Us,
			p95Us,
			kt.PercentOfTotal)
	}

	// Command buffer summary
	if len(metrics.CommandBufferTimings) > 0 {
		out += "\n=== Command Buffer Summary ===\n\n"
		out += fmt.Sprintf("%-10s %-30s %12s %10s\n", "Index", "Label", "Duration(ms)", "Encoders")
		out += fmt.Sprintf("%s\n", repeatStr("-", 70))

		for _, cbt := range metrics.CommandBufferTimings {
			label := cbt.Label
			if label == "" {
				label = "(unnamed)"
			}
			if len(label) > 30 {
				label = label[:27] + "..."
			}

			durationMs := float64(cbt.Duration) / float64(time.Millisecond)

			out += fmt.Sprintf("%-10d %-30s %12.2f %10d\n",
				cbt.Index,
				label,
				durationMs,
				cbt.EncoderCount)
		}
	}

	return out
}

// ExportTimingMetricsJSON exports timing metrics to JSON format.
func ExportTimingMetricsJSON(w io.Writer, metrics *TimingMetrics) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(metrics)
}

// ExportTimingMetricsCSV exports timing metrics to CSV format.
func ExportTimingMetricsCSV(w io.Writer, metrics *TimingMetrics) error {
	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Write header
	header := []string{
		"Kernel Name",
		"Invocation Count",
		"Total Duration (ns)",
		"Avg Duration (ns)",
		"Min Duration (ns)",
		"Max Duration (ns)",
		"P50 Duration (ns)",
		"P95 Duration (ns)",
		"P99 Duration (ns)",
		"Percent of Total",
	}
	if err := writer.Write(header); err != nil {
		return err
	}

	// Write data rows
	for _, kt := range metrics.KernelTimings {
		row := []string{
			kt.Name,
			fmt.Sprintf("%d", kt.InvocationCount),
			fmt.Sprintf("%d", kt.TotalDuration.Nanoseconds()),
			fmt.Sprintf("%d", kt.AvgDuration.Nanoseconds()),
			fmt.Sprintf("%d", kt.MinDuration.Nanoseconds()),
			fmt.Sprintf("%d", kt.MaxDuration.Nanoseconds()),
			fmt.Sprintf("%d", kt.P50Duration.Nanoseconds()),
			fmt.Sprintf("%d", kt.P95Duration.Nanoseconds()),
			fmt.Sprintf("%d", kt.P99Duration.Nanoseconds()),
			fmt.Sprintf("%.2f", kt.PercentOfTotal),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	return nil
}

// CompareTraces compares timing metrics from two traces for regression detection.
func CompareTraces(baseline, current *TimingMetrics) *TimingComparison {
	comp := &TimingComparison{
		BaselinePath: baseline.TracePath,
		CurrentPath:  current.TracePath,
		Differences:  make([]*KernelTimingDiff, 0),
	}

	// Create map of current kernel timings
	currentMap := make(map[string]*KernelTiming)
	for _, kt := range current.KernelTimings {
		currentMap[kt.Name] = kt
	}

	// Compare each baseline kernel
	for _, baselineKt := range baseline.KernelTimings {
		currentKt, exists := currentMap[baselineKt.Name]
		if !exists {
			// Kernel removed in current trace
			comp.Differences = append(comp.Differences, &KernelTimingDiff{
				KernelName:       baselineKt.Name,
				Status:           "removed",
				BaselineDuration: baselineKt.AvgDuration,
			})
			continue
		}

		// Calculate difference
		diff := &KernelTimingDiff{
			KernelName:       baselineKt.Name,
			Status:           "changed",
			BaselineDuration: baselineKt.AvgDuration,
			CurrentDuration:  currentKt.AvgDuration,
		}

		if baselineKt.AvgDuration > 0 {
			diff.PercentChange = float64(currentKt.AvgDuration-baselineKt.AvgDuration) / float64(baselineKt.AvgDuration) * 100.0
		}

		// Flag regressions (>10% slower)
		if diff.PercentChange > 10.0 {
			diff.IsRegression = true
			comp.RegressionCount++
		} else if diff.PercentChange < -10.0 {
			// Improvement
			comp.ImprovementCount++
		}

		comp.Differences = append(comp.Differences, diff)
		delete(currentMap, baselineKt.Name)
	}

	// Check for new kernels in current
	for name, kt := range currentMap {
		comp.Differences = append(comp.Differences, &KernelTimingDiff{
			KernelName:      name,
			Status:          "added",
			CurrentDuration: kt.AvgDuration,
		})
	}

	// Sort by percent change (regressions first)
	sort.Slice(comp.Differences, func(i, j int) bool {
		return comp.Differences[i].PercentChange > comp.Differences[j].PercentChange
	})

	return comp
}

// TimingComparison represents a comparison between two traces.
type TimingComparison struct {
	BaselinePath     string              `json:"baseline_path"`
	CurrentPath      string              `json:"current_path"`
	Differences      []*KernelTimingDiff `json:"differences"`
	RegressionCount  int                 `json:"regression_count"`
	ImprovementCount int                 `json:"improvement_count"`
}

// KernelTimingDiff represents the difference in timing for a kernel.
type KernelTimingDiff struct {
	KernelName       string        `json:"kernel_name"`
	Status           string        `json:"status"` // "changed", "added", "removed"
	BaselineDuration time.Duration `json:"baseline_duration,omitempty"`
	CurrentDuration  time.Duration `json:"current_duration,omitempty"`
	PercentChange    float64       `json:"percent_change,omitempty"`
	IsRegression     bool          `json:"is_regression,omitempty"`
}

// FormatTimingComparison formats a timing comparison report.
func FormatTimingComparison(comp *TimingComparison) string {
	var out string

	out += "=== Trace Comparison ===\n\n"
	out += fmt.Sprintf("Baseline: %s\n", comp.BaselinePath)
	out += fmt.Sprintf("Current:  %s\n\n", comp.CurrentPath)
	out += fmt.Sprintf("Regressions:  %d kernels slower by >10%%\n", comp.RegressionCount)
	out += fmt.Sprintf("Improvements: %d kernels faster by >10%%\n\n", comp.ImprovementCount)

	out += fmt.Sprintf("%-40s %-10s %12s %12s %10s\n",
		"Kernel Name", "Status", "Baseline(µs)", "Current(µs)", "Change")
	out += fmt.Sprintf("%s\n", repeatStr("-", 90))

	for _, diff := range comp.Differences {
		name := diff.KernelName
		if len(name) > 40 {
			name = name[:37] + "..."
		}

		baselineUs := float64(diff.BaselineDuration) / float64(time.Microsecond)
		currentUs := float64(diff.CurrentDuration) / float64(time.Microsecond)

		var changeStr string
		if diff.Status == "removed" {
			changeStr = "REMOVED"
		} else if diff.Status == "added" {
			changeStr = "NEW"
		} else {
			changeStr = fmt.Sprintf("%+.1f%%", diff.PercentChange)
			if diff.IsRegression {
				changeStr += " ⚠️"
			}
		}

		out += fmt.Sprintf("%-40s %-10s %12.1f %12.1f %10s\n",
			name,
			diff.Status,
			baselineUs,
			currentUs,
			changeStr)
	}

	return out
}
