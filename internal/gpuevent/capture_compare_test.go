package gpuevent

import (
	"strings"
	"testing"
)

func eventsFromLines(t *testing.T, lines ...string) []Event {
	t.Helper()
	return feed(t, lines...)
}

func TestCompareCapturesKernelDelta(t *testing.T) {
	base := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"hot","start_ns":0,"end_ns":1000000,"grid":"64x64x1","block":"16x16x1"}`,
		`{"kind":"kernel","name":"small","start_ns":1100000,"end_ns":1150000,"grid":"8x1x1","block":"32x1x1"}`,
	), nil)
	variant := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"hot","start_ns":0,"end_ns":700000,"grid":"32x32x1","block":"16x16x1"}`,
		`{"kind":"kernel","name":"small","start_ns":800000,"end_ns":850000,"grid":"8x1x1","block":"32x1x1"}`,
	), nil)

	c := CompareCaptures(base, variant)
	if len(c.KernelDeltas) != 2 {
		t.Fatalf("kernel deltas = %d, want 2", len(c.KernelDeltas))
	}
	hot := c.KernelDeltas[0]
	if hot.Name != "hot" || hot.BaseMeanNS != 1000000 || hot.VariantMeanNS != 700000 {
		t.Errorf("hot delta = %+v", hot)
	}
	if got := hot.DeltaPct; got > -29 || got < -31 {
		t.Errorf("hot DeltaPct = %.1f, want ~-30", got)
	}
}

func TestCompareCapturesNewAndVanishedKernels(t *testing.T) {
	base := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"gone","start_ns":0,"end_ns":100000}`,
		`{"kind":"kernel","name":"kept","start_ns":200000,"end_ns":300000}`,
	), nil)
	variant := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"kept","start_ns":0,"end_ns":150000}`,
		`{"kind":"kernel","name":"new","start_ns":200000,"end_ns":250000}`,
	), nil)
	c := CompareCaptures(base, variant)
	var vanished, added bool
	for _, d := range c.KernelDeltas {
		switch {
		case d.Name == "gone" && d.VariantCount == 0:
			vanished = true
		case d.Name == "new" && d.BaseCount == 0:
			added = true
		}
	}
	if !vanished || !added {
		t.Errorf("vanished=%v added=%v, deltas %+v", vanished, added, c.KernelDeltas)
	}
}

func TestCompareCapturesSummaryVerdicts(t *testing.T) {
	mk := func(lines ...string) *Report {
		return Analyze(eventsFromLines(t, lines...), nil)
	}
	clearlyFaster := CompareCaptures(
		mk(`{"kind":"kernel","name":"k","start_ns":0,"end_ns":1000000}`),
		mk(`{"kind":"kernel","name":"k","start_ns":0,"end_ns":600000}`),
	)
	if clearlyFaster.Verdict != CaptureImproved {
		t.Errorf("verdict = %v, want improved (%s)", clearlyFaster.Verdict, clearlyFaster.Summary)
	}
	clearlySlower := CompareCaptures(
		mk(`{"kind":"kernel","name":"k","start_ns":0,"end_ns":600000}`),
		mk(`{"kind":"kernel","name":"k","start_ns":0,"end_ns":1000000}`),
	)
	if clearlySlower.Verdict != CaptureRegressed {
		t.Errorf("verdict = %v, want regressed", clearlySlower.Verdict)
	}
	unchanged := CompareCaptures(
		mk(`{"kind":"kernel","name":"k","start_ns":0,"end_ns":1000000}`),
		mk(`{"kind":"kernel","name":"k","start_ns":0,"end_ns":1010000}`),
	)
	if unchanged.Verdict != CaptureUnchanged {
		t.Errorf("verdict = %v, want unchanged", unchanged.Verdict)
	}
}

func TestCompareCapturesEmptySide(t *testing.T) {
	base := Analyze(nil, nil)
	variant := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"k","start_ns":0,"end_ns":1000}`), nil)
	c := CompareCaptures(base, variant)
	if c.Verdict != CaptureInconclusive || !strings.Contains(c.Summary, "base") {
		t.Errorf("verdict = %v summary = %q", c.Verdict, c.Summary)
	}
}
