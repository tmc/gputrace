package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/tmc/gputrace"
)

func profilerMetrics() *gputrace.TimingMetrics {
	return &gputrace.TimingMetrics{
		TracePath: "trace.gputrace",
		KernelTimings: []*gputrace.KernelTiming{
			{Name: "gather_axis", InvocationCount: 1, TotalDuration: 938 * time.Microsecond, PercentOfTotal: 40},
			{Name: "gemm_bfloat16", InvocationCount: 96, TotalDuration: 1400 * time.Microsecond, PercentOfTotal: 60},
		},
	}
}

// The profiler table ranks by cost like the capture table does, so it needs
// the same single-dispatch marker. It shipped without one, which is the table
// the withdrawn gather_axis claim was read from.
func TestProfilerTimingTableMarksSingleDispatchRows(t *testing.T) {
	out := formatProfilerTimingMetrics(profilerMetrics())
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "gather_axis") && !strings.HasSuffix(line, gputrace.LowSampleMarker) {
			t.Errorf("one-call row is unmarked: %q", line)
		}
		if strings.HasPrefix(line, "gemm_bfloat16") && strings.HasSuffix(line, gputrace.LowSampleMarker) {
			t.Errorf("repeated row is marked: %q", line)
		}
	}
	if !strings.Contains(out, "single dispatch (1 of 2)") {
		t.Errorf("missing the marker footnote:\n%s", out)
	}
}

func TestTableMetricsLeavesTheExportUnfiltered(t *testing.T) {
	metrics := profilerMetrics()
	shown, note := tableMetrics(metrics, 2)

	if len(shown.KernelTimings) != 1 {
		t.Errorf("table rows = %d, want the one-call row dropped", len(shown.KernelTimings))
	}
	if len(metrics.KernelTimings) != 2 {
		t.Errorf("--min-calls mutated the metrics the exports share: %d rows left", len(metrics.KernelTimings))
	}
	if !strings.Contains(note, "dropped 1 of 2") {
		t.Errorf("note = %q, want the drop reported", note)
	}
}

func TestTableMetricsDefaultIsAdditiveOnly(t *testing.T) {
	metrics := profilerMetrics()
	shown, note := tableMetrics(metrics, 0)
	if len(shown.KernelTimings) != 2 || note != "" {
		t.Errorf("default filtered %d rows with note %q; want every row and no note",
			2-len(shown.KernelTimings), note)
	}
	if shown != metrics {
		t.Error("default path copied the metrics instead of passing them through")
	}
}
