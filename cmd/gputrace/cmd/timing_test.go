package cmd

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tmc/gputrace/internal/counter"
)

func TestTimingReportWriterUsesStderrForStdoutExports(t *testing.T) {
	tests := []struct {
		name string
		json string
		csv  string
		want *os.File
	}{
		{name: "no export", want: os.Stdout},
		{name: "json file", json: "timing.json", want: os.Stdout},
		{name: "csv file", csv: "timing.csv", want: os.Stdout},
		{name: "json stdout", json: "/dev/stdout", want: os.Stderr},
		{name: "csv stdout", csv: "/dev/stdout", want: os.Stderr},
		{name: "json dash stdout", json: "-", want: os.Stderr},
		{name: "csv dash stdout", csv: "-", want: os.Stderr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timingReportWriter(&timingOptions{
				json: tt.json,
				csv:  tt.csv,
			}); got != tt.want {
				t.Fatalf("timingReportWriter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProfilerTimingReportStatesMetricAndLimitation(t *testing.T) {
	stats := &counter.StreamDataStats{
		TotalDispatchTimeUs: 12,
		EncoderTimings:      []counter.EncoderTimingInfo{{Index: 0, DurationMicros: 20}},
		Dispatches: []counter.DispatchInfo{
			{FunctionName: "kernel", DurationUs: 12},
		},
		Timeline: &counter.TimelineInfo{
			TimebaseNumer: 1,
			TimebaseDenom: 1,
			CommandBufferTimestamps: []counter.CommandBufferTimestamp{
				{Index: 0, StartTicks: 10, EndTicks: 30},
			},
		},
	}
	metrics := convertStreamDataToTimingMetrics("trace.gputrace", stats)
	if metrics.TimingSource != "profiler" || metrics.TimingApproximate {
		t.Fatalf("timing provenance = %q approximate=%v", metrics.TimingSource, metrics.TimingApproximate)
	}
	if got, want := metrics.CommandBufferTimings[0].Duration, 20*time.Nanosecond; got != want {
		t.Fatalf("command buffer duration = %v, want %v", got, want)
	}

	report := formatProfilerTimingMetrics(metrics)
	for _, want := range []string{
		"Source: profiler streamData",
		"Dispatch span:",
		"1 timed function",
		"cumulative offsets",
		"Functions by Dispatch Span",
		"Span Share",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "Unique Kernels") || strings.Contains(report, "Cost") {
		t.Errorf("report uses misleading kernel/cost terminology:\n%s", report)
	}
}

func TestValidateTimingOutputPathsRejectsMultipleStdoutExports(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		csv     string
		wantErr bool
	}{
		{name: "no export"},
		{name: "json stdout", json: "/dev/stdout"},
		{name: "csv stdout", csv: "/dev/stdout"},
		{name: "json dash stdout", json: "-"},
		{name: "csv dash stdout", csv: "-"},
		{name: "both stdout", json: "/dev/stdout", csv: "/dev/stdout", wantErr: true},
		{name: "mixed stdout spellings", json: "-", csv: "/dev/stdout", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTimingOutputPaths(&timingOptions{
				json: tt.json,
				csv:  tt.csv,
			})
			if tt.wantErr && err == nil {
				t.Fatal("validateTimingOutputPaths returned nil error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateTimingOutputPaths returned error: %v", err)
			}
		})
	}
}

func TestWriteTimingOutputUsesSharedStdoutPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "empty", path: ""},
		{name: "dash", path: "-"},
		{name: "dev stdout", path: "/dev/stdout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := captureStdout(t, func() error {
				return writeTimingOutput(tt.path, "test", func(w io.Writer) error {
					_, err := io.WriteString(w, "payload\n")
					return err
				})
			})
			if err != nil {
				t.Fatalf("writeTimingOutput returned error: %v", err)
			}
			if out != "payload\n" {
				t.Fatalf("stdout = %q, want payload", out)
			}
		})
	}
}
