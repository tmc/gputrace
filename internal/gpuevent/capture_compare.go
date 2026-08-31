package gpuevent

import (
	"fmt"
	"sort"
)

// CaptureVerdict summarizes the kernel-level outcome of comparing two
// captures.
type CaptureVerdict string

const (
	CaptureImproved     CaptureVerdict = "improved"
	CaptureRegressed    CaptureVerdict = "regressed"
	CaptureUnchanged    CaptureVerdict = "unchanged"
	CaptureMixed        CaptureVerdict = "mixed"
	CaptureInconclusive CaptureVerdict = "inconclusive"
)

// KernelDelta is the per-kernel difference between two captures. Kernels
// present on only one side are reported with the other side's count at 0
// and OnlyIn naming the side that has it.
type KernelDelta struct {
	Name           string  `json:"name"`
	BaseCount      int     `json:"base_count"`
	VariantCount   int     `json:"variant_count"`
	BaseMeanNS     uint64  `json:"base_mean_ns"`
	VariantMeanNS  uint64  `json:"variant_mean_ns"`
	BaseTotalNS    uint64  `json:"base_total_ns"`
	VariantTotalNS uint64  `json:"variant_total_ns"`
	DeltaPct       float64 `json:"delta_pct"`             // mean-to-mean; negative = faster
	TotalDeltaNS   int64   `json:"total_delta_ns"`        // variant total - base total
	BaseOccupancy  float64 `json:"base_occupancy_pct"`    // [H] theoretical, from launch geometry
	VarOccupancy   float64 `json:"variant_occupancy_pct"` // [H]
	OnlyIn         string  `json:"only_in,omitempty"`     // "base" | "variant"
	// ShapeCount is how many distinct launch geometries this symbol was
	// launched at, taking the larger of the two sides. Above one, the mean
	// columns average populations that are not comparable and DeltaPct is
	// not a per-launch fact about any launch that happened. Read ShapeDeltas
	// instead.
	ShapeCount int `json:"shape_count,omitempty"`
}

// Heterogeneous reports whether this symbol spans more than one launch
// geometry, which makes its mean-to-mean delta uninterpretable as a
// per-launch statement.
func (d KernelDelta) Heterogeneous() bool { return d.ShapeCount > 1 }

// LaunchShapeDelta is the difference between two captures at one launch
// geometry. Unlike KernelDelta it is always a single population on each
// side, so its mean-to-mean delta is a statement about a launch that
// actually occurred.
type LaunchShapeDelta struct {
	LaunchKey
	BaseCount      int     `json:"base_count"`
	VariantCount   int     `json:"variant_count"`
	BaseMeanNS     uint64  `json:"base_mean_ns"`
	VariantMeanNS  uint64  `json:"variant_mean_ns"`
	BaseP50NS      uint64  `json:"base_p50_ns"`
	VariantP50NS   uint64  `json:"variant_p50_ns"`
	BaseTotalNS    uint64  `json:"base_total_ns"`
	VariantTotalNS uint64  `json:"variant_total_ns"`
	DeltaPct       float64 `json:"delta_pct"`      // mean-to-mean; negative = faster
	TotalDeltaNS   int64   `json:"total_delta_ns"` // variant total - base total
	Blocks         uint64  `json:"blocks"`         // grid block count, 0 = unknown
	// CountsMatch is true when both sides launched this geometry the same
	// number of times. When false the totals differ partly because the work
	// differs, so only the means compare like for like.
	CountsMatch bool   `json:"counts_match"`
	OnlyIn      string `json:"only_in,omitempty"` // "base" | "variant"
}

// UtilizationDelta compares the busy/idle budget of two captures. It is
// what separates "the kernels got faster" from "the device stopped
// waiting": a run can win on wall time with identical kernel times purely
// by closing gaps.
type UtilizationDelta struct {
	BaseOccupancyPct    float64 `json:"base_occupancy_pct"`
	VariantOccupancyPct float64 `json:"variant_occupancy_pct"`
	BaseIdleNS          uint64  `json:"base_idle_ns"`
	VariantIdleNS       uint64  `json:"variant_idle_ns"`
	BaseWallSpanNS      uint64  `json:"base_wall_span_ns"`
	VariantWallSpanNS   uint64  `json:"variant_wall_span_ns"`
	BaseGapCount        int     `json:"base_gap_count"`
	VariantGapCount     int     `json:"variant_gap_count"`
	BaseMeanGapNS       uint64  `json:"base_mean_gap_ns"`
	VariantMeanGapNS    uint64  `json:"variant_mean_gap_ns"`
}

