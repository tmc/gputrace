package shader

import (
	"os"
	"testing"

	"github.com/tmc/gputrace/internal/command"
	"github.com/tmc/gputrace/internal/trace"
)

func TestDebugEncoders(t *testing.T) {
	path := "../../testdata/traces/06-six-encoders/06-six-encoders-run1.gputrace"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("skipping test, trace file not found: %s. See docs/TESTING.md for fixture setup.", path)
	}

	tr, err := trace.Open(path)
	if err != nil {
		t.Fatalf("open trace fixture: %v", err)
	}
	defer tr.Close()

	commandBuffers, err := tr.ParseCommandBuffers()
	if err != nil {
		t.Fatal(err)
	}

	for _, cb := range commandBuffers {
		dcb, err := command.ParseDetailedCommandBuffer(tr, cb.Index)
		if err != nil {
			continue
		}

		t.Logf("CB %d: %d encoders", cb.Index, len(dcb.Encoders))
		for i, enc := range dcb.Encoders {
			t.Logf("  [%d] %s", i, enc.Label)
		}
	}
}
