package gputrace_test

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/analysis"
	"github.com/tmc/gputrace/internal/command"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/replay"
	"github.com/tmc/gputrace/internal/shader"
	"github.com/tmc/gputrace/internal/timing"
)

var (
	_ func(*gputrace.Trace) ([]*gputrace.EncoderTiming, error)                                                  = gputrace.ExtractTimingData
	_ func(*gputrace.Trace) (*timing.Store0TimingData, error)                                                   = gputrace.ExtractStore0Timing
	_ func(*gputrace.Trace, *timing.Store0TimingData) []*gputrace.EncoderTiming                                 = gputrace.ConvertStore0ToEncoderTimings
	_ func(*gputrace.Trace) []*gputrace.EncoderTiming                                                           = gputrace.GenerateSyntheticTiming
	_ func(*gputrace.Trace) (*gputrace.ShaderMetricsReport, error)                                              = gputrace.ExtractShaderMetrics
	_ func(...string) *gputrace.ShaderSourceMapper                                                              = gputrace.NewShaderSourceMapper
	_ func(io.Writer, *gputrace.ShaderMetricsReport) error                                                      = gputrace.FormatShadersSimple
	_ func(io.Writer, *gputrace.ShaderMetricsReport, *gputrace.Trace, bool) error                               = gputrace.FormatShadersXcodeStyle
	_ func(*gputrace.Trace, int) (*command.DetailedCommandBuffer, error)                                        = gputrace.ParseDetailedCommandBuffer
	_ func(*gputrace.Trace, io.Writer, int) error                                                               = gputrace.DumpCommandBuffer
	_ func(*gputrace.Trace, []*gputrace.EncoderTiming) (*profile.Profile, error)                                = gputrace.ToPprof
	_ func(*gputrace.Trace, []*gputrace.EncoderTiming, *gputrace.ShaderSourceMapper) (*profile.Profile, error)  = gputrace.ToPprofWithSource
	_ func(*gputrace.Trace, *gputrace.ShaderSourceMapper, *gputrace.PerfCounterStats) (*profile.Profile, error) = gputrace.ToPprofWithMetrics
	_ func(*gputrace.Trace, string) (*gputrace.XcodeCounterData, error)                                         = gputrace.ParseXcodeCountersCSV
	_ func(*gputrace.Trace) (*gputrace.TraceStatistics, error)                                                  = gputrace.ExtractStatistics
	_ func(*gputrace.Trace) *gputrace.TimingMetricsExtractor                                                    = gputrace.NewTimingMetricsExtractor
	_ func(*gputrace.Trace) (*gputrace.PerfCounterStats, error)                                                 = gputrace.ParsePerfCounters
	_ func(*gputrace.Trace) (*gputrace.BufferAccessAnalysis, error)                                             = gputrace.AnalyzeBufferAccess
	_ func(*gputrace.BufferAccessAnalysis, bool) string                                                         = gputrace.FormatBufferAccessReport
	_ func(*gputrace.Trace) (*gputrace.BufferTimelineAnalysis, error)                                           = gputrace.ExtractBufferTimeline
	_ func(*gputrace.BufferTimelineAnalysis, int) string                                                        = gputrace.FormatBufferTimelineASCII
	_ func(*gputrace.BufferTimelineAnalysis) string                                                             = gputrace.FormatBufferTimelineSummary
	_ func(*gputrace.Trace) (*analysis.BufferSizeInfo, error)                                                   = gputrace.ExtractBufferSizes
	_ func(*analysis.BufferSizeInfo, *analysis.BufferSizeInfo) *analysis.BufferDiff                             = gputrace.CompareBuffers
	_ func(*analysis.BufferDiff, string, string) string                                                         = gputrace.FormatBufferDiff
	_ func(*gputrace.Trace) *counter.CountersCSVExporter                                                        = gputrace.NewCountersCSVExporter
	_ func(*replay.CounterSamplingSimulation) string                                                            = gputrace.FormatCounterSamplingSimulation
	_ func(*counter.CounterSamplingResult) string                                                               = gputrace.FormatCounterSamplingResult
	_ func(*gputrace.Trace) *replay.ReplayEngine                                                                = gputrace.NewReplayEngine
	_ func(*gputrace.Trace, string) (*shader.ShaderSourceAttribution, error)                                    = gputrace.ExtractShaderSourceAttribution
	_ func(*shader.ShaderSourceAttribution, bool) string                                                        = gputrace.FormatShaderSourceAttribution
	_ func(*shader.ShaderSourceAttribution) string                                                              = gputrace.FormatShaderSourceAttributionHTML
	_ func(*gputrace.TimingMetrics) string                                                                      = gputrace.FormatTimingMetrics
	_ func(io.Writer, *gputrace.TimingMetrics) error                                                            = gputrace.ExportTimingMetricsJSON
	_ func(io.Writer, *gputrace.TimingMetrics) error                                                            = gputrace.ExportTimingMetricsCSV
	_ func(*gputrace.TimingMetrics, *gputrace.TimingMetrics) *timing.TimingComparison                           = gputrace.CompareTraces
	_ func(*timing.TimingComparison) string                                                                     = gputrace.FormatTimingComparison
	_ func(*gputrace.Trace) *timing.TimingExtractorProfilerRaw                                                  = gputrace.NewTimingExtractorProfilerRaw
	_ func(io.Writer, *gputrace.ShaderMetricsReport) error                                                      = gputrace.ExportShaderMetricsCSV
	_ func(io.Writer, *gputrace.ShaderMetricsReport) error                                                      = gputrace.ExportShaderMetricsJSON
	_ func(*gputrace.Trace) (*shader.ShaderCorrelationReport, error)                                            = gputrace.CorrelateShaderMetrics
	_ func(*shader.ShaderCorrelationReport) string                                                              = gputrace.FormatCorrelationReport
	_ func(*gputrace.Trace) (*gputrace.InsightsReport, error)                                                   = gputrace.GenerateInsights
	_ func(*gputrace.InsightsReport) string                                                                     = gputrace.FormatInsightsReport
)

func TestFacadeCalls(t *testing.T) {
	tracePath := filepath.Join("testdata", "traces", "01-single-encoder", "01-single-encoder-run1.gputrace")
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := gputrace.ExtractStatistics(trace); err != nil {
		t.Fatalf("ExtractStatistics: %v", err)
	}
	if timings := gputrace.GenerateSyntheticTiming(trace); len(timings) == 0 {
		t.Fatal("GenerateSyntheticTiming returned no timings")
	}
	if extractor := gputrace.NewTimingMetricsExtractor(trace); extractor == nil {
		t.Fatal("NewTimingMetricsExtractor returned nil")
	}
}
