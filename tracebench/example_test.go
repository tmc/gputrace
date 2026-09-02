package tracebench_test

import (
	"fmt"

	"github.com/tmc/gputrace/tracebench"
)

func ExampleReport_ReportMetrics() {
	dispatchSpan := uint64(1200)
	dispatches := uint64(8)
	report := &tracebench.Report{
		Work: &tracebench.Work{Count: 4, Unit: "op"},
		Structure: tracebench.Structure{
			Section:    tracebench.Section{Status: tracebench.StatusStructural},
			Dispatches: &dispatches,
		},
		Timing: tracebench.Timing{
			Section:        tracebench.Section{Status: tracebench.StatusMeasured},
			DispatchSpanNS: &dispatchSpan,
		},
	}
	if err := report.ReportMetrics(printReporter{}); err != nil {
		fmt.Println(err)
	}

	// Output:
	// dispatches/op 2
	// dispatch_span_ns/op 300
}

type printReporter struct{}

func (printReporter) ReportMetric(value float64, unit string) {
	fmt.Println(unit, value)
}
