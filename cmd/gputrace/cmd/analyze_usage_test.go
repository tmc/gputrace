package cmd

import (
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestAnalyzeUsageDOTDirections(t *testing.T) {
	events := []trace.DependencyEvent{
		{Offset: 10, Type: trace.EventCS, Label: "Writer"},
		{
			Offset:  11,
			Type:    trace.EventBind,
			Address: 0x2000,
			Name:    "Shared",
			Usage:   trace.MTLResourceUsageWrite,
		},
		{Offset: 20, Type: trace.EventCS, Label: "Reader"},
		{
			Offset:  21,
			Type:    trace.EventBind,
			Address: 0x2000,
			Name:    "Shared",
			Usage:   trace.MTLResourceUsageRead,
		},
		{Offset: 30, Type: trace.EventCS, Label: "Mutator"},
		{
			Offset:  31,
			Type:    trace.EventBind,
			Address: 0x2000,
			Name:    "Shared",
			Usage:   trace.MTLResourceUsageRead | trace.MTLResourceUsageWrite,
		},
	}

	var out strings.Builder
	if err := writeAnalyzeUsageDOT(&out, collectAnalyzeUsage(events)); err != nil {
		t.Fatalf("writeAnalyzeUsageDOT returned error: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		`"kernel:Writer" -> "buffer:0x2000" [label="Write"];`,
		`"buffer:0x2000" -> "kernel:Reader" [label="Read"];`,
		`"kernel:Mutator" -> "buffer:0x2000" [label="ReadWrite", dir=both];`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("DOT output missing %q:\n%s", want, got)
		}
	}
}

func TestAnalyzeUsageEventUseIsRead(t *testing.T) {
	events := []trace.DependencyEvent{
		{Offset: 20, Type: trace.EventUse, Address: 0x3000},
		{Offset: 10, Type: trace.EventCS, Label: "UsesImplicitly"},
		{Offset: 30, Type: trace.EventUse, Address: 0x3000},
	}

	stats := collectAnalyzeUsage(events)[0x3000]
	if stats == nil {
		t.Fatal("missing buffer stats for EventUse")
	}
	kernel := stats.Kernels["UsesImplicitly"]
	if kernel == nil {
		t.Fatal("missing kernel stats for EventUse")
	}
	if kernel.Reads != 2 || kernel.Writes != 0 {
		t.Fatalf("EventUse counts = reads:%d writes:%d, want reads:2 writes:0", kernel.Reads, kernel.Writes)
	}
}
