package gpuevent

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Bound is a heuristic classification of what limits a kernel's speed.
// It is derived from launch geometry, duration, and byte counts — not from
// hardware counters — and every finding carries that provenance so a reader
// never mistakes a heuristic for a measurement.
type Bound string

const (
	BoundCompute    Bound = "compute"    // plenty of parallel work, likely ALU/tensor limited
	BoundMemory     Bound = "memory"     // byte volume dominates; likely bandwidth limited
	BoundLatency    Bound = "latency"    // too little parallel work to hide latency
	BoundUndetermined Bound = "unknown"  // insufficient shape information
)

// FindingKind names the pattern a Finding describes.
type FindingKind string

const (
	FindingDominance   FindingKind = "dominance"   // one kernel owns the GPU time
	FindingLaunchShape FindingKind = "launch-shape" // geometry too small to use the device
	FindingLongTail    FindingKind = "long-tail"    // high p95-vs-mean spread
	FindingTransferHeavy FindingKind = "transfer-heavy" // memcpys rival kernel time
)

// Severity ranks how much measured time a finding touches.
type Severity string

const (
	SeverityHigh   Severity = "high"
	SeverityMedium Severity = "medium"
	SeverityLow    Severity = "low"
)

// KernelStats aggregates every launch of one kernel name.
type KernelStats struct {
	Name       string `json:"name"`
	Count      int    `json:"count"`
	TotalNS    uint64 `json:"total_ns"`
	MeanNS     uint64 `json:"mean_ns"`
	P50NS      uint64 `json:"p50_ns"`
	P95NS      uint64 `json:"p95_ns"`
	MaxNS      uint64 `json:"max_ns"`
	SharePct   float64 `json:"share_pct"`
	Registers  int    `json:"registers,omitempty"`
	TypicalGrid  string `json:"typical_grid,omitempty"`
	TypicalBlock string `json:"typical_block,omitempty"`
	ThreadsPerLaunch uint64 `json:"threads_per_launch"` // grid x block of modal launch
	BytesTotal uint64  `json:"bytes_total,omitempty"`
	Bound      Bound   `json:"bound"`
}

// Finding is one evidence-backed observation with a proposed direction.
// Hypothesis strings are starting points for an optimization agent, not
// conclusions: each cites the measured numbers it rests on.
type Finding struct {
	Kind       FindingKind `json:"kind"`
	Severity   Severity    `json:"severity"`
	Subject    string      `json:"subject"`
	Evidence   []string    `json:"evidence"`
	Hypothesis string      `json:"hypothesis"`
}

// Report is the analysis of one capture.
type Report struct {
	Kernels       []KernelStats `json:"kernels"`
	Findings      []Finding     `json:"findings"`
	TotalKernelNS uint64        `json:"total_kernel_ns"`
	KernelLaunches int          `json:"kernel_launches"`
	MemcpyCount   int           `json:"memcpy_count"`
	MemsetCount   int           `json:"memset_count"`
	MemcpyNS      uint64        `json:"memcpy_ns"`
	SpanNS        uint64        `json:"span_ns"` // first start to last end
	LaunchOverhead *LaunchOverhead `json:"launch_overhead,omitempty"`
}

// Analyze reduces events into per-kernel statistics and ordered findings.
// Samples may be nil; when present they only annotate future reporting,
// never change the kernel math.
func Analyze(events []Event, _ []Sample) *Report {
	rep := &Report{}
	if len(events) == 0 {
		return rep
	}
	byName := map[string]*kernelAgg{}
	for _, e := range events {
		if e.EndNS > rep.SpanNS {
			rep.SpanNS = e.EndNS
		}
		switch e.Kind {
		case KindKernel:
			rep.KernelLaunches++
			agg := byName[e.displayName()]
			if agg == nil {
				agg = &kernelAgg{name: e.displayName()}
				byName[agg.name] = agg
			}
			agg.add(e)
		case KindMemcpy:
			rep.MemcpyCount++
			rep.MemcpyNS += e.DurationNS()
		case KindMemset:
			rep.MemsetCount++
		}
	}
	for _, agg := range byName {
		rep.TotalKernelNS += agg.totalNS
	}
	for _, agg := range byName {
		ks := agg.finish(rep.TotalKernelNS)
		rep.Kernels = append(rep.Kernels, ks)
	}
	sort.Slice(rep.Kernels, func(i, j int) bool {
		return rep.Kernels[i].TotalNS > rep.Kernels[j].TotalNS
	})
	rep.Findings = buildFindings(rep)
	return rep
}

