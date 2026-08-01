package timing

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A trace with a kernel name but no measured timing used to be given synthetic
// durations guessed from that name. It now reports that timing is unavailable:
// the name is not evidence of how long the kernel ran.
func TestTimingMetricsExtractReportsUnavailableRatherThanSynthetic(t *testing.T) {
	tr := &Trace{
		Path:        timingMetricsTestTraceDir(t),
		KernelNames: []string{"block_softmax_float32"},
	}

	metrics, err := NewTimingMetricsExtractor(tr).Extract()
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if metrics.TimingSource != TimingSourceUnavailable {
		t.Fatalf("TimingSource = %q, want %q", metrics.TimingSource, TimingSourceUnavailable)
	}
	if len(metrics.EncoderTimings) != 0 {
		t.Fatalf("EncoderTimings = %d rows, want none invented", len(metrics.EncoderTimings))
	}
	if len(metrics.KernelTimings) != 0 {
		t.Fatalf("KernelTimings = %d rows, want none invented", len(metrics.KernelTimings))
	}
	if metrics.TotalDuration != 0 {
		t.Fatalf("TotalDuration = %v, want 0: no span was measured", metrics.TotalDuration)
	}

	// The report replaces the cost table, rather than printing an empty one.
	report := FormatTimingMetrics(metrics)
	if !strings.Contains(report, "cannot say how long they took") {
		t.Errorf("report does not state timing is unavailable:\n%s", report)
	}
	if strings.Contains(report, "Functions by Attributed Span") {
		t.Errorf("report still prints the cost table header:\n%s", report)
	}
	if strings.Contains(report, "Encoder/dispatch span") {
		t.Errorf("report still prints a span line for an unmeasured trace:\n%s", report)
	}
}

func TestTimingMetricsExtractMarksCaptureExtractedFallbackApproximate(t *testing.T) {
	const label = "encoder_from_capture"
	start := uint64(0x023456789abcdef1)
	end := start + 250_000

	tr := &Trace{
		Path:          timingMetricsTestTraceDir(t),
		CaptureData:   captureWithExtractedTiming(label, start, end),
		EncoderLabels: []string{label},
	}

	metrics, err := NewTimingMetricsExtractor(tr).Extract()
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if metrics.TimingSource != TimingSourceExtracted {
		t.Fatalf("TimingSource = %q, want %q", metrics.TimingSource, TimingSourceExtracted)
	}
	if !metrics.TimingApproximate {
		t.Fatalf("TimingApproximate = false, want true")
	}
	if got, want := metrics.TotalEncoders, 1; got != want {
		t.Fatalf("TotalEncoders = %d, want %d", got, want)
	}
	if got, want := metrics.EncoderTimings[0].DurationNs, end-start; got != want {
		t.Fatalf("DurationNs = %d, want %d", got, want)
	}
}

func TestTimingSourceApproximationLabels(t *testing.T) {
	tests := []struct {
		source TimingSource
		want   bool
	}{
		{source: TimingSourceProfiler, want: false},
		{source: TimingSourceExtracted, want: true},
	}

	for _, tt := range tests {
		if got := tt.source.IsApproximate(); got != tt.want {
			t.Fatalf("%s IsApproximate = %v, want %v", tt.source, got, tt.want)
		}
	}

	out := FormatTimingMetrics(&TimingMetrics{
		TracePath:            "trace.gputrace",
		TimingSource:         TimingSourceProfiler,
		TimingApproximate:    TimingSourceProfiler.IsApproximate(),
		KernelTimings:        []*KernelTiming{},
		EncoderTimings:       []*EncoderTiming{},
		CommandBufferTimings: []*CommandBufferTiming{},
	})
	if !strings.Contains(out, "Timing Source: profiler (measured)") {
		t.Fatalf("formatted metrics missing measured profiler source:\n%s", out)
	}
}

func timingMetricsTestTraceDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "capture"), nil, 0o644); err != nil {
		t.Fatalf("write empty capture: %v", err)
	}
	return dir
}

func captureWithExtractedTiming(label string, start, end uint64) []byte {
	const labelOffset = 96

	data := make([]byte, 160)
	binary.LittleEndian.PutUint64(data[labelOffset-40:], start)
	copy(data[labelOffset:], label)
	binary.LittleEndian.PutUint64(data[labelOffset+len(label)+8:], end)
	return data
}
