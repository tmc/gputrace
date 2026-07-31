package timing

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
