package gpuevent

import (
	"strings"
	"testing"
)

// graphNodeRun is one kernel node execution inside a CUDA graph.
func graphNodeRun(graph uint32, node uint64, start uint64) Event {
	return Event{Kind: KindKernel, Name: "k", GraphID: graph, GraphNodeID: node, StartNS: start, EndNS: start + 10}
}

// TestCompletenessFromDropCount checks the tracer's own report. The count
// arrives as one record per buffer that lost records, because the shim
// cannot emit a single total at exit.
func TestCompletenessFromDropCount(t *testing.T) {
	records := `{"kind":"dropped","records":7,"stream_id":7}
{"kind":"kernel","raw_symbol":"_Z1kv","start_ns":100,"end_ns":200,"stream_id":7}
{"kind":"dropped","records":5,"stream_id":13}
`
	cap, err := DecodeJSONL(strings.NewReader(records))
	if err != nil {
		t.Fatal(err)
	}
	if cap.DroppedRecords != 12 {
		t.Errorf("DroppedRecords = %d, want 12 (summed across buffers)", cap.DroppedRecords)
	}
	if len(cap.Events) != 1 {
		t.Errorf("drop records became %d events; they are not activity", len(cap.Events)-1)
	}
	c := MeasureCompleteness(cap)
	if c.Complete() {
		t.Error("a capture that dropped 12 records reports itself complete")
	}
	if got := c.Summary(); !strings.Contains(got, "INCOMPLETE") || !strings.Contains(got, "12") {
		t.Errorf("Summary = %q, want the count and the verdict", got)
	}
	if c.Remedy() == "" {
		t.Error("an incomplete capture offers no remedy")
	}
}

// TestCompletenessFromGraphInvariant is the half that needs nothing from
// the tracer. Launching a graph fires every node, so a node below the
// busiest one is an execution that happened and was not recorded — which
// is what a capture that lost records looks like from the inside when the
// tracer never noticed. It detects the loss; it does not measure it, for
// the reason MeasureCompleteness documents.
func TestCompletenessFromGraphInvariant(t *testing.T) {
	// Graph 1 launched three times: node 1 recorded all three, node 2 only
	// two. One execution is missing.
	partial := Capture{Events: []Event{
		graphNodeRun(1, 1, 100), graphNodeRun(1, 2, 110),
		graphNodeRun(1, 1, 200), graphNodeRun(1, 2, 210),
		graphNodeRun(1, 1, 300),
	}}
	c := MeasureCompleteness(partial)
	if c.DroppedRecords != 0 {
		t.Fatalf("DroppedRecords = %d, want 0; this capture's tracer reported nothing", c.DroppedRecords)
	}
	if got := c.MissingGraphKernels(); got != 1 {
		t.Errorf("MissingGraphKernels = %d, want 1", got)
	}
	if c.UnevenGraphs != 1 {
		t.Errorf("UnevenGraphs = %d, want 1", c.UnevenGraphs)
	}
	if c.Complete() {
		t.Error("a capture missing a graph node execution reports itself complete")
	}
	got := c.Summary()
	if !strings.Contains(got, "no drops") {
		t.Errorf("Summary = %q; it should say the tracer reported no drops, which is the point", got)
	}
	// The shortfall reads as a measurement and is not one, so the line
	// that carries it has to say so: on a real capture this reported 18
	// where the workload's own per-token invariant showed 27 missing.
	if !strings.Contains(got, "at least") || !strings.Contains(got, "a floor, not the loss") {
		t.Errorf("Summary = %q; a shortfall presented without its floor caveat will be read as the loss", got)
	}

	// The same graph with every node recorded is complete, and stays
	// complete however many times it was replayed.
	whole := Capture{Events: []Event{
		graphNodeRun(1, 1, 100), graphNodeRun(1, 2, 110),
		graphNodeRun(1, 1, 200), graphNodeRun(1, 2, 210),
	}}
	if got := MeasureCompleteness(whole); !got.Complete() {
		t.Errorf("an intact capture reports %q", got.Summary())
	}

	// The blind spot, asserted so it is not mistaken for coverage: a lost
	// whole launch leaves every node even, and the invariant reports
	// nothing. This is why the tracer's own drop count is not redundant.
	lostWholeLaunch := Capture{Events: []Event{
		graphNodeRun(1, 1, 100), graphNodeRun(1, 2, 110),
		// the second launch of graph 1 was dropped entirely
		graphNodeRun(1, 1, 300), graphNodeRun(1, 2, 310),
	}}
	if got := MeasureCompleteness(lostWholeLaunch); !got.Complete() {
		t.Errorf("the invariant claims to see a loss it cannot: %q", got.Summary())
	}
}

