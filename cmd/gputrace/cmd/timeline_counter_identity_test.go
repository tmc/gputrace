//go:build darwin

package cmd

import (
	"encoding/csv"
	"encoding/hex"
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

func TestExportCounterEncoderAggregatesReachPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	timeline := &Timeline{CounterEncoderAggregates: []counter.EncoderSamples{{
		EncoderID: ^uint64(0), KickTraceID: 123, Group: 2, Ordinal: 3,
		BatchID: 4, BatchIDRecorded: true, SampleIndex: 5, SampleIndexRecorded: true,
		SampleCount: 6, EndSamples: 7,
		GPUCycles: ^uint64(0), StartTicks: ^uint64(0) - 10,
		EndTicks: ^uint64(0) - 5, DurationNs: 9,
	}}}
	trace := filepath.Join(t.TempDir(), "counter-encoder-aggregates.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	query := perfettosql.Module + `
SELECT encoder_id_int64, encoder_id_uint64, kick_trace_id_int64,
       kick_trace_id_uint64, pass_group, execution_ordinal, batch_id,
       sample_index, sample_count, end_sample_count, gpu_cycles_int64,
       gpu_cycles_uint64, counter_start_ticks_int64,
       counter_start_ticks_uint64, counter_end_ticks_int64,
       counter_end_ticks_uint64, counter_duration_ns_int64,
       counter_duration_ns_uint64, attribution_basis, source, semantics,
       clock_domain, clock_mapping, timing_quality
FROM gputrace_counter_encoder_aggregate;
`
	command := exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(query)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("trace processor counter encoder aggregates: %v\n%s", err, output)
	}
	rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-1", "18446744073709551615", "123", "123", "2", "3", "4", "5", "6", "7",
		"-1", "18446744073709551615", "-11", "18446744073709551605",
		"-6", "18446744073709551610", "9", "9",
		"GRC encoder ID present in APSCounterData Encoder Infos",
		"APSCounterData Derived Counter Sample Data",
		"capture-attributed counter aggregate; no Metal encoder foreign key or timeline coordinate",
		"counter_raw", "none", "measured_unaligned",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], want) {
		t.Fatalf("PerfettoSQL counter encoder aggregate = %q, want header and %q", rows, want)
	}

	manifestQuery := perfettosql.Module + `
SELECT counter_encoder_aggregate_availability, counter_encoder_aggregate_count,
       counter_encoder_aggregate_count_semantics,
       counter_encoder_aggregate_source, counter_encoder_aggregate_clock,
       counter_encoder_aggregate_semantics
FROM gputrace_capture;
`
	command = exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(manifestQuery)
	output, err = command.Output()
	if err != nil {
		t.Fatalf("trace processor counter encoder aggregate manifest: %v\n%s", err, output)
	}
	rows, err = csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantManifest := []string{
		"available: capture-attributed APSCounterData aggregate rows", "1",
		"source rows across all pass groups; not a distinct Metal encoder count",
		"APSCounterData Derived Counter Sample Data joined to Encoder Infos",
		"raw counter ticks; no verified mapping to busy or wall time",
		"one aggregate per recorded encoder ID; group and ordinal are pass placement, timestamps remain unaligned",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], wantManifest) {
		t.Fatalf("PerfettoSQL counter encoder aggregate manifest = %q, want header and %q", rows, wantManifest)
	}
}

func TestExportCounterEncoderSamplesReachPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	timeline := &Timeline{CounterEncoderSamples: []counter.AttributedCounterSample{{
		BlobOrdinal: 2, RecordOrdinal: 3, EncoderGroup: 4, ExecutionOrdinal: 5,
		Timestamp: ^uint64(0), GPUCycles: ^uint64(0) - 1,
		SampleType: 5, EncoderID: ^uint64(0) - 2, KickTraceID: 9,
		KickSlotIdx: 10, SourceID: 11, Counters: []uint64{12, ^uint64(0)},
	}}}
	trace := filepath.Join(t.TempDir(), "counter-encoder-samples.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	query := perfettosql.Module + `
SELECT blob_ordinal, record_ordinal, encoder_group, execution_ordinal,
       counter_timestamp_int64, counter_timestamp_uint64,
       gpu_cycles_int64, gpu_cycles_uint64, sample_type,
       encoder_id_int64, encoder_id_uint64,
       kick_trace_id_int64, kick_trace_id_uint64,
       kick_slot_index, source_id, counter_value_count, counter_values_json,
       clock_domain, clock_mapping, timing_quality
FROM gputrace_counter_encoder_sample;
`
	command := exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(query)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("trace processor counter encoder samples: %v\n%s", err, output)
	}
	rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"2", "3", "4", "5", "-1", "18446744073709551615",
		"-2", "18446744073709551614", "5",
		"-3", "18446744073709551613", "9", "9", "10", "11", "2",
		"[12,18446744073709551615]", "counter_raw", "none", "measured_unaligned",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], want) {
		t.Fatalf("PerfettoSQL counter encoder sample = %q, want header and %q", rows, want)
	}

	manifestQuery := perfettosql.Module + `
SELECT counter_encoder_sample_availability, counter_encoder_sample_count,
       counter_encoder_sample_source, counter_encoder_sample_clock,
       counter_encoder_sample_semantics
FROM gputrace_capture;
`
	command = exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(manifestQuery)
	output, err = command.Output()
	if err != nil {
		t.Fatalf("trace processor counter encoder sample manifest: %v\n%s", err, output)
	}
	rows, err = csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][0] != "available: capture-attributed APSCounterData source records" || rows[1][1] != "1" {
		t.Fatalf("PerfettoSQL counter encoder sample manifest = %q", rows)
	}
}

func TestExportAPSDataInventoryReachesPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	dataBytes := 3
	binaryBytes := 16
	objectIndex := uint64(9)
	containerCount := 1
	blobs := []counter.StreamDataBlobInventory{{
		Family:  "aps_data",
		Ordinal: 2, Bytes: 123, SHA256: "sha256:abc", Dictionary: true,
		Keys: []counter.APSDataKeyInventory{{
			Ordinal: 0, Name: "APSTraceDataFile", ValueKind: "data",
			DataBytes: &dataBytes, DataSHA256: "sha256:def",
		}},
		Nodes: []counter.StreamDataNodeInventory{{
			Path: "/APSTraceDataFile", ParentPath: "", Depth: 1,
			Relation: "dictionary", Ordinal: 0, Name: "APSTraceDataFile",
			ObjectIndex: &objectIndex, ExpansionStatus: "expanded", ValueKind: "dictionary",
			ContainerCount: &containerCount,
		}, {
			Path: "/Binaries/bin-1", ParentPath: "/Binaries", Depth: 2,
			Relation: "dictionary", Ordinal: 0, Name: "bin-1",
			ExpansionStatus: "leaf", ValueKind: "data",
			DataBytes: &binaryBytes, DataSHA256: "sha256:binary",
		}, {
			Path: "/Program Address Mappings/0", ParentPath: "/Program Address Mappings", Depth: 2,
			Relation: "array", Ordinal: 0, ExpansionStatus: "expanded", ValueKind: "dictionary",
			ContainerCount: &containerCount,
		}, {
			Path: "/Program Address Mappings/0/binaryUniqueId", ParentPath: "/Program Address Mappings/0", Depth: 3,
			Relation: "dictionary", Ordinal: 0, Name: "binaryUniqueId",
			ExpansionStatus: "leaf", ValueKind: "string", ScalarType: "string", ScalarJSON: `"bin-1"`,
		}, {
			Path: "/Program Address Mappings/0/mappedAddress", ParentPath: "/Program Address Mappings/0", Depth: 3,
			Relation: "dictionary", Ordinal: 1, Name: "mappedAddress",
			ExpansionStatus: "leaf", ValueKind: "number", ScalarType: "uint64", ScalarJSON: "1099511627840",
		}, {
			Path: "/Program Address Mappings/0/encIndex", ParentPath: "/Program Address Mappings/0", Depth: 3,
			Relation: "dictionary", Ordinal: 2, Name: "encIndex",
			ExpansionStatus: "leaf", ValueKind: "number", ScalarType: "uint64", ScalarJSON: "0",
		}, {
			Path: "/Program Address Mappings/0/type", ParentPath: "/Program Address Mappings/0", Depth: 3,
			Relation: "dictionary", Ordinal: 3, Name: "type",
			ExpansionStatus: "leaf", ValueKind: "string", ScalarType: "string", ScalarJSON: `"driver"`,
		}},
	}}
	timeline := &Timeline{StreamMetadata: &counter.StreamDataMetadata{
		APSDataInventory: &counter.APSDataInventory{BlobRecords: blobs},
		ArchiveBlobs:     blobs,
	}, Events: []TimelineEvent{{
		Name: "Encoder 0", Category: "encoder", Phase: "i",
		ProcessID: 1, ThreadID: 1, Args: map[string]any{"index": 0},
	}}}
	trace := filepath.Join(t.TempDir(), "aps-data-inventory.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	queries := []struct {
		query string
		want  []string
	}{
		{`SELECT family, blob_ordinal, byte_count, blob_sha256, dictionary, key_count,
                    node_count, nodes_truncated
              FROM gputrace_stream_data_archive_blob;`, []string{
			"aps_data", "2", "123", "sha256:abc", "1", "1", "7", "0",
		}},
		{`SELECT family, blob_ordinal, key_ordinal, recorded_name, value_kind,
                    data_bytes, data_sha256
              FROM gputrace_stream_data_archive_key;`, []string{
			"aps_data", "2", "0", "APSTraceDataFile", "data", "3", "sha256:def",
		}},
		{`SELECT family, blob_ordinal, node_ordinal, path, parent_path, depth,
                    relation, child_ordinal, recorded_name, object_index,
                    expansion_status, value_kind, container_count, blob_sha256
              FROM gputrace_stream_data_archive_node
              WHERE path = '/APSTraceDataFile';`, []string{
			"aps_data", "2", "0", "/APSTraceDataFile", "", "1",
			"dictionary", "0", "APSTraceDataFile", "9",
			"expanded", "dictionary", "1", "sha256:abc",
		}},
		{`SELECT family, blob_ordinal, binary_ordinal, binary_unique_id,
                    byte_count, binary_sha256
              FROM gputrace_shader_binary;`, []string{
			"aps_data", "2", "0", "bin-1", "16", "sha256:binary",
		}},
		{`SELECT family, blob_ordinal, mapping_ordinal, binary_unique_id,
                    mapping_type, mapped_address_json, mapped_address,
                    recorded_field_count, binary_byte_count, binary_sha256,
                    binary_join_status
              FROM gputrace_program_address_mapping;`, []string{
			"aps_data", "2", "0", "bin-1", "driver",
			"1099511627840", "1099511627840", "4", "16", "sha256:binary", "matched",
		}},
		{`SELECT family, mapping_ordinal, binary_unique_id,
                    mapping_encoder_id_json, encoder_execution_ordinal,
                    encoder_name, encoder_join_status, encoder_join_basis
              FROM gputrace_encoder_program_mapping;`, []string{
			"aps_data", "0", "bin-1", "[NULL]", "0",
			"Encoder 0", "matched",
			"recorded encIndex equality to streamData Encoder Infos execution ordinal; not encID equality",
		}},
		{`SELECT mapping_encoder_id_count, counter_encoder_id_count,
                    mapping_to_counter_encoder_id_equal_count, counter_kick_id_count,
                    mapping_to_counter_kick_id_equal_count, mapping_encoder_index_count,
                    encoder_execution_ordinal_count,
                    mapping_index_to_encoder_ordinal_equal_count
              FROM gputrace_program_encoder_identity_audit;`, []string{
			"0", "0", "0", "0", "0", "1", "1", "1",
		}},
		{`SELECT blob_ordinal, byte_count, blob_sha256, dictionary, key_count,
                    node_count, nodes_truncated,
                    decode_error, source, semantics FROM gputrace_aps_data_blob;`, []string{
			"2", "123", "sha256:abc", "1", "1", "7", "0", "[NULL]",
			"streamData nested NSData archive entry",
			"content identity and deterministic nested dictionary and array projection; private values remain uninterpreted",
		}},
		{`SELECT blob_ordinal, key_ordinal, recorded_name, value_kind, blob_sha256,
                    scalar_type, scalar_json, data_bytes, data_sha256, container_count,
                    descriptor_error,
                    source, semantics FROM gputrace_aps_data_key;`, []string{
			"2", "0", "APSTraceDataFile", "data", "sha256:abc",
			"[NULL]", "[NULL]", "3", "sha256:def", "[NULL]", "[NULL]",
			"streamData nested archive root NSDictionary",
			"sorted root key identity and exact non-object value descriptor; private meaning remains uninterpreted",
		}},
		{`SELECT stream_data_aps_data_inventory_blob_record_count,
                    stream_data_aps_data_inventory_key_record_count,
                    stream_data_aps_data_inventory_blob_record_semantics
              FROM gputrace_capture;`, []string{
			"1", "1", "content identity and sorted root dictionary shape; private values remain uninterpreted",
		}},
		{`SELECT stream_data_archive_blob_count, stream_data_archive_key_count,
                    stream_data_archive_node_count,
                    stream_data_archive_expanded_node_count,
                    stream_data_archive_reference_node_count,
                    stream_data_archive_depth_limited_node_count,
                    stream_data_archive_node_truncated_blob_count,
                    stream_data_archive_shader_binary_count,
                    stream_data_archive_program_address_mapping_count,
                    stream_data_archive_program_address_mapping_binary_match_count,
                    stream_data_archive_program_address_mapping_binary_unmatched_count,
                    stream_data_archive_byte_count,
                    stream_data_archive_aps_data_blob_count,
                    stream_data_archive_scalar_value_count,
                    stream_data_archive_data_value_count,
                    stream_data_archive_container_value_count,
                    stream_data_archive_descriptor_error_count,
                    stream_data_archive_semantics
              FROM gputrace_capture;`, []string{
			"1", "1", "7", "2", "0", "0", "0", "1", "1", "1", "0", "123", "1", "0", "1", "0", "0",
			"source family, ordinal, content identity, and deterministic nested dictionary and array projection; private values remain uninterpreted",
		}},
	}
	for _, test := range queries {
		command := exec.Command(processor, "query", trace)
		command.Stdin = strings.NewReader(perfettosql.Module + test.query)
		output, err := command.Output()
		if err != nil {
			t.Fatalf("trace processor APSData inventory: %v\n%s", err, output)
		}
		rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || !slices.Equal(rows[1], test.want) {
			t.Fatalf("PerfettoSQL APSData inventory = %q, want header and %q", rows, test.want)
		}
	}
}

func TestExportRecordedConfigurationAndLimiterCatalogReachesPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	blob := counter.StreamDataBlobInventory{
		Family: "aps_data", Ordinal: 1, SHA256: "sha256:blob", Dictionary: true,
		Nodes: []counter.StreamDataNodeInventory{{
			Path: "/Configuration Variables/gpu_type", ParentPath: "/Configuration Variables",
			Relation: "dictionary", Name: "gpu_type", ExpansionStatus: "leaf",
			ValueKind: "string", ScalarType: "string", ScalarJSON: `"G16C"`,
		}, {
			Path: "/Configuration Variables/core_mask_list/0", ParentPath: "/Configuration Variables/core_mask_list",
			Relation: "array", Ordinal: 0, ExpansionStatus: "leaf",
			ValueKind: "number", ScalarType: "int64", ScalarJSON: "1023",
		}, {
			Path: "/APS Options/KickAndStateTracing/CountPeriod", ParentPath: "/APS Options/KickAndStateTracing",
			Relation: "dictionary", Name: "CountPeriod", ExpansionStatus: "leaf",
			ValueKind: "number", ScalarType: "int64", ScalarJSON: "4096",
		}, {
			Path: "/Counter Info/counter-a", ParentPath: "/Counter Info",
			Relation: "dictionary", Name: "counter-a", ExpansionStatus: "leaf",
			ValueKind: "number", ScalarType: "int64", ScalarJSON: "1",
		}, {
			Path: "/Limiter Counter List Map/APS_USC/0", ParentPath: "/Limiter Counter List Map/APS_USC",
			Relation: "array", Ordinal: 0, ExpansionStatus: "leaf",
			ValueKind: "string", ScalarType: "string", ScalarJSON: `"counter-a"`,
		}, {
			Path: "/limiter sample counters/0", ParentPath: "/limiter sample counters",
			Relation: "array", Ordinal: 0, ExpansionStatus: "leaf",
			ValueKind: "string", ScalarType: "string", ScalarJSON: `"counter-a"`,
		}, {
			Path: "/apsProfilingConfig/version", ParentPath: "/apsProfilingConfig",
			Relation: "dictionary", Name: "version", ExpansionStatus: "leaf",
			ValueKind: "number", ScalarType: "int64", ScalarJSON: "2",
		}, {
			Path: "/Timebase/0", ParentPath: "/Timebase",
			Relation: "array", Ordinal: 0, ExpansionStatus: "leaf",
			ValueKind: "number", ScalarType: "int64", ScalarJSON: "125",
		}, {
			Path: "/Timebase/1", ParentPath: "/Timebase",
			Relation: "array", Ordinal: 1, ExpansionStatus: "leaf",
			ValueKind: "number", ScalarType: "int64", ScalarJSON: "3",
		}},
	}
	timeline := &Timeline{
		StreamMetadata: &counter.StreamDataMetadata{ArchiveBlobs: []counter.StreamDataBlobInventory{blob}},
		CounterCatalog: []CounterCatalogEntry{{RecordedName: "counter-a"}},
	}
	trace := filepath.Join(t.TempDir(), "recorded-configuration.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	queries := []struct {
		query string
		want  []string
	}{
		{`SELECT family, path, recorded_name, scalar_type, hex(scalar_json), value
              FROM gputrace_stream_data_configuration ORDER BY path;`, []string{
			"aps_data", "/Configuration Variables/core_mask_list/0", "[NULL]", "int64", "31303233", "1023",
			"aps_data", "/Configuration Variables/gpu_type", "gpu_type", "string", "224731364322", "G16C",
		}},
		{`SELECT family, path, recorded_name, scalar_json, value
              FROM gputrace_aps_option;`, []string{
			"aps_data", "/APS Options/KickAndStateTracing/CountPeriod", "CountPeriod", "4096", "4096",
		}},
		{`SELECT family, recorded_name, scalar_json, value FROM gputrace_counter_info;`, []string{
			"aps_data", "counter-a", "1", "1",
		}},
		{`SELECT family, recorded_group, counter_ordinal, recorded_name
              FROM gputrace_limiter_counter_group;`, []string{
			"aps_data", "APS_USC", "0", "counter-a",
		}},
		{`SELECT family, counter_ordinal, recorded_name
              FROM gputrace_limiter_sample_counter;`, []string{
			"aps_data", "0", "counter-a",
		}},
		{`SELECT family, section, path, recorded_name, recorded_ordinal,
                    scalar_type, scalar_json, value
              FROM gputrace_profiler_configuration ORDER BY path;`, []string{
			"aps_data", "Timebase", "/Timebase/0", "[NULL]", "0", "int64", "125", "125",
			"aps_data", "Timebase", "/Timebase/1", "[NULL]", "1", "int64", "3", "3",
			"aps_data", "apsProfilingConfig", "/apsProfilingConfig/version", "version", "0", "int64", "2", "2",
		}},
		{`SELECT family, blob_ordinal, section, scalar_count, distinct_path_count
              FROM gputrace_profiler_configuration_audit ORDER BY section;`, []string{
			"aps_data", "1", "Timebase", "2", "2",
			"aps_data", "1", "apsProfilingConfig", "1", "1",
		}},
		{`SELECT family, sample_counter_count, grouped_counter_count,
                    sample_to_group_equal_count, sample_to_counter_info_equal_count,
                    sample_to_pass_catalog_equal_count
              FROM gputrace_limiter_catalog_audit;`, []string{
			"aps_data", "1", "1", "1", "1", "1",
		}},
		{`SELECT stream_data_archive_configuration_record_count,
                    stream_data_archive_aps_option_record_count,
                    stream_data_archive_counter_info_record_count,
                    stream_data_archive_limiter_group_record_count,
					stream_data_archive_limiter_sample_counter_record_count,
					stream_data_archive_profiling_configuration_record_count
              FROM gputrace_capture;`, []string{
			"2", "1", "1", "1", "1", "3",
		}},
	}
	for _, test := range queries {
		command := exec.Command(processor, "query", trace)
		command.Stdin = strings.NewReader(perfettosql.Module + test.query)
		output, err := command.Output()
		if err != nil {
			t.Fatalf("trace processor recorded configuration: %v\n%s", err, output)
		}
		rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, row := range rows[1:] {
			got = append(got, row...)
		}
		if !slices.Equal(got, test.want) {
			t.Fatalf("PerfettoSQL recorded configuration = %q, want %q", got, test.want)
		}
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

func TestExportPipelineCompilerDiagnosticsReachPerfettoSQL(t *testing.T) {
	processor := os.Getenv("TRACE_PROCESSOR_SHELL")
	if processor == "" {
		t.Skip("set TRACE_PROCESSOR_SHELL to run native PerfettoSQL integration")
	}
	remarks := "--- !Passed\nDebugLoc: { File: kernel.h, Line: 7 }\n"
	cached := false
	line, column := 7, 3
	argumentValue, emptyArgumentValue := "2", ""
	minusOne := int64(-1)
	zero := int64(0)
	timeline := &Timeline{
		PipelineCompilerSource: "streamData pipelinePerformanceStatistics",
		PipelineCompilerStats: []counter.PipelineStats{{
			PipelineID: 989, PipelineAddress: 0xfedc, FunctionName: "kernel", Remarks: &remarks,
			RecordedStatistics: []string{"Constant calculation phase present", "Spilled bytes"},
			CompilerRemarks: []counter.PipelineCompilerRemark{
				{Index: 0, Kind: "Passed", Pass: "loop-unroll", Name: "FullyUnrolled", Function: "agc.main", SourceFile: "kernel.h", SourceLine: &line, SourceColumn: &column, ParseStatus: "complete", Arguments: []counter.PipelineCompilerRemarkArgument{
					{Index: 0, Name: "UnrollCount", Raw: "  - UnrollCount: '2'", RawValue: "'2'", Value: &argumentValue, ParseStatus: "complete"},
				}},
				{Index: 1, Kind: "Analysis", Pass: "asm-printer", Name: "InstructionCount", Function: "agc.main", ParseStatus: "no_source_location", Arguments: []counter.PipelineCompilerRemarkArgument{
					{Index: 0, Name: "BasicBlock", Raw: "  - BasicBlock: ''", RawValue: "''", Value: &emptyArgumentValue, ParseStatus: "complete"},
				}},
			},
			CompilePerformance: &counter.PipelineCompilePerformance{
				FunctionWasCached: &cached, CompilerBackendNanoseconds: &minusOne,
				CompilerTotalNanoseconds: &zero,
			},
		}},
	}
	trace := filepath.Join(t.TempDir(), "pipeline-compiler.pftrace")
	if err := exportPerfettoForClock(timeline, trace, timelineClockBusy); err != nil {
		t.Fatal(err)
	}
	query := perfettosql.Module + `
SELECT pipeline_id, function_name, pipeline_address, pipeline_identity_scope,
       remarks, function_was_cached, compiler_backend_ns, compiler_total_ns,
       compiler_optimization_ns, spilled_bytes, device_atomic_instruction_count,
       constant_calculation_phase_present, recorded_statistic_count,
       hex(recorded_statistics_json), source, semantics, clock_domain, timing_quality
FROM gputrace_pipeline_compiler;
`
	command := exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(query)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("trace processor pipeline compiler: %v\n%s", err, output)
	}
	rows, err := csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"989", "kernel", "65244", "capture-local", remarks, "0", "-1", "0", "[NULL]",
		"0", "[NULL]", "0", "2", strings.ToUpper(hex.EncodeToString([]byte(`["Constant calculation phase present","Spilled bytes"]`))),
		"streamData pipelinePerformanceStatistics",
		"static compilation evidence; remarks are not measured source-line GPU cost; no clock or dispatch join",
		"none", "unavailable",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], want) {
		t.Fatalf("PerfettoSQL pipeline compiler = %q, want header and %q", rows, want)
	}

	remarkQuery := perfettosql.Module + `
SELECT pipeline_id, function_name, remark_index, remark_kind, compiler_pass,
       remark_name, remark_function, source_file, source_line, source_column,
       parse_status, clock_domain, timing_quality
FROM gputrace_pipeline_compiler_remark
ORDER BY remark_index;
`
	command = exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(remarkQuery)
	output, err = command.Output()
	if err != nil {
		t.Fatalf("trace processor pipeline compiler remarks: %v\n%s", err, output)
	}
	rows, err = csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantRemarks := [][]string{
		{"989", "kernel", "0", "Passed", "loop-unroll", "FullyUnrolled", "agc.main", "kernel.h", "7", "3", "complete", "none", "unavailable"},
		{"989", "kernel", "1", "Analysis", "asm-printer", "InstructionCount", "agc.main", "[NULL]", "[NULL]", "[NULL]", "no_source_location", "none", "unavailable"},
	}
	if len(rows) != 3 || !slices.Equal(rows[1], wantRemarks[0]) || !slices.Equal(rows[2], wantRemarks[1]) {
		t.Fatalf("PerfettoSQL pipeline compiler remarks = %q, want header and %q", rows, wantRemarks)
	}

	argumentQuery := perfettosql.Module + `
SELECT pipeline_id, remark_index, argument_index, argument_name, argument_raw,
       argument_raw_value, argument_value, parse_status, clock_domain, timing_quality
FROM gputrace_pipeline_compiler_remark_arg
ORDER BY remark_index, argument_index;
`
	command = exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(argumentQuery)
	output, err = command.Output()
	if err != nil {
		t.Fatalf("trace processor pipeline compiler remark arguments: %v\n%s", err, output)
	}
	rows, err = csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantArguments := [][]string{
		{"989", "0", "0", "UnrollCount", "  - UnrollCount: '2'", "'2'", "2", "complete", "none", "unavailable"},
		{"989", "1", "0", "BasicBlock", "  - BasicBlock: ''", "''", "", "complete", "none", "unavailable"},
	}
	if len(rows) != 3 || !slices.Equal(rows[1], wantArguments[0]) || !slices.Equal(rows[2], wantArguments[1]) {
		t.Fatalf("PerfettoSQL pipeline compiler remark arguments = %q, want header and %q", rows, wantArguments)
	}

	manifestQuery := perfettosql.Module + `
SELECT pipeline_compiler_availability, pipeline_compiler_count,
       pipeline_compiler_count_semantics,
       pipeline_compiler_source, pipeline_compiler_semantics,
       pipeline_compiler_remark_availability, pipeline_compiler_remark_count,
       pipeline_compiler_remark_source_location_count,
       pipeline_compiler_remark_resolved_source_location_count,
       pipeline_compiler_remark_unresolved_source_location_count,
       pipeline_compiler_remark_malformed_count,
       pipeline_compiler_remark_passed_count,
       pipeline_compiler_remark_missed_count,
       pipeline_compiler_remark_analysis_count,
       pipeline_compiler_remark_count_semantics,
       pipeline_compiler_remark_semantics,
       pipeline_compiler_remark_argument_availability,
       pipeline_compiler_remark_argument_count,
       pipeline_compiler_remark_argument_malformed_count,
       pipeline_compiler_remark_argument_count_semantics,
       pipeline_compiler_remark_argument_semantics
FROM gputrace_capture;
`
	command = exec.Command(processor, "query", trace)
	command.Stdin = strings.NewReader(manifestQuery)
	output, err = command.Output()
	if err != nil {
		t.Fatalf("trace processor pipeline compiler manifest: %v\n%s", err, output)
	}
	rows, err = csv.NewReader(strings.NewReader(string(output))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantManifest := []string{
		"available: recorded static compiler diagnostics", "1",
		"decoded source records; projected SQL rows may be lower under an explicit output budget",
		"streamData pipelinePerformanceStatistics",
		"static compilation evidence; remarks are not measured source-line GPU cost; no clock or dispatch join",
		"available: searchable projection of exact compiler Remarks YAML", "2", "1", "1", "0", "0", "1", "0", "1",
		"decoded source documents; projected SQL rows may be lower under an explicit output budget",
		"static compiler pass diagnostics; no duration, sample weight, runtime causality, or source-line GPU cost",
		"available: ordered scalar projection of compiler Remarks Args", "2", "0",
		"decoded source scalar entries; projected SQL rows may be lower when their parent remark is omitted under an explicit output budget",
		"recorded scalar names, order, raw text, and decoded string values only; pass-specific meaning remains uninterpreted",
	}
	if len(rows) != 2 || !slices.Equal(rows[1], wantManifest) {
		t.Fatalf("PerfettoSQL pipeline compiler manifest = %q, want header and %q", rows, wantManifest)
	}
}
