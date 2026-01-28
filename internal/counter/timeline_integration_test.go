//go:build darwin

package counter

import (
	"testing"
)

func TestAPSTimelineDataExtraction(t *testing.T) {
	dir := "/tmp/bench_traces/BenchmarkQwen25_MLP_Go-perfdata.gputrace/BenchmarkQwen25_MLP_Go_.gputrace.gpuprofiler_raw"
	
	stats, err := ParseStreamData(dir)
	if err != nil {
		t.Skipf("Test trace not available: %v", err)
	}
	
	t.Logf("APSTimelineData blobs found: %d", len(stats.APSTimelineData))
	
	if stats.Timeline == nil {
		t.Error("Timeline should not be nil")
		return
	}
	
	t.Logf("Timebase: %d/%d (%.2f ns/tick)", 
		stats.Timeline.TimebaseNumer, stats.Timeline.TimebaseDenom,
		float64(stats.Timeline.TimebaseNumer)/float64(stats.Timeline.TimebaseDenom))
	t.Logf("Absolute Time: %d", stats.Timeline.AbsoluteTime)
	t.Logf("Command Buffers: %d", len(stats.Timeline.CommandBufferTimestamps))
	
	// Verify timebase is reasonable (should be 125/3 = 41.67 ns/tick on Apple Silicon)
	if stats.Timeline.TimebaseNumer == 0 || stats.Timeline.TimebaseDenom == 0 {
		t.Error("Timebase should not be zero")
	}
	
	// Verify we have CB timestamps
	if len(stats.Timeline.CommandBufferTimestamps) == 0 {
		t.Error("Should have command buffer timestamps")
	}
	
	// Log CB timestamps
	for i, cb := range stats.Timeline.CommandBufferTimestamps {
		durNs := cb.DurationNs(stats.Timeline.TimebaseNumer, stats.Timeline.TimebaseDenom)
		t.Logf("  CB[%d]: start=%d end=%d duration=%.2f µs",
			i, cb.StartTicks, cb.EndTicks, float64(durNs)/1000)
	}
}
