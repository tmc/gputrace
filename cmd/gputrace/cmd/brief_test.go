package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/difftrace"
)

func TestBriefHeaderBytesStable(t *testing.T) {
	a := testBriefDocument("a", 10)
	b := testBriefDocument("b", 20)

	ah := marshalBriefHeader(t, a)
	bh := marshalBriefHeader(t, b)
	if !bytes.Equal(ah, bh) {
		t.Fatalf("headers differ\nA:\n%s\nB:\n%s", ah, bh)
	}
}

func TestBriefTokenBudgetTruncatesOutliers(t *testing.T) {
	outliers := []difftrace.PipelinePair{
		{FunctionName: "a", AbsDeltaUs: 30},
		{FunctionName: "b", AbsDeltaUs: 20},
		{FunctionName: "c", AbsDeltaUs: 10},
	}
	got := applyBriefTokenBudget(outliers, 2)
	if !got.truncated || got.dropped != 1 {
		t.Fatalf("truncated=%v dropped=%d, want true/1", got.truncated, got.dropped)
	}
	if len(got.outliers) != 2 || got.outliers[0].FunctionName != "a" || got.outliers[1].FunctionName != "b" {
		t.Fatalf("outliers = %+v", got.outliers)
	}
}

func testBriefDocument(label string, total int) briefDocument {
	rawA, rawB := 74, 18
	return briefDocument{
		SchemaVersion: "1",
		Header:        newBriefHeader(),
		Payload: briefPayload{
			TraceA: briefTraceSummary{
				Label:                label,
				TotalGPUUs:           total,
				ProfilerEncoders:     9,
				RawComputeEncoders:   &rawA,
				RawEncodersAvailable: true,
				RawEncodersSource:    "test",
			},
			TraceB: briefTraceSummary{
				Label:                "right",
				TotalGPUUs:           5,
				ProfilerEncoders:     9,
				RawComputeEncoders:   &rawB,
				RawEncodersAvailable: true,
				RawEncodersSource:    "test",
			},
		},
	}
}

func TestBriefTraceSummaryEncoderFields(t *testing.T) {
	brief := testBriefDocument("left", 10)
	data, err := json.Marshal(brief)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Payload struct {
			TraceA struct {
				ProfilerEncoders     *int   `json:"profiler_encoders"`
				RawComputeEncoders   *int   `json:"raw_compute_encoders"`
				RawEncodersAvailable bool   `json:"raw_compute_encoders_available"`
				RawEncodersSource    string `json:"raw_compute_encoders_source"`
				ComputeEncoders      *int   `json:"compute_encoders"`
			} `json:"trace_a"`
			TraceB struct {
				ProfilerEncoders   *int `json:"profiler_encoders"`
				RawComputeEncoders *int `json:"raw_compute_encoders"`
				ComputeEncoders    *int `json:"compute_encoders"`
			} `json:"trace_b"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Payload.TraceA.ProfilerEncoders == nil || *got.Payload.TraceA.ProfilerEncoders != 9 {
		t.Fatalf("trace_a profiler_encoders = %v, want 9", got.Payload.TraceA.ProfilerEncoders)
	}
	if got.Payload.TraceA.RawComputeEncoders == nil || *got.Payload.TraceA.RawComputeEncoders != 74 {
		t.Fatalf("trace_a raw_compute_encoders = %v, want 74", got.Payload.TraceA.RawComputeEncoders)
	}
	if !got.Payload.TraceA.RawEncodersAvailable || got.Payload.TraceA.RawEncodersSource != "test" {
		t.Fatalf("trace_a raw encoder provenance = %t %q", got.Payload.TraceA.RawEncodersAvailable, got.Payload.TraceA.RawEncodersSource)
	}
	if got.Payload.TraceB.ProfilerEncoders == nil || *got.Payload.TraceB.ProfilerEncoders != 9 {
		t.Fatalf("trace_b profiler_encoders = %v, want 9", got.Payload.TraceB.ProfilerEncoders)
	}
	if got.Payload.TraceB.RawComputeEncoders == nil || *got.Payload.TraceB.RawComputeEncoders != 18 {
		t.Fatalf("trace_b raw_compute_encoders = %v, want 18", got.Payload.TraceB.RawComputeEncoders)
	}
	if got.Payload.TraceA.ComputeEncoders != nil || got.Payload.TraceB.ComputeEncoders != nil {
		t.Fatalf("compute_encoders field still present")
	}
}

func marshalBriefHeader(t *testing.T, brief briefDocument) []byte {
	t.Helper()
	data, err := json.MarshalIndent(struct {
		Header briefHeader `json:"header"`
	}{Header: brief.Header}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestWriteBriefMarkdownCarriesComparisonProvenance(t *testing.T) {
	effective := 3900
	brief := briefDocument{
		Header: newBriefHeader(),
		Payload: briefPayload{
			TraceA: briefTraceSummary{
				Label:                 "go",
				Path:                  "/traces/go.gputrace",
				TotalGPUUs:            12_150,
				Dispatches:            488,
				ProfilerEncoders:      2,
				CommandBufferActiveUs: 3_828,
				TimingSource:          "streamData cumulative offsets",
				AttributionLimited:    true,
				Warnings:              []string{"encoder attribution unavailable"},
			},
			TraceB: briefTraceSummary{
				Label:                 "python",
				Path:                  "/traces/python.gputrace",
				TotalGPUUs:            10_769,
				Dispatches:            413,
				ProfilerEncoders:      2,
				CommandBufferActiveUs: 3_913,
				EffectiveGPUTimeUs:    &effective,
				TimingSource:          "streamData cumulative offsets",
			},
			TotalDeltaUs: 1_381,
			Outliers: []difftrace.PipelinePair{{
				FunctionName:   "kernel",
				ThreadgroupSig: "1x1x1/1x1x1",
				AUs:            10,
				BUs:            5,
				AbsDeltaUs:     5,
			}},
			Truncated:    true,
			DroppedCount: 7,
		},
	}

	var out bytes.Buffer
	if err := writeBriefMarkdown(&out, brief); err != nil {
		t.Fatalf("writeBriefMarkdown: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"`/traces/go.gputrace`",
		"`/traces/python.gputrace`",
		"Dispatch span",
		"Command-buffer active time",
		"Xcode Effective GPU Time",
		"Attribution warning:",
		"encoder attribution unavailable",
		"Dispatch-span delta (A-B): **+1381 µs**",
		"7 additional rows omitted",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown missing %q:\n%s", want, got)
		}
	}
}
