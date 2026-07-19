// Package gputrace parses Metal .gputrace bundles.
//
// A .gputrace file is a directory bundle containing Metal GPU capture data.
// Use [Open] to read a bundle:
//
//	trace, err := gputrace.Open("mytrace.gputrace")
//	if err != nil {
//		log.Fatal(err)
//	}
//
// The returned [Trace] contains the parsed metadata, capture data, command
// buffers, timing data, counters, and shaders.
//
// The command-line tool is in cmd/gputrace.
package gputrace

import (
	"io"

	"github.com/google/pprof/profile"
	"github.com/tmc/gputrace/internal/analysis"
	"github.com/tmc/gputrace/internal/command"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/export"
	"github.com/tmc/gputrace/internal/replay"
	"github.com/tmc/gputrace/internal/shader"
	"github.com/tmc/gputrace/internal/timing"
	"github.com/tmc/gputrace/internal/trace"
)

// Re-export main types from internal packages
type (
	Trace                  = trace.Trace
	Metadata               = trace.Metadata
	RecordType             = trace.RecordType
	EncoderTiming          = trace.EncoderTiming
	ComputeEncoder         = trace.ComputeEncoder
	CommandBuffer          = trace.CommandBuffer
	ShaderSourceMapper     = shader.ShaderSourceMapper
	ShaderMetrics          = shader.ShaderMetrics
	ShaderMetricsReport    = shader.ShaderMetricsReport
	PerfCounterStats       = counter.PerfCounterStats
	ShaderHardwareMetrics  = counter.ShaderHardwareMetrics
	XcodeCounterData       = counter.XcodeCounterData
	XcodeEncoderCounters   = counter.XcodeEncoderCounters
	TraceStatistics        = analysis.TraceStatistics
	TimingMetricsExtractor = timing.TimingMetricsExtractor

	// Buffer access analysis types (gputrace-93)
	BufferAccessAnalysis = analysis.BufferAccessAnalysis
	BufferAccessInfo     = analysis.BufferAccessInfo
	EncoderAccessInfo    = analysis.EncoderAccessInfo
	BufferAlias          = analysis.BufferAlias

	// Buffer timeline types (gputrace-94)
	BufferTimelineAnalysis = analysis.BufferTimelineAnalysis
	BufferLifecycle        = analysis.BufferLifecycle

	// Encoder timing from profiler data (streamData plist)
	EncoderTimingInfo = counter.EncoderTimingInfo

	// Counter sampling types (gputrace-104)
	CounterSamplingConfig = counter.CounterSamplingConfig

	// Timing metrics types (gputrace-106)
	TimingMetrics       = timing.TimingMetrics
	KernelTiming        = timing.KernelTiming
	CommandBufferTiming = timing.CommandBufferTiming

	// Insights types (gputrace-97)
	PerformanceInsight = analysis.PerformanceInsight
	InsightsReport     = analysis.InsightsReport
	InsightType        = analysis.InsightType
	InsightSeverity    = analysis.InsightSeverity

	// Kernel analysis types
	KernelStat = trace.KernelStat
	TimingStat = trace.TimingStat

	// API Call types (for buffer extraction)
	APICallList        = trace.APICallList
	InitCall           = trace.InitCall
	CommandBufferCalls = trace.CommandBufferCalls
	FormattedAPICall   = trace.FormattedAPICall
)

// Re-export constants
const (
	RecordTypeCommand      = trace.RecordTypeCommand
	RecordTypeString       = trace.RecordTypeString
	RecordTypeFunction     = trace.RecordTypeFunction
	RecordTypeInteger      = trace.RecordTypeInteger
	RecordTypeUnsignedLong = trace.RecordTypeUnsignedLong
)

// Re-export errors
var (
	ErrInvalidTrace    = trace.ErrInvalidTrace
	ErrInvalidMagic    = trace.ErrInvalidMagic
	ErrMissingMetadata = trace.ErrMissingMetadata
)

