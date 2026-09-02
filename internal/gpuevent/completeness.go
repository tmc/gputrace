package gpuevent

import "fmt"

// Completeness reports whether a capture holds every record the run
// produced. A partial capture is the harder of the two failures to notice:
// an empty one is obvious, while one missing half its records renders,
// summarizes, and diffs into confident numbers, because the loss is
// uniform across kernel names and sizes and nothing in the output looks
// wrong. On an MLX decode that shape read as a 43.9% kernel-time win.
//
// Two sources answer it, and they are not independent where it matters.
// The tracer's own drop count catches an overflow; the graph invariant
// needs no cooperation from the tracer at all. But a launch that left no
// record anywhere is invisible to both: it leaves every node of its graph
// even, and a record stranded in a buffer that was never completed was
// never dropped, so the counter reads zero honestly. That pairing is what
// let a capture missing 20-47% of its records report itself healthy and
// diff as a 43.9% kernel-time win [V].
//
// So this type detects loss; it does not bound it, and a clean result is
// "nothing contradicts it" rather than a guarantee. Bounding needs an
// instrument outside both — a quantity a missing launch makes wrong
// rather than absent. The workload supplies one: an op that fires once
// per token means a 128-token decode must show 129 of it, and a launch
// that left no record makes that count wrong out loud. That is what
// `gputrace gate -k` scores, and it is the thing to gate on. This type is
// what you check when you have not got it.
type Completeness struct {
	// Records is how many activity records the capture holds. Zero is the
	// terminal state of the stranding failure rather than a separate
	// error: the shim arms, the flush period returns rc=0, the drop
	// counter reads zero, and nothing is ever handed back. A capture with
	// no records is never complete, so the zero value of this type is not
	// usable as "healthy" -- build one with MeasureCompleteness.
	Records int
	// DroppedRecords is what the tracer said it discarded.
	DroppedRecords int
	// GraphKernels counts kernel node executions recorded from CUDA
	// graphs; ExpectedGraphKernels is what those graphs' launch counts
	// imply. See MissingGraphKernels for what a shortfall means.
	GraphKernels         int
	ExpectedGraphKernels int
	// UnevenGraphs counts the graphs whose nodes did not all execute the
	// same number of times. One uneven graph is a boundary effect; many
	// are loss spread across the run.
	UnevenGraphs int
}

// MeasureCompleteness derives a capture's completeness from its own
// records.
//
// The graph half rests on an invariant of CUDA graph execution: launching
// a graph fires every one of its nodes, so within one instantiated graph
// every kernel node must have executed exactly as many times as the graph
// was launched. The busiest node gives the launch count, and any node
// below it is an execution that happened and was not recorded.
//
// The shortfall is a lower bound, and usually a very loose one [V]. A
// tracer that drops records drops a whole buffer's worth at once, which
// is a contiguous run — and a whole graph launch lost that way leaves
// every node of that graph even, so the invariant never sees it. What it
// sees is the ragged edge where the loss started or stopped mid-launch.
// On a GB10 MLX capture believed on other evidence to be missing about
// half its records, this reported six. Read a nonzero shortfall as "this
// capture lost records", never as "this capture lost this many".
//
// One thing other than loss lowers a node's count: a graph launch still
// in flight when the capture ended. That is bounded by one launch per
// graph and lands on the graphs that ran last, so it is the reading to
// check first when the only uneven graph is at the end of the run.
// Conditional graph nodes, which by design do not fire on every launch,
// would also register here; none have been seen in a capture yet [?].
func MeasureCompleteness(cap Capture) Completeness {
	c := Completeness{Records: len(cap.Events), DroppedRecords: cap.DroppedRecords}
	g := AnalyzeGraphs(cap.Events)
	for _, gs := range g.Graphs {
		c.GraphKernels += gs.Kernels
		expected := gs.Launches * gs.Nodes
		c.ExpectedGraphKernels += expected
		if expected != gs.Kernels {
			c.UnevenGraphs++
		}
	}
	return c
}

// MissingGraphKernels is the number of graph kernel executions the
// invariant implies happened and no record describes. It is never
// negative: a node cannot execute more often than its graph launched.
func (c Completeness) MissingGraphKernels() int {
	if c.ExpectedGraphKernels <= c.GraphKernels {
		return 0
	}
	return c.ExpectedGraphKernels - c.GraphKernels
}

// Complete reports whether both sources agree the capture kept everything.
// A capture with no drop count and no CUDA graphs is reported complete
// because nothing contradicts it, which is the honest answer and not a
// guarantee.
func (c Completeness) Complete() bool {
	return c.Records > 0 && c.DroppedRecords == 0 && c.MissingGraphKernels() == 0
}

