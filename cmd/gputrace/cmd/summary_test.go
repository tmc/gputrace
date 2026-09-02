package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/tmc/gputrace/internal/evidence"
)

func TestWriteSummaryUsesCanonicalVocabulary(t *testing.T) {
	report := &evidence.Report{
		CommandBuffers:  37,
		ComputeEncoders: 23,
		Dispatches:      1166,
		CSLabels:        997,
		UniqueCSLabels:  80,
		DispatchSpan:    11 * time.Millisecond,
		CBActiveTime:    17 * time.Millisecond,
		CBWallSpan:      126 * time.Millisecond,
		TimingSource:    "profiler offsets",
		Functions: []evidence.Function{{
			Name: "kernel", Dispatches: 3, Span: time.Millisecond, SpanShare: 9.1,
		}},
	}
	var out bytes.Buffer
	writeSummary(&out, report, 5)
	got := out.String()
	for _, want := range []string{
		"23 profiler compute encoders",
		"997 CS/debug label records",
		"not encoder instances",
		"Dispatch span",
		"CB active",
		"CB wall",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
	if lines := strings.Count(got, "\n"); lines > 20 {
		t.Fatalf("summary has %d lines, want at most 20:\n%s", lines, got)
	}
}
