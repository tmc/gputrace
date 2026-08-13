package tracebench

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/perf/benchfmt"
)

func TestWriteBenchfmtTraceTotals(t *testing.T) {
	report := testReport(nil)
	var out bytes.Buffer
	if err := WriteBenchfmt(&out, report, BenchfmtOptions{Name: "BenchmarkDecode"}); err != nil {
		t.Fatal(err)
	}
	result := readResult(t, out.String())
	want := map[string]float64{
		"dispatches/trace":               20,
		"command-buffers/trace":          2,
		"encoders/trace":                 4,
		"dispatch_span_ns/trace":         1000,
		"command_buffer_active_ns/trace": 800,
	}
	checkValues(t, result, want)
}

func TestWriteBenchfmtNormalizesDeclaredWork(t *testing.T) {
	report := testReport(&Work{Count: 10, Unit: "op"})
	var out bytes.Buffer
	if err := WriteBenchfmt(&out, report, BenchfmtOptions{
		Name:   "BenchmarkDecode",
		Config: []Config{{"arm", "candidate"}},
	}); err != nil {
		t.Fatal(err)
	}
	result := readResult(t, out.String())
	want := map[string]float64{
		"dispatches/op":               2,
		"command-buffers/op":          0.2,
		"encoders/op":                 0.4,
		"dispatch_span_ns/op":         100,
		"command_buffer_active_ns/op": 80,
	}
	checkValues(t, result, want)
	if !strings.Contains(out.String(), "work-count: 10\n") ||
		!strings.Contains(out.String(), "work-unit: op\n") {
		t.Fatalf("normalization provenance missing:\n%s", out.String())
	}
}

func TestWorkValidation(t *testing.T) {
	tests := []struct {
		name string
		work *Work
		want string
	}{
		{"zero", &Work{Unit: "op"}, "positive"},
		{"missing unit", &Work{Count: 1}, "unsupported work unit"},
		{"unknown unit", &Work{Count: 1, Unit: "request"}, "unsupported work unit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateWork(test.work)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReportMetrics(t *testing.T) {
	recorder := make(metricRecorder)
	if err := testReport(&Work{Count: 2, Unit: "token"}).ReportMetrics(recorder); err != nil {
		t.Fatal(err)
	}
	if got := recorder["dispatches/token"]; got != 10 {
		t.Fatalf("dispatches/token = %v, want 10", got)
	}
	if got := recorder["dispatch_span_ns/token"]; got != 500 {
		t.Fatalf("dispatch_span_ns/token = %v, want 500", got)
	}
}

func testReport(work *Work) *Report {
	dispatch, active := uint64(1000), uint64(800)
	commandBuffers, encoders, dispatches := uint64(2), uint64(4), uint64(20)
	return &Report{
		SchemaVersion: SchemaVersion,
		Identity: Identity{
			Payload:         "full",
			ObserverVersion: "test",
			TraceUUID:       "trace-1",
		},
		Work: work,
		Structure: Structure{
			Section:        Section{Status: StatusStructural, Source: "capture"},
			CommandBuffers: &commandBuffers,
			Encoders:       &encoders,
			Dispatches:     &dispatches,
		},
		Timing: Timing{
			Section:               Section{Status: StatusMeasured, Source: "test clock"},
			DispatchSpanNS:        &dispatch,
			CommandBufferActiveNS: &active,
		},
	}
}

func readResult(t *testing.T, text string) *benchfmt.Result {
	t.Helper()
	reader := benchfmt.NewReader(strings.NewReader(text), "test.bench")
	var result *benchfmt.Result
	for reader.Scan() {
		switch record := reader.Result().(type) {
		case *benchfmt.Result:
			if result != nil {
				t.Fatal("more than one result")
			}
			result = record
		case *benchfmt.SyntaxError:
			t.Fatalf("benchfmt syntax error: %v\n%s", record, text)
		}
	}
	if err := reader.Err(); err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatalf("no result in:\n%s", text)
	}
	return result
}

func checkValues(t *testing.T, result *benchfmt.Result, want map[string]float64) {
	t.Helper()
	got := make(map[string]float64)
	for _, value := range result.Values {
		got[value.Unit] = value.Value
	}
	for unit, value := range want {
		if got[unit] != value {
			t.Errorf("%s = %v, want %v", unit, got[unit], value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d values, want %d: %v", len(got), len(want), got)
	}
}

type metricRecorder map[string]float64

func (r metricRecorder) ReportMetric(value float64, unit string) {
	r[unit] = value
}
