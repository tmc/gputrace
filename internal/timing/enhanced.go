package timing

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// EnhancedTiming combines timing data from multiple sources:
// - MTSP records (command structure)
// - kdebug events (accurate timing)
// - Signposts (shader profiling)
type EnhancedTiming struct {
	// From MTSP
	EncoderLabel string
	KernelName   string
	EncoderIndex int

	// From kdebug events
	SubmissionTime  time.Time
	ExecutionStart  time.Time
	ExecutionEnd    time.Time
	CommandBufferID uint64

	// From signposts
	ShaderIntervals []*trace.SignpostInterval
	SignpostName    string

	// Calculated metrics
	QueueLatency  time.Duration // ExecutionStart - SubmissionTime
	ExecutionTime time.Duration // ExecutionEnd - ExecutionStart
	TotalTime     time.Duration // ExecutionEnd - SubmissionTime
	Utilization   float64       // % of total GPU time

	// Raw timestamps (mach_absolute_time)
	SubmissionTimestamp uint64
	StartTimestamp      uint64
	EndTimestamp        uint64
}

// EnhancedTimingExtractor extracts and correlates timing from multiple sources.
type EnhancedTimingExtractor struct {
	trace          *Trace
	kdebugParser   *trace.KDebugParser
	signpostParser *trace.SignpostParser
}

// NewEnhancedTimingExtractor creates a new enhanced timing extractor.
func NewEnhancedTimingExtractor(trace *Trace) *EnhancedTimingExtractor {
	return &EnhancedTimingExtractor{
		trace:          trace,
		kdebugParser:   NewKDebugParser(trace),
		signpostParser: NewSignpostParser(trace),
	}
}

// ExtractEnhancedTiming extracts timing from all available sources and correlates them.
func (e *EnhancedTimingExtractor) ExtractEnhancedTiming() ([]*EnhancedTiming, error) {
	// Extract from all sources
	mtspTimings, mtspErr := e.extractMTSPTiming()
	kdebugTimings, kdebugErr := e.extractKDebugTiming()
	signpostTimings, signpostErr := e.extractSignpostTiming()

	// If no data from any source, return error
	if mtspErr != nil && kdebugErr != nil && signpostErr != nil {
		return nil, fmt.Errorf("failed to extract timing from any source: mtsp=%v kdebug=%v signpost=%v",
			mtspErr, kdebugErr, signpostErr)
	}

	// Correlate the data
	enhanced := e.correlateTiming(mtspTimings, kdebugTimings, signpostTimings)

	// Calculate metrics
	e.calculateMetrics(enhanced)

	return enhanced, nil
}

// extractMTSPTiming extracts timing from MTSP records.
func (e *EnhancedTimingExtractor) extractMTSPTiming() ([]*EncoderTiming, error) {
	// Try standard timing extraction
	timings, err := ExtractTimingData(e.trace)
	if err != nil || len(timings) == 0 {
		// Fall back to synthetic timing
		timings = GenerateSyntheticTiming(e.trace)
		if len(timings) == 0 {
			return nil, fmt.Errorf("no MTSP timing available")
		}
	}
	return timings, nil
}

// extractKDebugTiming extracts timing from kdebug events.
func (e *EnhancedTimingExtractor) extractKDebugTiming() ([]*EncoderTiming, error) {
	return e.kdebugParser.EnhancedTimingFromKDebug()
}

// extractSignpostTiming extracts timing from signposts.
func (e *EnhancedTimingExtractor) extractSignpostTiming() ([]*EncoderTiming, error) {
	return e.signpostParser.EnhancedTimingFromSignposts()
}

