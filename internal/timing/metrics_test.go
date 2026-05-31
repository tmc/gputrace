package timing

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTimingMetricsExtractMarksSyntheticFallbackApproximate(t *testing.T) {
	tr := &Trace{
		Path:        timingMetricsTestTraceDir(t),
		KernelNames: []string{"block_softmax_float32"},
	}

	metrics, err := NewTimingMetricsExtractor(tr).Extract()
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if metrics.TimingSource != TimingSourceSynthetic {
		t.Fatalf("TimingSource = %q, want %q", metrics.TimingSource, TimingSourceSynthetic)
	}
	if !metrics.TimingApproximate {
		t.Fatalf("TimingApproximate = false, want true")
	}
	if got, want := metrics.TotalEncoders, 1; got != want {
		t.Fatalf("TotalEncoders = %d, want %d", got, want)
	}
	if got, want := metrics.EncoderTimings[0].Label, "block_softmax_float32"; got != want {
		t.Fatalf("encoder label = %q, want %q", got, want)
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
		{source: TimingSourceSynthetic, want: true},
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
