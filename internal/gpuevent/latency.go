package gpuevent

import (
	"fmt"
	"sort"
)

// DurationStats summarizes one population of measured durations.
type DurationStats struct {
	Count  int    `json:"count"`
	MeanNS uint64 `json:"mean_ns"`
	P50NS  uint64 `json:"p50_ns"`
	P95NS  uint64 `json:"p95_ns"`
	MaxNS  uint64 `json:"max_ns"`
}

// LaunchLatency decomposes the interval between a kernel being queued in a
// command buffer and starting on the device, from CUPTI's per-launch
// latency timestamps (cuptiActivityEnableLatencyTimestamps).
//
// Captures routinely supply those timestamps for only part of their
// kernels — see decodeLatency for why — so this type reports usability
// rather than a bare number. Only records whose Latency is Known
// contribute, and Usable reports whether the surviving population is
// large enough to characterize the capture. Callers must not print
// latency figures when it is false.
type LaunchLatency struct {
	Kernels         int           `json:"kernels"`
	Timed           int           `json:"timed"`        // kernels carrying a usable decomposition
	Rejected        int           `json:"rejected"`     // kernels the capture left untimed
	CoveragePct     float64       `json:"coverage_pct"` // Timed as a share of Kernels
	QueueToSubmitNS DurationStats `json:"queue_to_submit_ns"`
	SubmitToStartNS DurationStats `json:"submit_to_start_ns"`
	QueueToStartNS  DurationStats `json:"queue_to_start_ns"`
	Usable          bool          `json:"usable"`
	Reason          string        `json:"reason,omitempty"` // why unusable, or what limits it
}

// minLatencyCoveragePct is the share of kernels that must carry valid
// latency timestamps before the aggregate describes the capture rather
// than an unrepresentative corner of it.
const minLatencyCoveragePct = 10.0

// ValidLatency reports whether this kernel carries a usable launch
// latency decomposition.
func (e Event) ValidLatency() bool {
	return e.Kind == KindKernel && e.Latency.Known
}

// LaunchLatencyAnalysis decomposes queue -> submit -> start across every
// kernel carrying usable latency timestamps.
func LaunchLatencyAnalysis(events []Event) *LaunchLatency {
	out := &LaunchLatency{}
	var queueToSubmit, submitToStart, queueToStart []uint64
	for _, e := range events {
		if e.Kind != KindKernel {
			continue
		}
		out.Kernels++
		if e.ValidLatency() {
			out.Timed++
			queueToSubmit = append(queueToSubmit, e.Latency.QueueToSubmitNS)
			submitToStart = append(submitToStart, e.Latency.SubmitToStartNS)
			queueToStart = append(queueToStart, e.Latency.QueueToStartNS())
			continue
		}
		out.Rejected++
	}
	if out.Kernels == 0 {
		out.Reason = "capture has no kernels"
		return out
	}
	out.CoveragePct = 100 * float64(out.Timed) / float64(out.Kernels)
	out.QueueToSubmitNS = summarize(queueToSubmit)
	out.SubmitToStartNS = summarize(submitToStart)
	out.QueueToStartNS = summarize(queueToStart)
	switch {
	case out.Timed == 0:
		out.Reason = fmt.Sprintf("no kernel carries a consistent queued/submitted pair; all %d launches went untimed (CUDA-graph launches report none)", out.Rejected)
	case out.CoveragePct < minLatencyCoveragePct:
		out.Reason = fmt.Sprintf("only %.1f%% of %d kernels carry latency timestamps (%d rejected); too few to characterize the capture", out.CoveragePct, out.Kernels, out.Rejected)
	default:
		out.Usable = true
		if out.CoveragePct < 100 {
			out.Reason = fmt.Sprintf("computed over the %.1f%% of kernels with latency timestamps; the rest (CUDA-graph launches) report none", out.CoveragePct)
		}
	}
	return out
}

// summarize reduces a duration population to its ordered statistics.
func summarize(v []uint64) DurationStats {
	if len(v) == 0 {
		return DurationStats{}
	}
	sorted := append([]uint64(nil), v...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum uint64
	for _, d := range sorted {
		sum += d
	}
	return DurationStats{
		Count:  len(sorted),
		MeanNS: sum / uint64(len(sorted)),
		P50NS:  percentile(sorted, 0.50),
		P95NS:  percentile(sorted, 0.95),
		MaxNS:  sorted[len(sorted)-1],
	}
}
