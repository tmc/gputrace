package cmd

import (
	"encoding/binary"
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
	}
	args := gprwcntrEventArgs(rec)
	checks := map[string]interface{}{
		"stream_index":             3,
		"record_index":             11,
		"timestamp_ticks":          uint64(1234),
		"timestamp_domain":         "mach absolute ticks",
		"grc_gpu_cycles_raw":       uint64(5678),
		"grc_sample_type_raw":      uint64(6),
		"grc_encoder_id_raw":       uint64(counter.GRCMachineWideID),
		"grc_kick_trace_id_raw":    uint64(counter.GRCMachineWideID),
		"grc_kick_slot_index_raw":  uint64(9),
		"grc_source_id_raw":        uint64(10),
		"machine_wide":             true,
		"record_stride_bytes":      80,
		"record_column_count":      9,
		"hardware_counter_columns": 2,
		"record_format":            "GPRWCNTR variable-stride record",
	}
	for key, want := range checks {
		if got := args[key]; got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	if got := args["counter_decode_status"]; got != "fixed GRC columns decoded; hardware counter columns remain uninterpreted" {
		t.Fatalf("counter_decode_status = %q", got)
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
SELECT stream_id, source_record_index, grc_gpu_cycles_raw,
       grc_sample_type_raw, grc_encoder_id_raw, grc_kick_trace_id_raw,
       grc_kick_slot_index_raw, grc_source_id_raw, machine_wide,
       record_stride_bytes, record_column_count, hardware_counter_columns
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
	want := []string{"3", "11", "5678", "6", "4294967295", "4294967295", "9", "10", "1", "80", "9", "2"}
	if len(rows) != 2 || !slices.Equal(rows[1], want) {
		t.Fatalf("PerfettoSQL rows = %q, want header and %q", rows, want)
	}
}

func TestParseGPRWCNTRRecordsHaveStreamLocalOrdinals(t *testing.T) {
	const stride = 80
	data := make([]byte, 2*stride)
	for i, timestamp := range []uint64{100, 200} {
		off := i * stride
		copy(data[off:], "GPRWCNTR")
		binary.LittleEndian.PutUint64(data[off+8:], timestamp)
	}
	records, err := parseRawProfilerRecords(data, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	for i, record := range records {
		if record.StreamIndex != 4 || record.RecordIndex != i || record.Stride != stride {
			t.Errorf("record %d identity = stream %d record %d stride %d, want stream 4 record %d stride %d", i, record.StreamIndex, record.RecordIndex, record.Stride, i, stride)
		}
	}
}
