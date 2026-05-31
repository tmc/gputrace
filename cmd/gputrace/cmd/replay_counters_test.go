package cmd

import (
	"strings"
	"testing"
)

func TestReplayCountersFailsClosedBeforeOpeningTraceWithoutSimulate(t *testing.T) {
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
	if strings.Contains(err.Error(), "failed to open trace") {
		t.Fatalf("error %q indicates trace opening happened before fail-closed gate", err)
	}
}

func TestReplayCountersHelpDocumentsSimulationGate(t *testing.T) {
	help := replayCountersCmd.Long
	for _, want := range []string{
		"--simulate builds a sampling plan only",
		"--simulate does not replay GPU work",
		"fails closed before trace replay or GPU work",
		"replay-counters trace.gputrace --simulate",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("replay-counters help does not contain %q", want)
		}
	}

	for _, stale := range []string{
		"Collect FRESH data via replay",
		"Collects NEW counter data from actual GPU replay",
		"Want to re-run and profile workload? Use replay-counters",
	} {
		if strings.Contains(help, stale) {
			t.Fatalf("replay-counters help still contains stale future-work wording %q", stale)
		}
	}
}
