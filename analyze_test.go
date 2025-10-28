package gputrace

import (
	"fmt"
	"testing"
)

func TestAnalyzeTraceStructure(t *testing.T) {
	tracePath := "/Users/tmc/ml-explore/mlx-go/examples/mlx-lm-go/models/BenchmarkLlamaForward.gputrace"

	trace, err := Open(tracePath)
	if err != nil {
		t.Fatalf("Failed to open trace: %v", err)
	}

	report := trace.AnalyzeTraceStructure()
	fmt.Println(report)
}
