package difftrace

import (
	"encoding/json"
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

func TestRenderTextByUnmatchedHonorsLimit(t *testing.T) {
	report := renderTextViewReport()
	report.Unmatched = append(report.Unmatched,
		UnmatchedDispatch{Trace: "A", SourceIndex: 2, FunctionName: "second"},
		UnmatchedDispatch{Trace: "A", SourceIndex: 3, FunctionName: "third"},
	)

	got := RenderText(report, "unmatched", false, false, false, false, 2)
	if strings.Contains(got, "third") {
		t.Fatalf("unmatched rows exceed limit:\n%s", got)
	}
	if !strings.Contains(got, "second") {
		t.Fatalf("unmatched rows stopped before limit:\n%s", got)
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

func TestRenderQuickExplain(t *testing.T) {
	report := renderTextViewReport()

	got := RenderQuick(report, 10, true)
	if !strings.Contains(got, "\nInterpretation: ") {
		t.Fatalf("missing interpretation:\n%s", got)
	}
}

func TestRawStructuralDiffReportsCountsAndUnavailableTiming(t *testing.T) {
	countA, countB := 869, 958
	a := &TraceData{
		Path:                 "a.gputrace",
		Label:                "A",
		StructuralDispatches: &countA,
		Warnings:             []string{"no profiler data found for a.gputrace"},
	}
	b := &TraceData{
		Path:                 "b.gputrace",
		Label:                "B",
		StructuralDispatches: &countB,
		Warnings:             []string{"no profiler data found for b.gputrace"},
	}
	report := BuildReport(a, b, AlignDispatches(a, b, AlignOptions{}), ReportOptions{})

	if report.Summary.DispatchCountA != 869 || report.Summary.DispatchCountB != 958 {
		t.Fatalf("dispatch counts = %d/%d, want 869/958", report.Summary.DispatchCountA, report.Summary.DispatchCountB)
	}
	if report.Summary.DispatchCountDelta != -89 {
		t.Fatalf("dispatch count delta = %d, want -89", report.Summary.DispatchCountDelta)
	}
	if report.Summary.TimingAvailable {
		t.Fatal("timing marked available without profiler data")
	}
	if report.Summary.TimingMetric != "unavailable" {
		t.Fatalf("timing metric = %q, want unavailable", report.Summary.TimingMetric)
	}

	for name, got := range map[string]string{
		"quick": RenderQuick(report, 10, true),
		"text":  RenderText(report, "", false, false, false, true, 10),
	} {
		if !strings.Contains(got, "Dispatch count delta (A-B): -89  (A=869 B=958)") {
			t.Fatalf("%s output missing structural delta:\n%s", name, got)
		}
		if !strings.Contains(got, "Timing comparison: unavailable") {
			t.Fatalf("%s output missing unavailable timing:\n%s", name, got)
		}
		if strings.Contains(got, "Dispatch span delta") || strings.Contains(got, "delta: +0us") {
			t.Fatalf("%s output presents unavailable timing as zero:\n%s", name, got)
		}
	}
}

func TestRawStructuralQuickJSONMarksTimingUnavailable(t *testing.T) {
	countA, countB := 869, 958
	a := &TraceData{Path: "a.gputrace", StructuralDispatches: &countA}
	b := &TraceData{Path: "b.gputrace", StructuralDispatches: &countB}
	report := BuildReport(a, b, AlignDispatches(a, b, AlignOptions{}), ReportOptions{})

	data, err := json.Marshal(NewQuickReport(report, 10))
	if err != nil {
		t.Fatal(err)
	}
	var got QuickReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Summary.TimingAvailable || got.Summary.TimingMetric != "unavailable" {
		t.Fatalf("quick JSON timing = available:%t metric:%q", got.Summary.TimingAvailable, got.Summary.TimingMetric)
	}
	if got.Summary.DispatchCountDelta != -89 {
		t.Fatalf("quick JSON dispatch delta = %d, want -89", got.Summary.DispatchCountDelta)
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

func TestRenderCSVByUnmatchedHonorsLimit(t *testing.T) {
	report := renderTextViewReport()
	report.Unmatched = append(report.Unmatched,
		UnmatchedDispatch{Trace: "A", SourceIndex: 2, FunctionName: "second"},
		UnmatchedDispatch{Trace: "A", SourceIndex: 3, FunctionName: "third"},
	)

	got, err := RenderCSV(report, "unmatched", 2)
	if err != nil {
		t.Fatalf("RenderCSV returned error: %v", err)
	}
	if lines := strings.Count(got, "\n"); lines != 3 {
		t.Fatalf("csv lines = %d, want header plus 2 rows:\n%s", lines, got)
	}
}

func TestRenderTextOccurrencesHonorsLimit(t *testing.T) {
	report := renderTextViewReport()
	report.OccurrenceMatches = []OccurrenceMatch{
		{FunctionName: "first"},
		{FunctionName: "second"},
	}

	got := RenderText(report, "occurrences", false, false, false, false, 1)
	if !strings.Contains(got, "first") || strings.Contains(got, "second") {
		t.Fatalf("occurrence rows do not honor limit:\n%s", got)
	}
}

func TestRenderTextDisplaysUnavailableEncoder(t *testing.T) {
	report := renderTextViewReport()
	report.EncoderReports = []EncoderReport{{EncoderIndex: -1}}

	got := RenderText(report, "encoder", false, false, false, false, 1)
	if strings.Contains(got, "\n-1 ") || !strings.Contains(got, "\nn/a") {
		t.Fatalf("unavailable encoder is not human-readable:\n%s", got)
	}
}

func TestRenderQuickIncludesWarnings(t *testing.T) {
	report := Report{
		Warnings: []string{"profiler-only payload: structural comparison unavailable"},
	}
	got := RenderQuick(report, 10, false)
	if !strings.Contains(got, "Warnings:\n  - profiler-only payload: structural comparison unavailable\n") {
		t.Fatalf("RenderQuick warning missing:\n%s", got)
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
