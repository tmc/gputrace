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
	"github.com/tmc/gputrace/internal/analysis"
	"github.com/tmc/gputrace/internal/counter"
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
