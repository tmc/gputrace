package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestBenchRequiresCompleteWorkDenominator(t *testing.T) {
	tests := []struct {
		name string
		opts benchOptions
		want string
	}{
		{"count only", benchOptions{work: 1}, "requires --bench-work-unit"},
		{"unit only", benchOptions{workUnit: "op"}, "requires --bench-work"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := new(cobra.Command)
			err := runBench(cmd, "not-opened", &test.opts)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBenchStructuralTraceUsesTraceUnits(t *testing.T) {
	path := filepath.Join("..", "..", "..", "testdata", "traces", "01-single-encoder", "01-single-encoder-run1.gputrace")
	var out bytes.Buffer
	cmd := new(cobra.Command)
	cmd.SetOut(&out)
	if err := runBench(cmd, path, &benchOptions{format: "benchfmt", name: "BenchmarkFixture"}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "dispatches/trace") {
		t.Fatalf("trace-scoped dispatch unit missing:\n%s", text)
	}
	if strings.Contains(text, "/op") {
		t.Fatalf("undeclared per-operation unit present:\n%s", text)
	}
}