// Re-export magic constants
const (
	MagicMTSP   = trace.MagicMTSP
	MagicXDIC   = trace.MagicXDIC
	MagicBPList = trace.MagicBPList
)

// Re-export insight type constants (gputrace-97)
const (
	InsightBottleneck   = analysis.InsightBottleneck
	InsightOptimization = analysis.InsightOptimization
	InsightAntiPattern  = analysis.InsightAntiPattern
	InsightInfo         = analysis.InsightInfo
)

// Re-export insight severity constants (gputrace-97)
const (
	SeverityCritical = analysis.SeverityCritical
	SeverityHigh     = analysis.SeverityHigh
	SeverityMedium   = analysis.SeverityMedium
	SeverityLow      = analysis.SeverityLow
	SeverityInfo     = analysis.SeverityInfo
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

// GenerateSyntheticTiming generates synthetic timing data for t.
func GenerateSyntheticTiming(t *Trace) []*EncoderTiming {
	return timing.GenerateSyntheticTiming(t)
}

// ExtractShaderMetrics extracts shader metrics from t.
func ExtractShaderMetrics(t *Trace) (*ShaderMetricsReport, error) {
	return shader.ExtractShaderMetrics(t)
}

// NewShaderSourceMapper returns a source mapper that searches searchPaths.
func NewShaderSourceMapper(searchPaths ...string) *ShaderSourceMapper {
	return shader.NewShaderSourceMapper(searchPaths...)
}

// FormatShadersSimple writes a simple shader report to w.
func FormatShadersSimple(w io.Writer, report *ShaderMetricsReport) error {
	return shader.FormatShadersSimple(w, report)
}

// FormatShadersXcodeStyle writes an Xcode-style shader report to w.
func FormatShadersXcodeStyle(w io.Writer, report *ShaderMetricsReport, t *Trace, showEstimates bool) error {
	return shader.FormatShadersXcodeStyle(w, report, t, showEstimates)
}

// ParseDetailedCommandBuffer parses command buffer cbIndex from t.
func ParseDetailedCommandBuffer(t *Trace, cbIndex int) (*command.DetailedCommandBuffer, error) {
	return command.ParseDetailedCommandBuffer(t, cbIndex)
}

// DumpCommandBuffer writes command buffer cbIndex from t to w.
func DumpCommandBuffer(t *Trace, w io.Writer, cbIndex int) error {
	return command.DumpCommandBuffer(t, w, cbIndex)
}

// ToPprof converts timing data to a pprof profile.
func ToPprof(t *Trace, timings []*EncoderTiming) (*profile.Profile, error) {
	return export.ToPprof(t, timings)
}

// ToPprofWithSource converts timing data and source mappings to a pprof profile.
func ToPprofWithSource(t *Trace, timings []*EncoderTiming, mapper *ShaderSourceMapper) (*profile.Profile, error) {
	return export.ToPprofWithSource(t, timings, mapper)
}

// ToPprofWithMetrics converts counter metrics to a pprof profile.
func ToPprofWithMetrics(t *Trace, mapper *ShaderSourceMapper, stats *PerfCounterStats) (*profile.Profile, error) {
	return export.ToPprofWithMetrics(t, mapper, stats)
}

// ParseXcodeCountersCSV parses an Xcode counters CSV file for t.
func ParseXcodeCountersCSV(t *Trace, csvPath string) (*XcodeCounterData, error) {
	return counter.ParseXcodeCountersCSV(t, csvPath)
}

// ExtractStatistics extracts summary statistics from t.
func ExtractStatistics(t *Trace) (*TraceStatistics, error) {
	return analysis.ExtractStatistics(t)
}

// NewTimingMetricsExtractor returns a timing metrics extractor for t.
func NewTimingMetricsExtractor(t *Trace) *TimingMetricsExtractor {
	return timing.NewTimingMetricsExtractor(t)
}

// ParsePerfCounters parses performance counters from t.
func ParsePerfCounters(t *Trace) (*PerfCounterStats, error) {
	return counter.ParsePerfCounters(t)
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

// NewCountersCSVExporter returns a counter CSV exporter for t.
func NewCountersCSVExporter(t *Trace) *counter.CountersCSVExporter {
	return counter.NewCountersCSVExporter(t)
}

// FormatCounterSamplingSimulation formats a counter sampling simulation.
func FormatCounterSamplingSimulation(sim *replay.CounterSamplingSimulation) string {
	return replay.FormatCounterSamplingSimulation(sim)
}

// FormatCounterSamplingResult formats a counter sampling result.
func FormatCounterSamplingResult(result *counter.CounterSamplingResult) string {
	return counter.FormatCounterSamplingResult(result)
}

// NewReplayEngine returns a replay engine for t.
func NewReplayEngine(t *Trace) *replay.ReplayEngine {
	return replay.NewReplayEngine(t)
}

// ExtractShaderSourceAttribution extracts source attribution for shaderName.
func ExtractShaderSourceAttribution(t *Trace, shaderName string) (*shader.ShaderSourceAttribution, error) {
	return shader.ExtractShaderSourceAttribution(t, shaderName)
}

// FormatShaderSourceAttribution formats shader source attribution.
func FormatShaderSourceAttribution(attr *shader.ShaderSourceAttribution, showHints bool) string {
	return shader.FormatShaderSourceAttribution(attr, showHints)
}

// FormatShaderSourceAttributionHTML formats shader source attribution as HTML.
func FormatShaderSourceAttributionHTML(attr *shader.ShaderSourceAttribution) string {
	return shader.FormatShaderSourceAttributionHTML(attr)
}

// FormatTimingMetrics formats timing metrics.
func FormatTimingMetrics(metrics *TimingMetrics) string {
	return timing.FormatTimingMetrics(metrics)
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

// ExportShaderMetricsCSV writes shader metrics as CSV.
func ExportShaderMetricsCSV(w io.Writer, report *ShaderMetricsReport) error {
	return shader.ExportShaderMetricsCSV(w, report)
}

// ExportShaderMetricsJSON writes shader metrics as JSON.
func ExportShaderMetricsJSON(w io.Writer, report *ShaderMetricsReport) error {
	return shader.ExportShaderMetricsJSON(w, report)
}

// CorrelateShaderMetrics correlates shader metrics for t.
func CorrelateShaderMetrics(t *Trace) (*shader.ShaderCorrelationReport, error) {
	return shader.CorrelateShaderMetrics(t)
}

// FormatCorrelationReport formats a shader correlation report.
func FormatCorrelationReport(report *shader.ShaderCorrelationReport) string {
	return shader.FormatCorrelationReport(report)
}

// GenerateInsights generates performance insights for t.
func GenerateInsights(t *Trace) (*InsightsReport, error) {
	return analysis.GenerateInsights(t)
}

// FormatInsightsReport formats a performance insights report.
func FormatInsightsReport(report *InsightsReport) string {
	return analysis.FormatInsightsReport(report)
}

// Open opens and parses a .gputrace bundle.
func Open(path string) (*Trace, error) {
	return trace.Open(path)
}

// ExtractEncoderTimingsFromProfiler extracts real timing data from .gpuprofiler_raw streamData.
// Returns per-encoder timing info, total time in microseconds, and any error.
func ExtractEncoderTimingsFromProfiler(t *Trace) ([]EncoderTimingInfo, int, error) {
	return counter.ExtractEncoderTimingsFromProfiler(t)
}

// PipelineStats contains shader compilation statistics from streamData.
type PipelineStats = counter.PipelineStats

// StreamDataStats contains all parsed statistics from streamData.
type StreamDataStats = counter.StreamDataStats

// ExtractPipelineStats extracts pipeline compilation stats from .gpuprofiler_raw streamData.
// This provides instruction counts, register allocation, and other compilation metrics.
func ExtractPipelineStats(t *Trace) (*StreamDataStats, error) {
	return counter.ExtractPipelineStatsFromTrace(t)
}
