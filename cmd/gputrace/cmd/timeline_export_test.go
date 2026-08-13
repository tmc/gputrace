//go:build darwin

package cmd

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/mlxsemantic"
	"github.com/tmc/gputrace/internal/perfetto"
	tracepkg "github.com/tmc/gputrace/internal/trace"
	"github.com/tmc/gputrace/internal/xcodebindings"
)

func TestExportChromeTracingIncludesTimingMetadata(t *testing.T) {
	effective := uint64(1650625)
	timeline := &Timeline{
		Timing: &TimelineTiming{
			EncoderSpanNs:         9876000,
			DispatchSpanNs:        10117000,
			EffectiveGPUTimeNs:    &effective,
			CommandBufferActiveNs: 2246081,
			CommandBufferWallNs:   356626625,
			DisplayDurationNs:     effective,
			DisplayDurationSource: "APSTimelineData ReplayerGPUTime",
			TimingSource:          "APSTimelineData ReplayerGPUTime (Xcode Effective GPU Time)",
		},
	}

	out := filepath.Join(t.TempDir(), "timeline.json")
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	var doc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
		OtherData   struct {
			GputraceTiming       map[string]interface{} `json:"gputrace_timing"`
			GputraceXcodeMetrics map[string]interface{} `json:"gputrace_xcode_metrics"`
		} `json:"otherData"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got := uint64(doc.OtherData.GputraceTiming["display_duration_ns"].(float64)); got != effective {
		t.Fatalf("gputrace_timing display_duration_ns = %d, want %d", got, effective)
	}
	if got := doc.OtherData.GputraceXcodeMetrics["has_effective_gpu_time"]; got != true {
		t.Fatalf("has_effective_gpu_time = %v, want true", got)
	}
	bindings := doc.OtherData.GputraceXcodeMetrics["binding_candidates"].(map[string]interface{})
	if got, want := bindings["high_register"], "GTMioShaderBinaryData.LiveRegisterForInstructionAtIndex"; got != want {
		t.Fatalf("binding candidate high_register = %v, want %q", got, want)
	}

	var foundSummary, foundCoverage bool
	for _, ev := range doc.TraceEvents {
		if ev.Name == "Xcode Timing Summary" && ev.Category == "xcode_timing" {
			foundSummary = true
			if got := ev.Args["timing_source"]; got != timeline.Timing.TimingSource {
				t.Fatalf("summary timing_source = %v, want %q", got, timeline.Timing.TimingSource)
			}
		}
		if ev.Name == "Xcode Metrics Coverage" && ev.Category == "xcode_metrics" {
			foundCoverage = true
			if got := ev.Args["has_effective_gpu_time"]; got != true {
				t.Fatalf("coverage has_effective_gpu_time = %v, want true", got)
			}
		}
	}
	if !foundSummary {
		t.Fatal("missing Xcode Timing Summary event")
	}
	if !foundCoverage {
		t.Fatal("missing Xcode Metrics Coverage event")
	}
}

func TestApplyXcodeGPUTime(t *testing.T) {
	timeline := &Timeline{Timing: &TimelineTiming{TimingSource: "APSTimelineData"}}
	applyXcodeGPUTime(timeline, 9_161_250)
	if timeline.Timing.EffectiveGPUTimeNs == nil || *timeline.Timing.EffectiveGPUTimeNs != 9_161_250 {
		t.Fatalf("effective GPU time = %#v, want 9161250", timeline.Timing.EffectiveGPUTimeNs)
	}
	if got, want := timeline.Timing.DisplayDurationNs, uint64(9_161_250); got != want {
		t.Fatalf("display duration = %d, want %d", got, want)
	}
	if !strings.Contains(timeline.Timing.DisplayDurationSource, "Xcode Overview GPU Time") {
		t.Fatalf("display duration source = %q", timeline.Timing.DisplayDurationSource)
	}
	if !strings.Contains(timeline.Timing.TimingSource, "Xcode Overview GPU Time") {
		t.Fatalf("timing source = %q", timeline.Timing.TimingSource)
	}
}

func TestExportChromeTracingDoesNotMutateTimelineEvents(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{{
			Name:      "kernel",
			Category:  "kernel",
			Phase:     "X",
			Timestamp: 10,
			Duration:  5,
			ProcessID: 1,
			ThreadID:  3,
		}},
		CounterTracks: []CounterTrack{{
			Name: "ALU Utilization",
			Unit: "%",
			Samples: []CounterSample{
				{Timestamp: 20, Value: 42},
			},
		}},
	}

	out := filepath.Join(t.TempDir(), "timeline.json")
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}
	if got, want := len(timeline.Events), 1; got != want {
		t.Fatalf("timeline events after export = %d, want %d", got, want)
	}
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("second exportChromeTracing: %v", err)
	}
	if got, want := len(timeline.Events), 1; got != want {
		t.Fatalf("timeline events after second export = %d, want %d", got, want)
	}
}

func TestExportChromeTracingKeepsContainedDispatchOnEncoderTrack(t *testing.T) {
	timeline := &Timeline{Events: []TimelineEvent{
		{
			Name:      "encoder",
			Category:  "encoder",
			Phase:     "X",
			Timestamp: 10,
			Duration:  20,
			ProcessID: 1,
			ThreadID:  1,
		},
		{
			Name:      "kernel",
			Category:  "kernel",
			Phase:     "X",
			Timestamp: 12,
			Duration:  5,
			ProcessID: 1,
			ThreadID:  1,
			Args: map[string]interface{}{
				"encoder_containment": "strict",
				"function_name":       "kernel",
				"pipeline_state":      "0x1234",
			},
		},
	}}

	out := filepath.Join(t.TempDir(), "timeline.json")
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var doc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	for _, event := range doc.TraceEvents {
		if event.Name == "kernel" && event.Category == "kernel" {
			if got, want := event.ThreadID, 1; got != want {
				t.Fatalf("kernel tid = %d, want encoder tid %d", got, want)
			}
			return
		}
	}
	t.Fatal("missing kernel event")
}

func TestTimelineMetadataForActiveTracks(t *testing.T) {
	events := []TimelineEvent{
		{Name: "process_name", Category: "__metadata", Phase: "M", ProcessID: 1},
		{Name: "thread_name", Category: "__metadata", Phase: "M", ProcessID: 1, ThreadID: 1},
		{Name: "thread_name", Category: "__metadata", Phase: "M", ProcessID: 1, ThreadID: 2},
		{Name: "kernel", Category: "kernel", Phase: "X", ProcessID: 1, ThreadID: 1},
	}

	got := timelineMetadataForActiveTracks(events)
	if len(got) != 3 {
		t.Fatalf("events after filtering = %d, want 3", len(got))
	}
	for _, event := range got {
		if event.Name == "thread_name" && event.ThreadID == 2 {
			t.Fatal("metadata retained an unused track")
		}
	}
}

func TestTimelineClockProvenance(t *testing.T) {
	for _, test := range []struct {
		clock    timelineClock
		included []string
	}{
		{clock: timelineClockBusy, included: []string{"encoder", "kernel", "counter"}},
		{clock: timelineClockWall, included: []string{"command_buffer", "restore", "profiler_stream", "gprwcntr"}},
	} {
		t.Run(string(test.clock), func(t *testing.T) {
			args := timelineClockProvenance(test.clock)
			if got := args["clock_domain"]; got != string(test.clock) {
				t.Fatalf("clock_domain = %q, want %q", got, test.clock)
			}
			got := args["included_categories"].([]string)
			if strings.Join(got, ",") != strings.Join(test.included, ",") {
				t.Fatalf("included_categories = %#v, want %#v", got, test.included)
			}
			if args["clock_mapping"] == "" {
				t.Fatal("clock_mapping is empty")
			}
		})
	}
}

func TestExportChromeTracingCounterTrackMetadataIncludesXcodeProvenance(t *testing.T) {
	timeline := &Timeline{CounterTracks: []CounterTrack{{
		Name:             "ALU Utilization",
		Unit:             "Percentage of Peak ALU Performance",
		XcodeGroups:      []string{"ALU"},
		XcodeCatalogPath: "/Applications/Xcode-rc.app/GPUCounterGraph.plist",
		Samples:          []CounterSample{{Timestamp: 20, Value: 42}},
	}}}

	out := filepath.Join(t.TempDir(), "timeline.json")
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var doc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	for _, event := range doc.TraceEvents {
		if event.Name != "thread_name" || event.Args["xcode_catalog_path"] == nil {
			continue
		}
		if got, want := event.Args["xcode_catalog_path"], timeline.CounterTracks[0].XcodeCatalogPath; got != want {
			t.Fatalf("catalog path = %q, want %q", got, want)
		}
		groups, ok := event.Args["xcode_groups"].([]interface{})
		if !ok || len(groups) != 1 || groups[0] != "ALU" {
			t.Fatalf("groups = %#v, want [ALU]", event.Args["xcode_groups"])
		}
		return
	}
	t.Fatal("missing counter metadata with Xcode provenance")
}

func TestExportChromeTracingStdoutWritesCleanJSON(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{{
			Name:      "kernel",
			Category:  "kernel",
			Phase:     "X",
			Timestamp: 10,
			Duration:  5,
			ProcessID: 1,
			ThreadID:  3,
		}},
	}

	out, err := captureStdout(t, func() error {
		return exportChromeTracing(timeline, "/dev/stdout")
	})
	if err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}

	var doc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not clean JSON: %v\n%s", err, out)
	}
	if len(doc.TraceEvents) == 0 {
		t.Fatalf("stdout JSON contains no trace events:\n%s", out)
	}
}

func TestExportPerfettoWritesNativeProtobuf(t *testing.T) {
	pstate := 500
	gpuGeneration := uint32(2)
	timeline := &Timeline{
		ClockDomain:     "busy",
		GPUGeneration:   &gpuGeneration,
		MetalDeviceName: "Apple M4 Max",
		MetalPluginName: "AGXMetalG16X",
		AbsoluteTime:    465068216775,
		ContinuousTime:  123456,
		PState:          &pstate,
		TimebaseNumer:   125,
		TimebaseDenom:   3,
		Timing: &TimelineTiming{
			EncoderSpanNs:         20_000,
			DispatchSpanNs:        5_000,
			CommandBufferActiveNs: 30_000,
			CommandBufferWallNs:   50_000,
			RestoreActiveNs:       31_000,
			RestoreWallNs:         51_000,
			DisplayDurationNs:     30_000,
			DisplayDurationSource: "APSTimelineData command buffer active time",
			TimingSource:          "APSTimelineData",
			EncoderTimingSource:   "profiler",
		},
		Events: []TimelineEvent{
			{
				Name:      "encoder",
				Category:  "encoder",
				Phase:     "X",
				Timestamp: 10,
				Duration:  20,
				ProcessID: 1,
				ThreadID:  1,
			},
			{
				Name:      "kernel",
				Category:  "kernel",
				Phase:     "X",
				Timestamp: 12,
				Duration:  5,
				ProcessID: 1,
				ThreadID:  1,
				Args: map[string]interface{}{
					"encoder_containment": "strict",
				},
			},
		},
		CounterTracks: []CounterTrack{{
			Name:        "GPU Cycles",
			Description: "measured cycles",
			Samples:     []CounterSample{{Timestamp: 20_000, Value: 0}},
		}},
		UnavailableEvidence: []UnavailableEvidence{{
			Family: "APSCounterData time series",
			Reason: "counter clock is not joined",
		}},
		UnattributedCounters: []UnattributedCounterMetric{{
			Label:       "kernel0",
			Attribution: "unknown",
			Source:      "PerfCounterStats pipeline row",
			Values:      map[string]interface{}{"alu_utilization_pct": 42.5},
		}},
		MLXSemantics: &mlxsemantic.Sidecar{Schema: mlxsemantic.SchemaV1},
		MLXSemanticReport: &mlxsemantic.Report{
			UsedNodes:        2,
			UnusedNodes:      1,
			MatchedTargets:   map[string]int{"dispatch": 1},
			UnmatchedTargets: map[string]int{"dispatch": 3},
		},
	}
	out := filepath.Join(t.TempDir(), "timeline.pftrace")
	if err := exportPerfettoForClock(timeline, out, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[0] != 0x0a {
		t.Fatalf("native trace starts %x, want protobuf Trace.packet tag 0a", data[:1])
	}
	if json.Valid(data) {
		t.Fatal("native Perfetto output is JSON")
	}
	for _, want := range []string{
		"unavailable_evidence_0_family", "APSCounterData time series", "counter clock is not joined",
		"mlx_semantic_unused_nodes", "mlx_semantic_unmatched_dispatch", perfetto.SchemaRevision,
		"input_content_digest_availability", "unavailable_syscalls", "packet_family_gpu_render_stage_event",
		"exporter_version", "capture_mode_availability", "replay_mode_availability",
		"counter_catalog_availability", "counter_decoder_availability", "raw_counter_artifact_availability",
		"Apple M4 Max", "AGXMetalG16X", "environment_device_name_source", "environment_gpu_generation",
		"untimed_dispatch_count",
		"Unattributed counter metrics: kernel0", "alu_utilization_pct",
		"Unavailable evidence: APSCounterData time series",
		"encoder_span_ns", "dispatch_span_ns", "command_buffer_active_time_ns",
		"display_duration_source", "encoder_timing_source",
		"clock_conversion_availability", "absolute_time", "timebase_numer", "timebase_denom",
		"continuous_time", "continuous_time_availability",
		"pstate", "pstate_availability", "pstate_semantics",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("native trace missing manifest value %q", want)
		}
	}
}

func TestApplyStreamIdentity(t *testing.T) {
	gpuGeneration := uint32(2)
	version := int64(5)
	zero := int64(0)
	recordSize := int64(32)
	recordCount := int64(37)
	remainder := int64(0)
	twoEntries := int64(2)
	zeroEntries := int64(0)
	counterDecode := &counter.StreamDataCounterDecode{DecodedSamples: 20}
	apsInventory := &counter.APSDataInventory{Blobs: 2, Dictionaries: 2, WithAPSTraceDataFile: 1}
	timeline := &Timeline{}
	applyStreamIdentity(timeline, &counter.StreamDataStats{
		GPUGeneration:   &gpuGeneration,
		MetalDeviceName: "Apple M4 Max",
		MetalPluginName: "AGXMetalG16X",
		Metadata: counter.StreamDataMetadata{
			Version:               &version,
			ProfiledExecutionMode: &zero,
			TraceName:             "trace.gputrace",
			Tables: counter.StreamDataTables{CommandBuffers: &counter.StreamDataTable{
				Bytes: 1184, RecordSize: &recordSize, RecordCount: &recordCount, RemainderBytes: &remainder,
			}},
			Families:         counter.StreamDataFamilies{APSData: &twoEntries, ShaderProfilerData: &zeroEntries},
			DecodedFamilies:  counter.StreamDataDecodedFamilies{APSData: &twoEntries, ShaderProfilerData: &zeroEntries},
			CounterDecode:    counterDecode,
			APSDataInventory: apsInventory,
		},
	})
	if timeline.GPUGeneration == nil || *timeline.GPUGeneration != 2 || timeline.MetalDeviceName != "Apple M4 Max" || timeline.MetalPluginName != "AGXMetalG16X" {
		t.Fatalf("stream identity = %#v", timeline)
	}
	if timeline.GPUGeneration == &gpuGeneration {
		t.Fatal("stream identity retained the source pointer")
	}
	if timeline.StreamMetadata == nil || timeline.StreamMetadata.Version == nil || *timeline.StreamMetadata.Version != 5 || timeline.StreamMetadata.ProfiledExecutionMode == nil || *timeline.StreamMetadata.ProfiledExecutionMode != 0 {
		t.Fatalf("stream metadata = %#v", timeline.StreamMetadata)
	}
	if timeline.StreamMetadata.Version == &version || timeline.StreamMetadata.ProfiledExecutionMode == &zero {
		t.Fatal("stream metadata retained source pointers")
	}
	if table := timeline.StreamMetadata.Tables.CommandBuffers; table == nil || table.RecordSize == &recordSize || table.RecordCount == &recordCount || table.RemainderBytes == &remainder {
		t.Fatalf("stream table was not independently cloned: %#v", table)
	}
	if timeline.StreamMetadata.Families.APSData == &twoEntries || timeline.StreamMetadata.Families.ShaderProfilerData == &zeroEntries {
		t.Fatal("stream family counts retained source pointers")
	}
	if timeline.StreamMetadata.DecodedFamilies.APSData == &twoEntries || timeline.StreamMetadata.DecodedFamilies.ShaderProfilerData == &zeroEntries {
		t.Fatal("decoded stream family counts retained source pointers")
	}
	if timeline.StreamMetadata.CounterDecode == nil || timeline.StreamMetadata.CounterDecode == counterDecode || timeline.StreamMetadata.CounterDecode.DecodedSamples != 20 {
		t.Fatalf("counter decode was not independently cloned: %#v", timeline.StreamMetadata.CounterDecode)
	}
	if timeline.StreamMetadata.APSDataInventory == nil || timeline.StreamMetadata.APSDataInventory == apsInventory || timeline.StreamMetadata.APSDataInventory.WithAPSTraceDataFile != 1 {
		t.Fatalf("APSData inventory was not independently cloned: %#v", timeline.StreamMetadata.APSDataInventory)
	}
}

func TestPerfettoStreamMetadataArgsPreservesZeroAndFalse(t *testing.T) {
	zero := int64(0)
	falseValue := false
	recordSize := int64(4)
	recordCount := int64(2)
	remainder := int64(2)
	twoEntries := int64(2)
	zeroEntries := int64(0)
	args := perfettoStreamMetadataArgs(&counter.StreamDataMetadata{
		ProfiledExecutionMode:        &zero,
		DataSourceHasUnusedResources: &falseValue,
		NumBlitCalls:                 &zero,
		Tables: counter.StreamDataTables{Encoders: &counter.StreamDataTable{
			Bytes: 10, RecordSize: &recordSize, RecordCount: &recordCount, RemainderBytes: &remainder,
		}},
		Families:        counter.StreamDataFamilies{APSData: &twoEntries, ShaderProfilerData: &zeroEntries},
		DecodedFamilies: counter.StreamDataDecodedFamilies{APSData: &zeroEntries, ShaderProfilerData: &zeroEntries},
		CounterDecode: &counter.StreamDataCounterDecode{
			GPRWCNTRBlobs: 3, DecodedSamples: 20, AttributedSamples: 7,
			MachineWideSamples: 11, UnattributedSamples: 2, StrideMismatchBlobs: 1,
		},
		APSDataInventory: &counter.APSDataInventory{
			Blobs: 2, Dictionaries: 2, WithCounterInfo: 1, WithAPSTraceDataFile: 1,
		},
	})
	for _, key := range []string{"stream_data_profiled_execution_mode", "stream_data_has_unused_resources", "stream_data_num_blit_calls"} {
		if _, ok := args[key]; !ok {
			t.Fatalf("%s absent from %#v", key, args)
		}
	}
	if got := args["stream_data_profile_mode_semantics"]; got == "" {
		t.Fatalf("profile mode semantics = %#v", got)
	}
	if got := perfettoStreamMetadataArgs(nil)["stream_data_metadata_availability"]; got == "" {
		t.Fatalf("missing metadata availability = %#v", got)
	}
	if got, want := args["stream_data_encoder_table_integrity"], "incomplete: trailing bytes do not form a complete record"; got != want {
		t.Fatalf("encoder table integrity = %#v, want %#v", got, want)
	}
	if got := args["stream_data_function_table_availability"]; got != "unavailable: archive data key is absent" {
		t.Fatalf("function table availability = %#v", got)
	}
	if got := args["stream_data_aps_data_entry_count"]; got != int64(2) {
		t.Fatalf("APSData entry count = %#v, want 2", got)
	}
	if got := args["stream_data_shader_profiler_data_entry_count"]; got != int64(0) {
		t.Fatalf("shader profiler entry count = %#v, want recorded zero", got)
	}
	if got := args["stream_data_aps_data_decoded_blob_count"]; got != int64(0) {
		t.Fatalf("APSData decoded blob count = %#v, want recorded zero", got)
	}
	if got := args["stream_data_aps_data_non_blob_entry_count"]; got != int64(2) {
		t.Fatalf("APSData non-blob entry count = %#v, want 2", got)
	}
	if got := args["stream_data_aps_timeline_data_decode_availability"]; got != "unavailable: archive array key is absent or malformed" {
		t.Fatalf("APSTimelineData decode availability = %#v", got)
	}
	if got := args["stream_data_counter_decode_decoded_samples"]; got != 20 {
		t.Fatalf("decoded counter samples = %#v, want 20", got)
	}
	if got := args["stream_data_counter_decode_unattributed_samples"]; got != 2 {
		t.Fatalf("unattributed counter samples = %#v, want 2", got)
	}
	if got := args["stream_data_counter_decode_stride_mismatch_blobs"]; got != 1 {
		t.Fatalf("counter stride mismatches = %#v, want 1", got)
	}
	if got := args["stream_data_aps_data_inventory_dictionaries"]; got != 2 {
		t.Fatalf("APSData dictionaries = %#v, want 2", got)
	}
	if got := args["stream_data_aps_data_inventory_with_counter_info"]; got != 1 {
		t.Fatalf("APSData counter-info dictionaries = %#v, want 1", got)
	}
	if got := args["stream_data_gpu_timeline_data_availability"]; got != "unavailable: archive array key is absent or malformed" {
		t.Fatalf("GPU timeline availability = %#v", got)
	}
}

func TestAppendDecodedStreamDataFamilyArgsRejectsImpossibleCount(t *testing.T) {
	entries, blobs := int64(1), int64(2)
	args := make(map[string]any)
	appendDecodedStreamDataFamilyArgs(args, "aps_data", &entries, &blobs)
	if got := args["stream_data_aps_data_decode_availability"]; got != "inconsistent: decoded blob count exceeds archive entry count" {
		t.Fatalf("decode availability = %#v", got)
	}
	if _, ok := args["stream_data_aps_data_non_blob_entry_count"]; ok {
		t.Fatalf("impossible non-blob count was exported: %#v", args)
	}
}

func TestTimelineJSONDistinguishesZeroGPUGenerationFromAbsence(t *testing.T) {
	zero := uint32(0)
	for _, test := range []struct {
		name        string
		timeline    Timeline
		wantPresent bool
	}{
		{name: "absent", timeline: Timeline{}},
		{name: "zero", timeline: Timeline{GPUGeneration: &zero}, wantPresent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.timeline)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			value, ok := got["gpu_generation"]
			if ok != test.wantPresent {
				t.Fatalf("gpu_generation present = %v, want %v: %s", ok, test.wantPresent, data)
			}
			if ok && value != float64(0) {
				t.Fatalf("gpu_generation = %#v, want zero", value)
			}
		})
	}
}

func TestPerfettoClockConversionArgs(t *testing.T) {
	zeroPState := 0
	nonzeroPState := 500
	tests := []struct {
		name       string
		timeline   *Timeline
		available  bool
		continuous bool
		pstate     bool
	}{
		{name: "nil"},
		{name: "missing absolute time", timeline: &Timeline{TimebaseNumer: 125, TimebaseDenom: 3}},
		{name: "missing denominator", timeline: &Timeline{AbsoluteTime: 7, TimebaseNumer: 125}},
		{name: "available", timeline: &Timeline{AbsoluteTime: 465068216775, TimebaseNumer: 125, TimebaseDenom: 3}, available: true},
		{name: "continuous only", timeline: &Timeline{ContinuousTime: 99}, continuous: true},
		{name: "pstate zero", timeline: &Timeline{PState: &zeroPState}, pstate: true},
		{name: "pstate nonzero", timeline: &Timeline{PState: &nonzeroPState}, pstate: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := perfettoClockConversionArgs(test.timeline)
			_, hasAbsolute := got["absolute_time"]
			if hasAbsolute != test.available {
				t.Fatalf("absolute_time present = %v, want %v: %#v", hasAbsolute, test.available, got)
			}
			if got["clock_conversion_domain"] != "wall" || got["clock_conversion_availability"] == "" {
				t.Fatalf("clock conversion receipt = %#v", got)
			}
			_, hasContinuous := got["continuous_time"]
			if hasContinuous != test.continuous {
				t.Fatalf("continuous_time present = %v, want %v: %#v", hasContinuous, test.continuous, got)
			}
			if got["continuous_time_availability"] == "" {
				t.Fatalf("continuous time receipt = %#v", got)
			}
			_, hasPState := got["pstate"]
			if hasPState != test.pstate {
				t.Fatalf("pstate present = %v, want %v: %#v", hasPState, test.pstate, got)
			}
			if got["pstate_availability"] == "" {
				t.Fatalf("pstate receipt = %#v", got)
			}
			if test.available {
				if got["timebase_numer"] != uint64(125) || got["timebase_denom"] != uint64(3) {
					t.Fatalf("timebase = %#v, want 125/3", got)
				}
				if got["clock_conversion_formula"] == "" || got["clock_conversion_source"] == "" {
					t.Fatalf("available clock conversion lacks provenance: %#v", got)
				}
			}
		})
	}
}

func TestTimelineJSONDistinguishesZeroPStateFromAbsence(t *testing.T) {
	zero := 0
	for _, test := range []struct {
		name       string
		timeline   Timeline
		wantPState bool
	}{
		{name: "absent", timeline: Timeline{}},
		{name: "zero", timeline: Timeline{PState: &zero}, wantPState: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.timeline)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			value, ok := got["pstate"]
			if ok != test.wantPState {
				t.Fatalf("pstate present = %v, want %v: %s", ok, test.wantPState, data)
			}
			if ok && value != float64(0) {
				t.Fatalf("pstate = %#v, want zero", value)
			}
		})
	}
}

func TestTimelineUntimedDispatchCount(t *testing.T) {
	timeline := &Timeline{Events: []TimelineEvent{
		{Category: "kernel", Phase: "X", Duration: 10},
		{Category: "kernel", Phase: "i"},
		{Category: "kernel", Phase: "X"},
		{Category: "encoder", Phase: "i"},
	}}
	if got, want := timelineUntimedDispatchCount(timeline), 2; got != want {
		t.Fatalf("untimed dispatch count = %d, want %d", got, want)
	}
}

func TestPerfettoTimingSummaryArgsOmitsUnavailableZero(t *testing.T) {
	args := perfettoTimingSummaryArgs(&TimelineTiming{EncoderTimingSource: "unavailable", EncoderTimingApproximate: true})
	for _, key := range []string{"encoder_span_ns", "dispatch_span_ns", "command_buffer_active_time_ns", "display_duration_ns", "effective_gpu_time_ns"} {
		if _, ok := args[key]; ok {
			t.Errorf("%s is present for unavailable zero timing", key)
		}
	}
	if got := args["encoder_timing_source"]; got != "unavailable" {
		t.Fatalf("encoder_timing_source = %v, want unavailable", got)
	}
	if got := args["encoder_timing_approximate"]; got != true {
		t.Fatalf("encoder_timing_approximate = %v, want true", got)
	}

	zero := uint64(0)
	args = perfettoTimingSummaryArgs(&TimelineTiming{EffectiveGPUTimeNs: &zero})
	if got, ok := args["effective_gpu_time_ns"]; !ok || got != uint64(0) {
		t.Fatalf("effective_gpu_time_ns = %v, %v, want measured zero", got, ok)
	}
}

func TestPerfettoEventArgsAddsTimingProvenanceWithoutMutation(t *testing.T) {
	timeline := &Timeline{Timing: &TimelineTiming{TimingSource: "streamData", EncoderTimingApproximate: true}}
	event := TimelineEvent{Args: map[string]interface{}{"index": 7}}
	args := perfettoEventArgs(timeline, event, timelineClockBusy)
	if args["clock_domain"] != "busy" || args["timing_source"] != "streamData" || args["timing_quality"] != "approximate" {
		t.Fatalf("Perfetto args = %+v", args)
	}
	if _, ok := event.Args["clock_domain"]; ok {
		t.Fatal("perfettoEventArgs mutated canonical event args")
	}
}

func TestAppendMetalDispatchDetailProjection(t *testing.T) {
	parent := perfetto.TrackUUID("test", "parent")
	trace := &perfetto.Trace{Tracks: []perfetto.Track{{UUID: parent, Name: "parent"}}}
	timeline := &Timeline{
		Timing:   &TimelineTiming{TimingSource: "profiled"},
		Encoders: []EncoderInfo{{Index: 0, Label: "eval/A"}, {Index: 1, Label: "eval/B"}},
		Events: []TimelineEvent{
			{Name: "a", Category: "kernel", Phase: "X", Timestamp: 1, Duration: 2, Args: map[string]interface{}{"encoder_index": 0}},
			{Name: "b", Category: "kernel", Phase: "X", Timestamp: 3, Duration: 4, Args: map[string]interface{}{"encoder_index": 1}},
			{Name: "unknown", Category: "kernel", Phase: "X", Args: map[string]interface{}{}},
		},
	}
	tracks, events := appendMetalDispatchDetailProjection(trace, timeline, parent)
	if tracks != 2 || events != 2 || len(trace.Tracks) != 3 || len(trace.Events) != 2 {
		t.Fatalf("projection = tracks %d events %d; trace has %d tracks %d events", tracks, events, len(trace.Tracks), len(trace.Events))
	}
	if trace.Tracks[1].ParentUUID != parent || trace.Tracks[1].Name != "Encoder 0 · 1 dispatches · 0.002 ms · 1 functions — eval/A" {
		t.Fatalf("first detail track = %+v", trace.Tracks[1])
	}
	if got := trace.Events[0].Args["presentation_projection"]; got != "encoder_dispatch_detail" {
		t.Fatalf("projection marker = %v", got)
	}
	if trace.Events[0].StartNS != 1_000 || trace.Events[0].DurationNS != 2_000 {
		t.Fatalf("detail timing = %d+%d", trace.Events[0].StartNS, trace.Events[0].DurationNS)
	}
}

func TestIncludeMetalDispatchDetailProjection(t *testing.T) {
	timed := &Timeline{Events: []TimelineEvent{{Category: "kernel", Duration: 1}}}
	untimed := &Timeline{Events: []TimelineEvent{{Category: "kernel"}}}
	if !includeMetalDispatchDetailProjection(timed, timelineClockBusy, 0) {
		t.Fatal("lossless busy export omitted dispatch detail")
	}
	if includeMetalDispatchDetailProjection(timed, timelineClockBusy, 500_000) {
		t.Fatal("constrained export included redundant dispatch detail")
	}
	if includeMetalDispatchDetailProjection(timed, timelineClockWall, 0) {
		t.Fatal("wall export included busy dispatch detail")
	}
	if includeMetalDispatchDetailProjection(untimed, timelineClockBusy, 0) {
		t.Fatal("untimed export included one detail track per dispatch")
	}
}

func TestExportPerfettoOmitsUntimedDispatchDetailProjection(t *testing.T) {
	timeline := &Timeline{Events: []TimelineEvent{
		{Name: "label/A", Category: "kernel", Phase: "i", Args: map[string]interface{}{"encoder_index": 0}},
		{Name: "label/B", Category: "kernel", Phase: "i", Args: map[string]interface{}{"encoder_index": 1}},
	}}
	out := filepath.Join(t.TempDir(), "untimed.pftrace")
	if err := exportPerfettoForClock(timeline, out, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("kernel_detail")) {
		t.Fatal("untimed export contains dispatch detail projection")
	}
	want := "omitted because dispatch timing is unavailable; aggregate GPU totals use native gpu_slice only"
	if !bytes.Contains(data, []byte(want)) {
		t.Fatalf("untimed export missing manifest reason %q", want)
	}
}

func TestAppendMLXSemanticEvents(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{{
			Name: "kernel", Category: "kernel", Phase: "X", Timestamp: 10, Duration: 5,
		}},
		MLXSemantics: &mlxsemantic.Sidecar{
			Schema: mlxsemantic.SchemaV1,
			Nodes: []mlxsemantic.Node{
				{ID: "run", Kind: "run", Name: "decode"},
				{ID: "op", ParentID: "run", Kind: "operation", Name: "matmul", Attrs: map[string]any{"dtype": "bfloat16"}},
			},
			Links: []mlxsemantic.Link{{ID: "link", SemanticID: "op", Target: mlxsemantic.Target{Kind: "dispatch", Index: 0}}},
		},
	}
	trace := &perfetto.Trace{}
	appendMLXSemanticEvents(trace, timeline)
	if got, want := len(trace.Tracks), 2; got != want {
		t.Fatalf("semantic tracks = %d, want %d", got, want)
	}
	if got, want := len(trace.Events), 3; got != want {
		t.Fatalf("semantic events = %d, want %d", got, want)
	}
	if got := trace.Events[0].Args["join_basis"]; got != "sidecar-declaration" {
		t.Fatalf("node declaration basis = %v", got)
	}
	if got := trace.Events[1].Args["semantic_parent_id"]; got != "run" {
		t.Fatalf("semantic parent = %v, want run", got)
	}
	if got := trace.Events[1].Args["dtype"]; got != "bfloat16" {
		t.Fatalf("semantic dtype = %v, want bfloat16", got)
	}
	if trace.Events[0].Kind != perfetto.EventInstant || trace.Events[0].Args["clock_domain"] != "none" || trace.Events[0].Args["timing_quality"] != "unavailable" {
		t.Fatalf("node declaration timing = %+v", trace.Events[0])
	}
	if got := trace.Events[2].Args["join_basis"]; got != "sidecar-explicit-id" {
		t.Fatalf("join basis = %v", got)
	}
	if got := trace.Events[2].Args["semantic_link_id"]; got != "link" {
		t.Fatalf("semantic link = %v, want link", got)
	}
	if got := trace.Events[2].Args["target_index"]; got != 0 {
		t.Fatalf("semantic target index = %v, want 0", got)
	}
}

func TestMLXSemanticProjectionCounts(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{{Category: "kernel"}},
		MLXSemantics: &mlxsemantic.Sidecar{Links: []mlxsemantic.Link{
			{Target: mlxsemantic.Target{Kind: "dispatch", Index: 0}},
			{Target: mlxsemantic.Target{Kind: "command_buffer", Index: 0}},
		}},
	}
	projected, unprojected := mlxSemanticProjectionCounts(timeline)
	if projected["dispatch"] != 1 || unprojected["command_buffer"] != 1 {
		t.Fatalf("projection counts = %v, %v", projected, unprojected)
	}
}

func TestAttachMLXSidecarChecksTraceIdentity(t *testing.T) {
	traceDir := filepath.Join(t.TempDir(), "trace.gputrace")
	if err := os.Mkdir(traceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(traceDir, "capture"), []byte("trace"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := mlxsemantic.Digest(traceDir)
	if err != nil {
		t.Fatal(err)
	}
	sidecar := mlxsemantic.Sidecar{
		Schema: mlxsemantic.SchemaV1,
		Trace:  mlxsemantic.Identity{UUID: "trace-id", ContentDigest: digest},
		Nodes:  []mlxsemantic.Node{{ID: "op", Kind: "operation", Name: "matmul"}},
		Links:  []mlxsemantic.Link{{ID: "link", SemanticID: "op", Target: mlxsemantic.Target{Kind: "dispatch", Index: 0}}},
	}
	data, err := json.Marshal(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	sidecarPath := filepath.Join(t.TempDir(), "semantic.json")
	if err := os.WriteFile(sidecarPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	timeline := &Timeline{Events: []TimelineEvent{{Category: "kernel"}}}
	if err := attachMLXSidecar(timeline, traceDir, "trace-id", sidecarPath); err != nil {
		t.Fatal(err)
	}
	if timeline.MLXSemantics == nil || timeline.MLXSidecarDigest == "" {
		t.Fatal("sidecar was not attached with its digest")
	}
	if timeline.MLXSemanticReport == nil || timeline.MLXSemanticReport.MatchedTargets["dispatch"] != 1 {
		t.Fatalf("semantic coverage = %+v, want one matched dispatch", timeline.MLXSemanticReport)
	}
	if err := attachMLXSidecar(&Timeline{Events: []TimelineEvent{{Category: "kernel"}}}, traceDir, "other", sidecarPath); err == nil {
		t.Fatal("wrong trace UUID was accepted")
	}
}

func TestTimelineOutputPath(t *testing.T) {
	tests := []struct {
		name   string
		format string
		output string
		want   string
	}{
		{name: "text default", format: "text", want: ""},
		{name: "json default", format: "json", want: "timeline.json"},
		{name: "chrome default", format: "chrome", want: "timeline.json"},
		{name: "perfetto default", format: "perfetto", want: "timeline.pftrace"},
		{name: "html default", format: "html", want: "timeline.html"},
		{name: "html explicit file", format: "html", output: "custom.htm", want: "custom.htm"},
		{name: "text explicit file", format: "text", output: "timeline.txt", want: "timeline.txt"},
		{name: "json stdout", format: "json", output: "/dev/stdout", want: "/dev/stdout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timelineOutputPath(tt.format, tt.output); got != tt.want {
				t.Fatalf("timelineOutputPath(%q, %q) = %q, want %q", tt.format, tt.output, got, tt.want)
			}
		})
	}
}

func TestTimelineSQLOutput(t *testing.T) {
	if err := validateTimelineSQLOutput(&timelineOptions{format: "json", sqlOutput: "gputrace.sql"}); err == nil {
		t.Fatal("JSON accepted --sql-out")
	}
	path := filepath.Join(t.TempDir(), "gputrace.sql")
	if err := writeTimelinePerfettoSQL(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"gputrace_capture", "gputrace_dispatch", "gputrace_dispatch_arg", "gputrace_encoder_arg", "gputrace_raw_profiler_sample", "gputrace_track_event_arg", "gputrace_pipeline", "gputrace_counter_series", "gputrace_unmatched"} {
		if !bytes.Contains(data, []byte("CREATE PERFETTO VIEW "+view)) {
			t.Errorf("SQL output missing %s", view)
		}
	}
}

func TestTimelineDurationPhase(t *testing.T) {
	tests := []struct {
		name string
		dur  uint64
		want string
	}{
		{name: "zero", dur: 0, want: "i"},
		{name: "one microsecond", dur: 1, want: "X"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := timelineDurationPhase(test.dur); got != test.want {
				t.Fatalf("timelineDurationPhase(%d) = %q, want %q", test.dur, got, test.want)
			}
		})
	}
}

func TestAddRestoreEventsPreservesRawWallEvidence(t *testing.T) {
	timeline := &Timeline{}
	addRestoreEvents(timeline, &counter.TimelineInfo{
		AbsoluteTime:  100,
		TimebaseNumer: 125,
		TimebaseDenom: 3,
		RestoreTimestamps: []counter.TimestampRange{
			{Index: 7, StartTicks: 100, EndTicks: 101},
			{Index: 8, StartTicks: 130, EndTicks: 129},
		},
	})
	if got, want := len(timeline.Events), 1; got != want {
		t.Fatalf("restore events = %d, want %d", got, want)
	}
	event := timeline.Events[0]
	if got, want := event.Category, "restore"; got != want {
		t.Fatalf("category = %q, want %q", got, want)
	}
	if got, want := event.TimestampNS, uint64(0); got != want {
		t.Fatalf("timestamp_ns = %d, want %d", got, want)
	}
	if got, want := event.DurationNS, uint64(41); got != want {
		t.Fatalf("duration_ns = %d, want %d", got, want)
	}
	if got, want := event.Args["start_ticks"], uint64(100); got != want {
		t.Fatalf("start_ticks = %#v, want %#v", got, want)
	}
	if got, want := event.Args["evidence_kind"], "replay_restore_interval"; got != want {
		t.Fatalf("evidence_kind = %#v, want %#v", got, want)
	}
	if got, want := event.Phase, "X"; got != want {
		t.Fatalf("phase = %q, want %q for nonzero sub-microsecond interval", got, want)
	}
}

func TestValidateTimelineFormat(t *testing.T) {
	for _, format := range []string{"chrome", "perfetto", "html", "json", "text"} {
		t.Run(format, func(t *testing.T) {
			if err := validateTimelineFormat(format); err != nil {
				t.Fatalf("validateTimelineFormat(%q): %v", format, err)
			}
		})
	}

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{
			name:   "empty",
			format: "",
			want:   `invalid timeline format "" (supported: chrome, perfetto, html, json, text)`,
		},
		{
			name:   "uppercase",
			format: "Chrome",
			want:   `invalid timeline format "Chrome" (supported: chrome, perfetto, html, json, text)`,
		},
		{
			name:   "unsupported",
			format: "svg",
			want:   `invalid timeline format "svg" (supported: chrome, perfetto, html, json, text)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTimelineFormat(tt.format)
			if err == nil {
				t.Fatalf("validateTimelineFormat(%q) = nil, want error", tt.format)
			}
			if got := err.Error(); got != tt.want {
				t.Fatalf("validateTimelineFormat(%q) = %q, want %q", tt.format, got, tt.want)
			}
		})
	}
}

