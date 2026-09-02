package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"golang.org/x/perf/benchfmt"
)

func TestRawTraceBenchfmtAdmission(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}

	tests := []struct {
		name string
		run  func(*cobra.Command) error
	}{
		{
			name: "profiler",
			run: func(cmd *cobra.Command) error {
				return runProfiler(cmd, []string{tracePath}, &profilerOptions{
					benchfmt: true,
					limit:    20,
				})
			},
		},
		{
			name: "timing",
			run: func(cmd *cobra.Command) error {
				return runTiming(cmd, []string{tracePath}, &timingOptions{
					benchfmt: true,
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&out)

			if err := test.run(cmd); err == nil {
				t.Fatal("command returned nil error for raw trace")
			}
			if out.Len() != 0 {
				t.Fatalf("command wrote stdout on error:\n%s", out.String())
			}
		})
	}
}

func TestStatsRawTraceBenchfmtIsStructuralOnly(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runStats(cmd, []string{tracePath}, &statsOptions{benchfmt: true}); err != nil {
		t.Fatal(err)
	}

	result := readSingleBenchfmtResult(t, out.String())
	for _, unit := range []string{
		benchfmtDispatchesUnit,
		benchfmtCommandBuffersUnit,
	} {
		if value, ok := result.Value(unit); !ok || value <= 0 {
			t.Errorf("%s = %v, %v, want positive structural value", unit, value, ok)
		}
	}
	for _, unit := range []string{
		benchfmtDispatchSpanUnit,
		benchfmtCBActiveUnit,
		benchfmtCBWallUnit,
		benchfmtEffectiveGPUUnit,
		benchfmtProfilerCostSamplesUnit,
		benchfmtProfilerSampleCostUnit,
	} {
		if value, ok := result.Value(unit); ok {
			t.Errorf("%s = %v, want omitted for raw trace", unit, value)
		}
	}
}

func TestStatsBenchfmtRepeatedResults(t *testing.T) {
	tracePath := "../../../testdata/traces/01-single-encoder/01-single-encoder-run1.gputrace"
	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		t.Skipf("trace fixture not available: %s", tracePath)
	}

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	for range 2 {
		if err := runStats(cmd, []string{tracePath}, &statsOptions{benchfmt: true}); err != nil {
			t.Fatal(err)
		}
	}

	reader := benchfmt.NewReader(strings.NewReader(out.String()), "test.bench")
	var results []*benchfmt.Result
	for reader.Scan() {
		result, ok := reader.Result().(*benchfmt.Result)
		if !ok {
			t.Fatalf("record type = %T, want *benchfmt.Result", reader.Result())
		}
		results = append(results, result)
	}
	if err := reader.Err(); err != nil {
		t.Fatal(err)
	}
	if got, want := len(results), 2; got != want {
		t.Fatalf("results = %d, want %d\n%s", got, want, out.String())
	}
	if string(results[0].Name) != string(results[1].Name) {
		t.Fatalf("benchmark names = %q and %q, want equal", results[0].Name, results[1].Name)
	}
	if results[0].Iters != 1 || results[1].Iters != 1 {
		t.Fatalf("iterations = %d and %d, want 1 and 1", results[0].Iters, results[1].Iters)
	}
}

func readSingleBenchfmtResult(t *testing.T, output string) *benchfmt.Result {
	t.Helper()

	reader := benchfmt.NewReader(strings.NewReader(output), "test.bench")
	if !reader.Scan() {
		t.Fatalf("read benchmark result: %v\n%s", reader.Err(), output)
	}
	result, ok := reader.Result().(*benchfmt.Result)
	if !ok {
		t.Fatalf("record type = %T, want *benchfmt.Result", reader.Result())
	}
	if reader.Scan() {
		t.Fatalf("unexpected second benchmark result:\n%s", output)
	}
	if err := reader.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
