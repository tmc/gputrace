package timing

import "fmt"

// A dispatch span is the delta between cumulative dispatch offsets, so it
// carries whatever boundary and gap time preceded the next dispatch. Averaged
// over many calls that error is small; on a single call it is the measurement.
// A one-call row can therefore sort to the top of a cost-ranked table on time
// that the kernel did not spend, which reads as a finding.
//
// LowSampleCalls is the invocation count at or below which a row is reported
// as a single sample rather than a rate.
const LowSampleCalls = 1

// LowSampleMarker follows the share of any row at or below LowSampleCalls.
const LowSampleMarker = "!"

// IsLowSample reports whether the row rests on too few dispatches for its
// span to be read as representative.
func (kt *KernelTiming) IsLowSample() bool {
	return kt.InvocationCount <= LowSampleCalls
}

// CountLowSample returns how many of timings rest on a single dispatch.
func CountLowSample(timings []*KernelTiming) int {
	n := 0
	for _, kt := range timings {
		if kt.IsLowSample() {
			n++
		}
	}
	return n
}

// LowSampleFootnote explains the marker, or returns "" when no row carries it.
func LowSampleFootnote(timings []*KernelTiming) string {
	low := CountLowSample(timings)
	if low == 0 {
		return ""
	}
	return fmt.Sprintf("\n%s marks a row measured from a single dispatch (%d of %d). A lone span\n"+
		"  carries the boundary and gap time before the next dispatch, so it ranks by\n"+
		"  cost it may not have spent. Compare against a repeated row before citing it.\n",
		LowSampleMarker, low, len(timings))
}

// FilterMinCalls keeps rows measured from at least min dispatches and reports
// how many it dropped. min <= 1 keeps everything, which is the default: the
// table is ranked by cost and removing rows from a ranking silently edits it.
// Marking a row is additive and needs no opt-in; removing one does.
func FilterMinCalls(timings []*KernelTiming, min int) (kept []*KernelTiming, dropped int) {
	if min <= 1 {
		return timings, 0
	}
	kept = make([]*KernelTiming, 0, len(timings))
	for _, kt := range timings {
		if kt.InvocationCount >= min {
			kept = append(kept, kt)
			continue
		}
		dropped++
	}
	return kept, dropped
}

// MinCallsNote states what a --min-calls filter removed. A filtered table no
// longer sums to the whole, and the reader has to be told so by the table
// itself rather than by remembering which flag they passed.
func MinCallsNote(min, dropped, total int) string {
	if dropped == 0 {
		return ""
	}
	return fmt.Sprintf("--min-calls %d dropped %d of %d rows; the shares above are of the\n"+
		"  unfiltered total and no longer sum to 100%%.\n", min, dropped, total)
}
