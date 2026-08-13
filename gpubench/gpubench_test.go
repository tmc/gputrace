package gpubench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUnavailableExecutable(t *testing.T) {
	client := Client{Executable: filepath.Join(t.TempDir(), "missing-gputrace")}
	if err := client.Available(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Available error = %v, want ErrUnavailable", err)
	}
	if _, err := client.Report(context.Background(), "trace.gputrace", ReportOptions{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Report error = %v, want ErrUnavailable", err)
	}
}

func TestAnalyzeAndReportMetrics(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "gputrace")
	script := `#!/bin/sh
cat <<'EOF'
{"schema_version":1,"identity":{"path":"trace.gputrace","trace_uuid":"ABC","payload":"full","observer_version":"v1"},"work":{"count":4,"unit":"op"},"structure":{"status":"structural","source":"capture","command_buffers":2,"encoders":4,"dispatches":8},"timing":{"status":"measured","source":"streamData","dispatch_span_ns":1200}}
EOF
`
	if err := os.WriteFile(tool, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	report, err := (Client{Executable: tool}).Report(context.Background(), "trace.gputrace", ReportOptions{
		Work: &Work{Count: 4, Unit: "op"},
	})
	if err != nil {
		t.Fatal(err)
	}
	metrics := make(metricRecorder)
	if err := report.ReportMetrics(metrics); err != nil {
		t.Fatal(err)
	}
	want := metricRecorder{
		"dispatches/op":       2,
		"command-buffers/op":  0.5,
		"encoders/op":         1,
		"dispatch_span_ns/op": 300,
	}
	if !reflect.DeepEqual(metrics, want) {
		t.Fatalf("metrics = %v, want %v", metrics, want)
	}
}

func TestClientOperationsExecConfiguredGputrace(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "gputrace")
	log := filepath.Join(dir, "argv")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$GPUBENCH_ARGV_LOG"
if test "$1" = bench; then
  printf '%s\n' '{"schema_version":1,"identity":{"path":"trace","payload":"full","observer_version":"test"},"structure":{"status":"structural","dispatches":1},"timing":{"status":"unsupported"}}'
fi
`
	if err := os.WriteFile(tool, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	client := Client{
		Executable: tool,
		Env:        []string{"GPUBENCH_ARGV_LOG=" + log},
	}
	ctx := context.Background()
	if _, err := client.Capture(ctx, CaptureOptions{Output: filepath.Join(dir, "run.gputrace"), Dir: dir}, "workload", "arg"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Profile(ctx, "run.gputrace", ProfileOptions{Output: filepath.Join(dir, "profiled.gpuprofiler_raw"), ProfilerOnly: true, Wait: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Report(ctx, "profiled.gputrace", ReportOptions{Work: &Work{Count: 2, Unit: "op"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"capture --output " + filepath.Join(dir, "run.gputrace") + " --dir " + dir + " -- workload arg",
		"profile-replay run.gputrace --output " + filepath.Join(dir, "profiled.gpuprofiler_raw") + " --profiler-only --wait",
		"bench profiled.gputrace --format json --bench-work 2 --bench-work-unit op",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("argv = %#v, want %#v", lines, want)
	}
}

func TestReportMetricsTraceScoped(t *testing.T) {
	dispatches := uint64(8)
	report := &Report{
		Structure: Structure{
			Section:    Section{Status: StatusStructural},
			Dispatches: &dispatches,
		},
	}
	metrics := make(metricRecorder)
	if err := report.ReportMetrics(metrics); err != nil {
		t.Fatal(err)
	}
	if got := metrics["dispatches/trace"]; got != 8 {
		t.Fatalf("dispatches/trace = %v, want 8", got)
	}
}

func TestWorkValidation(t *testing.T) {
	for _, work := range []Work{{Unit: "op"}, {Count: 1}, {Count: 1, Unit: "request"}} {
		if err := validateWork(&work); err == nil {
			t.Fatalf("validateWork(%+v) succeeded", work)
		}
	}
}

type metricRecorder map[string]float64

func (r metricRecorder) ReportMetric(value float64, unit string) {
	r[unit] = value
}