// CaptureComparison is the kernel-level result of diffing two captures.
type CaptureComparison struct {
	Verdict        CaptureVerdict   `json:"verdict"`
	Summary        string           `json:"summary"`
	KernelDeltas   []KernelDelta    `json:"kernel_deltas"`
	BaseTotalNS    uint64           `json:"base_total_ns"`
	VariantTotalNS uint64           `json:"variant_total_ns"`
	TotalDeltaPct  float64          `json:"total_delta_pct"`
	Utilization    UtilizationDelta `json:"utilization"`
	// OnlyInBase and OnlyInVariant name the kernels one capture ran and
	// the other did not — the difference most easily missed when reading
	// a table sorted by delta.
	OnlyInBase    []string `json:"only_in_base,omitempty"`
	OnlyInVariant []string `json:"only_in_variant,omitempty"`
	// ShapeDeltas is the same comparison keyed on the full launch geometry
	// rather than the symbol. It is the only one of the two whose mean
	// columns compare a single population on each side.
	ShapeDeltas []LaunchShapeDelta `json:"shape_deltas,omitempty"`
	// HeterogeneousKernels names the symbols launched at more than one
	// geometry, whose KernelDeltas rows therefore average populations that
	// are not comparable.
	HeterogeneousKernels []string `json:"heterogeneous_kernels,omitempty"`
}