// correlateTiming correlates timing data from multiple sources.
func (e *EnhancedTimingExtractor) correlateTiming(
	mtsp []*EncoderTiming,
	kdebug []*EncoderTiming,
	signposts []*EncoderTiming,
) []*EnhancedTiming {

	// Start with MTSP as the base (has structure)
	var enhanced []*EnhancedTiming

	if len(mtsp) > 0 {
		// Use MTSP as base
		for i, timing := range mtsp {
			et := &EnhancedTiming{
				EncoderLabel:   timing.Label,
				EncoderIndex:   i,
				StartTimestamp: timing.StartTimestamp,
				EndTimestamp:   timing.EndTimestamp,
			}

			// Try to find matching kernel name
			if i < len(e.trace.KernelNames) {
				et.KernelName = e.trace.KernelNames[i]
			}

			enhanced = append(enhanced, et)
		}

		// Correlate with kdebug
		if len(kdebug) > 0 {
			e.correlateWithKDebug(enhanced, kdebug)
		}

		// Correlate with signposts
		if len(signposts) > 0 {
			e.correlateWithSignposts(enhanced, signposts)
		}

	} else if len(kdebug) > 0 {
		// Use kdebug as base if no MTSP
		for i, timing := range kdebug {
			et := &EnhancedTiming{
				EncoderLabel:   fmt.Sprintf("GPU_Encoder_%d", i),
				EncoderIndex:   i,
				StartTimestamp: timing.StartTimestamp,
				EndTimestamp:   timing.EndTimestamp,
			}
			enhanced = append(enhanced, et)
		}

		// Correlate with signposts
		if len(signposts) > 0 {
			e.correlateWithSignposts(enhanced, signposts)
		}

	} else if len(signposts) > 0 {
		// Use signposts as base (last resort)
		for i, timing := range signposts {
			et := &EnhancedTiming{
				EncoderLabel:   timing.Label,
				EncoderIndex:   i,
				StartTimestamp: timing.StartTimestamp,
				EndTimestamp:   timing.EndTimestamp,
			}
			enhanced = append(enhanced, et)
		}
	}

	return enhanced
}

// correlateWithKDebug matches kdebug events with enhanced timings by timestamp.
func (e *EnhancedTimingExtractor) correlateWithKDebug(
	enhanced []*EnhancedTiming,
	kdebug []*EncoderTiming,
) {
	// Sort kdebug by start timestamp
	sort.Slice(kdebug, func(i, j int) bool {
		return kdebug[i].StartTimestamp < kdebug[j].StartTimestamp
	})

	// Match by closest timestamp
	for _, et := range enhanced {
		var closest *EncoderTiming
		var minDiff uint64 = ^uint64(0) // Max uint64

		for _, kd := range kdebug {
			// Calculate time difference
			var diff uint64
			if kd.StartTimestamp > et.StartTimestamp {
				diff = kd.StartTimestamp - et.StartTimestamp
			} else {
				diff = et.StartTimestamp - kd.StartTimestamp
			}

			if diff < minDiff {
				minDiff = diff
				closest = kd
			}
		}

		// If found a close match (within 10ms), use kdebug timing
		if closest != nil && minDiff < 10_000_000 { // 10ms in nanoseconds
			et.StartTimestamp = closest.StartTimestamp
			et.EndTimestamp = closest.EndTimestamp
		}
	}
}

// correlateWithSignposts matches signpost intervals with enhanced timings.
func (e *EnhancedTimingExtractor) correlateWithSignposts(
	enhanced []*EnhancedTiming,
	signposts []*EncoderTiming,
) {
	// Match by timestamp overlap
	for _, et := range enhanced {
		for _, sp := range signposts {
			// Check if signpost overlaps with timing
			if sp.StartTimestamp >= et.StartTimestamp &&
				sp.EndTimestamp <= et.EndTimestamp {

				et.SignpostName = sp.Label
				// Could store full signpost data here
				break
			}
		}
	}
}

// calculateMetrics calculates derived metrics for enhanced timings.
func (e *EnhancedTimingExtractor) calculateMetrics(timings []*EnhancedTiming) {
	if len(timings) == 0 {
		return
	}

	// Calculate total GPU time
	var totalNs uint64
	for _, t := range timings {
		duration := t.EndTimestamp - t.StartTimestamp
		totalNs += duration
	}

	// Calculate per-timing metrics
	for _, t := range timings {
		duration := t.EndTimestamp - t.StartTimestamp

		// Convert timestamps to time.Time (approximate)
		t.ExecutionStart = time.Unix(0, int64(t.StartTimestamp))
		t.ExecutionEnd = time.Unix(0, int64(t.EndTimestamp))
		t.ExecutionTime = time.Duration(duration)

		// If we have submission time, calculate queue latency
		if t.SubmissionTimestamp > 0 && t.SubmissionTimestamp < t.StartTimestamp {
			t.SubmissionTime = time.Unix(0, int64(t.SubmissionTimestamp))
			queueLatency := t.StartTimestamp - t.SubmissionTimestamp
			t.QueueLatency = time.Duration(queueLatency)
			t.TotalTime = time.Duration(t.EndTimestamp - t.SubmissionTimestamp)
		} else {
			t.TotalTime = t.ExecutionTime
		}

		// Calculate utilization
		if totalNs > 0 {
			t.Utilization = float64(duration) / float64(totalNs) * 100.0
		}
	}
}

