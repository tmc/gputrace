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
