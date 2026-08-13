package gpubench_test

import (
	"context"
	"testing"

	"github.com/tmc/gputrace/gpubench"
)

func BenchmarkTraceEvidence(b *testing.B) {
	// Capture and profile in a separate setup step. They are evidence arms, not
	// samples of the ordinary untraced benchmark timer.
	client := gpubench.Client{}
	report, err := client.Analyze(context.Background(), "decode-perfdata.gputrace", gpubench.AnalyzeOptions{
		Work: &gpubench.Work{Count: 32, Unit: "token"},
	})
	if err != nil {
		b.Skip(err)
	}
	if err := report.ReportMetrics(b); err != nil {
		b.Fatal(err)
	}
}
