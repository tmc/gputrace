package cmd

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"golang.org/x/perf/benchfmt"
)

func TestWriteBenchfmt(t *testing.T) {
	record := benchfmtRecord{
		Suffix: "Qwen 2.5/0.5B",
		Config: []benchfmtConfig{
			{Key: "timing-source", Value: "streamData"},
			{Key: "goarch", Value: "arm64"},
			{Key: "goos", Value: "darwin"},
			{Key: "trace-uuid", Value: "ABC-123"},
			{Key: "pkg", Value: "github.com/tmc/gputrace"},
		},
		Values: []benchfmtValue{
			{Value: 23170000, Unit: benchfmtDispatchSpanUnit},
			{Value: 869, Unit: benchfmtDispatchesUnit},
			{Value: 30, Unit: benchfmtCommandBuffersUnit},
		},
	}
	var out bytes.Buffer
	if err := writeBenchfmt(&out, record); err != nil {
		t.Fatal(err)
	}
	want := `goos: darwin
goarch: arm64
pkg: github.com/tmc/gputrace
trace-uuid: ABC-123
timing-source: streamData

BenchmarkGPUTrace/Qwen_2_5_0_5B-1 1 2.317e+07 dispatch_span_ns/trace 869 dispatches/trace 30 command-buffers/trace
`
	if got := out.String(); got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}

	reader := benchfmt.NewReader(strings.NewReader(out.String()), "test.bench")
	if !reader.Scan() {
		t.Fatalf("Scan: %v", reader.Err())
	}
	result, ok := reader.Result().(*benchfmt.Result)
	if !ok {
		t.Fatalf("record type = %T", reader.Result())
	}
	if got, want := string(result.Name), "GPUTrace/Qwen_2_5_0_5B-1"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	if result.Iters != 1 {
		t.Fatalf("iters = %d, want 1", result.Iters)
	}
	if len(result.Values) != 3 {
		t.Fatalf("values = %d, want 3", len(result.Values))
	}
	for _, want := range record.Config {
		if got := result.GetConfig(want.Key); got != want.Value {
			t.Fatalf("config %q = %q, want %q", want.Key, got, want.Value)
		}
	}
	for _, want := range record.Values {
		got, ok := result.Value(want.Unit)
		if !ok || got != want.Value {
			t.Fatalf("value %q = %v, %v, want %v, true", want.Unit, got, ok, want.Value)
		}
	}
	if reader.Scan() {
		t.Fatalf("unexpected record %T", reader.Result())
	}
	if err := reader.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteBenchfmtDefaultName(t *testing.T) {
	var out bytes.Buffer
	err := writeBenchfmt(&out, benchfmtRecord{
		Values: []benchfmtValue{{Value: 1, Unit: benchfmtEncodersUnit}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "BenchmarkGPUTrace-1 1 1 encoders/trace\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestWriteBenchfmtRejectsInvalidRecords(t *testing.T) {
	tests := []struct {
		name   string
		record benchfmtRecord
		want   string
	}{
		{
			name:   "no values",
			record: benchfmtRecord{},
			want:   "no measurements",
		},
		{
			name: "negative iterations",
			record: benchfmtRecord{
				Iters:  -1,
				Values: []benchfmtValue{{Value: 1, Unit: benchfmtEncodersUnit}},
			},
			want: "iteration count",
		},
		{
			name: "duplicate unit",
			record: benchfmtRecord{Values: []benchfmtValue{
				{Value: 1, Unit: benchfmtDispatchesUnit},
				{Value: 2, Unit: benchfmtDispatchesUnit},
			}},
			want: "duplicate benchfmt unit",
		},
		{
			name: "nonfinite",
			record: benchfmtRecord{Values: []benchfmtValue{
				{Value: math.Inf(1), Unit: benchfmtDispatchesUnit},
			}},
			want: "invalid benchfmt value",
		},
		{
			name: "negative value",
			record: benchfmtRecord{Values: []benchfmtValue{
				{Value: -1, Unit: benchfmtDispatchesUnit},
			}},
			want: "invalid benchfmt value",
		},
		{
			name: "unknown unit",
			record: benchfmtRecord{Values: []benchfmtValue{
				{Value: 1, Unit: "ns/op"},
			}},
			want: "invalid benchfmt unit",
		},
		{
			name: "uppercase config",
			record: benchfmtRecord{
				Config: []benchfmtConfig{{Key: "GoOS", Value: "darwin"}},
				Values: []benchfmtValue{{Value: 1, Unit: benchfmtDispatchesUnit}},
			},
			want: "invalid benchfmt config key",
		},
		{
			name: "config newline",
			record: benchfmtRecord{
				Config: []benchfmtConfig{{Key: "model", Value: "qwen\nbad: value"}},
				Values: []benchfmtValue{{Value: 1, Unit: benchfmtDispatchesUnit}},
			},
			want: "invalid benchfmt config value",
		},
		{
			name: "duplicate config",
			record: benchfmtRecord{
				Config: []benchfmtConfig{
					{Key: "model", Value: "a"},
					{Key: "model", Value: "b"},
				},
				Values: []benchfmtValue{{Value: 1, Unit: benchfmtDispatchesUnit}},
			},
			want: "duplicate benchfmt config key",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			err := writeBenchfmt(&out, test.record)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			if out.Len() != 0 {
				t.Fatalf("partial output = %q", out.String())
			}
		})
	}
}

func TestBenchfmtConfigFlagsAndMerge(t *testing.T) {
	var enabled bool
	var flags benchfmtConfigFlags
	cmd := &cobra.Command{Use: "test"}
	addBenchfmtFlags(cmd, &enabled, &flags)
	if err := cmd.ParseFlags([]string{
		"--benchfmt",
		"--bench-config", "model=qwen2.5",
		"--bench-config=goos=darwin",
	}); err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("benchfmt flag is false")
	}
	if err := validateBenchfmtFlags(enabled, flags); err != nil {
		t.Fatal(err)
	}

	got, err := mergeBenchfmtConfig([]benchfmtConfig{
		{Key: "goos", Value: "linux"},
		{Key: "goarch", Value: "arm64"},
	}, flags)
	if err != nil {
		t.Fatal(err)
	}
	want := []benchfmtConfig{
		{Key: "goos", Value: "darwin"},
		{Key: "goarch", Value: "arm64"},
		{Key: "model", Value: "qwen2.5"},
	}
	if len(got) != len(want) {
		t.Fatalf("merged config = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged config[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestBenchfmtConfigFlagsRejectInvalid(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing equals", args: []string{"--bench-config", "model"}},
		{name: "uppercase key", args: []string{"--bench-config", "Goos=darwin"}},
		{name: "newline", args: []string{"--bench-config", "model=qwen\nbad: value"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var enabled bool
			var flags benchfmtConfigFlags
			cmd := &cobra.Command{Use: "test"}
			addBenchfmtFlags(cmd, &enabled, &flags)
			if err := cmd.ParseFlags(test.args); err == nil {
				t.Fatal("ParseFlags succeeded")
			}
		})
	}
}

func TestBenchfmtConfigFlagsAllowExperimentKeys(t *testing.T) {
	var flags benchfmtConfigFlags
	if err := flags.Set("kv-layout=static"); err != nil {
		t.Fatal(err)
	}
	got, err := mergeBenchfmtConfig(nil, flags)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (benchfmtConfig{Key: "kv-layout", Value: "static"}) {
		t.Fatalf("config = %#v", got)
	}
}

func TestBenchfmtConfigRequiresBenchfmt(t *testing.T) {
	flags := benchfmtConfigFlags{{Key: "model", Value: "qwen"}}
	if err := validateBenchfmtFlags(false, flags); err == nil {
		t.Fatal("validateBenchfmtFlags succeeded")
	}
}

func TestMergeBenchfmtConfigRejectsDuplicateOverride(t *testing.T) {
	_, err := mergeBenchfmtConfig(nil, benchfmtConfigFlags{
		{Key: "model", Value: "a"},
		{Key: "model", Value: "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate --bench-config") {
		t.Fatalf("error = %v", err)
	}
}
