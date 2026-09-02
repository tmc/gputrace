package gpuevent

import "sort"

// Gap is one interval in which no measured GPU activity was running.
type Gap struct {
	StartNS    uint64 `json:"start_ns"`
	DurationNS uint64 `json:"duration_ns"`
	AfterName  string `json:"after_name,omitempty"`  // activity that ended the gap open
	BeforeName string `json:"before_name,omitempty"` // activity that closed it
}

// Utilization is the busy/idle budget of a capture: how much of the wall
// span between the first activity start and the last activity end the GPU
// spent executing something.
//
// BusyNS is the union of activity intervals, not their sum: concurrent
// kernels on different streams overlap, and summing them double-counts
// wall time (a capture with two streams fully overlapped would otherwise
// report 200% occupancy). Provenance [V]: measured from activity record
// timestamps, no heuristic.
type Utilization struct {
	WallSpanNS   uint64  `json:"wall_span_ns"`
	BusyNS       uint64  `json:"busy_ns"`
	IdleNS       uint64  `json:"idle_ns"`
	OccupancyPct float64 `json:"occupancy_pct"` // BusyNS / WallSpanNS
	GapCount     int     `json:"gap_count"`
	MeanGapNS    uint64  `json:"mean_gap_ns"`
	P95GapNS     uint64  `json:"p95_gap_ns"`
	MaxGapNS     uint64  `json:"max_gap_ns"`
	TopGaps      []Gap   `json:"top_gaps,omitempty"`
	Concurrency  float64 `json:"concurrency"` // summed activity time / BusyNS
}

// activityLabel names an activity for gap reporting. Transfers carry no
// symbol, so their kind is the honest label.
func activityLabel(e Event) string {
	if n := e.displayName(); n != "" {
		return n
	}
	return string(e.Kind)
}

// topGapCount bounds the gap list kept for reporting; the aggregate
// statistics always cover every gap.
const topGapCount = 5

// UtilizationOf computes the busy/idle budget over every measured
// activity (kernels, copies, fills). Events need not be sorted.
func UtilizationOf(events []Event) Utilization {
	type interval struct {
		start, end uint64
		name       string
	}
	intervals := make([]interval, 0, len(events))
	var summed uint64
	for _, e := range events {
		if e.EndNS <= e.StartNS {
			continue // zero-length or malformed; contributes no wall time
		}
		intervals = append(intervals, interval{e.StartNS, e.EndNS, activityLabel(e)})
		summed += e.EndNS - e.StartNS
	}
	if len(intervals) == 0 {
		return Utilization{}
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return intervals[i].end < intervals[j].end
	})

	out := Utilization{}
	firstStart := intervals[0].start
	// Merge overlapping intervals into busy runs; the holes between runs
	// are the idle gaps.
	var gaps []Gap
	runStart, runEnd := intervals[0].start, intervals[0].end
	runEndName := intervals[0].name
	var lastEnd uint64
	for _, iv := range intervals[1:] {
		if iv.start <= runEnd {
			if iv.end > runEnd {
				runEnd, runEndName = iv.end, iv.name
			}
			continue
		}
		out.BusyNS += runEnd - runStart
		gaps = append(gaps, Gap{
			StartNS:    runEnd,
			DurationNS: iv.start - runEnd,
			AfterName:  runEndName,
			BeforeName: iv.name,
		})
		runStart, runEnd, runEndName = iv.start, iv.end, iv.name
	}
	out.BusyNS += runEnd - runStart
	lastEnd = runEnd

	out.WallSpanNS = lastEnd - firstStart
	if out.WallSpanNS > out.BusyNS {
		out.IdleNS = out.WallSpanNS - out.BusyNS
	}
	if out.WallSpanNS > 0 {
		out.OccupancyPct = 100 * float64(out.BusyNS) / float64(out.WallSpanNS)
	}
	if out.BusyNS > 0 {
		out.Concurrency = float64(summed) / float64(out.BusyNS)
	}
	out.GapCount = len(gaps)
	if len(gaps) > 0 {
		durations := make([]uint64, len(gaps))
		var sum uint64
		for i, g := range gaps {
			durations[i] = g.DurationNS
			sum += g.DurationNS
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		out.MeanGapNS = sum / uint64(len(gaps))
		out.P95GapNS = percentile(durations, 0.95)
		out.MaxGapNS = durations[len(durations)-1]

		byDuration := append([]Gap(nil), gaps...)
		sort.SliceStable(byDuration, func(i, j int) bool {
			return byDuration[i].DurationNS > byDuration[j].DurationNS
		})
		if len(byDuration) > topGapCount {
			byDuration = byDuration[:topGapCount]
		}
		out.TopGaps = byDuration
	}
	return out
}
