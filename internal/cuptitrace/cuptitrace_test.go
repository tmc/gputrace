// SPDX-License-Identifier: Apache-2.0

package cuptitrace

import (
	"testing"

	"github.com/tmc/gputrace/internal/gpuevent"
)

// TestBuildCapturePreservesRealtimeAnchor pins the fix for two captures that
// could not share a timeline. ReadCapture decodes a ClockSync record, but the
// cupti command used to unpack the capture into fields and rebuild it, which
// dropped that record one line later -- so every trace declared its own clock
// anchored to nothing and Perfetto refused to merge two of them.
func TestBuildCapturePreservesRealtimeAnchor(t *testing.T) {
	cap := gpuevent.Capture{
		Events: []gpuevent.Event{{
			Kind: gpuevent.KindKernel, Name: "k",
			StartNS: 2000, EndNS: 3000, Grid: "1x1x1", Block: "32x1x1",
		}},
		// unix runs 500ns ahead of the cupti clock.
		ClockSync: &gpuevent.ClockSync{UnixNS: 10500, CuptiNS: 10000},
	}
	trace, err := BuildCapture(cap, "test", Options{})
	if err != nil {
		t.Fatalf("BuildCapture: %v", err)
	}
	// Normalize rebases to the first event, 2000, so source zero is cupti
	// 2000, whose wall time is 2500.
	if trace.RealtimeAnchorNS != 2500 {
		t.Errorf("RealtimeAnchorNS = %d, want 2500", trace.RealtimeAnchorNS)
	}
}

// TestBuildCaptureWithoutSyncClaimsNoWallTime checks the absence path: with no
// sync record the trace must keep its own clock rather than assert a wall time
// nothing measured.
func TestBuildCaptureWithoutSyncClaimsNoWallTime(t *testing.T) {
	cap := gpuevent.Capture{Events: []gpuevent.Event{{
		Kind: gpuevent.KindKernel, Name: "k",
		StartNS: 2000, EndNS: 3000, Grid: "1x1x1", Block: "32x1x1",
	}}}
	trace, err := BuildCapture(cap, "test", Options{})
	if err != nil {
		t.Fatalf("BuildCapture: %v", err)
	}
	if trace.RealtimeAnchorNS != 0 {
		t.Errorf("RealtimeAnchorNS = %d, want 0 with no sync record", trace.RealtimeAnchorNS)
	}
}

// TestRealtimeAnchorRefusesUnderflow covers the direction the arithmetic can
// fail in: a cupti clock ahead of unix by more than the capture origin would
// wrap an unsigned subtraction. No anchor is better than a wrapped one.
func TestRealtimeAnchorRefusesUnderflow(t *testing.T) {
	if _, ok := realtimeAnchor(100, gpuevent.ClockSync{UnixNS: 1, CuptiNS: 1000}); ok {
		t.Error("anchor computed from an underflowing pair, want refusal")
	}
	got, ok := realtimeAnchor(1000, gpuevent.ClockSync{UnixNS: 1, CuptiNS: 500})
	if !ok || got != 501 {
		t.Errorf("anchor = %d, %v; want 501, true", got, ok)
	}
}
