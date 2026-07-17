package cmd

import (
	"strings"
	"testing"
)

func TestBuffersDiffCommandValidatesInputsBeforeOpening(t *testing.T) {
	cmd := newBuffersDiffCommand(new(buffersDiffOptions))
	cmd.SetArgs([]string{"missing-left.gputrace", "missing-right.gputrace"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "trace1: trace file not found") {
		t.Fatalf("Execute error = %v, want trace1 validation error", err)
	}
}
