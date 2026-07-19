package gputrace

import (
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/replay"
)

// ParseXcodeCountersCSV parses an Xcode counters CSV file for t.
func ParseXcodeCountersCSV(t *Trace, csvPath string) (*XcodeCounterData, error) {
	return counter.ParseXcodeCountersCSV(t, csvPath)
}

// ParsePerfCounters parses performance counters from t.
func ParsePerfCounters(t *Trace) (*PerfCounterStats, error) {
	return counter.ParsePerfCounters(t)
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
