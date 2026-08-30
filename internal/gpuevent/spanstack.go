package gpuevent

import "sort"

// SpanStacks returns, for every event in events, the chain of application
// spans enclosing it, ordered outermost first. An event no span encloses
// gets a nil chain, and the result is indexed in step with events.
//
// Containment is the rule AttributeSpans uses: a span encloses an activity
// when it covers the whole [StartNS,EndNS] interval and either declares the
// activity's stream or declares no stream at all. The difference is what is
// kept. AttributeSpans keeps only the tightest match, because a kernel must
// be counted once; a call-stack rendering needs every enclosing span, since
// the chain is the stack.
//
// Spans and events must already share a clock domain. DecodeJSONL
// translates unix-stamped spans into the capture domain via the clock_sync
// record; without one, such spans enclose nothing rather than enclosing the
// wrong kernels.
func SpanStacks(events []Event, spans []Span) [][]Span {
	out := make([][]Span, len(events))
	if len(spans) == 0 || len(events) == 0 {
		return out
	}

	byStart := make([]int, len(spans))
	for i := range byStart {
		byStart[i] = i
	}
	sort.Slice(byStart, func(a, b int) bool {
		return spans[byStart[a]].StartNS < spans[byStart[b]].StartNS
	})
	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		return events[order[a]].StartNS < events[order[b]].StartNS
	})

	// active holds spans that started at or before the current event and
	// have not yet ended. Visiting events in start order makes retirement
	// permanent: a span that ended before this event started cannot
	// enclose any later one either.
	var active []int
	next := 0
	for _, ei := range order {
		e := events[ei]
		for next < len(byStart) && spans[byStart[next]].StartNS <= e.StartNS {
			active = append(active, byStart[next])
			next++
		}
		kept := active[:0]
		for _, si := range active {
			if spans[si].EndNS >= e.StartNS {
				kept = append(kept, si)
			}
		}
		active = kept

		var chain []Span
		for _, si := range active {
			s := spans[si]
			if e.StartNS < s.StartNS || e.EndNS > s.EndNS {
				continue // overlaps but does not contain
			}
			if !s.coversStream(e.StreamID) {
				continue
			}
			chain = append(chain, s)
		}
		sort.SliceStable(chain, func(a, b int) bool {
			if chain[a].StartNS != chain[b].StartNS {
				return chain[a].StartNS < chain[b].StartNS
			}
			return chain[a].EndNS > chain[b].EndNS // longer span is the outer one
		})
		out[ei] = chain
	}
	return out
}

// IdleAfter reports, for every event in events, the device time on its own
// stream between the event ending and the next measured activity starting.
// The last activity on a stream reports zero: nothing measured follows it,
// so the capture cannot say whether the stream was idle or the capture
// simply ended.
//
// Per stream is the only meaningful granularity. Kernels on different
// streams overlap, so a gap computed against the global next activity
// measures other streams' work, not idleness.
//
// The gap closes on the next activity of any kind, copies and fills
// included: a stream running a memcpy is busy, and counting that interval
// as idle would inflate the very number this exists to measure. Overlapping
// activities on one stream report zero rather than a negative gap.
func IdleAfter(events []Event) []uint64 {
	out := make([]uint64, len(events))
	type streamKey struct {
		device uint32
		stream uint32
	}
	byStream := make(map[streamKey][]int)
	for i, e := range events {
		if e.EndNS <= e.StartNS {
			continue // zero-length or malformed: no gap to measure from
		}
		k := streamKey{e.DeviceID, e.StreamID}
		byStream[k] = append(byStream[k], i)
	}
	for _, idx := range byStream {
		sort.Slice(idx, func(a, b int) bool {
			return events[idx[a]].StartNS < events[idx[b]].StartNS
		})
		// A long activity can still be running when a later-starting one
		// begins, so the gap is measured from the furthest end seen so
		// far and charged to the activity that ended there — the one the
		// stream actually went idle after.
		var frontier uint64
		frontierIdx := -1
		for _, ei := range idx {
			e := events[ei]
			if frontierIdx >= 0 && e.StartNS > frontier {
				out[frontierIdx] = e.StartNS - frontier
			}
			if e.EndNS > frontier {
				frontier = e.EndNS
				frontierIdx = ei
			}
		}
	}
	return out
}