// TestCompletenessWithoutGraphsMakesNoClaim: a capture with no CUDA graphs
// has no invariant to check, and must not be reported as verified against
// one it does not have.
func TestCompletenessWithoutGraphsMakesNoClaim(t *testing.T) {
	c := MeasureCompleteness(Capture{Events: []Event{
		{Kind: KindKernel, Name: "k", StartNS: 1, EndNS: 2},
	}})
	if !c.Complete() {
		t.Error("nothing contradicts this capture, so it is not incomplete")
	}
	got := c.Summary()
	if !strings.Contains(got, "no CUDA graphs") {
		t.Errorf("Summary = %q, want it to say the cross-check did not run", got)
	}
	// Neither check can confirm a capture, only fail it, so the clean
	// verdict must not read as a guarantee.
	if !strings.Contains(got, "looks complete") || !strings.Contains(got, "nothing contradicts it") {
		t.Errorf("Summary = %q; a clean result claims more than the checks can establish", got)
	}
}

// TestCompareCapturesRefusesPartialCaptures is the gate the artifact's
// retracted numbers needed: a capture missing 48% of its records diffed as
// a 43.9% kernel-time win, which is a plausible, well-formatted answer for
// a subject that was only half present.
func TestCompareCapturesRefusesPartialCaptures(t *testing.T) {
	kernels := []Event{{Kind: KindKernel, Name: "saxpy", StartNS: 0, EndNS: 1000}}
	base := Analyze(kernels, nil)
	variant := Analyze(kernels, nil)
	// Built by hand rather than measured, so Records must be set: the zero
	// value of Completeness describes a capture holding nothing, which is
	// never healthy.
	healthy := Completeness{Records: len(kernels)}
	base.Completeness = &healthy
	partial := Completeness{Records: len(kernels), DroppedRecords: 4000}
	variant.Completeness = &partial

	c := CompareCaptures(base, variant)
	if c.Verdict != CaptureInconclusive {
		t.Errorf("verdict = %q, want %q for a capture that dropped records", c.Verdict, CaptureInconclusive)
	}
	if !strings.Contains(c.Summary, "variant") || !strings.Contains(c.Summary, "4000") {
		t.Errorf("summary = %q, want it to name the side and the count", c.Summary)
	}

	// Two complete captures still diff, and a pair whose decoder never
	// checked is compared rather than blocked on evidence nobody gathered.
	variant.Completeness = &healthy
	if got := CompareCaptures(base, variant); got.Verdict == CaptureInconclusive {
		t.Errorf("two complete captures were refused: %s", got.Summary)
	}
	base.Completeness, variant.Completeness = nil, nil
	if got := CompareCaptures(base, variant); got.Verdict == CaptureInconclusive {
		t.Errorf("unchecked captures were refused: %s", got.Summary)
	}
}

// TestEmptyCaptureIsNeverComplete: zero records is the terminal state of
// the stranding failure, not a separate error and not an inability to
// check. The tracer arms, the flush period returns success, the drop
// counter reads zero, and nothing is ever handed back -- on a GB10 MLX
// decode a 16 MiB buffer produced exactly that, a capture with nothing in
// it and every self-report saying fine. A type that answered "looks
// complete" here would be agreeing with all of them.
func TestEmptyCaptureIsNeverComplete(t *testing.T) {
	c := MeasureCompleteness(Capture{})
	if c.Complete() {
		t.Error("a capture holding no records reports itself complete")
	}
	if got := c.Summary(); !strings.Contains(got, "EMPTY") {
		t.Errorf("Summary = %q, want it to name the capture empty", got)
	}
	// The remedy must not send the reader the way that caused this: a
	// larger buffer strands more, and is what empties a capture outright.
	got := c.Remedy()
	if !strings.Contains(got, "FLUSH_MS") {
		t.Errorf("Remedy = %q, want the flush interval", got)
	}
	if !strings.Contains(got, "do not raise") {
		t.Errorf("Remedy = %q; it must warn against the buffer knob, which is what produces an empty capture", got)
	}

	// The zero value is not usable as "healthy", which is the hazard of
	// making Records load-bearing; assert it so a hand-built Completeness
	// cannot quietly claim a clean capture.
	if (Completeness{}).Complete() {
		t.Error("the zero value reports complete")
	}
}
