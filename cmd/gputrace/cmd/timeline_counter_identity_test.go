//go:build darwin

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

func TestExportEncoderCounterIdentityReachesPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	timeline := &Timeline{Events: []TimelineEvent{{
		Name: "Encoder 0", Category: "encoder", Phase: "X", TimestampNS: 1_000,
		DurationNS: 100, ProcessID: 1, ThreadID: 1,
		Args: map[string]interface{}{"index": 0},
	}}}
	archive := &counter.CounterArchive{
		Encoders: []counter.EncoderSamples{{Ordinal: 0, GPUCycles: 100, EndSamples: 16}},
		TraceIDs: &counter.TraceIDTable{Rows: []counter.TraceIDInfo{{
			TraceID: 123, BatchID: 7, SampleIndex: 44,
		}}},
	}
	annotateEncoderCounterArchive(timeline, archive)

	trace := filepath.Join(t.TempDir(), "encoder-counter-identity.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	query := perfettosql.Module + `
SELECT encoder_id, counter_batch_id, counter_sample_index,
       counter_batch_id_source, counter_sample_index_source,
       counter_trace_id_relation, counter_clock_relation
FROM gputrace_encoder;
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
		"0", "7", "44",
		"APSCounterData TraceId to BatchId by encoder execution ordinal",
		"APSCounterData TraceId to SampleIndex by encoder execution ordinal",
		"positional only; TraceId does not equal GRC encoder or kick trace id",
		"aggregate details only; counter sample timestamps are not joined to the busy clock",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], want) {
		t.Fatalf("PerfettoSQL rows = %q, want header and %q", rows, want)
	}
}

func TestExportCounterCatalogReachesPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	timeline := &Timeline{CounterCatalog: []CounterCatalogEntry{
		{GroupOrdinal: 0, ColumnOrdinal: 0, RecordedName: "GRC_TIMESTAMP", Classification: "fixed GRC"},
		{GroupOrdinal: 0, ColumnOrdinal: 7, RecordedName: "opaque-a", Classification: "pass-specific"},
	}}
	trace := filepath.Join(t.TempDir(), "counter-catalog.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	query := perfettosql.Module + `
SELECT group_ordinal, column_ordinal, recorded_name, classification, source,
       semantics, clock_domain, timing_quality
FROM gputrace_counter_catalog
ORDER BY group_ordinal, column_ordinal;
`
	command := exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(query)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("trace processor catalog: %v\n%s", err, output)
	}
	rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := []string{
		"APSCounterData Subdivided Dictionary passList",
		"recorded column identity only; no values, units, derived meaning, encoder attribution, or clock mapping",
		"none", "unavailable",
	}
	want := [][]string{
		append([]string{"0", "0", "GRC_TIMESTAMP", "fixed GRC"}, wantSuffix...),
		append([]string{"0", "7", "opaque-a", "pass-specific"}, wantSuffix...),
	}
	if len(rows) != 3 || !slices.Equal(rows[1], want[0]) || !slices.Equal(rows[2], want[1]) {
		t.Fatalf("PerfettoSQL catalog rows = %q, want header and %q", rows, want)
	}

	manifestQuery := perfettosql.Module + `
SELECT counter_catalog_availability, counter_catalog_entries,
       counter_catalog_source, counter_catalog_semantics,
       counter_decoder_availability
FROM gputrace_capture;
`
	command = exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(manifestQuery)
	output, err = command.Output()
	if err != nil {
		t.Fatalf("trace processor manifest: %v\n%s", err, output)
	}
	rows, err = csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantManifest := []string{
		"available: recorded APSCounterData pass columns; names remain opaque", "2",
		"APSCounterData Subdivided Dictionary passList",
		"recorded column identity only; no values, units, derived meaning, encoder attribution, or clock mapping",
		"unavailable: no clock-aligned decoded hardware counter series is retained",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], wantManifest) {
		t.Fatalf("PerfettoSQL manifest rows = %q, want header and %q", rows, wantManifest)
	}
}

func TestExportCounterTraceIDsReachPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	timeline := &Timeline{CounterTraceIDs: []CounterTraceIDEntry{
		{RowOrdinal: 0, TraceID: 123, BatchID: 7, SampleIndex: 44},
		{RowOrdinal: 1, TraceID: ^uint64(0), BatchID: 8, SampleIndex: 55},
	}}
	trace := filepath.Join(t.TempDir(), "counter-trace-ids.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	query := perfettosql.Module + `
SELECT row_ordinal, trace_id_int64, trace_id_uint64, batch_id, sample_index, source, semantics,
       clock_domain, timing_quality
FROM gputrace_counter_trace_id
ORDER BY row_ordinal;
`
	command := exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(query)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("trace processor trace ids: %v\n%s", err, output)
	}
	rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := []string{
		"APSCounterData TraceId to BatchId and TraceId to SampleIndex tables",
		"source row identity; only row ordinal has a positional relation to encoder execution order; no GRC equality or clock mapping",
		"none", "unavailable",
	}
	want := [][]string{
		append([]string{"0", "123", "123", "7", "44"}, wantSuffix...),
		append([]string{"1", "-1", "18446744073709551615", "8", "55"}, wantSuffix...),
	}
	if len(rows) != 3 || !slices.Equal(rows[1], want[0]) || !slices.Equal(rows[2], want[1]) {
		t.Fatalf("PerfettoSQL trace id rows = %q, want header and %q", rows, want)
	}

	manifestQuery := perfettosql.Module + `
SELECT counter_trace_id_availability, counter_trace_id_rows,
       counter_trace_id_source, counter_trace_id_semantics
FROM gputrace_capture;
`
	command = exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(manifestQuery)
	output, err = command.Output()
	if err != nil {
		t.Fatalf("trace processor trace id manifest: %v\n%s", err, output)
	}
	rows, err = csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantManifest := []string{
		"available: recorded APSCounterData TraceId table", "2",
		"APSCounterData TraceId to BatchId and TraceId to SampleIndex tables",
		"source row identity; only row ordinal has a positional relation to encoder execution order; no GRC equality or clock mapping",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], wantManifest) {
		t.Fatalf("PerfettoSQL trace id manifest = %q, want header and %q", rows, wantManifest)
	}
}

func TestExportStreamDataTableReachesPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	recordSize := int64(2)
	recordCount := int64(2)
	emptyCount := int64(0)
	remainder := int64(1)
	emptyRemainder := int64(0)
	timeline := &Timeline{StreamDataStrings: []string{"kernel", "", "/source.metal"}, StreamMetadata: &counter.StreamDataMetadata{
		Tables: counter.StreamDataTables{CommandBuffers: &counter.StreamDataTable{
			Bytes: 0, RecordSize: &recordSize, RecordCount: &emptyCount, RemainderBytes: &emptyRemainder,
			SHA256: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", RawBytesHex: "",
		}, GPUCommands: &counter.StreamDataTable{
			Bytes: 5, RecordSize: &recordSize, RecordCount: &recordCount, RemainderBytes: &remainder,
			SHA256: "sha256:table", RawBytesHex: "0102030405",
		}, Functions: &counter.StreamDataTable{DecodeError: "archive data reference is malformed"}},
	}}
	trace := filepath.Join(t.TempDir(), "stream-data-table.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	query := perfettosql.Module + `
SELECT table_name, source_key, byte_count, raw_bytes_hex, table_sha256,
       record_size, record_count, remainder_bytes,
       clock_domain, timing_quality
FROM gputrace_stream_data_table
ORDER BY table_name;
`
	command := exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(query)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("trace processor streamData table: %v\n%s", err, output)
	}
	rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantEmpty := []string{
		"command_buffer", "commandBufferInfoData", "0", "", "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"2", "0", "0", "none", "unavailable",
	}
	want := []string{
		"gpu_command", "gpuCommandInfoData", "5", "0102030405", "sha256:table",
		"2", "2", "1", "none", "unavailable",
	}
	if len(rows) != 3 || !slices.Equal(rows[1], wantEmpty) || !slices.Equal(rows[2], want) {
		t.Fatalf("PerfettoSQL streamData rows = %q, want header and %q, %q", rows, wantEmpty, want)
	}

	manifestQuery := perfettosql.Module + `
SELECT stream_data_function_table_availability,
       stream_data_function_table_decode_error,
       stream_data_function_table_raw_bytes_availability
FROM gputrace_capture;
`
	command = exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(manifestQuery)
	output, err = command.Output()
	if err != nil {
		t.Fatalf("trace processor streamData manifest: %v\n%s", err, output)
	}
	rows, err = csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantManifest := []string{
		"unavailable: archive data reference is malformed",
		"archive data reference is malformed",
		"unavailable: source table bytes were not recovered",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], wantManifest) {
		t.Fatalf("PerfettoSQL streamData manifest = %q, want header and %q", rows, wantManifest)
	}

	stringQuery := perfettosql.Module + `
SELECT source_index, recorded_value, source, semantics, clock_domain, timing_quality
FROM gputrace_stream_data_string
ORDER BY source_index;
`
	command = exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(stringQuery)
	output, err = command.Output()
	if err != nil {
		t.Fatalf("trace processor streamData strings: %v\n%s", err, output)
	}
	rows, err = csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantStringSuffix := []string{
		"streamData keyed archive strings NSArray",
		"source array index and value only; classification and cross-table relationships remain uninterpreted",
		"none", "unavailable",
	}
	wantStrings := [][]string{
		append([]string{"0", "kernel"}, wantStringSuffix...),
		append([]string{"1", ""}, wantStringSuffix...),
		append([]string{"2", "/source.metal"}, wantStringSuffix...),
	}
	if len(rows) != 4 || !slices.Equal(rows[1], wantStrings[0]) || !slices.Equal(rows[2], wantStrings[1]) || !slices.Equal(rows[3], wantStrings[2]) {
		t.Fatalf("PerfettoSQL streamData strings = %q, want header and %q", rows, wantStrings)
	}

	stringManifestQuery := perfettosql.Module + `
SELECT stream_data_string_table_availability, stream_data_string_count,
       stream_data_string_source, stream_data_string_semantics
FROM gputrace_capture;
`
	command = exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(stringManifestQuery)
	output, err = command.Output()
	if err != nil {
		t.Fatalf("trace processor streamData string manifest: %v\n%s", err, output)
	}
	rows, err = csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantStringManifest := []string{
		"available: exact ordered streamData strings array", "3",
		"streamData keyed archive strings NSArray",
		"source array index and value only; classification and cross-table relationships remain uninterpreted",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], wantStringManifest) {
		t.Fatalf("PerfettoSQL streamData string manifest = %q, want header and %q", rows, wantStringManifest)
	}
}
