package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tmc/gputrace/internal/gpuevent"
)

func TestIsCaptureInput(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"events.jsonl", true},
		{"run.JSONL", true},
		{"trace.gputrace", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isCaptureInput(tt.path); got != tt.want {
			t.Errorf("isCaptureInput(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestWriteCaptureDiff(t *testing.T) {
	cmp := &gpuevent.CaptureComparison{
		Verdict:        gpuevent.CaptureImproved,
		Summary:        "faster overall",
		BaseTotalNS:    2_000_000,
		VariantTotalNS: 1_000_000,
		TotalDeltaPct:  -50,
		KernelDeltas: []gpuevent.KernelDelta{
			{Name: "hot", BaseCount: 10, VariantCount: 10, BaseMeanNS: 200_000, VariantMeanNS: 100_000, TotalDeltaNS: -1_000_000, BaseOccupancy: 50, VarOccupancy: 75},
			{Name: "gone", BaseCount: 4, VariantCount: 0, BaseMeanNS: 1000, TotalDeltaNS: -4000, OnlyIn: "base"},
		},
		OnlyInBase:  []string{"gone"},
		Utilization: gpuevent.UtilizationDelta{BaseWallSpanNS: 10_000_000, VariantWallSpanNS: 4_000_000, BaseOccupancyPct: 20, VariantOccupancyPct: 25, BaseIdleNS: 8_000_000, VariantIdleNS: 3_000_000, BaseGapCount: 9, VariantGapCount: 4},
	}
	var buf bytes.Buffer
	writeCaptureDiff(&buf, cmp, "base.gpucapture", "variant.gpucapture", 20)
	got := buf.String()
	for _, want := range []string{
		"verdict: improved",
		"-1.00ms",          // signed delta on the hot kernel
		"50% -> 75%",       // occupancy movement
		"20.0% -> 25.0%",   // occupancy budget
		"Only in base (1)", // the kernel one side stopped running
		"- ",               // the row marker for it
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestSignedDur(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{{-1_500_000, "-1.50ms"}, {2000, "+2.0us"}, {0, "+0ns"}}
	for _, tt := range tests {
		if got := signedDur(tt.in); got != tt.want {
			t.Errorf("signedDur(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