// CompareCaptures diffs two capture reports kernel by kernel, ordered by
// absolute mean-time impact. This complements wall-clock optimize compare:
// unified-memory setup and process overhead can hide real kernel-level wins
// (or losses) on integrated-GPU hosts.
func CompareCaptures(base, variant *Report) *CaptureComparison {
	c := &CaptureComparison{}
	if base == nil || variant == nil || len(base.Kernels) == 0 || len(variant.Kernels) == 0 {
		c.Verdict = CaptureInconclusive
		side := "variant"
		if base == nil || len(base.Kernels) == 0 {
			side = "base"
		}
		c.Summary = fmt.Sprintf("%s capture has no kernels; nothing to compare", side)
		return c
	}
	// A capture that lost records is a sample, not a measurement, and the
	// loss is uniform enough to read as a real difference: an MLX decode
	// missing 48% of its records diffed as a 43.9% kernel-time win over
	// the arm that kept them. Refusing here is the same call the empty
	// capture gets, for the same reason. Reports whose decoder never
	// checked (Completeness nil) are compared as before rather than
	// blocked on evidence nobody gathered.
	for _, side := range []struct {
		name string
		rep  *Report
	}{{"base", base}, {"variant", variant}} {
		if side.rep.Completeness == nil || side.rep.Completeness.Complete() {
			continue
		}
		c.Verdict = CaptureInconclusive
		c.Summary = fmt.Sprintf("%s %s; the totals would be a share of the run, not the run",
			side.name, side.rep.Completeness.Summary())
		return c
	}
	c.BaseTotalNS = base.TotalKernelNS
	c.VariantTotalNS = variant.TotalKernelNS
	if base.TotalKernelNS > 0 {
		c.TotalDeltaPct = pct(variant.TotalKernelNS, base.TotalKernelNS)
	}

	baseBy := map[string]KernelStats{}
	for _, k := range base.Kernels {
		baseBy[k.Name] = k
	}
	variantBy := map[string]KernelStats{}
	for _, k := range variant.Kernels {
		variantBy[k.Name] = k
	}
	// A symbol's per-launch mean is only interpretable if it was launched at
	// one geometry. Count the geometries on each side and keep the larger:
	// a symbol that is single-shape in one capture and multi-shape in the
	// other still cannot be compared mean to mean.
	shapeCount := map[string]int{}
	for name, n := range ShapeCountsByName(base.LaunchShapes) {
		shapeCount[name] = n
	}
	for name, n := range ShapeCountsByName(variant.LaunchShapes) {
		if n > shapeCount[name] {
			shapeCount[name] = n
		}
	}

	names := make([]string, 0, len(baseBy)+len(variantBy))
	for n := range baseBy {
		names = append(names, n)
	}
	for n := range variantBy {
		if _, ok := baseBy[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	for _, n := range names {
		b, v := baseBy[n], variantBy[n]
		d := KernelDelta{
			Name:           n,
			BaseCount:      b.Count,
			VariantCount:   v.Count,
			BaseMeanNS:     b.MeanNS,
			VariantMeanNS:  v.MeanNS,
			BaseTotalNS:    b.TotalNS,
			VariantTotalNS: v.TotalNS,
			TotalDeltaNS:   int64(v.TotalNS) - int64(b.TotalNS),
			BaseOccupancy:  b.TheoreticalOccupancyPct,
			VarOccupancy:   v.TheoreticalOccupancyPct,
		}
		switch {
		case b.Count == 0:
			d.OnlyIn = "variant"
			c.OnlyInVariant = append(c.OnlyInVariant, n)
		case v.Count == 0:
			d.OnlyIn = "base"
			c.OnlyInBase = append(c.OnlyInBase, n)
		}
		if b.MeanNS > 0 && v.MeanNS > 0 {
			d.DeltaPct = pct(v.MeanNS, b.MeanNS)
		} else if b.MeanNS == 0 {
			d.DeltaPct = -100 // appeared from nothing
		} else {
			d.DeltaPct = 100 // vanished
		}
		if n := shapeCount[n]; n > 1 {
			d.ShapeCount = n
		}
		c.KernelDeltas = append(c.KernelDeltas, d)
	}
	sort.Slice(c.KernelDeltas, func(i, j int) bool {
		ai, aj := impact(c.KernelDeltas[i]), impact(c.KernelDeltas[j])
		if ai != aj {
			return ai > aj
		}
		return c.KernelDeltas[i].Name < c.KernelDeltas[j].Name
	})
	c.ShapeDeltas = compareLaunchShapes(base.LaunchShapes, variant.LaunchShapes)
	for name, n := range shapeCount {
		if n > 1 {
			c.HeterogeneousKernels = append(c.HeterogeneousKernels, name)
		}
	}
	sort.Strings(c.HeterogeneousKernels)

	c.Utilization = UtilizationDelta{
		BaseOccupancyPct:    base.Utilization.OccupancyPct,
		VariantOccupancyPct: variant.Utilization.OccupancyPct,
		BaseIdleNS:          base.Utilization.IdleNS,
		VariantIdleNS:       variant.Utilization.IdleNS,
		BaseWallSpanNS:      base.Utilization.WallSpanNS,
		VariantWallSpanNS:   variant.Utilization.WallSpanNS,
		BaseGapCount:        base.Utilization.GapCount,
		VariantGapCount:     variant.Utilization.GapCount,
		BaseMeanGapNS:       base.Utilization.MeanGapNS,
		VariantMeanGapNS:    variant.Utilization.MeanGapNS,
	}

	c.Verdict, c.Summary = verdictFor(c)
	return c
}

// impact ranks a delta by the GPU time it moved, in nanoseconds. Ordering
// by percentage alone puts a 90% swing on a kernel that runs for a
// microsecond above a 5% swing on one that owns the capture; ordering by
// moved time puts the reader in front of what actually changed.
func impact(d KernelDelta) float64 {
	return absF(float64(d.TotalDeltaNS))
}

func pct(variant, base uint64) float64 {
	if base == 0 {
		return 0
	}
	return (float64(variant) - float64(base)) / float64(base) * 100
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func captureTotalVerdict(c *CaptureComparison) (CaptureVerdict, string) {
	if c.TotalDeltaPct <= -5 {
		return CaptureImproved, fmt.Sprintf("faster overall (%.1f%% total kernel time)", c.TotalDeltaPct)
	}
	if c.TotalDeltaPct >= 5 {
		return CaptureRegressed, fmt.Sprintf("slower overall (%.1f%% total kernel time)", c.TotalDeltaPct)
	}
	return CaptureUnchanged, fmt.Sprintf("total kernel time within noise (%.1f%%)", c.TotalDeltaPct)
}

func verdictFor(c *CaptureComparison) (CaptureVerdict, string) {
	const threshold = 5.0 // percent change considered signal for a kernel
	var improved, regressed int
	top := ""
	if len(c.KernelDeltas) > 0 {
		top = c.KernelDeltas[0].Name
	}
	for _, d := range c.KernelDeltas {
		if absF(d.DeltaPct) < threshold {
			continue
		}
		if d.DeltaPct < 0 {
			improved++
		} else {
			regressed++
		}
	}
	totalDelta := absF(c.TotalDeltaPct)
	// A full rename (no shared kernel names) makes per-kernel deltas
	// meaningless: every old kernel "vanished" (-100%) and every new one
	// "appeared" (+100%). Detect the signature — all deltas at exactly
	// ±100% with no shared names — and fall back to total time.
	fullRename := len(c.KernelDeltas) > 0 && improved+regressed == len(c.KernelDeltas)
	if fullRename {
		for _, d := range c.KernelDeltas {
			shared := d.BaseCount > 0 && d.VariantCount > 0
			if shared || (absF(d.DeltaPct) != 100) {
				fullRename = false
				break
			}
		}
	}
	if fullRename {
		return captureTotalVerdict(c)
	}

	switch {
	case improved > 0 && regressed > 0:
		return CaptureMixed, fmt.Sprintf("mixed: %d kernels faster, %d slower beyond %.0f%%", improved, regressed, threshold)
	case improved > 0:
		return CaptureImproved, fmt.Sprintf("faster overall (%.1f%%); largest kernel delta on %q", totalDelta, top)
	case regressed > 0:
		return CaptureRegressed, fmt.Sprintf("slower overall (%.1f%%); largest kernel delta on %q", totalDelta, top)
	default:
		return CaptureUnchanged, fmt.Sprintf("no kernel moved beyond %.0f%%", threshold)
	}
}

// compareLaunchShapes diffs two captures at the level of the launch key, so
// each row's mean columns describe one population on each side.
//
// Rows are ordered by GPU time moved. A row whose counts differ between the
// arms is still reported -- the work itself differing is a finding -- but it
// is marked, because its total delta then mixes "each launch is slower" with
// "there are more launches", and only the mean separates them.
func compareLaunchShapes(base, variant []LaunchShapeStats) []LaunchShapeDelta {
	baseBy := map[LaunchKey]LaunchShapeStats{}
	for _, s := range base {
		baseBy[s.LaunchKey] = s
	}
	variantBy := map[LaunchKey]LaunchShapeStats{}
	for _, s := range variant {
		variantBy[s.LaunchKey] = s
	}
	keys := make([]LaunchKey, 0, len(baseBy)+len(variantBy))
	for k := range baseBy {
		keys = append(keys, k)
	}
	for k := range variantBy {
		if _, ok := baseBy[k]; !ok {
			keys = append(keys, k)
		}
	}
	out := make([]LaunchShapeDelta, 0, len(keys))
	for _, k := range keys {
		b, v := baseBy[k], variantBy[k]
		blocks := b.Blocks
		if blocks == 0 {
			blocks = v.Blocks
		}
		d := LaunchShapeDelta{
			LaunchKey:      k,
			BaseCount:      b.Count,
			VariantCount:   v.Count,
			BaseMeanNS:     b.MeanNS,
			VariantMeanNS:  v.MeanNS,
			BaseP50NS:      b.P50NS,
			VariantP50NS:   v.P50NS,
			BaseTotalNS:    b.TotalNS,
			VariantTotalNS: v.TotalNS,
			TotalDeltaNS:   int64(v.TotalNS) - int64(b.TotalNS),
			Blocks:         blocks,
			CountsMatch:    b.Count == v.Count && b.Count > 0,
		}
		switch {
		case b.Count == 0:
			d.OnlyIn = "variant"
		case v.Count == 0:
			d.OnlyIn = "base"
		}
		switch {
		case b.MeanNS > 0 && v.MeanNS > 0:
			d.DeltaPct = pct(v.MeanNS, b.MeanNS)
		case b.MeanNS == 0:
			d.DeltaPct = -100
		default:
			d.DeltaPct = 100
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := abs64(out[i].TotalDeltaNS), abs64(out[j].TotalDeltaNS)
		if ai != aj {
			return ai > aj
		}
		return out[i].LaunchKey.String() < out[j].LaunchKey.String()
	})
	return out
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
