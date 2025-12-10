// Package gputrace provides parsing and analysis for .gputrace GPU trace files from Metal.
//
// A .gputrace file is a directory bundle containing multiple files that represent
// Metal GPU capture data. This package provides utilities to parse trace metadata,
// extract kernel names, labels, and timing information.
//
// The main entry point is the Open function which returns a Trace:
//
//	trace, err := gputrace.Open("path/to/trace.gputrace")
//	if err != nil {
//		log.Fatal(err)
//	}
//
// The Trace struct provides access to all parsed data and analysis capabilities.
//
// For command-line usage, see cmd/gputrace which provides various subcommands
// for analyzing traces, exporting to different formats, and generating insights.
package gputrace

import (
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
	Store0TimingData       = timing.Store0TimingData
	Store0Encoder          = timing.Store0Encoder
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
	BufferTimelineEvent    = analysis.BufferTimelineEvent

	// Buffer diff types (gputrace-95)
	BufferSizeInfo = analysis.BufferSizeInfo
	BufferMetadata = analysis.BufferMetadata
	BufferDiff     = analysis.BufferDiff
	BufferChange   = analysis.BufferChange

	// Counter export types (gputrace-101)
	CountersCSVExporter = counter.CountersCSVExporter

	// Counter sampling types (gputrace-104)
	CounterSamplingConfig     = counter.CounterSamplingConfig
	CounterSamplingResult     = counter.CounterSamplingResult
	CounterSamplingSimulation = replay.CounterSamplingSimulation

	// Replay engine types (gputrace-103, gputrace-104)
	ReplayEngine      = replay.ReplayEngine
	ReplayCommand     = replay.ReplayCommand
	ReplayEncoderInfo = replay.ReplayEncoderInfo
	ReplayPlan        = replay.ReplayPlan
	ReplayValidation  = replay.ReplayValidation
	CommandQueueInfo  = replay.CommandQueueInfo

	// Shader source attribution types (gputrace-105)
	ShaderSourceAttribution = shader.ShaderSourceAttribution
	SourceLineAttribution   = shader.SourceLineAttribution

	// Timing metrics types (gputrace-106)
	TimingMetrics       = timing.TimingMetrics
	KernelTiming        = timing.KernelTiming
	CommandBufferTiming = timing.CommandBufferTiming
	TimingComparison    = timing.TimingComparison

	// Timing profiler types (gputrace-107)
	TimingExtractorProfilerRaw = timing.TimingExtractorProfilerRaw
	ProfilerRawTiming          = timing.ProfilerRawTiming

	// Correlation types (gputrace-96)
	CorrelatedShaderMetrics = shader.CorrelatedShaderMetrics
	ShaderCorrelationReport = shader.ShaderCorrelationReport

	// Insights types (gputrace-97)
	PerformanceInsight = analysis.PerformanceInsight
	InsightsReport     = analysis.InsightsReport
	InsightType        = analysis.InsightType
	InsightSeverity    = analysis.InsightSeverity

	// Pipeline function mapping types
	PipelineFunctionMap = trace.PipelineFunctionMap
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

// Re-export functions
var (
	ExtractTimingData             = timing.ExtractTimingData
	ExtractStore0Timing           = timing.ExtractStore0Timing
	ConvertStore0ToEncoderTimings = timing.ConvertStore0ToEncoderTimings
	GenerateSyntheticTiming       = timing.GenerateSyntheticTiming
	ExtractShaderMetrics          = shader.ExtractShaderMetrics
	NewShaderSourceMapper         = shader.NewShaderSourceMapper
	FormatShadersXcodeStyle       = shader.FormatShadersXcodeStyle
	ParseDetailedCommandBuffer    = command.ParseDetailedCommandBuffer
	DumpCommandBuffer             = command.DumpCommandBuffer
	ToPprof           = export.ToPprof
	ToPprofWithSource  = export.ToPprofWithSource
	ToPprofWithMetrics = export.ToPprofWithMetrics
	ParseXcodeCountersCSV         = counter.ParseXcodeCountersCSV
	ExtractStatistics             = analysis.ExtractStatistics
	NewTimingMetricsExtractor     = timing.NewTimingMetricsExtractor
	ParsePerfCounters             = counter.ParsePerfCounters

	// Buffer access analysis functions (gputrace-93)
	AnalyzeBufferAccess      = analysis.AnalyzeBufferAccess
	FormatBufferAccessReport = analysis.FormatBufferAccessReport

	// Buffer timeline functions (gputrace-94)
	ExtractBufferTimeline       = analysis.ExtractBufferTimeline
	FormatBufferTimelineASCII   = analysis.FormatBufferTimelineASCII
	FormatBufferTimelineSummary = analysis.FormatBufferTimelineSummary

	// Buffer diff functions (gputrace-95)
	ExtractBufferSizes = analysis.ExtractBufferSizes
	CompareBuffers     = analysis.CompareBuffers
	FormatBufferDiff   = analysis.FormatBufferDiff

	// Counter export functions (gputrace-101)
	NewCountersCSVExporter = counter.NewCountersCSVExporter

	// Counter sampling functions (gputrace-104)
	FormatCounterSamplingSimulation = replay.FormatCounterSamplingSimulation
	FormatCounterSamplingResult     = counter.FormatCounterSamplingResult

	// Replay engine functions (gputrace-103, gputrace-104)
	NewReplayEngine        = replay.NewReplayEngine
	FormatReplayPlan       = replay.FormatReplayPlan
	FormatReplayValidation = replay.FormatReplayValidation
	FormatReplayAnalysis   = replay.FormatReplayAnalysis

	// Shader source attribution functions (gputrace-105)
	ExtractShaderSourceAttribution    = shader.ExtractShaderSourceAttribution
	FormatShaderSourceAttribution     = shader.FormatShaderSourceAttribution
	FormatShaderSourceAttributionHTML = shader.FormatShaderSourceAttributionHTML

	// Timing metrics functions (gputrace-106)
	FormatTimingMetrics     = timing.FormatTimingMetrics
	ExportTimingMetricsJSON = timing.ExportTimingMetricsJSON
	ExportTimingMetricsCSV  = timing.ExportTimingMetricsCSV
	CompareTraces           = timing.CompareTraces
	FormatTimingComparison  = timing.FormatTimingComparison

	// Timing profiler functions (gputrace-107)
	NewTimingExtractorProfilerRaw = timing.NewTimingExtractorProfilerRaw

	// Shader export functions (gputrace-98)
	FormatShaderMetricsReport = shader.FormatShaderMetricsReport
	ExportShaderMetricsCSV    = shader.ExportShaderMetricsCSV
	ExportShaderMetricsJSON   = shader.ExportShaderMetricsJSON

	// Correlation functions (gputrace-96)
	CorrelateShaderMetrics  = shader.CorrelateShaderMetrics
	FormatCorrelationReport = shader.FormatCorrelationReport

	// Insights functions (gputrace-97)
	GenerateInsights     = analysis.GenerateInsights
	FormatInsightsReport = analysis.FormatInsightsReport
)

// Open opens and parses a .gputrace bundle.
func Open(path string) (*Trace, error) {
	return trace.Open(path)
}
