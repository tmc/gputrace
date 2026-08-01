package gputrace

import (
	"io"

	"github.com/tmc/gputrace/internal/timing"
)

// ExtractTimingData extracts encoder timing data from t.
func ExtractTimingData(t *Trace) ([]*EncoderTiming, error) {
	return timing.ExtractTimingData(t)
}

// ExtractStore0Timing extracts timing data from the store0 capture stream.
func ExtractStore0Timing(t *Trace) (*timing.Store0TimingData, error) {
	return timing.ExtractStore0Timing(t)
}

// ConvertStore0ToEncoderTimings converts store0 timing data to encoder timings.
func ConvertStore0ToEncoderTimings(t *Trace, store0Data *timing.Store0TimingData) []*EncoderTiming {
	return timing.ConvertStore0ToEncoderTimings(t, store0Data)
}

// NewTimingMetricsExtractor returns a timing metrics extractor for t.
func NewTimingMetricsExtractor(t *Trace) *TimingMetricsExtractor {
	return timing.NewTimingMetricsExtractor(t)
}

// FormatTimingMetrics formats timing metrics.
func FormatTimingMetrics(metrics *TimingMetrics) string {
	return timing.FormatTimingMetrics(metrics)
}

// LowSampleMarker follows a row measured from a single dispatch.
const LowSampleMarker = timing.LowSampleMarker

// LowSampleFootnote explains LowSampleMarker, or returns "" when unused.
func LowSampleFootnote(timings []*KernelTiming) string {
	return timing.LowSampleFootnote(timings)
}

// FilterMinCalls keeps rows measured from at least min dispatches and reports
// how many it dropped.
func FilterMinCalls(timings []*KernelTiming, min int) ([]*KernelTiming, int) {
	return timing.FilterMinCalls(timings, min)
}

// MinCallsNote states what a --min-calls filter removed.
func MinCallsNote(min, dropped, total int) string {
	return timing.MinCallsNote(min, dropped, total)
}

// ExportTimingMetricsJSON writes timing metrics as JSON.
func ExportTimingMetricsJSON(w io.Writer, metrics *TimingMetrics) error {
	return timing.ExportTimingMetricsJSON(w, metrics)
}

// ExportTimingMetricsCSV writes timing metrics as CSV.
func ExportTimingMetricsCSV(w io.Writer, metrics *TimingMetrics) error {
	return timing.ExportTimingMetricsCSV(w, metrics)
}

// CompareTraces compares baseline and current timing metrics.
func CompareTraces(baseline, current *TimingMetrics) *timing.TimingComparison {
	return timing.CompareTraces(baseline, current)
}

// FormatTimingComparison formats a timing comparison.
func FormatTimingComparison(comp *timing.TimingComparison) string {
	return timing.FormatTimingComparison(comp)
}

// NewTimingExtractorProfilerRaw returns a raw profiler timing extractor for t.
func NewTimingExtractorProfilerRaw(t *Trace) *timing.TimingExtractorProfilerRaw {
	return timing.NewTimingExtractorProfilerRaw(t)
}
