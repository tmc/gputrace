package gpuevent

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// LaunchKey identifies one launch geometry of one kernel. It is the finest
// grouping under which a mean duration is a statement about a single
// population.
//
// A kernel symbol on its own is not that grouping. The same symbol is
// routinely launched at many geometries in one run, and their durations
// differ by orders of magnitude, so a mean over the symbol describes no
// launch that occurred. Registers and shared memory join the key because
// they change how many blocks are resident, which is what a duration
// comparison is usually trying to hold fixed.
type LaunchKey struct {
	Name           string `json:"name"`
	Grid           string `json:"grid"`
	Block          string `json:"block"`
	Registers      int    `json:"registers,omitempty"`
	SharedMemBytes int    `json:"shared_mem_bytes,omitempty"`
}

// String renders the key for display, shortest distinguishing form first.
func (k LaunchKey) String() string {
	s := k.Name + " " + k.Grid + "/" + k.Block
	if k.Registers > 0 {
		s += fmt.Sprintf(" r%d", k.Registers)
	}
	if k.SharedMemBytes > 0 {
		s += fmt.Sprintf(" s%d", k.SharedMemBytes)
	}
	return s
}

// LaunchShapeStats is the timing of one kernel at one launch geometry.
type LaunchShapeStats struct {
	LaunchKey
	Count   int    `json:"count"`
	TotalNS uint64 `json:"total_ns"`
	MeanNS  uint64 `json:"mean_ns"`
	P50NS   uint64 `json:"p50_ns"`
	P95NS   uint64 `json:"p95_ns"`
	// Blocks is the grid's block count, the product of its dimensions. It
	// is reported beside duration because it, not duration, is what
	// separates a launch that fills the device from one that does not --
	// and cross-arm penalties have been observed to track it.
	Blocks uint64 `json:"blocks"`
	// ThreadsPerLaunch is Blocks times the block's thread count.
	ThreadsPerLaunch uint64 `json:"threads_per_launch"`
}

// dims multiplies a "AxBxC" geometry string. It returns 0 when the string
// is absent or unparseable, which callers must treat as "unknown" rather
// than as zero work.
func dims(s string) uint64 {
	if s == "" {
		return 0
	}
	parts := strings.Split(s, "x")
	var n uint64 = 1
	for _, p := range parts {
		v, err := strconv.ParseUint(strings.TrimSpace(p), 10, 64)
		if err != nil || v == 0 {
			return 0
		}
		n *= v
	}
	return n
}

type shapeAgg struct {
	key       LaunchKey
	durations []uint64
	totalNS   uint64
}

// AnalyzeLaunchShapes groups kernel events by their full launch key.
//
// This is the grouping a cross-capture comparison must use. AnalyzeKernels
// groups by symbol, which is the right unit for "where does this run spend
// its time" and the wrong one for "is this kernel slower", because the
// second question needs the two sides to be the same population.
func AnalyzeLaunchShapes(events []Event) []LaunchShapeStats {
	by := map[LaunchKey]*shapeAgg{}
	for _, e := range events {
		if e.Kind != KindKernel {
			continue
		}
		k := LaunchKey{
			Name:           e.displayName(),
			Grid:           e.Grid,
			Block:          e.Block,
			Registers:      e.Registers,
			SharedMemBytes: e.SharedMem,
		}
		a := by[k]
		if a == nil {
			a = &shapeAgg{key: k}
			by[k] = a
		}
		d := e.DurationNS()
		a.durations = append(a.durations, d)
		a.totalNS += d
	}
	out := make([]LaunchShapeStats, 0, len(by))
	for _, a := range by {
		sort.Slice(a.durations, func(i, j int) bool { return a.durations[i] < a.durations[j] })
		blocks := dims(a.key.Grid)
		s := LaunchShapeStats{
			LaunchKey: a.key,
			Count:     len(a.durations),
			TotalNS:   a.totalNS,
			MeanNS:    a.totalNS / uint64(len(a.durations)),
			P50NS:     percentile(a.durations, 0.50),
			P95NS:     percentile(a.durations, 0.95),
			Blocks:    blocks,
		}
		if t := dims(a.key.Block); t > 0 && blocks > 0 {
			s.ThreadsPerLaunch = blocks * t
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalNS != out[j].TotalNS {
			return out[i].TotalNS > out[j].TotalNS
		}
		return out[i].LaunchKey.String() < out[j].LaunchKey.String()
	})
	return out
}

// ShapeCountsByName reports how many distinct launch geometries each kernel
// symbol was launched at. A count above one means any per-launch mean for
// that symbol spans more than one population.
func ShapeCountsByName(shapes []LaunchShapeStats) map[string]int {
	n := map[string]int{}
	for _, s := range shapes {
		n[s.Name]++
	}
	return n
}
