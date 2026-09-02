package counter

import "sort"

// Execution Cost per encoder.
//
// Xcode's Execution Cost column is a per-encoder share of GPU work that sums to
// 100% over the capture's encoders. gputrace's older figure is keyed by
// pipeline id, which is a different grouping of the same workload and cannot be
// compared row for row.
//
// The counter archive carries what is needed to rebuild it. Each pass reads the
// hardware counters at every encoder's start and end, and the end record's
// GRC_GPU_CYCLES is the cycles the GPU spent between the two. Encoders are
// identified by ordinal, not by id: the capture is replayed once per pass, so
// the same encoder gets a fresh id in each of the 16 Encoder Infos groups and
// only its position within the group is stable.
//
// Accuracy, measured against Xcode's own export of the same capture
// (testdata/xcode-oracle/compute-kernel-encoders.txt, 23 encoders summing to
// 99.995%):
//
//	max |residual|  0.911 pp   (encoder 10, 8.829% against Xcode's 9.740%)
//	rms residual    0.278 pp
//
// [D] derived, and deliberately not claimed as exact. Every per-pass variant
// tried was worse or no better: end-timestamp minus begin-timestamp spans
// (rms 0.230 pp), single-source subsets (best rms 0.198 pp, max 0.730 pp), and
// any single pass on its own (best rms 0.188 pp, max 0.447 pp). No aggregation
// reproduces Xcode's column, so the residual is a property of the method, not
// of the archive being incomplete. Report the figure with its residual; do not
// present it as Xcode's number.
//
// Sample-count share is NOT a cost proxy and must not be used as one. The
// counter reads are scheduled, not statistical: in the reference archive 20 of
// 23 encoders have exactly 304 samples and the other 3 have exactly 112,
// whatever their cost. That is why cost comes from GRC_GPU_CYCLES.

// EncoderCost is the Execution Cost of one encoder of the capture.
type EncoderCost struct {
	Ordinal     int     `json:"ordinal"`      // Encoder execution order, 0-based
	BatchID     int     `json:"batch_id"`     // From the TraceId tables
	SampleIndex int     `json:"sample_index"` // From the TraceId tables
	CostPercent float64 `json:"cost_percent"` // Share of GPU cycles, 0-100
	GPUCycles   uint64  `json:"gpu_cycles"`   // Cycles summed over every pass
	EndRecords  int     `json:"end_records"`  // GRC_SAMPLE_TYPE 5 records behind the figure
	SampleCount int     `json:"sample_count"` // Every record naming this encoder
}

// Sparse reports whether the figure rests on too few counter reads to state
// without a caveat. The reference archive gives every encoder either 160 or 64
// end records - the capture is replayed once per Encoder Infos group, 16 of
// them, so that is 10 or 4 reads per group. One read per group is the floor
// below which a figure is outside anything measured.
func (c EncoderCost) Sparse() bool { return c.EndRecords < 16 }

// EncoderCosts returns the per-encoder Execution Cost, in execution order.
//
// It returns nil when the archive attributes no sample, which is the honest
// answer for a capture whose counter stream is entirely machine-wide.
func (a *CounterArchive) EncoderCosts() []EncoderCost {
	if a == nil {
		return nil
	}
	byOrdinal := make(map[int]*EncoderCost)
	for _, e := range a.Encoders {
		c := byOrdinal[e.Ordinal]
		if c == nil {
			c = &EncoderCost{Ordinal: e.Ordinal, BatchID: e.BatchID, SampleIndex: e.SampleIndex}
			byOrdinal[e.Ordinal] = c
		}
		c.GPUCycles += e.GPUCycles
		c.EndRecords += e.EndSamples
		c.SampleCount += e.SampleCount
	}
	var total uint64
	for _, c := range byOrdinal {
		total += c.GPUCycles
	}
	if total == 0 {
		return nil
	}
	costs := make([]EncoderCost, 0, len(byOrdinal))
	for _, c := range byOrdinal {
		c.CostPercent = 100 * float64(c.GPUCycles) / float64(total)
		costs = append(costs, *c)
	}
	sort.Slice(costs, func(i, j int) bool { return costs[i].Ordinal < costs[j].Ordinal })
	return costs
}