// TimingQuality indicates the quality/source of timing data.
type TimingQuality int

const (
	TimingQualitySynthetic TimingQuality = iota // Estimated timing
	TimingQualityMTSP                           // From MTSP records
	TimingQualitySignpost                       // From signposts
	TimingQualityKDebug                         // From kdebug (most accurate)
	TimingQualityCombined                       // Multiple sources correlated
)

// GetTimingQuality returns the quality level of the timing data.
func (t *EnhancedTiming) GetTimingQuality() TimingQuality {
	hasKDebug := t.SubmissionTimestamp > 0
	hasSignpost := t.SignpostName != ""
	hasMTSP := t.EncoderLabel != ""

	if hasKDebug && hasSignpost && hasMTSP {
		return TimingQualityCombined
	} else if hasKDebug {
		return TimingQualityKDebug
	} else if hasSignpost {
		return TimingQualitySignpost
	} else if hasMTSP {
		return TimingQualityMTSP
	}
	return TimingQualitySynthetic
}

// FormatEnhancedTiming returns a human-readable representation.
func FormatEnhancedTiming(t *EnhancedTiming) string {
	return fmt.Sprintf(
		"%-30s kernel=%-40s exec=%8.2fms queue=%6.2fms util=%5.1f%% quality=%v",
		t.EncoderLabel,
		t.KernelName,
		float64(t.ExecutionTime.Nanoseconds())/1e6,
		float64(t.QueueLatency.Nanoseconds())/1e6,
		t.Utilization,
		t.GetTimingQuality(),
	)
}

// EnhancedTimingReport generates a detailed report of enhanced timing data.
func EnhancedTimingReport(timings []*EnhancedTiming) string {
	if len(timings) == 0 {
		return "No timing data available\n"
	}

	report := "Enhanced GPU Timing Report\n"
	report += "==========================\n\n"

	// Calculate totals
	var totalExec, totalQueue time.Duration
	qualityCounts := make(map[TimingQuality]int)

	for _, t := range timings {
		totalExec += t.ExecutionTime
		totalQueue += t.QueueLatency
		qualityCounts[t.GetTimingQuality()]++
	}

	// Summary
	report += fmt.Sprintf("Total Encoders: %d\n", len(timings))
	report += fmt.Sprintf("Total Execution Time: %.2f ms\n", float64(totalExec.Nanoseconds())/1e6)
	report += fmt.Sprintf("Total Queue Latency: %.2f ms\n", float64(totalQueue.Nanoseconds())/1e6)
	report += fmt.Sprintf("Average Execution: %.2f ms\n", float64(totalExec.Nanoseconds())/1e6/float64(len(timings)))
	report += fmt.Sprintf("Average Queue: %.2f ms\n\n", float64(totalQueue.Nanoseconds())/1e6/float64(len(timings)))

	// Quality breakdown
	report += "Timing Quality:\n"
	for quality, count := range qualityCounts {
		report += fmt.Sprintf("  %v: %d\n", quality, count)
	}
	report += "\n"

	// Detailed breakdown
	report += "Detailed Timing:\n"
	report += fmt.Sprintf("%-30s %-40s %10s %10s %8s %10s\n",
		"Encoder", "Kernel", "Exec (ms)", "Queue (ms)", "Util %", "Quality")
	report += strings.Repeat("-", 120) + "\n"

	for _, t := range timings {
		report += fmt.Sprintf("%-30s %-40s %10.2f %10.2f %7.1f%% %10v\n",
			t.EncoderLabel,
			truncate(t.KernelName, 40),
			float64(t.ExecutionTime.Nanoseconds())/1e6,
			float64(t.QueueLatency.Nanoseconds())/1e6,
			t.Utilization,
			t.GetTimingQuality(),
		)
	}

	return report
}

func (q TimingQuality) String() string {
	switch q {
	case TimingQualitySynthetic:
		return "synthetic"
	case TimingQualityMTSP:
		return "mtsp"
	case TimingQualitySignpost:
		return "signpost"
	case TimingQualityKDebug:
		return "kdebug"
	case TimingQualityCombined:
		return "combined"
	default:
		return "unknown"
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
