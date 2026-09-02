package gpubench_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tmc/gputrace/gpubench"
)

func BenchmarkTraceEvidence(b *testing.B) {
	// Capture and profile in a separate setup step. They are evidence arms, not
	// samples of the ordinary untraced benchmark timer.
	client := gpubench.Client{}
	if err := client.Available(); err != nil {
		if errors.Is(err, gpubench.ErrUnavailable) {
			b.Skip(err)
		}
		b.Fatal(err)
	}
	report, err := client.Report(context.Background(), "decode-perfdata.gputrace", gpubench.ReportOptions{
		Work: &gpubench.Work{Count: 32, Unit: "token"},
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := report.ReportMetrics(b); err != nil {
		b.Fatal(err)
	}
}
