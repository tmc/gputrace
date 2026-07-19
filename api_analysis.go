package gputrace

import "github.com/tmc/gputrace/internal/analysis"

// ExtractStatistics extracts summary statistics from t.
func ExtractStatistics(t *Trace) (*TraceStatistics, error) {
	return analysis.ExtractStatistics(t)
}

// AnalyzeBufferAccess analyzes buffer access in t.
func AnalyzeBufferAccess(t *Trace) (*BufferAccessAnalysis, error) {
	return analysis.AnalyzeBufferAccess(t)
}

// FormatBufferAccessReport formats a buffer access report.
func FormatBufferAccessReport(a *BufferAccessAnalysis, verbose bool) string {
	return analysis.FormatBufferAccessReport(a, verbose)
}

// ExtractBufferTimeline extracts the buffer timeline from t.
func ExtractBufferTimeline(t *Trace) (*BufferTimelineAnalysis, error) {
	return analysis.ExtractBufferTimeline(t)
}

// FormatBufferTimelineASCII formats a buffer timeline with the given width.
func FormatBufferTimelineASCII(a *BufferTimelineAnalysis, width int) string {
	return analysis.FormatBufferTimelineASCII(a, width)
}

// FormatBufferTimelineSummary formats a buffer timeline summary.
func FormatBufferTimelineSummary(a *BufferTimelineAnalysis) string {
	return analysis.FormatBufferTimelineSummary(a)
}

// ExtractBufferSizes extracts buffer size information from t.
func ExtractBufferSizes(t *Trace) (*analysis.BufferSizeInfo, error) {
	return analysis.ExtractBufferSizes(t)
}

// CompareBuffers compares two sets of buffer size information.
func CompareBuffers(info1, info2 *analysis.BufferSizeInfo) *analysis.BufferDiff {
	return analysis.CompareBuffers(info1, info2)
}

// FormatBufferDiff formats a buffer comparison.
func FormatBufferDiff(diff *analysis.BufferDiff, trace1Path, trace2Path string) string {
	return analysis.FormatBufferDiff(diff, trace1Path, trace2Path)
}

// GenerateInsights generates performance insights for t.
func GenerateInsights(t *Trace) (*InsightsReport, error) {
	return analysis.GenerateInsights(t)
}

// FormatInsightsReport formats a performance insights report.
func FormatInsightsReport(report *InsightsReport) string {
	return analysis.FormatInsightsReport(report)
}
