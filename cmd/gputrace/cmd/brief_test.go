package cmd

import (
	"bytes"
	"encoding/json"
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
	return briefDocument{
		SchemaVersion: "1",
		Header:        newBriefHeader(),
		Payload: briefPayload{
			TraceA: briefTraceSummary{
				Label:              label,
				TotalGPUUs:         total,
				ProfilerEncoders:   9,
				RawComputeEncoders: 74,
			},
			TraceB: briefTraceSummary{
				Label:              "right",
				TotalGPUUs:         5,
				ProfilerEncoders:   9,
				RawComputeEncoders: 18,
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
				ProfilerEncoders   *int `json:"profiler_encoders"`
				RawComputeEncoders *int `json:"raw_compute_encoders"`
				ComputeEncoders    *int `json:"compute_encoders"`
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