func TestRunTimelineValidatesFormatBeforeTraceIO(t *testing.T) {
	err := runTimeline(nil, []string{filepath.Join(t.TempDir(), "missing.gputrace")}, &timelineOptions{
		format: "svg",
	})
	if err == nil {
		t.Fatal("runTimeline = nil, want error")
	}
	want := `invalid timeline format "svg" (supported: chrome, perfetto, html, json, text)`
	if got := err.Error(); got != want {
		t.Fatalf("runTimeline error = %q, want %q", got, want)
	}
}

func TestTimelineForClockKeepsOnlyComparableEvents(t *testing.T) {
	timeline := &Timeline{
		StartTime: 99,
		EndTime:   999_999_999,
		Duration:  999_999_900,
		Events: []TimelineEvent{
			{Category: "command_buffer", Timestamp: 300_000},
			{Category: "restore", Timestamp: 310_000},
			{Category: "profiler_stream", Timestamp: 320_000},
			{Category: "gprwcntr", Timestamp: 340_000},
			{Category: "encoder", Timestamp: 0},
			{Category: "kernel", Timestamp: 100},
		},
		Encoders:      []EncoderInfo{{Index: 0}},
		Kernels:       []KernelInfo{{Name: "kernel", Encoder: 0}},
		CounterTracks: []CounterTrack{{Name: "GPU Cycles", Samples: []CounterSample{{Timestamp: 200_000, Value: 1}}}},
	}

	busy := timelineForClock(timeline, timelineClockBusy)
	if got, want := len(busy.Events), 2; got != want {
		t.Fatalf("busy events = %d, want %d", got, want)
	}
	if got, want := len(busy.CounterTracks), 1; got != want {
		t.Fatalf("busy counter tracks = %d, want %d", got, want)
	}
	if got, want := busy.Events[0].Category, "encoder"; got != want {
		t.Fatalf("first busy category = %q, want %q", got, want)
	}
	if got, want := busy.ClockDomain, string(timelineClockBusy); got != want {
		t.Fatalf("busy clock_domain = %q, want %q", got, want)
	}
	if got, want := busy.Duration, uint64(200_000); got != want {
		t.Fatalf("busy duration = %d, want %d", got, want)
	}
	if got, want := *busy.EvidenceInventory, (TimelineEvidenceInventory{CommandBuffers: 1, RestoreIntervals: 1, Encoders: 1, Dispatches: 1, ProfilerStreams: 1, ProfilerRecords: 1, UntimedDispatches: 1}); got != want {
		t.Fatalf("busy evidence inventory = %#v, want %#v", got, want)
	}

	wall := timelineForClock(timeline, timelineClockWall)
	if got, want := len(wall.Events), 4; got != want {
		t.Fatalf("wall events = %d, want %d", got, want)
	}
	if len(wall.Encoders) != 0 || len(wall.Kernels) != 0 || len(wall.CounterTracks) != 0 {
		t.Fatalf("wall timeline retained busy data: %#v", wall)
	}
	for _, event := range wall.Events {
		if event.Category == "encoder" || event.Category == "kernel" || event.Category == "dispatch" {
			t.Fatalf("wall timeline contains busy event: %#v", event)
		}
	}
	if got, want := wall.ClockDomain, string(timelineClockWall); got != want {
		t.Fatalf("wall clock_domain = %q, want %q", got, want)
	}
	if got, want := wall.Duration, uint64(340_000_000); got != want {
		t.Fatalf("wall duration = %d, want %d", got, want)
	}
	if got, want := *wall.EvidenceInventory, *busy.EvidenceInventory; got != want {
		t.Fatalf("wall evidence inventory = %#v, want %#v", got, want)
	}
	if got, want := len(timeline.Events), 6; got != want {
		t.Fatalf("source timeline events = %d, want %d", got, want)
	}
}

