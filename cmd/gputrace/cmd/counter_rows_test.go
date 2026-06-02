//go:build darwin

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/gputrace/internal/counter"
)

func TestBuildRouteJoinedCounterRowsRequiresXctraceIDs(t *testing.T) {
	stats := &counter.StreamDataStats{
		DerivedCounters: []counter.DerivedCounterData{
			{
				Samples: []counter.DerivedCounterSample{
					{Values: map[string][]uint64{"GPU Read Bandwidth": {10, 20}}},
				},
			},
		},
	}
	intervals := []xctraceIntervalRow{
		{StartNs: 1000, DurationNs: 500, Process: "target-app", CommandBufferID: 0, EncoderID: 7},
	}

	if _, err := buildRouteJoinedCounterRows(stats, intervals); err == nil {
		t.Fatal("expected timestamp-only/partial-ID counter attribution to be rejected")
	}
}

func TestBuildRouteJoinedCounterRowsEmitsStableIDRows(t *testing.T) {
	stats := &counter.StreamDataStats{
		DerivedCounters: []counter.DerivedCounterData{
			{
				Samples: []counter.DerivedCounterSample{
					{
						Values: map[string][]uint64{
							"ALU Utilization":     {3, 4},
							"GPU Read Bandwidth":  {10, 20},
							"GPU Write Bandwidth": {1, 2},
						},
					},
				},
			},
		},
	}
	intervals := []xctraceIntervalRow{
		{
			StartNs:         1000,
			DurationNs:      500,
			Process:         "target-app (123)",
			Label:           "decode_vm_op",
			CommandBufferID: 42,
			EncoderID:       99,
		},
	}

	rows, err := buildRouteJoinedCounterRows(stats, intervals)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	for _, row := range rows {
		if row.XctraceCommandBufferID != 42 || row.XctraceEncoderID != 99 {
			t.Fatalf("missing stable IDs in row: %+v", row)
		}
		if row.CounterStartNs != 1000 || row.CounterEndNs != 1500 {
			t.Fatalf("unexpected counter window: %+v", row)
		}
		if row.TimestampOverlapAllowed {
			t.Fatalf("timestamp overlap should remain disallowed: %+v", row)
		}
		if row.JoinKeySource == "" {
			t.Fatalf("missing join key source: %+v", row)
		}
	}
	if rows[0].CounterName != "ALU Utilization" {
		t.Fatalf("rows are not deterministically sorted: %+v", rows)
	}
}

func TestCounterRowsDecisionReasonAllowsOnlyUsableRows(t *testing.T) {
	if got := counterRowsDecisionReason(true, true); got == "" || got == counterRowsDecisionReason(false, false) {
		t.Fatalf("usable/unusable reasons should be explicit and distinct: %q", got)
	}
}

func TestRAMCounterNamesRequiresMemoryLikeCounter(t *testing.T) {
	rows := []routeJoinedCounterRow{
		{CounterName: "MTLStatTotalGPUCycles"},
		{CounterName: "GPU Read Bandwidth"},
		{CounterName: "DRAM Write Bytes"},
	}
	names := ramCounterNames(rows)
	if len(names) != 2 {
		t.Fatalf("ram counter names = %+v, want two memory-like counters", names)
	}
}

func TestBuildRouteJoinedCounterRowsRejectsMissingIntervalForSample(t *testing.T) {
	stats := &counter.StreamDataStats{
		DerivedCounters: []counter.DerivedCounterData{
			{
				Samples: []counter.DerivedCounterSample{
					{Values: map[string][]uint64{"A": {1}}},
					{Values: map[string][]uint64{"A": {2}}},
				},
			},
		},
	}
	intervals := []xctraceIntervalRow{
		{StartNs: 1000, DurationNs: 500, Process: "target-app", CommandBufferID: 42, EncoderID: 99},
	}

	if _, err := buildRouteJoinedCounterRows(stats, intervals); err == nil {
		t.Fatal("expected missing interval ID row to be rejected")
	}
}

func TestParseTimingRowsJSONFiltersAndSortsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace-timing-rows.json")
	raw := []xctraceIntervalRow{
		{
			StartNs:         3000,
			DurationNs:      10,
			Process:         "other",
			CommandBufferID: 1,
			EncoderID:       3,
		},
		{
			StartNs:         2000,
			DurationNs:      10,
			Process:         "gputrace",
			CommandBufferID: 1,
			EncoderID:       2,
		},
		{
			StartNs:         1000,
			DurationNs:      10,
			Process:         "gputrace",
			CommandBufferID: 1,
			EncoderID:       1,
		},
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	raw[0].Process = "target-gputrace-helper (44)"
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	rows, rowsRead, err := parseTimingRowsJSON(path, "gputrace", 10)
	if err != nil {
		t.Fatal(err)
	}
	if rowsRead != 3 {
		t.Fatalf("rowsRead = %d, want 3", rowsRead)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	if rows[0].StartNs != 1000 || rows[1].StartNs != 2000 || rows[2].StartNs != 3000 {
		t.Fatalf("rows not sorted: %+v", rows)
	}
}
