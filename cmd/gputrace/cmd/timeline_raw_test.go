package cmd

import (
	"encoding/csv"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
	"github.com/tmc/gputrace/internal/perfettosql"
)

func TestGPRWCNTREventArgs(t *testing.T) {
	rec := rawProfilerRecord{
		Sample: counter.GPRWCNTRSample{
			Timestamp: 1234, GPUCycles: 5678, SampleType: 6,
			EncoderID: counter.GRCMachineWideID, KickTraceID: counter.GRCMachineWideID,
			KickSlotIdx: 9, SourceID: 10, Counters: []uint64{11, 12},
		},
		Stride: 80, StreamIndex: 3, RecordIndex: 11,
		Source: "RDE_0", RingBufferIndex: 2, StreamSampleCount: 17, StreamMachineWideSamples: 17,
	}
	args := gprwcntrEventArgs(rec)
	checks := map[string]interface{}{
		"stream_index":                3,
		"record_index":                11,
		"timestamp_ticks":             uint64(1234),
		"timestamp_domain":            "mach absolute ticks",
		"grc_gpu_cycles_raw":          uint64(5678),
		"grc_sample_type_raw":         uint64(6),
		"grc_encoder_id_raw":          uint64(counter.GRCMachineWideID),
		"grc_kick_trace_id_raw":       uint64(counter.GRCMachineWideID),
		"grc_kick_slot_index_raw":     uint64(9),
		"grc_source_id_raw":           uint64(10),
		"machine_wide":                true,
		"record_stride_bytes":         80,
		"record_column_count":         9,
		"hardware_counter_columns":    2,
		"record_format":               "GPRWCNTR variable-stride record",
		"stream_source":               "RDE_0",
		"stream_ring_buffer_index":    2,
		"stream_sample_count":         17,
		"stream_machine_wide_samples": 17,
	}
	for key, want := range checks {
		if got := args[key]; got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	if got := args["counter_decode_status"]; got != "fixed GRC columns decoded; hardware counter columns remain uninterpreted" {
		t.Fatalf("counter_decode_status = %q", got)
	}
	if got := args["counter_catalog_join"]; got != "unavailable: ShaderProfilerData stream has no APSCounterData pass-group identity" {
		t.Fatalf("counter_catalog_join = %q", got)
	}
	if got := args["hardware_counter_0_raw"]; got != uint64(11) {
		t.Errorf("hardware_counter_0_raw = %v, want 11", got)
	}
	if got := args["hardware_counter_1_raw"]; got != uint64(12) {
		t.Errorf("hardware_counter_1_raw = %v, want 12", got)
	}
}

func TestExportRawProfilerFixedFieldsReachPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	record := rawProfilerRecord{
		Sample: counter.GPRWCNTRSample{
			Timestamp: 1234, GPUCycles: 5678, SampleType: 6,
			EncoderID: counter.GRCMachineWideID, KickTraceID: counter.GRCMachineWideID,
			KickSlotIdx: 9, SourceID: 10, Counters: []uint64{11, 12},
		},
		Stride: 80, StreamIndex: 3, RecordIndex: 11,
		Source: "RDE_0", RingBufferIndex: 2, StreamSampleCount: 17, StreamMachineWideSamples: 17,
	}
	timeline := &Timeline{Events: []TimelineEvent{{
		Name: "Sample", Category: "gprwcntr", Phase: "I", TimestampNS: 1_000,
		ProcessID: 1, ThreadID: 10, Args: gprwcntrEventArgs(record),
	}}}
	trace := filepath.Join(t.TempDir(), "raw.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockWall); err != nil {
		t.Fatal(err)
	}
	query := perfettosql.Module + `
SELECT stream_id, source_record_index, stream_source, stream_ring_buffer_index,
       stream_sample_count, stream_machine_wide_samples, stream_carrier,
       grc_gpu_cycles_raw,
       grc_sample_type_raw, grc_encoder_id_raw, grc_kick_trace_id_raw,
       grc_kick_slot_index_raw, grc_source_id_raw, machine_wide,
       record_stride_bytes, record_column_count, hardware_counter_columns,
       counter_catalog_join
FROM gputrace_raw_profiler_sample;
`
	command := exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(query)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("trace processor: %v\n%s", err, output)
	}
	rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"3", "11", "RDE_0", "2", "17", "17",
		"APSTimelineData EncoderProfiles exact ShaderProfilerData field",
		"5678", "6", "4294967295", "4294967295", "9", "10", "1", "80", "9", "2",
		"unavailable: ShaderProfilerData stream has no APSCounterData pass-group identity",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], want) {
		t.Fatalf("PerfettoSQL rows = %q, want header and %q", rows, want)
	}
}

func TestExportRawProfilerHardwareCountersReachPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	record := rawProfilerRecord{
		Sample: counter.GPRWCNTRSample{
			Timestamp: 1234, GPUCycles: 5678, SampleType: 6,
			EncoderID: counter.GRCMachineWideID, KickTraceID: counter.GRCMachineWideID,
			KickSlotIdx: 9, SourceID: 10, Counters: []uint64{444, ^uint64(0)},
		},
		Stride: 80, StreamIndex: 3, RecordIndex: 11,
	}
	timeline := &Timeline{Events: []TimelineEvent{{
		Name: "Sample", Category: "gprwcntr", Phase: "I", TimestampNS: 1_000,
		ProcessID: 1, ThreadID: 10, Args: gprwcntrEventArgs(record),
	}}}
	trace := filepath.Join(t.TempDir(), "raw-counters.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockWall); err != nil {
		t.Fatal(err)
	}
	query := perfettosql.Module + `
SELECT stream_id, source_record_index, counter_ordinal, raw_value_int64, raw_value_uint64, semantics
FROM gputrace_raw_profiler_sample_arg
ORDER BY counter_ordinal;
`
	command := exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(query)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("trace processor: %v\n%s", err, output)
	}
	rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"3", "11", "0", "444", "444", "ordinal only; counter name, unit, and interpretation unavailable"},
		{"3", "11", "1", "-1", "18446744073709551615", "ordinal only; counter name, unit, and interpretation unavailable"},
	}
	if len(rows) != 3 || !slices.Equal(rows[1], want[0]) || !slices.Equal(rows[2], want[1]) {
		t.Fatalf("PerfettoSQL rows = %q, want header and %q", rows, want)
	}
}

func TestEnhanceTimelineWithRawDataUsesExactProfiles(t *testing.T) {
	timeline := &Timeline{
		AbsoluteTime: 100, TimebaseNumer: 2, TimebaseDenom: 1,
		rawProfilerProfiles: []counter.EncoderProfile{{
			Index: 4, Source: "RDE_0", RingBufferIndex: 3, RecordStride: 80,
			SampleCount: 2, MachineWideSamples: 2,
			Samples: []counter.GPRWCNTRSample{{Timestamp: 110}, {Timestamp: 120}},
		}},
	}
	if err := enhanceTimelineWithRawData(timeline); err != nil {
		t.Fatal(err)
	}
	if got, want := len(timeline.Events), 3; got != want {
		t.Fatalf("events = %d, want metadata and two samples", got)
	}
	for i, event := range timeline.Events[1:] {
		if event.TimestampNS != uint64((i+1)*20) {
			t.Errorf("record %d timestamp = %d, want %d", i, event.TimestampNS, (i+1)*20)
		}
		if event.Args["stream_index"] != 4 || event.Args["record_index"] != i || event.Args["stream_source"] != "RDE_0" || event.Args["stream_ring_buffer_index"] != 3 {
			t.Errorf("record %d provenance = %#v", i, event.Args)
		}
	}
}

func TestEnhanceTimelineWithRawDataDoesNotPublishPartialStream(t *testing.T) {
	original := TimelineEvent{Name: "existing"}
	timeline := &Timeline{
		AbsoluteTime: 100, TimebaseNumer: 1, TimebaseDenom: 1,
		Events: []TimelineEvent{original},
		rawProfilerProfiles: []counter.EncoderProfile{{
			Index: 4, RecordStride: 80, SampleCount: 2,
			Samples: []counter.GPRWCNTRSample{{Timestamp: 110}, {Timestamp: 99}},
		}},
	}
	if err := enhanceTimelineWithRawData(timeline); err == nil {
		t.Fatal("enhance succeeded with a sample before absolute time")
	}
	if len(timeline.Events) != 1 || timeline.Events[0].Name != original.Name {
		t.Fatalf("events changed after failed enhancement: %#v", timeline.Events)
	}
}