func TestTimelineForClockWithoutRawProfilerSamples(t *testing.T) {
	timeline := &Timeline{Events: []TimelineEvent{
		{Category: "command_buffer", Timestamp: 300_000},
		{Category: "profiler_stream", Timestamp: 320_000},
		{Category: "gprwcntr", Timestamp: 340_000},
	}}

	wall := timelineForClockWithRawSamples(timeline, timelineClockWall, false)
	if got, want := len(wall.Events), 1; got != want {
		t.Fatalf("wall events without raw samples = %d, want %d", got, want)
	}
	if wall.RawProfilerSamples {
		t.Fatal("wall timeline says raw profiler samples were included")
	}
	for _, event := range wall.Events {
		if event.Category == "gprwcntr" {
			t.Fatalf("wall timeline retained raw profiler record: %#v", event)
		}
	}

	withRaw := timelineForClockWithRawSamples(timeline, timelineClockWall, true)
	if got, want := len(withRaw.Events), 3; got != want {
		t.Fatalf("wall events with raw samples = %d, want %d", got, want)
	}
	if !withRaw.RawProfilerSamples {
		t.Fatal("wall timeline does not record raw profiler samples")
	}
}

func TestTimelineClockProvenanceWithoutRawProfilerSamples(t *testing.T) {
	args := timelineClockProvenanceWithRawSamples(timelineClockWall, false)
	if got := strings.Join(args["included_categories"].([]string), ","); got != "command_buffer,restore" {
		t.Fatalf("included_categories = %q, want command_buffer,restore", got)
	}
	if got := strings.Join(args["excluded_categories"].([]string), ","); !strings.Contains(got, "gprwcntr") {
		t.Fatalf("excluded_categories = %q, want gprwcntr", got)
	}
	if args["raw_profiler_samples"] == "" {
		t.Fatal("raw profiler sample provenance is missing")
	}
}

