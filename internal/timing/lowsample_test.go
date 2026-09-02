package timing

import (
	"strings"
	"testing"
	"time"
)

func TestIsLowSample(t *testing.T) {
	tests := []struct {
		name  string
		calls int
		want  bool
	}{
		{"single dispatch", 1, true},
		{"two dispatches", 2, false},
		{"many dispatches", 56, false},
		{"no dispatches", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kt := &KernelTiming{InvocationCount: tt.calls}
			if got := kt.IsLowSample(); got != tt.want {
				t.Errorf("IsLowSample() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCountLowSample(t *testing.T) {
	timings := []*KernelTiming{
		{InvocationCount: 1},
		{InvocationCount: 56},
		{InvocationCount: 1},
	}
	if got := CountLowSample(timings); got != 2 {
		t.Errorf("CountLowSample() = %d, want 2", got)
	}
	if got := CountLowSample(nil); got != 0 {
		t.Errorf("CountLowSample(nil) = %d, want 0", got)
	}
}

// TestFormatTimingMetricsMarksLowSampleRows pins the reason the marker exists:
// a one-call row can outrank repeated work on a span it may not have spent, so
// the caveat has to appear next to the ranking, not only in the docs.
func TestFormatTimingMetricsMarksLowSampleRows(t *testing.T) {
	metrics := &TimingMetrics{
		KernelTimings: []*KernelTiming{
			{Name: "gather_axis", InvocationCount: 1, TotalDuration: 938 * time.Microsecond, PercentOfTotal: 40},
			{Name: "vv_Add", InvocationCount: 56, TotalDuration: 500 * time.Microsecond, PercentOfTotal: 20},
		},
	}
	report := FormatTimingMetrics(metrics)

	for _, line := range strings.Split(report, "\n") {
		switch {
		case strings.HasPrefix(line, "gather_axis"):
			if !strings.HasSuffix(strings.TrimRight(line, " "), LowSampleMarker) {
				t.Errorf("single-dispatch row is not marked:\n%s", line)
			}
		case strings.HasPrefix(line, "vv_Add"):
			if strings.HasSuffix(strings.TrimRight(line, " "), LowSampleMarker) {
				t.Errorf("repeated row is marked:\n%s", line)
			}
		}
	}
	if !strings.Contains(report, "single dispatch (1 of 2)") {
		t.Errorf("report does not count the low-sample rows:\n%s", report)
	}
}

func TestFormatTimingMetricsOmitsFootnoteWhenAllRepeated(t *testing.T) {
	metrics := &TimingMetrics{
		KernelTimings: []*KernelTiming{{Name: "vv_Add", InvocationCount: 56}},
	}
	if report := FormatTimingMetrics(metrics); strings.Contains(report, "single dispatch") {
		t.Errorf("footnote shown with no low-sample rows:\n%s", report)
	}
}
