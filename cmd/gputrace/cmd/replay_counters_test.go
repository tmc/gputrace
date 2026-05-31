package cmd

import (
	"strings"
	"testing"
)

func TestReplayCountersRequiresSimulateUntilMetalBindings(t *testing.T) {
	oldSimulate := simulateOnlyFlag
	oldOutput := counterOutputFlag
	oldCounterSets := counterSetsFlag
	t.Cleanup(func() {
		simulateOnlyFlag = oldSimulate
		counterOutputFlag = oldOutput
		counterSetsFlag = oldCounterSets
	})

	simulateOnlyFlag = false
	counterOutputFlag = ""
	counterSetsFlag = nil

	err := runReplayCounters(nil, []string{t.TempDir()})
	if err == nil {
		t.Fatal("runReplayCounters succeeded without Metal bindings")
	}
	if !strings.Contains(err.Error(), "rerun with --simulate") {
		t.Fatalf("error %q does not mention --simulate", err)
	}
}

func TestReplayCountersHelpDocumentsSimulationGate(t *testing.T) {
	help := replayCountersCmd.Long
	for _, want := range []string{
		"Use --simulate",
		"fails closed until Metal API",
		"replay-counters trace.gputrace --simulate",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("replay-counters help does not contain %q", want)
		}
	}
}