func TestTimelineClockValidation(t *testing.T) {
	if err := validateTimelineClock(timelineClockBusy); err != nil {
		t.Fatalf("validate busy: %v", err)
	}
	if err := validateTimelineClock(timelineClockWall); err != nil {
		t.Fatalf("validate wall: %v", err)
	}
	if err := validateTimelineClock(timelineClockBoth); err != nil {
		t.Fatalf("validate both: %v", err)
	}
	if err := validateTimelineClock(timelineClockLive); err != nil {
		t.Fatalf("validate live: %v", err)
	}
	if err := validateTimelineClock("mixed"); err == nil {
		t.Fatal("validate mixed = nil, want error")
	}
}

func TestExportTimelineBothKeepsClockDomainsSeparate(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{
			{Category: "command_buffer", Timestamp: 300_000, Duration: 10},
			{Category: "encoder", Timestamp: 0, Duration: 10},
			{Category: "kernel", Timestamp: 10, Duration: 5},
		},
		Encoders: []EncoderInfo{{Index: 0, Duration: 10}},
		Kernels:  []KernelInfo{{Name: "kernel", Encoder: 0, Duration: 5}},
	}

	out := filepath.Join(t.TempDir(), "both.json")
	if err := exportTimelineBoth(timeline, "json", out); err != nil {
		t.Fatalf("exportTimelineBoth: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var got timelineBothJSON
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got.ClockDomain != string(timelineClockBoth) {
		t.Fatalf("clock_domain = %q, want %q", got.ClockDomain, timelineClockBoth)
	}
	if got.ClockMapping == "" {
		t.Fatal("clock_mapping is empty")
	}
	if got.Busy == nil || got.Wall == nil {
		t.Fatalf("missing clock view: busy=%v wall=%v", got.Busy != nil, got.Wall != nil)
	}
	if got.Busy.ClockDomain != string(timelineClockBusy) || got.Wall.ClockDomain != string(timelineClockWall) {
		t.Fatalf("clock domains = busy %q, wall %q", got.Busy.ClockDomain, got.Wall.ClockDomain)
	}
	if got, want := len(got.Busy.Events), 2; got != want {
		t.Fatalf("busy events = %d, want %d", got, want)
	}
	if got, want := len(got.Wall.Events), 1; got != want {
		t.Fatalf("wall events = %d, want %d", got, want)
	}
}

func TestExportTimelineBothHTMLHasSeparatePanels(t *testing.T) {
	timeline := &Timeline{Events: []TimelineEvent{
		{Category: "command_buffer", Timestamp: 300_000, Duration: 10},
		{Category: "encoder", Timestamp: 0, Duration: 10},
	}}
	out := filepath.Join(t.TempDir(), "both.html")
	if err := exportTimelineBoth(timeline, "html", out); err != nil {
		t.Fatalf("exportTimelineBoth: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	for _, want := range []string{"total information view", "GPU busy time", "Wall-clock scheduling", "no measured mapping"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("HTML does not contain %q", want)
		}
	}
}

func TestExportTimelineBothRejectsOneAxisFormats(t *testing.T) {
	err := exportTimelineBoth(&Timeline{}, "perfetto", filepath.Join(t.TempDir(), "both.json"))
	if err == nil || !strings.Contains(err.Error(), "one global time axis") {
		t.Fatalf("exportTimelineBoth perfetto error = %v, want one-axis error", err)
	}
}

func TestExportTextTimelineWritesOutputFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "timeline.txt")
	if err := exportTextTimeline(&Timeline{}, nil, out); err != nil {
		t.Fatalf("exportTextTimeline: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if got, want := string(data), "No timeline data available.\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestExportTextTimelineSyntheticCommandBufferUsesMilliseconds(t *testing.T) {
	out := filepath.Join(t.TempDir(), "timeline.txt")
	timeline := &Timeline{
		Duration: 10_837_000,
		Encoders: []EncoderInfo{{
			Index:    0,
			Label:    "encoder",
			Duration: 10_837_000,
		}},
	}
	if err := exportTextTimeline(timeline, nil, out); err != nil {
		t.Fatalf("exportTextTimeline: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(data), "CB#0 [0.0ms, duration=10.84ms]") {
		t.Fatalf("synthetic command buffer has wrong duration: %s", data)
	}
}

func TestExportTextTimelineSummarizesUnitsAndMissingDuration(t *testing.T) {
	out := filepath.Join(t.TempDir(), "timeline.txt")
	timeline := &Timeline{
		TracePath: "/trace/example.gputrace",
		Events: []TimelineEvent{
			{Name: "CB#0", Category: "command_buffer", Duration: 250, Args: map[string]interface{}{"index": 0}},
			{Name: "CB#1", Category: "command_buffer", Timestamp: 250, Args: map[string]interface{}{"index": 1}},
		},
		Encoders: []EncoderInfo{{Index: 0}},
		Kernels:  []KernelInfo{{Name: "kernel", Encoder: 0}},
		Timing: &TimelineTiming{
			EncoderSpanNs:         1_000_000,
			DispatchSpanNs:        2_000_000,
			CommandBufferActiveNs: 500_000,
			CommandBufferWallNs:   3_000_000,
			EncoderTimingSource:   "profiler",
		},
	}
	if err := exportTextTimeline(timeline, nil, out); err != nil {
		t.Fatalf("exportTextTimeline: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"Trace: /trace/example.gputrace",
		"Events: 2 command buffers, 1 encoder, 1 kernel dispatch",
		"Timing source: profiler (measured)",
		"Dispatch span: 2.00 ms",
		"Command-buffer active time: 500.00 us",
		"Xcode Effective GPU Time: unavailable",
		"Row units: start and duration are milliseconds",
		"CB#1 [0.2ms, duration unavailable: no end timestamp]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

// The timeline used to place encoder spans built from name-guessed durations
// on the same axis as measured ones, where a span's width reads as its cost.
// A trace with no measured timing now contributes no encoder spans at all.
func TestGenerateTimelineOmitsSpansWhenTimingUnavailable(t *testing.T) {
	tr := &gputrace.Trace{
		Path:        timelineTimingSourceTraceDir(t),
		KernelNames: []string{"block_softmax_float32"},
	}

	timeline, err := generateTimeline(tr)
	if err != nil {
		t.Fatalf("generateTimeline: %v", err)
	}

	if timeline.Timing == nil {
		t.Fatal("timeline timing metadata is nil")
	}
	if got, want := timeline.Timing.EncoderTimingSource, "unavailable"; got != want {
		t.Fatalf("EncoderTimingSource = %q, want %q", got, want)
	}
	if !timeline.Timing.EncoderTimingApproximate {
		t.Fatal("EncoderTimingApproximate = false, want true: an absent measurement is not an exact one")
	}

	if event := firstTimelineEventByCategory(timeline, "encoder"); event != nil {
		t.Fatalf("encoder span emitted for an unmeasured trace: %+v", event.Args)
	}
}

func TestGenerateTimelineAnnotatesExtractedTimingSource(t *testing.T) {
	const label = "encoder_from_capture"
	start := uint64(0x023456789abcdef1)
	end := start + 250_000

	tr := &gputrace.Trace{
		Path:          timelineTimingSourceTraceDir(t),
		CaptureData:   timelineCaptureWithExtractedTiming(label, start, end),
		EncoderLabels: []string{label},
	}

	timeline, err := generateTimeline(tr)
	if err != nil {
		t.Fatalf("generateTimeline: %v", err)
	}

	if timeline.Timing == nil {
		t.Fatal("timeline timing metadata is nil")
	}
	if got, want := timeline.Timing.EncoderTimingSource, "extracted"; got != want {
		t.Fatalf("EncoderTimingSource = %q, want %q", got, want)
	}
	if !timeline.Timing.EncoderTimingApproximate {
		t.Fatal("EncoderTimingApproximate = false, want true")
	}

	event := firstTimelineEventByCategory(timeline, "encoder")
	if event == nil {
		t.Fatal("missing encoder event")
	}
	if got, want := event.Args["timing_source"], "extracted"; got != want {
		t.Fatalf("event timing_source = %v, want %q", got, want)
	}
	if got, want := event.Args["timing_approximate"], true; got != want {
		t.Fatalf("event timing_approximate = %v, want %v", got, want)
	}
	if got, want := event.Args["real_timing"], false; got != want {
		t.Fatalf("event real_timing = %v, want %v", got, want)
	}
}

func TestTimelineTimingSourceHelpersMarkProfilerMeasured(t *testing.T) {
	metrics := &gputrace.TimingMetrics{
		TimingSource:      "profiler",
		TimingApproximate: false,
	}

	args := map[string]interface{}{}
	addTimingMetricsEventArgs(args, metrics)
	if got, want := args["timing_source"], "profiler"; got != want {
		t.Fatalf("timing_source = %v, want %q", got, want)
	}
	if got, want := args["timing_approximate"], false; got != want {
		t.Fatalf("timing_approximate = %v, want %v", got, want)
	}
	if got, want := args["real_timing"], true; got != want {
		t.Fatalf("real_timing = %v, want %v", got, want)
	}

	timeline := &Timeline{}
	annotateTimelineWithTimingMetrics(timeline, metrics)
	timingArgs := timelineTimingArgs(timeline.Timing)
	if got, want := timingArgs["encoder_timing_source"], "profiler"; got != want {
		t.Fatalf("encoder_timing_source = %v, want %q", got, want)
	}
	if got, want := timingArgs["encoder_timing_approximate"], false; got != want {
		t.Fatalf("encoder_timing_approximate = %v, want %v", got, want)
	}
}

func TestAddDispatchKernelEventsIncludesXcodeShaderArgs(t *testing.T) {
	timeline := &Timeline{
		Events: []TimelineEvent{{
			Name:      "encoder0",
			Category:  "encoder",
			Phase:     "X",
			ProcessID: 1,
			ThreadID:  2,
			Args:      map[string]interface{}{"index": 0},
		}},
		Encoders: []EncoderInfo{{
			Index:     0,
			Label:     "encoder0",
			Type:      "compute",
			StartTime: 1000,
			EndTime:   21000,
			Duration:  20000,
		}},
	}
	stats := &counter.StreamDataStats{
		Pipelines: []counter.PipelineStats{{
			PipelineID:             42,
			PipelineAddress:        0xabc,
			FunctionName:           "kernel0",
			TemporaryRegisterCount: 13,
			UniformRegisterCount:   4,
			SpilledBytes:           8,
			InstructionCount:       99,
			ALUInstructionCount:    77,
			FP16InstructionCount:   55,
		}},
		Dispatches: []counter.DispatchInfo{{
			Index:                   2,
			PipelineIndex:           0,
			PipelineID:              42,
			FunctionName:            "kernel0",
			EncoderIndex:            0,
			CumulativeUs:            7,
			DurationUs:              7,
			ProfilingSampleSharePct: 85.25,
			SampleCount:             3,
			SamplingDensity:         0.42,
			StartTicks:              10,
			EndTicks:                20,
		}},
	}
	perfStats := &gputrace.PerfCounterStats{
		ShaderMetrics: []gputrace.ShaderHardwareMetrics{{
			ShaderName:     "kernel0",
			PipelineState:  0xabc,
			SIMDGroups:     128,
			AllocatedRegs:  17,
			HighRegister:   19,
			SpilledBytes:   16,
			ALUUtilization: 71.25,
		}},
	}
	shaderReport := &gputrace.ShaderMetricsReport{
		Shaders: []*gputrace.ShaderMetrics{{
			Name:              "kernel0",
			PercentOfTotal:    88.5,
			TotalThreadgroups: 4096,
			TotalDurationNs:   7000,
		}},
	}
	simd := timelineDispatchCaptureStats{
		byName:     map[string]uint64{"kernel0": 4},
		total:      4,
		dispatches: []tracepkg.AttributedDispatch{{CommandBuffer: 3, CaptureOffset: 4096, DispatchThreads: tracepkg.DispatchThreads{ThreadsX: 64, ThreadsY: 2, ThreadsZ: 1, ThreadsPerGroupX: 32, ThreadsPerGroupY: 1, ThreadsPerGroupZ: 1}}},
	}

	if !addDispatchKernelEvents(timeline, stats, simd, shaderReport, perfStats, nil, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	if got := len(timeline.Kernels); got != 1 {
		t.Fatalf("kernels = %d, want 1", got)
	}
	if got := len(timeline.Events); got != 2 {
		t.Fatalf("events = %d, want 2", got)
	}
	ev := timeline.Events[1]
	if ev.Name != "kernel0" || ev.Category != "kernel" {
		t.Fatalf("event = %s/%s, want kernel0/kernel", ev.Name, ev.Category)
	}
	if got, want := ev.ThreadID, 2; got != want {
		t.Fatalf("dispatch tid = %d, want encoder tid %d", got, want)
	}
	if got, want := ev.Timestamp, uint64(1); got != want {
		t.Fatalf("timestamp = %d, want %d", got, want)
	}
	if got, want := ev.Duration, uint64(7); got != want {
		t.Fatalf("duration = %d, want %d", got, want)
	}
	checkArg := func(key string, want interface{}) {
		t.Helper()
		if got := ev.Args[key]; got != want {
			t.Fatalf("arg %s = %#v, want %#v", key, got, want)
		}
	}
	checkArg("simd_group_share_pct", 100.0)
	checkArg("profiling_sample_share_estimate_pct", 85.25)
	checkArg("pipeline_state", "0xabc")
	checkArg("pipeline_address", uint64(0xabc))
	checkArg("pipeline_identity_source", "streamData pipelineStateInfoData")
	checkArg("pipeline_identity_scope", "capture-local")
	checkArg("simd_groups", uint64(4))
	checkArg("grid_size", "64,2,1")
	checkArg("threadgroup_size", "32,1,1")
	checkArg("geometry_source", "capture dispatch record matched by dispatch order after exact count check")
	checkArg("command_buffer_index", 3)
	checkArg("capture_offset", int64(4096))
	checkArg("capture_structure_source", "capture dispatch record matched by dispatch order after exact count check")
	checkArg("allocated_registers", 17)
	checkArg("high_register", 19)
	checkArg("spilled_bytes", 16)
	checkArg("instruction_count", 99)
	checkArg("shader_duration_ns", uint64(7000))
	checkArg("gprwcntr_sample_count", 3)
	checkArg("sample_attribution_basis", "GPRWCNTR samples in a scaled cumulative-dispatch window")
	checkArg("sample_window_basis", "cumulative dispatch time scaled over the first APSTimelineData command buffer")
	checkArg("sample_timestamp_domain", "mach absolute ticks")
	checkArg("xcode_view", "Shaders")
	checkArg("encoder_containment", "strict")
}

func TestAddDispatchKernelEventsUsesEncoderCounterFallback(t *testing.T) {
	timeline := &Timeline{
		Encoders: []EncoderInfo{{
			Index:     0,
			Label:     "encoder0",
			Type:      "compute",
			StartTime: 1000,
			EndTime:   21000,
			Duration:  20000,
		}},
	}
	stats := &counter.StreamDataStats{
		Pipelines: []counter.PipelineStats{{
			PipelineID:             42,
			TemporaryRegisterCount: 13,
		}},
		Dispatches: []counter.DispatchInfo{{
			Index:         0,
			PipelineIndex: 0,
			PipelineID:    42,
			EncoderIndex:  0,
			DurationUs:    5,
		}},
	}
	encoderMetrics := []counter.EncoderCounterMetrics{{
		EncoderIndex:       0,
		Attribution:        counter.CounterAttributionEncoder,
		ALUUtilization:     71.25,
		ComputeUtilization: 80,
	}}

	if !addDispatchKernelEvents(timeline, stats, timelineDispatchCaptureStats{}, nil, nil, encoderMetrics, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	args := timeline.Events[0].Args
	if got, want := args["alu_utilization_pct"], 71.25; got != want {
		t.Fatalf("alu_utilization_pct = %#v, want %#v", got, want)
	}
	if _, ok := args["occupancy_pct"]; ok {
		t.Fatal("occupancy_pct must not be emitted; gputrace cannot measure occupancy")
	}
	if got, want := args["alu_utilization_source"], "encoder counter fallback"; got != want {
		t.Fatalf("alu_utilization_source = %#v, want %#v", got, want)
	}
}

func TestPerfettoRendersUnknownCounterMetricsAsUnattributed(t *testing.T) {
	timeline := &Timeline{Encoders: []EncoderInfo{{
		Index:     0,
		Label:     "encoder0",
		Type:      "compute",
		StartTime: 1000,
		EndTime:   21000,
		Duration:  20000,
	}}}
	stats := &counter.StreamDataStats{
		Pipelines: []counter.PipelineStats{{PipelineID: 42}},
		Dispatches: []counter.DispatchInfo{{
			Index:        0,
			PipelineID:   42,
			EncoderIndex: 0,
			DurationUs:   5,
		}},
	}
	metrics := []counter.EncoderCounterMetrics{{
		EncoderIndex:   0, // A stale/default index must not override attribution.
		EncoderLabel:   "pipeline0",
		Attribution:    counter.CounterAttributionUnknown,
		ALUUtilization: 71.25,
	}}

	recordUnattributedCounterMetrics(timeline, metrics)
	if !addDispatchKernelEvents(timeline, stats, timelineDispatchCaptureStats{}, nil, nil, metrics, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	if got, ok := timeline.Events[0].Args["alu_utilization_pct"]; ok {
		t.Fatalf("dispatch received unknown counter value %#v", got)
	}

	out := filepath.Join(t.TempDir(), "timeline.json")
	if err := exportChromeTracing(timeline, out); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, event := range doc.TraceEvents {
		if event.Category != "counter_attribution" {
			continue
		}
		if got, want := event.Args["attribution"], "unknown"; got != want {
			t.Fatalf("attribution = %#v, want %#v", got, want)
		}
		if got, want := event.Args["pipeline_label"], "pipeline0"; got != want {
			t.Fatalf("pipeline_label = %#v, want %#v", got, want)
		}
		if got, want := event.Args["alu_utilization_pct"], 71.25; got != want {
			t.Fatalf("alu_utilization_pct = %#v, want %#v", got, want)
		}
		if _, ok := event.Args["encoder_index"]; ok {
			t.Fatal("unattributed event contains encoder_index")
		}
		return
	}
	t.Fatal("missing counter_attribution event")
}

func TestAddDispatchKernelEventsMarksBoundaryDispatch(t *testing.T) {
	timeline := &Timeline{Encoders: []EncoderInfo{{
		Index:     0,
		Label:     "encoder0",
		Type:      "compute",
		StartTime: 0,
		EndTime:   1000,
		Duration:  1000,
	}}}
	stats := &counter.StreamDataStats{Dispatches: []counter.DispatchInfo{{
		Index:        0,
		PipelineID:   1,
		EncoderIndex: 0,
		DurationUs:   2,
	}}}

	if !addDispatchKernelEvents(timeline, stats, timelineDispatchCaptureStats{}, nil, nil, nil, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	if got, want := timeline.Events[0].Args["encoder_containment"], "not_strictly_contained"; got != want {
		t.Fatalf("encoder_containment = %v, want %q", got, want)
	}
}

func TestAddDispatchKernelEventsAnnotatesSource(t *testing.T) {
	dir := t.TempDir()
	source := `#include <metal_stdlib>
using namespace metal;

kernel void source_backed_kernel(device float *out [[buffer(0)]],
                                 uint tid [[thread_position_in_grid]]) {
	out[tid] = 1;
}
`
	sourcePath := filepath.Join(dir, "kernels.metal")
	if err := os.WriteFile(sourcePath, []byte(source), 0666); err != nil {
		t.Fatal(err)
	}

	mapper := gputrace.NewShaderSourceMapper(dir)
	if err := mapper.IndexShaderSources(); err != nil {
		t.Fatal(err)
	}
	timeline := &Timeline{
		Encoders: []EncoderInfo{{Index: 0, StartTime: 1000}},
	}
	stats := &counter.StreamDataStats{
		Dispatches: []counter.DispatchInfo{{
			Index:         0,
			FunctionName:  "source_backed_kernel",
			EncoderIndex:  0,
			PipelineIndex: 0,
			DurationUs:    7,
		}},
	}

	if !addDispatchKernelEvents(timeline, stats, timelineDispatchCaptureStats{}, nil, nil, nil, mapper) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	args := timeline.Events[0].Args
	if got := args["source_available"]; got != true {
		t.Fatalf("source_available = %#v, want true", got)
	}
	if got := args["source_file"]; got != sourcePath {
		t.Fatalf("source_file = %#v, want %q", got, sourcePath)
	}
	if got := args["source_line"]; got != 4 {
		t.Fatalf("source_line = %#v, want 4", got)
	}
}

func TestBuildXcodeParityReport(t *testing.T) {
	timeline := &Timeline{
		Timing: &TimelineTiming{
			TimingSource:          "command buffer active time",
			DisplayDurationSource: "command buffer active time",
		},
		Events: []TimelineEvent{{
			Category: "kernel",
			Args: map[string]interface{}{
				"alu_utilization_pct": 0.0,
				"allocated_registers": 13,
			},
		}},
	}
	report := buildXcodeParityReport("trace.gputrace", timeline, xcodebindingsReportForTest())
	if report.KernelEvents != 1 {
		t.Fatalf("KernelEvents = %d, want 1", report.KernelEvents)
	}
	if len(report.RemainingGaps) == 0 {
		t.Fatal("missing remaining gaps")
	}
	// The fixture's alu_utilization_pct is 0.0, which is what the fallback
	// stamped when nothing had been read. It must land in the gap list.
	if !stringSliceContains(report.AbsentFields, "alu_utilization_pct") {
		t.Fatalf("alu_utilization_pct = 0 must not count as present: %+v", report.AbsentFields)
	}
	for _, example := range report.ClosedExamples {
		if strings.Contains(example, "alu_utilization_pct") {
			t.Fatalf("alu_utilization_pct reported closed on a zero value: %q", example)
		}
	}
}

func TestXcodeParityStreamDataEvidenceReportsSafeNextSteps(t *testing.T) {
	report := xcodeParityReport{
		Timing: map[string]interface{}{},
		RemainingGaps: []xcodeParityGap{
			{Metric: "high_register", Next: "old"},
			{Metric: "alu_utilization_pct", Next: "old"},
			{Metric: "effective_gpu_time", Next: "old"},
		},
		StreamData: &xcodebindings.StreamDataSummary{
			SelectedValues: []xcodebindings.ValueSummary{
				{Key: "Binaries", Count: 734},
				{Key: "Derived Counter Sample Data", Count: 16},
				{Key: "Derived Counters Info Data"},
				{Key: "ReplayerGPUTime", Count: 1},
			},
		},
	}

	report.applyStreamDataEvidence()

	gaps := make(map[string]xcodeParityGap)
	for _, gap := range report.RemainingGaps {
		gaps[gap.Metric] = gap
	}
	if got := gaps["high_register"].Next; !strings.Contains(got, "GTShaderProfilerStreamData") && !strings.Contains(got, "selector") {
		t.Fatalf("high_register next = %q, want runtime selector compatibility warning", got)
	}
	if got := gaps["alu_utilization_pct"].Next; !strings.Contains(got, "counter info dictionary is empty") {
		t.Fatalf("alu_utilization_pct next = %q, want empty counter info warning", got)
	}
	if got := gaps["effective_gpu_time"].Status; got != "archived as zero in Xcode streamData" {
		t.Fatalf("effective_gpu_time status = %q, want archived zero", got)
	}
}

func xcodebindingsReportForTest() xcodebindings.Report {
	return xcodebindings.Report{
		Summary: map[string]int{
			"classes_present":   4,
			"classes_missing":   0,
			"selectors_present": 42,
			"selectors_missing": 0,
		},
		Gaps: []xcodebindings.Gap{
			{
				Metric:  "high_register",
				Binding: "GTMioShaderBinaryData.liveRegisterForInstructionAtIndex:",
				Status:  "binding present; adapter missing",
				Next:    "map shader binary data",
			},
			{
				Metric:  "alu_utilization_pct",
				Binding: "XRGPUAPSDataProcessor derived counters",
				Status:  "binding present; adapter missing",
				Next:    "resolve ALU counter",
			},
		},
	}
}

func TestTimelineDispatchSIMDGroup(t *testing.T) {
	dispatch := tracepkg.DispatchThreads{
		ThreadsX:         1000,
		ThreadsY:         1,
		ThreadsZ:         1,
		ThreadsPerGroupX: 256,
		ThreadsPerGroupY: 1,
		ThreadsPerGroupZ: 1,
	}
	if got, want := dispatch.SIMDGroups(), uint64(32); got != want {
		t.Fatalf("timelineDispatchSIMDGroup = %d, want %d", got, want)
	}
}

func TestTimelineDispatchCaptureEvidenceRequiresExactCount(t *testing.T) {
	tracePath := "../../../testdata/traces/06-six-encoders/06-six-encoders-run1.gputrace"
	tr, err := tracepkg.Open(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	dispatches, err := tr.ParseAttributedDispatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatches) == 0 {
		t.Fatal("fixture has no dispatches")
	}
	stats := &counter.StreamDataStats{Dispatches: make([]counter.DispatchInfo, len(dispatches))}
	got := timelineDispatchCaptureEvidence(tr, stats)
	if len(got.dispatches) != len(dispatches) {
		t.Fatalf("matched dispatches = %d, want %d", len(got.dispatches), len(dispatches))
	}
	if got.dispatches[0].CaptureOffset == 0 {
		t.Fatal("matched dispatch has no capture offset")
	}

	stats.Dispatches = stats.Dispatches[:len(stats.Dispatches)-1]
	got = timelineDispatchCaptureEvidence(tr, stats)
	if len(got.dispatches) != 0 || len(got.byIndex) != 0 {
		t.Fatalf("mismatched evidence = %+v, want empty", got)
	}
}

func TestAddCaptureDispatchEventsUsesAttributedLaunches(t *testing.T) {
	tracePath := "../../../testdata/traces/06-six-encoders/06-six-encoders-run1.gputrace"
	tr, err := tracepkg.Open(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	dispatches, err := tr.ParseAttributedDispatches()
	if err != nil {
		t.Fatal(err)
	}
	timeline := &Timeline{}
	if err := addCaptureDispatchEvents(timeline, tr, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := len(timeline.Events), len(dispatches); got != want {
		t.Fatalf("events = %d, want %d dispatch records", got, want)
	}
	named := 0
	for _, event := range timeline.Events {
		if event.Category != "dispatch" || event.Phase != "i" || event.Duration != 0 {
			t.Fatalf("event = %+v, want untimed instant", event)
		}
		if event.Args["encoder_attribution"] != "unavailable" {
			t.Fatalf("encoder_attribution = %v", event.Args["encoder_attribution"])
		}
		if event.Args["function_attribution"] == "preceding pipeline state" {
			named++
		}
	}
	if named == 0 {
		t.Fatal("fixture has no named dispatch to exercise nil store statistics")
	}
}

func TestGenerateTimelineWithoutPerfDataIncludesDispatchSIMDGroups(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}

	tr, err := tracepkg.Open(tracePath)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer tr.Close()

	timeline, err := generateTimeline(tr)
	if err != nil {
		t.Fatalf("generateTimeline: %v", err)
	}
	if len(timeline.Encoders) != 0 {
		t.Fatalf("encoders = %d, want 0 without encoder lifecycle evidence", len(timeline.Encoders))
	}
	if timeline.ObservedCSLabels == 0 || timeline.UniqueCSLabels == 0 {
		t.Fatalf("CS label evidence = %d observations, %d unique", timeline.ObservedCSLabels, timeline.UniqueCSLabels)
	}
	found := false
	for _, event := range timeline.Events {
		if (event.Category != "kernel" && event.Category != "dispatch") || event.Args == nil {
			continue
		}
		if got, ok := event.Args["simd_groups"].(uint64); ok && got == 32 {
			if source := fmt.Sprint(event.Args["source"]); !strings.Contains(source, "dispatch geometry") {
				t.Fatalf("source = %q, want dispatch geometry", source)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no kernel event with simd_groups=32 in %#v", timeline.Events)
	}
}

func TestGenerateInteractiveHTMLIncludesShaderTooltipFields(t *testing.T) {
	html := generateInteractiveHTML(`{"events":[]}`)
	for _, want := range []string{
		"Profiling Sample Share (estimate)",
		"Pipeline ID",
		"Instructions",
		"ALU Instructions",
		"FP32 Instructions",
		"FP16 Instructions",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("generated HTML missing %q", want)
		}
	}
}

func TestGenerateInteractiveHTMLIncludesEstimatedTimingWarning(t *testing.T) {
	html := generateInteractiveHTML(`{"events":[]}`)
	for _, want := range []string{
		"warning-banner",
		"Precise hardware timing data is unavailable",
		"Estimated Timing",
		"Timing Mode",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("generated HTML missing estimated timing element %q", want)
		}
	}
}

func TestAnnotateEncoderCounterArchive(t *testing.T) {
	timeline := &Timeline{
		Encoders: []EncoderInfo{
			{Index: 0, StartTime: 100, EndTime: 200},
			{Index: 1, StartTime: 300, EndTime: 400},
		},
		Events: []TimelineEvent{
			{Category: "encoder", Args: map[string]interface{}{"index": 0}},
			{Category: "encoder", Args: map[string]interface{}{"index": 1}},
		},
	}
	archive := &counter.CounterArchive{Encoders: []counter.EncoderSamples{
		{Ordinal: 0, GPUCycles: 100, EndSamples: 16, SampleCount: 32},
		{Ordinal: 1, GPUCycles: 300, EndSamples: 16, SampleCount: 32},
	}}

	annotateEncoderCounterArchive(timeline, archive)

	if got, want := timeline.Events[0].Args["gpu_cycles"], uint64(100); got != want {
		t.Fatalf("gpu_cycles = %v, want %v", got, want)
	}
	if got, want := timeline.Events[0].Args["execution_cost_pct"], 25.0; got != want {
		t.Fatalf("execution_cost_pct = %v, want %v", got, want)
	}
	if got, want := timeline.Events[0].Args["counter_attribution_basis"], "Encoder Infos execution ordinal"; got != want {
		t.Fatalf("counter_attribution_basis = %v, want %q", got, want)
	}
	if got, want := timeline.Events[0].Args["counter_coverage"], "at least one end-counter read per replay group"; got != want {
		t.Fatalf("counter_coverage = %v, want %q", got, want)
	}
}

func TestAnnotateEncoderCounterArchiveMarksSparseValues(t *testing.T) {
	timeline := &Timeline{Events: []TimelineEvent{{
		Category: "encoder",
		Args:     map[string]interface{}{"index": 0},
	}}}
	archive := &counter.CounterArchive{Encoders: []counter.EncoderSamples{{
		Ordinal:    0,
		GPUCycles:  100,
		EndSamples: 15,
	}}}

	annotateEncoderCounterArchive(timeline, archive)

	if got, want := timeline.Events[0].Args["counter_coverage"], "sparse: fewer than 16 end-counter reads"; got != want {
		t.Fatalf("counter_coverage = %v, want %q", got, want)
	}
}

func TestDispatchKernelArgsOmitsUnreadEncoderCounters(t *testing.T) {
	args := dispatchKernelArgs(counter.DispatchInfo{}, nil, 0, 0, nil, nil, &counter.EncoderCounterMetrics{}, nil)
	for _, key := range []string{"alu_utilization_pct", "alu_utilization_source"} {
		if got, ok := args[key]; ok {
			t.Fatalf("%s = %#v, want absent: nothing was read into it", key, got)
		}
	}
}

func TestAddPipelineCompilerArgs(t *testing.T) {
	pipeline := &counter.PipelineStats{
		PipelineID:                                7,
		PipelineAddress:                           0x1234,
		FunctionName:                              "kernel",
		TemporaryRegisterCount:                    1,
		UniformRegisterCount:                      2,
		SpilledBytes:                              3,
		ThreadInvariantSpilled:                    4,
		ThreadgroupMemory:                         5,
		InstructionCount:                          6,
		ALUInstructionCount:                       7,
		FP32InstructionCount:                      8,
		FP16InstructionCount:                      9,
		INT32InstructionCount:                     10,
		INT16InstructionCount:                     11,
		BranchInstructionCount:                    12,
		DeviceLoadCount:                           13,
		DeviceStoreCount:                          14,
		DeviceAtomicCount:                         15,
		TextureReadCount:                          16,
		TextureWriteCount:                         17,
		ThreadgroupLoadCount:                      18,
		ThreadgroupStoreCount:                     19,
		ThreadgroupAtomicCount:                    20,
		WaitInstructionCount:                      21,
		ConstantCalculationTemporaryRegisterCount: 22,
		ConstantCalculationPhasePresent:           true,
		CompilationTimeMs:                         23.5,
	}
	args := map[string]interface{}{}
	addPipelineCompilerArgs(args, pipeline, "test")

	tests := []struct {
		key  string
		want interface{}
	}{
		{"pipeline_id", 7},
		{"pipeline_state", "0x1234"},
		{"pipeline_address", uint64(0x1234)},
		{"function_name", "kernel"},
		{"allocated_registers", 1},
		{"uniform_registers", 2},
		{"spilled_bytes", 3},
		{"thread_invariant_spilled", 4},
		{"threadgroup_memory", 5},
		{"instruction_count", 6},
		{"alu_instruction_count", 7},
		{"fp32_instruction_count", 8},
		{"fp16_instruction_count", 9},
		{"int32_instruction_count", 10},
		{"int16_instruction_count", 11},
		{"branch_instruction_count", 12},
		{"device_load_instruction_count", 13},
		{"device_store_instruction_count", 14},
		{"device_atomic_instruction_count", 15},
		{"texture_reads_instruction_count", 16},
		{"texture_writes_instruction_count", 17},
		{"threadgroup_load_instruction_count", 18},
		{"threadgroup_store_instruction_count", 19},
		{"threadgroup_atomic_instruction_count", 20},
		{"wait_instruction_count", 21},
		{"constant_calculation_temporary_register_count", 22},
		{"constant_calculation_phase_present", true},
		{"compilation_time_ms", 23.5},
		{"metrics_source", "test"},
	}
	for _, test := range tests {
		if got := args[test.key]; got != test.want {
			t.Errorf("%s = %#v, want %#v", test.key, got, test.want)
		}
	}
}

func TestAddPipelineCompilerArgsKeepsDispatchIdentity(t *testing.T) {
	args := map[string]interface{}{"pipeline_id": 99}
	addPipelineCompilerArgs(args, &counter.PipelineStats{PipelineID: 7}, "test")
	if got, want := args["pipeline_id"], 99; got != want {
		t.Fatalf("pipeline_id = %v, want existing %v", got, want)
	}
	addPipelineCompilerArgs(args, nil, "test")
}

func timelineTimingSourceTraceDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "capture"), nil, 0o644); err != nil {
		t.Fatalf("write empty capture: %v", err)
	}
	return dir
}

func timelineCaptureWithExtractedTiming(label string, start, end uint64) []byte {
	const labelOffset = 96

	data := make([]byte, 160)
	binary.LittleEndian.PutUint64(data[labelOffset-40:], start)
	copy(data[labelOffset:], label)
	binary.LittleEndian.PutUint64(data[labelOffset+len(label)+8:], end)
	return data
}

func firstTimelineEventByCategory(timeline *Timeline, category string) *TimelineEvent {
	if timeline == nil {
		return nil
	}
	for i := range timeline.Events {
		if timeline.Events[i].Category == category {
			return &timeline.Events[i]
		}
	}
	return nil
}

func findCounterTrackForTest(t *testing.T, tracks []CounterTrack, name string) CounterTrack {
	t.Helper()
	for _, track := range tracks {
		if track.Name == name {
			return track
		}
	}
	t.Fatalf("missing counter track %q", name)
	return CounterTrack{}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestAddDispatchKernelEventsJoinsPipelinesByID guards the join between
// gpuCommandInfoData dispatches and pipelinePerformanceStatistics. The latter
// is an NSDictionary, so its slice order does not follow the pipeline index
// carried by the dispatch records; joining positionally attributes one
// kernel's register and instruction counts to another.
func TestAddDispatchKernelEventsJoinsPipelinesByID(t *testing.T) {
	timeline := &Timeline{
		Encoders: []EncoderInfo{{Index: 0, Label: "encoder0", Type: "compute", StartTime: 1000, EndTime: 21000, Duration: 20000}},
	}
	stats := &counter.StreamDataStats{
		// Dictionary order is the reverse of the pipeline index order.
		Pipelines: []counter.PipelineStats{
			{PipelineID: 458, FunctionName: "other_kernel", InstructionCount: 999},
			{PipelineID: 446, FunctionName: "kernel0", InstructionCount: 12},
		},
		Dispatches: []counter.DispatchInfo{{
			Index:         0,
			PipelineIndex: 0,
			PipelineID:    446,
			FunctionName:  "kernel0",
			EncoderIndex:  0,
			DurationUs:    7,
		}},
	}
	if !addDispatchKernelEvents(timeline, stats, timelineDispatchCaptureStats{}, nil, nil, nil, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	args := timeline.Kernels[0].Args
	if got, want := args["function_name"], "kernel0"; got != want {
		t.Fatalf("function_name = %#v, want %#v", got, want)
	}
	if got, want := args["instruction_count"], 12; got != want {
		t.Fatalf("instruction_count = %#v, want %#v", got, want)
	}
}

// TestAddDispatchKernelEventsNamesAgreeWithPipelineID checks the invariant the
// positional join broke, over every emitted event rather than a single
// dispatch: an event's name comes from pipelineStateInfoData by pipeline index
// and its function_name argument comes from pipelinePerformanceStatistics by
// pipeline ID, so the two must name the same kernel. Under the positional join
// this reported, for example, pipeline 458's name next to pipeline_id 446.
func TestAddDispatchKernelEventsNamesAgreeWithPipelineID(t *testing.T) {
	const n = 8
	timeline := &Timeline{
		Encoders: []EncoderInfo{{Index: 0, Label: "encoder0", Type: "compute", StartTime: 1000, EndTime: 21000, Duration: 20000}},
	}
	stats := &counter.StreamDataStats{}
	for i := 0; i < n; i++ {
		// Dictionary order is rotated relative to the pipeline index order, so
		// every pipeline lands at a different slice position than its index.
		j := (i + 3) % n
		stats.Pipelines = append(stats.Pipelines, counter.PipelineStats{
			PipelineID:       440 + j,
			FunctionName:     fmt.Sprintf("kernel%d", j),
			InstructionCount: 100 + j,
		})
		stats.Dispatches = append(stats.Dispatches, counter.DispatchInfo{
			Index:         i,
			PipelineIndex: i,
			PipelineID:    440 + i,
			FunctionName:  fmt.Sprintf("kernel%d", i),
			EncoderIndex:  0,
			DurationUs:    1,
		})
	}
	if !addDispatchKernelEvents(timeline, stats, timelineDispatchCaptureStats{}, nil, nil, nil, nil) {
		t.Fatal("addDispatchKernelEvents returned false")
	}
	if len(timeline.Kernels) != n {
		t.Fatalf("got %d kernels, want %d", len(timeline.Kernels), n)
	}
	check := func(kind, name string, args map[string]interface{}) {
		t.Helper()
		fn, ok := args["function_name"].(string)
		if !ok {
			t.Errorf("%s %q: no function_name argument", kind, name)
			return
		}
		if fn != name {
			t.Errorf("%s pipeline_id=%v: name %q but function_name %q", kind, args["pipeline_id"], name, fn)
		}
		want := 100 + args["pipeline_id"].(int) - 440
		if got := args["instruction_count"]; got != want {
			t.Errorf("%s pipeline_id=%v: instruction_count = %v, want %v", kind, args["pipeline_id"], got, want)
		}
	}
	for _, k := range timeline.Kernels {
		check("kernel", k.Name, k.Args)
	}
	for _, e := range timeline.Events {
		if e.Category == "kernel" {
			check("event", e.Name, e.Args)
		}
	}
}

func TestUnprofiledRawTraceEmitsPhaseIInstantEvents(t *testing.T) {
	rawTrace := "/Users/tmc/tmp/mlx-go-fast/verify/verify-dbg.gputrace"
	if _, err := os.Stat(rawTrace); os.IsNotExist(err) {
		t.Skipf("raw trace fixture absent: %s", rawTrace)
	}

	trace, err := gputrace.Open(rawTrace)
	if err != nil {
		t.Fatalf("open raw trace: %v", err)
	}

	timeline, err := generateTimeline(trace)
	if err != nil {
		t.Fatalf("build timeline: %v", err)
	}

	outPath := filepath.Join(t.TempDir(), "unprofiled.json")
	if err := exportChromeTracing(timeline, outPath); err != nil {
		t.Fatalf("exportChromeTracing: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read exported timeline: %v", err)
	}

	var traceDoc struct {
		TraceEvents []TimelineEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &traceDoc); err != nil {
		t.Fatalf("unmarshal exported timeline: %v", err)
	}

	var phaseXCount, phaseICount int
	for _, event := range traceDoc.TraceEvents {
		if event.Category == "encoder" || event.Category == "kernel" || event.Category == "dispatch" {
			if event.Phase == "X" {
				phaseXCount++
				t.Errorf("unprofiled event %q category=%q has finite Phase X duration %d", event.Name, event.Category, event.Duration)
			} else if event.Phase == "i" {
				phaseICount++
				if event.Duration != 0 {
					t.Errorf("unprofiled instant event %q category=%q has non-zero duration %d", event.Name, event.Category, event.Duration)
				}
			}
		}
	}

	if phaseICount == 0 {
		t.Fatalf("expected Phase 'i' instant events for unprofiled trace, found 0")
	}
	if phaseXCount > 0 {
		t.Fatalf("found %d finite synthetic Phase 'X' events in unprofiled trace export, want 0", phaseXCount)
	}
}