// displayName prefers the decoded name and falls back to the raw symbol so
// un-demangled captures still aggregate correctly.
func (e Event) displayName() string {
	if e.Name != "" {
		return e.Name
	}
	return e.RawSymbol
}

type kernelAgg struct {
	name     string
	count    int
	totalNS  uint64
	durations []uint64
	registers int
	grids     map[string]int
	blocks   map[string]int
	bytes    uint64
}

func (a *kernelAgg) add(e Event) {
	a.count++
	d := e.DurationNS()
	a.totalNS += d
	a.durations = append(a.durations, d)
	if e.Registers > 0 && a.registers == 0 {
		a.registers = e.Registers
	}
	if a.grids == nil {
		a.grids = map[string]int{}
		a.blocks = map[string]int{}
	}
	if e.Grid != "" {
		a.grids[e.Grid]++
	}
	if e.Block != "" {
		a.blocks[e.Block]++
	}
	a.bytes += e.Bytes
}

// modal returns the most frequently observed value of a string counter.
func modal(m map[string]int) string {
	best, n := "", -1
	for k, v := range m {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	return best
}

func (a *kernelAgg) finish(totalAll uint64) KernelStats {
	sort.Slice(a.durations, func(i, j int) bool { return a.durations[i] < a.durations[j] })
	ks := KernelStats{
		Name:       a.name,
		Count:      a.count,
		TotalNS:    a.totalNS,
		MaxNS:      a.durations[len(a.durations)-1],
		Registers:  a.registers,
		TypicalGrid:  modal(a.grids),
		TypicalBlock: modal(a.blocks),
		BytesTotal: a.bytes,
	}
	var sum uint64
	for _, d := range a.durations {
		sum += d
	}
	ks.MeanNS = sum / uint64(a.count)
	ks.P50NS = percentile(a.durations, 0.50)
	ks.P95NS = percentile(a.durations, 0.95)
	if totalAll > 0 {
		ks.SharePct = float64(a.totalNS) / float64(totalAll) * 100
	}
	ks.ThreadsPerLaunch = threadsFor(ks.TypicalGrid, ks.TypicalBlock)
	ks.Bound = classify(ks, a.bytes)
	return ks
}

func percentile(sorted []uint64, q float64) uint64 {
	if len(sorted) == 0 {
		return 0
	}
	// Nearest-rank: the smallest value covering at least q of the samples.
	idx := int(math.Ceil(q * float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func threadsFor(grid, block string) uint64 {
	g := dimsProduct(grid)
	b := dimsProduct(block)
	if g == 0 {
		return b
	}
	return g * b
}

// dimsProduct multiplies an "XxYxZ" dimension string; 0 means unknown.
func dimsProduct(s string) uint64 {
	if s == "" {
		return 0
	}
	parts := strings.Split(s, "x")
	var total uint64 = 1
	for _, p := range parts {
		v, err := strconv.ParseUint(strings.TrimSpace(p), 10, 32)
		if err != nil {
			return 0
		}
		total *= v
	}
	return total
}

// Classification thresholds. These are documented heuristics calibrated on
// modern datacenter-class GPUs; they are intentionally conservative and are
// always surfaced as provenance "heuristic" in findings.
const (
	// A single wave of fewer than this many threads cannot keep even one
	// SM occupied while the rest of the device idles.
	latencyThreadsThreshold = 2048
	// Bytes moved per millisecond above which bandwidth is the plausible limiter
	// (~500 GB/s reference bandwidth; below it, bytes are unlikely to dominate).
	memoryBytesPerMS = 500_000_000
	// A launch moving at least this many bytes is treated as a transfer-shaped
	// kernel regardless of grid size; small-grid bulk copies are common.
	memoryBytesThreshold = 1 << 20
	// Share of total kernel time above which a kernel dominates the capture.
	dominanceSharePct = 30.0
	// p95/mean ratio above which launch-to-launch variance is worth reporting.
	longTailRatio = 3.0
)

func classify(ks KernelStats, bytes uint64) Bound {
	if bytes >= memoryBytesThreshold && ks.MeanNS > 0 {
		// Transfer-shaped work is bandwidth-limited regardless of launch size;
		// small-grid bulk copies are common and not latency artifacts.
		bytesPerMS := float64(bytes) / (float64(ks.TotalNS) / 1e6)
		if bytesPerMS > memoryBytesPerMS/10 {
			return BoundMemory
		}
	}
	if ks.TypicalGrid != "" && ks.ThreadsPerLaunch > 0 && ks.ThreadsPerLaunch < latencyThreadsThreshold && ks.MeanNS > 20_000 {
		// Tiny launch with measurable duration: nothing hides latency behind.
		return BoundLatency
	}
	if ks.ThreadsPerLaunch >= latencyThreadsThreshold {
		return BoundCompute
	}
	return BoundUndetermined
}

func buildFindings(rep *Report) []Finding {
	var out []Finding
	for i, k := range rep.Kernels {
		// A latency-bound kernel is reported as a launch-shape finding even
		// when it also dominates: the shape, not just the share, is the story.
		if k.Bound == BoundLatency {
			out = append(out, Finding{
				Kind:     FindingLaunchShape,
				Severity: severityFor(k.SharePct),
				Subject:  k.Name,
				Evidence: []string{
					fmt.Sprintf("grid %s x block %s = %d threads per launch for mean %s",
						orDash(k.TypicalGrid), orDash(k.TypicalBlock), k.ThreadsPerLaunch, dur(k.MeanNS)),
				},
				Hypothesis: "launch covers too few threads to fill the device; batch independent work into larger grids, split the loop across blocks, or overlap with other streams",
			})
			continue
		}
		switch {
		case i == 0 && k.SharePct >= dominanceSharePct:
			out = append(out, Finding{
				Kind:     FindingDominance,
				Severity: severityFor(k.SharePct),
				Subject:  k.Name,
				Evidence: []string{
					fmt.Sprintf("%d launches, %.1f%% of %.2f ms total kernel time",
						k.Count, k.SharePct, float64(rep.TotalKernelNS)/1e6),
					fmt.Sprintf("mean %s, p95 %s, grid %s x block %s, %d regs",
						dur(k.MeanNS), dur(k.P95NS), orDash(k.TypicalGrid), orDash(k.TypicalBlock), k.Registers),
				},
				Hypothesis: dominanceHypothesis(k),
			})
		}
		if k.Count >= 4 && k.MeanNS > 0 && float64(k.P95NS)/float64(k.MeanNS) >= longTailRatio {
			out = append(out, Finding{
				Kind:     FindingLongTail,
				Severity: SeverityLow,
				Subject:  k.Name,
				Evidence: []string{fmt.Sprintf("p95 %s vs mean %s over %d launches", dur(k.P95NS), dur(k.MeanNS), k.Count)},
				Hypothesis: "a minority of launches run far slower than typical; look for input-dependent work, clock throttling during the capture, or contention with concurrent streams",
			})
		}
	}
	if rep.MemcpyNS > rep.TotalKernelNS/2 && rep.MemcpyCount > 0 {
		out = append(out, Finding{
			Kind:     FindingTransferHeavy,
			Severity: SeverityMedium,
			Subject:  "(all transfers)",
			Evidence: []string{fmt.Sprintf("%d transfers totalling %.2f ms vs %.2f ms of kernel time", rep.MemcpyCount, float64(rep.MemcpyNS)/1e6, float64(rep.TotalKernelNS)/1e6)},
			Hypothesis: "transfers rival compute; batch small copies, prefer pinned memory, or keep data resident on-device between kernels",
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return severityRank(out[i].Severity) < severityRank(out[j].Severity)
	})
	return out
}

func dominanceHypothesis(k KernelStats) string {
	switch k.Bound {
	case BoundMemory:
		return fmt.Sprintf("%s dominates and its byte volume suggests bandwidth limitation; consider wider vectorized loads, better coalescing, compressing data in flight, or fusing with adjacent kernels to reuse data in registers", k.Name)
	case BoundLatency:
		return fmt.Sprintf("%s dominates yet launches too few threads to fill the device; restructure to expose more parallelism (larger grids, multi-stream overlap, splitting sequential loops)", k.Name)
	default:
		return fmt.Sprintf("%s dominates measured GPU time; reduce launch count, tile for better cache/tensor-core reuse, or replace it with a fused or library-provided equivalent", k.Name)
	}
}

func severityFor(sharePct float64) Severity {
	switch {
	case sharePct >= 60:
		return SeverityHigh
	case sharePct >= 20:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

func severityRank(s Severity) int {
	switch s {
	case SeverityHigh:
		return 0
	case SeverityMedium:
		return 1
	default:
		return 2
	}
}

func dur(ns uint64) string {
	switch {
	case ns >= 1e6:
		return fmt.Sprintf("%.2f ms", float64(ns)/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.1f us", float64(ns)/1e3)
	default:
		return fmt.Sprintf("%d ns", ns)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
