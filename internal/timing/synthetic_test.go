package timing

import (
	"testing"

	"github.com/tmc/gputrace/internal/trace"
)

func TestObservedKernelLabelsFallBackToDiscoveredFunctions(t *testing.T) {
	tr := &trace.Trace{KernelNames: []string{"a", "b"}}
	got := observedKernelLabels(tr)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("observed kernel labels = %q, want [a b]", got)
	}
}
