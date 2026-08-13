package gpubench

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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
	report, err := (Client{Path: tool}).Analyze(context.Background(), "trace.gputrace", AnalyzeOptions{
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
