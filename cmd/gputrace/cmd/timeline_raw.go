package cmd

import (
	"fmt"

	"github.com/tmc/gputrace/internal/counter"
)

// enhanceTimelineWithRawData adds records from the exact ShaderProfilerData
// fields decoded by internal/counter. It does not scan unrelated NSData
// objects in the enclosing archive for the same magic.
func enhanceTimelineWithRawData(timeline *Timeline) error {
	if timeline == nil {
		return fmt.Errorf("nil timeline")
	}
	if timeline.AbsoluteTime == 0 || timeline.TimebaseNumer == 0 || timeline.TimebaseDenom == 0 {
		return fmt.Errorf("APSTimelineData clock conversion is incomplete")
	}

	events := []TimelineEvent{{
		Name: "thread_name", Phase: "M", ProcessID: 1, ThreadID: 10,
		Args: map[string]interface{}{"name": "GPRWCNTR Samples"},
	}}
	for _, profile := range timeline.rawProfilerProfiles {
		for recordIndex, sample := range profile.Samples {
			if sample.Timestamp < timeline.AbsoluteTime {
				return fmt.Errorf("stream %d record %d precedes APSTimelineData absolute time", profile.Index, recordIndex)
			}
			timestampNS := (sample.Timestamp - timeline.AbsoluteTime) * timeline.TimebaseNumer / timeline.TimebaseDenom
			record := rawProfilerRecord{
				Sample: sample, Stride: profile.RecordStride,
				StreamIndex: profile.Index, RecordIndex: recordIndex,
				Source: profile.Source, RingBufferIndex: profile.RingBufferIndex,
				StreamSampleCount:        profile.SampleCount,
				StreamMachineWideSamples: profile.MachineWideSamples,
			}
			events = append(events, TimelineEvent{
				Name: "Sample", Category: "gprwcntr", Phase: "i",
				Timestamp: timestampNS / 1000, TimestampNS: timestampNS,
				ProcessID: 1, ThreadID: 10, Args: gprwcntrEventArgs(record),
			})
		}
	}
	timeline.Events = append(timeline.Events, events...)
	return nil
}

func gprwcntrEventArgs(rec rawProfilerRecord) map[string]interface{} {
	args := map[string]interface{}{
		"index":                       rec.StreamIndex,
		"stream_index":                rec.StreamIndex,
		"record_index":                rec.RecordIndex,
		"stream_source":               rec.Source,
		"stream_ring_buffer_index":    rec.RingBufferIndex,
		"stream_sample_count":         rec.StreamSampleCount,
		"stream_machine_wide_samples": rec.StreamMachineWideSamples,
		"stream_carrier":              "APSTimelineData EncoderProfiles exact ShaderProfilerData field",
		"timestamp_ticks":             rec.Sample.Timestamp,
		"timestamp_domain":            "mach absolute ticks",
		"coordinate_basis":            "GPRWCNTR timestamp converted with APSTimelineData Absolute Time and Timebase",
		"grc_gpu_cycles_raw":          rec.Sample.GPUCycles,
		"grc_sample_type_raw":         rec.Sample.SampleType,
		"grc_encoder_id_raw":          rec.Sample.EncoderID,
		"grc_kick_trace_id_raw":       rec.Sample.KickTraceID,
		"grc_kick_slot_index_raw":     rec.Sample.KickSlotIdx,
		"grc_source_id_raw":           rec.Sample.SourceID,
		"machine_wide":                rec.Sample.MachineWide(),
		"record_stride_bytes":         rec.Stride,
		"record_column_count":         7 + len(rec.Sample.Counters),
		"hardware_counter_columns":    len(rec.Sample.Counters),
		"record_format":               "GPRWCNTR variable-stride record",
		"counter_decode_status":       "fixed GRC columns decoded; hardware counter columns remain uninterpreted",
		"counter_catalog_join":        "unavailable: ShaderProfilerData stream has no APSCounterData pass-group identity",
	}
	for i, value := range rec.Sample.Counters {
		args[fmt.Sprintf("hardware_counter_%d_raw", i)] = value
	}
	return args
}

type rawProfilerRecord struct {
	Sample                   counter.GPRWCNTRSample
	Stride                   int
	StreamIndex              int
	RecordIndex              int
	Source                   string
	RingBufferIndex          int
	StreamSampleCount        int
	StreamMachineWideSamples int
}
