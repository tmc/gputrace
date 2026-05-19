//go:build darwin

package counter

import (
	"os"
	"testing"
)

func TestParseStreamData(t *testing.T) {
	// Use the MLP trace from /tmp if available
	gpuprofDir := "/tmp/bench_traces/BenchmarkQwen25_MLP_Go-perfdata.gputrace/BenchmarkQwen25_MLP_Go_.gputrace.gpuprofiler_raw"

	if _, err := os.Stat(gpuprofDir); os.IsNotExist(err) {
		t.Skip("Test trace not available")
	}

	stats, err := ParseStreamData(gpuprofDir)
	if err != nil {
		t.Fatalf("ParseStreamData failed: %v", err)
	}

	t.Logf("Found %d pipelines", stats.NumPipelines)
	t.Logf("Found %d function names", len(stats.FunctionNames))
	t.Logf("Found %d encoders with timing data", stats.NumEncoders)
	t.Logf("Found %d dispatches", stats.NumGPUCommands)
	t.Logf("Total GPU time: %d us (%.2f ms)", stats.TotalTimeUs, float64(stats.TotalTimeUs)/1000)

	// Print encoder timings
	if len(stats.EncoderTimings) > 0 {
		t.Log("\nEncoder timings:")
		for _, et := range stats.EncoderTimings {
			t.Logf("  Encoder %d: offset=%d us, duration=%d us", et.Index, et.EndOffsetMicros, et.DurationMicros)
		}
	}

	// Print pipeline stats with new fields
	t.Log("\nPipeline stats:")
	for i, p := range stats.Pipelines {
		t.Logf("  Pipeline %d: addr=0x%x func=%q", i, p.PipelineAddress, p.FunctionName)
		t.Logf("    Instructions: total=%d ALU=%d FP32=%d FP16=%d INT32=%d branch=%d",
			p.InstructionCount, p.ALUInstructionCount, p.FP32InstructionCount,
			p.FP16InstructionCount, p.INT32InstructionCount, p.BranchInstructionCount)
		t.Logf("    Memory: load=%d store=%d tg_load=%d tg_store=%d",
			p.DeviceLoadCount, p.DeviceStoreCount, p.ThreadgroupLoadCount, p.ThreadgroupStoreCount)
		t.Logf("    Registers: temp=%d uniform=%d spill=%d",
			p.TemporaryRegisterCount, p.UniformRegisterCount, p.SpilledBytes)
	}

	// Print dispatch timings
	t.Log("\nDispatch timings (first 15):")
	for i, d := range stats.Dispatches {
		if i >= 15 {
			t.Logf("  ... (%d more)", len(stats.Dispatches)-15)
			break
		}
		t.Logf("  [%d] pipeline=%d dur=%d us func=%q", d.Index, d.PipelineIndex, d.DurationUs, d.FunctionName)
	}

	// Print function names
	t.Log("\nFunction names:")
	for i, fn := range stats.FunctionNames {
		t.Logf("  [%d] %s", i, fn)
	}
}

func TestTimelineTimingTotals(t *testing.T) {
	info := &TimelineInfo{
		TimebaseNumer: 125,
		TimebaseDenom: 3,
		CommandBufferTimestamps: []CommandBufferTimestamp{
			{Index: 0, StartTicks: 100, EndTicks: 124},
			{Index: 1, StartTicks: 200, EndTicks: 248},
		},
		RestoreTimestamps: []TimestampRange{
			{Index: 0, StartTicks: 124, EndTicks: 200},
		},
	}
	info.computeTimingTotals()

	if got, want := info.CommandBufferActiveNs, uint64(3000); got != want {
		t.Fatalf("CommandBufferActiveNs = %d, want %d", got, want)
	}
	if got, want := info.CommandBufferWallNs, uint64(6166); got != want {
		t.Fatalf("CommandBufferWallNs = %d, want %d", got, want)
	}
	if got, want := info.RestoreActiveNs, uint64(3166); got != want {
		t.Fatalf("RestoreActiveNs = %d, want %d", got, want)
	}
}

func TestCommandBufferTimestampDurationRejectsNegativeRange(t *testing.T) {
	cb := CommandBufferTimestamp{StartTicks: 200, EndTicks: 100}
	if got := cb.DurationNs(125, 3); got != 0 {
		t.Fatalf("DurationNs = %d, want 0", got)
	}
}
