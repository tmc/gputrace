package gpuevent

import (
	"fmt"
	"sort"
)

// CaptureVerdict summarizes the kernel-level outcome of comparing two
// captures.
type CaptureVerdict string

const (
	CaptureImproved    CaptureVerdict = "improved"
	CaptureRegressed   CaptureVerdict = "regressed"
	CaptureUnchanged   CaptureVerdict = "unchanged"
	CaptureMixed       CaptureVerdict = "mixed"
	CaptureInconclusive CaptureVerdict = "inconclusive"
)

// KernelDelta is the per-kernel difference between two captures. Kernels
// present on only one side are reported with the other side's count at 0.
type KernelDelta struct {
	Name           string  `json:"name"`
	BaseCount      int     `json:"base_count"`
	VariantCount   int     `json:"variant_count"`
	BaseMeanNS     uint64  `json:"base_mean_ns"`
	VariantMeanNS  uint64  `json:"variant_mean_ns"`
	BaseTotalNS    uint64  `json:"base_total_ns"`
	VariantTotalNS uint64  `json:"variant_total_ns"`
	DeltaPct       float64 `json:"delta_pct"` // mean-to-mean; negative = faster
}

// CaptureComparison is the kernel-level result of diffing two captures.
type CaptureComparison struct {
	Verdict       CaptureVerdict `json:"verdict"`
	Summary       string         `json:"summary"`
	KernelDeltas  []KernelDelta  `json:"kernel_deltas"`
	BaseTotalNS   uint64         `json:"base_total_ns"`
	VariantTotalNS uint64        `json:"variant_total_ns"`
	TotalDeltaPct float64        `json:"total_delta_pct"`
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
		if len(base.Kernels) == 0 {
			side = "base"
		}
		c.Summary = fmt.Sprintf("%s capture has no kernels; nothing to compare", side)
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
			Name:         n,
			BaseCount:    b.Count,
			VariantCount: v.Count,
			BaseMeanNS:   b.MeanNS,
			VariantMeanNS: v.MeanNS,
			BaseTotalNS:  b.TotalNS,
			VariantTotalNS: v.TotalNS,
		}
		if b.MeanNS > 0 && v.MeanNS > 0 {
			d.DeltaPct = pct(v.MeanNS, b.MeanNS)
		} else if b.MeanNS == 0 {
			d.DeltaPct = -100 // appeared from nothing
		} else {
			d.DeltaPct = 100 // vanished
		}
		c.KernelDeltas = append(c.KernelDeltas, d)
	}
	sort.Slice(c.KernelDeltas, func(i, j int) bool {
		ai, aj := absF(c.KernelDeltas[i].DeltaPct)*impactWeight(c.KernelDeltas[i]), absF(c.KernelDeltas[j].DeltaPct)*impactWeight(c.KernelDeltas[j])
		return ai > aj
	})

	c.Verdict, c.Summary = verdictFor(c)
	return c
}

// impactWeight scales a kernel's delta by its share of combined time so a
// small change to a hot kernel outranks a big change to a cold one.
func impactWeight(d KernelDelta) float64 {
	total := float64(d.BaseTotalNS + d.VariantTotalNS)
	if total == 0 {
		return 0
	}
	return total / total // placeholder scale: weight 1; ordering by |delta%| alone
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
