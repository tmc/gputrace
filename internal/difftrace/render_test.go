package difftrace

import (
	"strings"
	"testing"
)

func TestRenderTextByMatchesShowsMatchedRows(t *testing.T) {
	report := renderTextViewReport()

	got := RenderText(report, "matches", false, false, false, false, 10)
	if !strings.Contains(got, "\nMatched Dispatches\n") {
		t.Fatalf("missing matched rows section:\n%s", got)
	}
	if !strings.Contains(got, "foo") {
		t.Fatalf("missing matched function row:\n%s", got)
	}
}

func TestRenderTextByUnmatchedShowsUnmatchedRows(t *testing.T) {
	report := renderTextViewReport()

	got := RenderText(report, "unmatched", false, false, false, false, 10)
	if !strings.Contains(got, "\nUnmatched Dispatches\n") {
		t.Fatalf("missing unmatched rows section:\n%s", got)
	}
	if !strings.Contains(got, "extra") {
		t.Fatalf("missing unmatched function row:\n%s", got)
	}
}

func TestNewQuickReportLimitsSections(t *testing.T) {
	report := renderTextViewReport()
	report.TopFunctionDeltas = []FunctionDelta{
		{FunctionName: "a"},
		{FunctionName: "b"},
	}
	report.TopDispatchOutliers = []MatchPair{
		{FunctionName: "a"},
		{FunctionName: "b"},
	}

	got := NewQuickReport(report, 1)
	if len(got.TopFunctionDeltas) != 1 {
		t.Fatalf("top function deltas = %d, want 1", len(got.TopFunctionDeltas))
	}
	if len(got.TopDispatchOutliers) != 1 {
		t.Fatalf("top dispatch outliers = %d, want 1", len(got.TopDispatchOutliers))
	}
	if got.TopFunctionDeltas[0].FunctionName != "a" {
		t.Fatalf("top function = %q, want a", got.TopFunctionDeltas[0].FunctionName)
	}
}

func TestRenderCSVByPipelinePairs(t *testing.T) {
	report := Report{PipelinePairs: []PipelinePair{{
		FunctionName:   "foo",
		ThreadgroupSig: "1x1x1/1x1x1",
		AUs:            50,
		BUs:            25,
		AbsDeltaUs:     25,
		APipelineID:    10,
		BPipelineID:    20,
		APipelineHash:  "ha",
		BPipelineHash:  "hb",
		StaticCounterDelta: StaticCounters{
			Instructions: -12,
			Registers:    -2,
			Stores:       -1,
		},
	}}}

	got, err := RenderCSV(report, "pipeline-pairs", 10)
	if err != nil {
		t.Fatalf("RenderCSV returned error: %v", err)
	}
	want := "function,threadgroup_sig,a_us,b_us,abs_delta_us,a_pipeline_id,b_pipeline_id,a_pipeline_hash,b_pipeline_hash,static_counter_delta_instructions,static_counter_delta_registers,static_counter_delta_loads,static_counter_delta_stores\n" +
		"foo,1x1x1/1x1x1,50,25,25,10,20,ha,hb,-12,-2,0,-1\n"
	if got != want {
		t.Fatalf("csv mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func renderTextViewReport() Report {
	a := &TraceData{Path: "a.gputrace", Label: "a", Dispatches: []Dispatch{
		{SourceIndex: 0, FunctionName: "foo", FunctionKey: functionKey("foo", 1), PipelineID: 1, EncoderIndex: 2, DurationUs: 10},
		{SourceIndex: 1, FunctionName: "extra", FunctionKey: functionKey("extra", 2), PipelineID: 2, EncoderIndex: 2, DurationUs: 7},
	}}
	b := &TraceData{Path: "b.gputrace", Label: "b", Dispatches: []Dispatch{
		{SourceIndex: 0, FunctionName: "foo", FunctionKey: functionKey("foo", 1), PipelineID: 1, EncoderIndex: 2, DurationUs: 8},
	}}
	aligned := AlignDispatches(a, b, AlignOptions{})
	return BuildReport(a, b, aligned, ReportOptions{Limit: 10})
}
