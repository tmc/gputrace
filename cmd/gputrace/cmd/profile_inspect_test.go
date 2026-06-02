//go:build darwin

package cmd

import "testing"

func TestProfileInspectRequireCountersFailsClosed(t *testing.T) {
	oldRequireReal := profileInspectRequireReal
	oldRequireCounters := profileInspectRequireCounters
	defer func() {
		profileInspectRequireReal = oldRequireReal
		profileInspectRequireCounters = oldRequireCounters
	}()

	profileInspectRequireReal = true
	profileInspectRequireCounters = true
	out := &profileInspectOutput{
		RealTiming:          true,
		TimingClaimsAllowed: true,
	}
	if err := enforceProfileInspectRequirements(out); err == nil {
		t.Fatal("expected --require-counters to reject timing-only streamData")
	}

	out.CounterBearingStreamData = true
	out.DerivedCounterSampleCount = 1
	if err := enforceProfileInspectRequirements(out); err != nil {
		t.Fatalf("counter-bearing streamData should satisfy requirements: %v", err)
	}
}

func TestProfileInspectRequireRealFailsClosed(t *testing.T) {
	oldRequireReal := profileInspectRequireReal
	oldRequireCounters := profileInspectRequireCounters
	defer func() {
		profileInspectRequireReal = oldRequireReal
		profileInspectRequireCounters = oldRequireCounters
	}()

	profileInspectRequireReal = true
	profileInspectRequireCounters = false
	if err := enforceProfileInspectRequirements(&profileInspectOutput{}); err == nil {
		t.Fatal("expected --require-real to reject missing timing")
	}
}
