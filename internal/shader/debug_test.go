package shader

import (
	"fmt"
	"testing"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/command"
	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

func TestDebugEncoders(t *testing.T) {
	tr, err := trace.Open("../../testdata/traces/06-six-encoders/06-six-encoders-run1.gputrace")
	if err != nil {
		t.Fatal(err)
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

		fmt.Printf("CB %d: %d encoders\n", cb.Index, len(dcb.Encoders))
		for i, enc := range dcb.Encoders {
			fmt.Printf("  [%d] %s\n", i, enc.Label)
		}
	}
}
