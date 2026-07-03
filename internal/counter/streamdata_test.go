//go:build darwin

package counter

import (
	"testing"
)

const streamDataIntegrationDirEnv = "GPUTRACE_COUNTER_STREAMDATA_DIR"

func TestParseStreamDataIntegration(t *testing.T) {
	gpuprofDir := integrationPathFromEnv(t, streamDataIntegrationDirEnv)
	stats, err := ParseStreamData(gpuprofDir, nil)
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

func TestDispatchInfoDisplayNameUsesPipelineID(t *testing.T) {
	d := DispatchInfo{PipelineIndex: 0, PipelineID: 2288}
	if got, want := d.DisplayName(), "(pipeline_2288)"; got != want {
		t.Fatalf("DisplayName = %q, want %q", got, want)
	}

	d.FunctionName = "kernel0"
	if got, want := d.DisplayName(), "kernel0"; got != want {
		t.Fatalf("DisplayName = %q, want %q", got, want)
	}
}

func TestAttachPipelineMetadataUsesPipelineID(t *testing.T) {
	pipelines := []PipelineStats{
		{PipelineID: 200, InstructionCount: 20},
		{PipelineID: 100, InstructionCount: 10},
	}
	infos := []pipelineInfo{
		{ID: 100, Address: 0x1000, FunctionName: "first"},
		{ID: 200, Address: 0x2000, FunctionName: "second"},
	}

	attachPipelineMetadata(pipelines, infos, nil)

	if pipelines[0].PipelineAddress != 0x2000 || pipelines[0].FunctionName != "second" {
		t.Fatalf("pipeline 200 metadata = addr 0x%x func %q, want addr 0x2000 func second", pipelines[0].PipelineAddress, pipelines[0].FunctionName)
	}
	if pipelines[1].PipelineAddress != 0x1000 || pipelines[1].FunctionName != "first" {
		t.Fatalf("pipeline 100 metadata = addr 0x%x func %q, want addr 0x1000 func first", pipelines[1].PipelineAddress, pipelines[1].FunctionName)
	}

	nameByIndex, idByIndex := pipelineDispatchMaps(infos, nil)
	if idByIndex[0] != 100 || nameByIndex[0] != "first" {
		t.Fatalf("dispatch pipeline 0 = id %d func %q, want id 100 func first", idByIndex[0], nameByIndex[0])
	}
	if idByIndex[1] != 200 || nameByIndex[1] != "second" {
		t.Fatalf("dispatch pipeline 1 = id %d func %q, want id 200 func second", idByIndex[1], nameByIndex[1])
	}
}