// Summary renders the verdict as one self-contained clause, and exists so
// the readers that print it — doctor, capture, the exporters, the diff —
// cannot drift into disagreeing about the same bundle.
func (c Completeness) Summary() string {
	if c.Complete() {
		// "looks", not "is". Both checks can only fail; neither can
		// confirm. A wholly unrecorded launch leaves every node of its
		// graph even and the drop counter at zero, so a capture missing
		// one reports exactly this line — observed on a GB10 MLX decode
		// whose argmax ran 128 times where the workload demands 129 [V].
		// Score a capture against an invariant the workload supplies,
		// not against this.
		if c.ExpectedGraphKernels > 0 {
			return fmt.Sprintf("capture looks complete: no records dropped, and all %d graph kernel executions accounted for (nothing contradicts it; neither check can see a wholly unrecorded launch)",
				c.ExpectedGraphKernels)
		}
		return "capture looks complete: no records dropped, and no CUDA graphs to cross-check against (nothing contradicts it, which is weaker than a guarantee)"
	}
	if c.Records == 0 {
		return "capture is EMPTY: it holds no activity records at all, which is what stranding looks like at its limit — " +
			"the tracer arms, reports success, and hands nothing back"
	}
	switch {
	case c.DroppedRecords > 0 && c.MissingGraphKernels() > 0:
		return fmt.Sprintf("capture is INCOMPLETE: the tracer dropped %d records, and at least %d graph kernel executions went unrecorded (of %d expected across %d graphs)%s",
			c.DroppedRecords, c.MissingGraphKernels(), c.ExpectedGraphKernels, c.UnevenGraphs, graphFloorNote)
	case c.DroppedRecords > 0:
		return fmt.Sprintf("capture is INCOMPLETE: the tracer dropped %d records", c.DroppedRecords)
	default:
		return fmt.Sprintf("capture is INCOMPLETE: the tracer reported no drops, but at least %d graph kernel executions went unrecorded (of %d expected across %d graphs)%s",
			c.MissingGraphKernels(), c.ExpectedGraphKernels, c.UnevenGraphs, graphFloorNote)
	}
}

// graphFloorNote rides along with every shortfall the graph invariant
// reports, because the number reads as a measurement and is not one. It is
// a floor twice over: the check covers only graph-resident kernels, so
// eager launches are outside it entirely, and a wholly unrecorded launch
// leaves every node of its graph even, so it leaves no trace here at all.
// Measured on a GB10 MLX decode [V]: this reported 18 while the workload's
// own per-token invariant showed 27 argmax launches missing.
const graphFloorNote = " — a floor, not the loss: only graph-resident kernels are checked, and a wholly unrecorded launch leaves no trace in it"

// Remedy is the action a partial capture calls for, or "" when there is
// nothing to fix.
//
// It branches on which source found the loss, because the two have
// different causes. A tracer reporting drops overflowed its activity
// buffers and wants bigger ones.
//
// A tracer reporting none while the graph invariant fails dropped
// nothing: the records were stranded in host buffers that were never
// handed back, so the loss is bounded by how long a buffer can sit
// unflushed. The shim's flush thread calls cuptiActivityFlushAll on an
// interval, which returns partial buffers; shortening the interval
// shortens the window.
//
// The knob to reach for used to be the buffer size, in the counterintuitive
// direction — smaller stranded less, because records only came back when
// a buffer filled. On a GB10 MLX decode whose argmax must run 129 times,
// that arrangement recovered 119 at 1 MiB, 102 at the 4 MiB default, and
// nothing at all at 16 MiB [V]. Once flushing stopped depending on a
// buffer filling, the same ladder ran 127-128 at every size from 1 to 64
// MiB [V]. The size is no longer the lever; the interval is.
func (c Completeness) Remedy() string {
	switch {
	case c.Complete():
		return ""
	case c.Records == 0:
		// Never "raise the buffer" here. Raising it is what produces this
		// state: on a GB10 MLX decode the recorded kernel count fell
		// monotonically as the buffer grew -- 1 MiB kept 119 of 129
		// argmax launches, the 4 MiB default 102, and at 16 MiB CUPTI
		// requested two buffers, completed zero, and wrote a capture with
		// nothing in it while every self-report still said fine [V].
		return "the tracer recorded nothing: shorten the flush interval, GPUTRACE_CAPTURE_FLUSH_MS=5 gputrace capture ...\n" +
			"do not raise GPUTRACE_CAPTURE_BUFSIZE_MB — a larger buffer is what strands a whole capture"
	case c.DroppedRecords > 0:
		return "the activity buffers overflowed: re-capture with a larger one, GPUTRACE_CAPTURE_BUFSIZE_MB=8 gputrace capture ...\n" +
			"nothing derived from this bundle should be compared against another capture until it reports complete"
	default:
		return "the tracer dropped nothing, so records were stranded in buffers that were not flushed before the target exited;\n" +
			"the loss is bounded by the flush interval, so shorten it: GPUTRACE_CAPTURE_FLUSH_MS=5 gputrace capture ...\n" +
			"nothing derived from this bundle should be compared against another capture until it reports complete"
	}
}
