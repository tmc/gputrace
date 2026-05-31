package analysis

import (
	"os"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestAnalyzeTraceStructure(t *testing.T) {
	tracePath := os.Getenv("GPUTRACE_ANALYZE_TEST_TRACE")
	if tracePath == "" {
		t.Skip("set GPUTRACE_ANALYZE_TEST_TRACE to run this integration test")
	}

	info, err := os.Stat(tracePath)
	if err != nil {
		t.Fatalf("GPUTRACE_ANALYZE_TEST_TRACE=%q is not accessible: %v", tracePath, err)
	}
	if !info.IsDir() {
		t.Fatalf("GPUTRACE_ANALYZE_TEST_TRACE=%q must point to a .gputrace directory", tracePath)
	}

	tr, err := trace.Open(tracePath)
	if err != nil {
		t.Fatalf("open trace from GPUTRACE_ANALYZE_TEST_TRACE=%q: %v", tracePath, err)
	}

	report := tr.AnalyzeTraceStructure()
	t.Log(report)
}
