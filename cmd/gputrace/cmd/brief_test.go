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
		SchemaVersion: "0",
		Header:        newBriefHeader(),
		Payload: briefPayload{
			TraceA: briefTraceSummary{Label: label, TotalGPUUs: total},
			TraceB: briefTraceSummary{Label: "right", TotalGPUUs: 5},
		},
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
