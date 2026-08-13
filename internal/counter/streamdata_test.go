//go:build darwin

package counter

import (
	"testing"

	"github.com/tmc/apple/x/plist"
)

const streamDataIntegrationDirEnv = "GPUTRACE_COUNTER_STREAMDATA_DIR"

func TestArchivedString(t *testing.T) {
	objects := []any{
		"$null",
		"AGXMetalG16X",
		map[string]any{"NS.string": "Apple M4 Max"},
		map[string]any{"NS.string": plist.UID(1)},
	}
	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "direct", value: "G16C", want: "G16C"},
		{name: "uid", value: plist.UID(1), want: "AGXMetalG16X"},
		{name: "archived NSString", value: plist.UID(2), want: "Apple M4 Max"},
		{name: "nested uid", value: plist.UID(3), want: "AGXMetalG16X"},
		{name: "out of range", value: plist.UID(9)},
		{name: "wrong type", value: uint64(1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := archivedString(objects, test.value); got != test.want {
				t.Fatalf("archivedString() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseStreamDataMetadataPreservesZeroAndFalse(t *testing.T) {
	objects := []any{
		"$null", int64(0), int64(5), "trace.gputrace", true,
		map[string]any{"NS.data": make([]byte, 10)},
		map[string]any{"NS.objects": []any{plist.UID(1), plist.UID(2)}},
		map[string]any{"NS.objects": []any{}},
	}
	metadata := parseStreamDataMetadata(objects, map[string]any{
		"version":                      plist.UID(2),
		"traceName":                    plist.UID(3),
		"profiledExecutionMode":        plist.UID(1),
		"dataSourceHasUnusedResources": false,
		"supportsSeparateAPSData":      plist.UID(4),
		"numBlitCalls":                 int64(0),
		"encoderInfoData":              plist.UID(5),
		"encoderInfoSize":              int64(4),
		"functionInfoData":             plist.UID(5),
		"APSData":                      plist.UID(6),
		"shaderProfilerData":           plist.UID(7),
	})
	if metadata.Version == nil || *metadata.Version != 5 || metadata.TraceName != "trace.gputrace" {
		t.Fatalf("identity metadata = %#v", metadata)
	}
	if metadata.ProfiledExecutionMode == nil || *metadata.ProfiledExecutionMode != 0 {
		t.Fatalf("profiled execution mode = %#v, want recorded zero", metadata.ProfiledExecutionMode)
	}
	if metadata.DataSourceHasUnusedResources == nil || *metadata.DataSourceHasUnusedResources {
		t.Fatalf("unused resources = %#v, want recorded false", metadata.DataSourceHasUnusedResources)
	}
	if metadata.SupportsSeparateAPSData == nil || !*metadata.SupportsSeparateAPSData {
		t.Fatalf("supports separate APS data = %#v, want true", metadata.SupportsSeparateAPSData)
	}
	if metadata.NumBlitCalls == nil || *metadata.NumBlitCalls != 0 {
		t.Fatalf("num blit calls = %#v, want recorded zero", metadata.NumBlitCalls)
	}
	if metadata.ProfiledProfilerMode != nil {
		t.Fatalf("absent profiled profiler mode = %#v, want nil", metadata.ProfiledProfilerMode)
	}
	if table := metadata.Tables.Encoders; table == nil || table.Bytes != 10 || table.RecordSize == nil || *table.RecordSize != 4 || table.RecordCount == nil || *table.RecordCount != 2 || table.RemainderBytes == nil || *table.RemainderBytes != 2 {
		t.Fatalf("encoder table = %#v", table)
	}
	if table := metadata.Tables.Functions; table == nil || table.Bytes != 10 || table.RecordSize != nil || table.RecordCount != nil || table.RemainderBytes != nil {
		t.Fatalf("function table without size = %#v", table)
	}
	if metadata.Tables.CommandBuffers != nil {
		t.Fatalf("absent command buffer table = %#v", metadata.Tables.CommandBuffers)
	}
	if metadata.Families.APSData == nil || *metadata.Families.APSData != 2 {
		t.Fatalf("APSData entries = %#v, want 2", metadata.Families.APSData)
	}
	if metadata.Families.ShaderProfilerData == nil || *metadata.Families.ShaderProfilerData != 0 {
		t.Fatalf("shader profiler entries = %#v, want recorded zero", metadata.Families.ShaderProfilerData)
	}
	if metadata.Families.APSTimelineData != nil {
		t.Fatalf("absent APSTimelineData entries = %#v, want nil", metadata.Families.APSTimelineData)
	}
}

func TestDecodedStreamDataFamiliesCountsOnlyNSData(t *testing.T) {
	objects := []any{
		"$null",
		map[string]any{"NS.data": []byte("one")},
		map[string]any{"NS.data": []byte("two")},
		map[string]any{"not-data": true},
		map[string]any{"NS.objects": []any{plist.UID(1), plist.UID(2), plist.UID(3), "not-a-uid", plist.UID(99)}},
		map[string]any{"NS.objects": []any{}},
	}
	got := decodedStreamDataFamilies(objects, map[string]any{
		"APSData":         plist.UID(4),
		"APSTimelineData": plist.UID(5),
		"APSCounterData":  plist.UID(3),
	})
	if got.APSData == nil || *got.APSData != 2 {
		t.Fatalf("APSData decoded blobs = %#v, want 2", got.APSData)
	}
	if got.APSTimelineData == nil || *got.APSTimelineData != 0 {
		t.Fatalf("APSTimelineData decoded blobs = %#v, want recorded zero", got.APSTimelineData)
	}
	if got.APSCounterData != nil {
		t.Fatalf("malformed APSCounterData = %#v, want nil", got.APSCounterData)
	}
	if got.ShaderProfilerData != nil {
		t.Fatalf("absent shaderProfilerData = %#v, want nil", got.ShaderProfilerData)
	}
}

func TestSummarizeCounterArchive(t *testing.T) {
	got := summarizeCounterArchive(&CounterArchive{
		Encoders:           []EncoderSamples{{}, {}},
		TotalSamples:       20,
		AttributedSamples:  7,
		MachineWideSamples: 11,
		KnownEncoderIDs:    4,
		PassColumns:        [][]string{{"a"}, {"b"}},
		TraceIDs:           &TraceIDTable{Rows: []TraceIDInfo{{}, {}, {}}},
		Blobs:              5,
		StrideMismatches:   1,
	})
	if got == nil || got.DecodedSamples != 20 || got.AttributedSamples != 7 || got.MachineWideSamples != 11 || got.UnattributedSamples != 2 || got.EncoderAggregates != 2 || got.PassColumnGroups != 2 || got.TraceIDRows != 3 || got.GPRWCNTRBlobs != 5 || got.StrideMismatchBlobs != 1 {
		t.Fatalf("counter decode summary = %#v", got)
	}
	if summarizeCounterArchive(nil) != nil {
		t.Fatal("nil counter archive produced a summary")
	}
}

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
