package cmd

import (
	"strings"
	"testing"
)

func TestReplayCountersRejectsInvalidTraceWithoutSimulate(t *testing.T) {
	err := runReplayCounters(nil, []string{t.TempDir()}, &replayCountersOptions{})
	if err == nil {
		t.Fatal("runReplayCounters succeeded with an invalid trace path")
	}
	if replayCountersRealAvailable() {
		if strings.Contains(err.Error(), "rerun with --simulate") {
			t.Fatalf("real Metal build still reports simulation-only error: %q", err)
		}
		return
	}
	if !strings.Contains(err.Error(), "rerun with --simulate") {
		t.Fatalf("error %q does not mention --simulate", err)
	}
	if strings.Contains(err.Error(), "failed to open trace") {
		t.Fatalf("error %q indicates trace opening happened before fail-closed gate", err)
	}
}

func TestReplayCountersHelpDocumentsModes(t *testing.T) {
	help := replayCountersCmd.Long
	for _, want := range []string{
		"--simulate builds a sampling plan only",
		"--simulate does not replay GPU work",
		"replay-counters trace.gputrace --simulate",
		"Raw resolved bytes are retained",
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
