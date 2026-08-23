package optimize

import (
	"encoding/json"
	"fmt"
)

// Verdict is the outcome of comparing two measured runs.
type Verdict string

const (
	// Improved means the variant median beat the baseline with separated
	// distributions.
	Improved Verdict = "improved"
	// Regressed means the variant median lost with separated distributions.
	Regressed Verdict = "regressed"
	// Equivalent means the medians sit within each other's noise band.
	Equivalent Verdict = "equivalent"
	// NoisyChange means medians differ but IQRs overlap: a real-looking
	// delta the evidence cannot separate from noise. An agent must not
	// claim improvement from this verdict; rerun with more iterations.
	NoisyChange Verdict = "noisy-change"
	// Inconclusive means the samples themselves cannot support any claim.
	Inconclusive Verdict = "inconclusive"
)

// Comparison reports an A/B verdict with the numbers behind it.
type Comparison struct {
	Verdict         Verdict `json:"verdict"`
	Reason          string  `json:"reason"`
	BaseMedianNS    uint64  `json:"base_median_ns"`
	VariantMedianNS uint64  `json:"variant_median_ns"`
	BaseQ1NS        uint64  `json:"base_q1_ns,omitempty"`
	BaseQ3NS        uint64  `json:"base_q3_ns,omitempty"`
	VariantQ1NS     uint64  `json:"variant_q1_ns,omitempty"`
	VariantQ3NS     uint64  `json:"variant_q3_ns,omitempty"`
	DeltaNS         int64   `json:"delta_ns"` // negative = faster
	DeltaPct        float64 `json:"delta_pct"`
}

// Compare decides whether variant improved on base, regressed, or whether
// the difference cannot be distinguished from run-to-run noise.
//
// The test is distribution separation, not median distance: when the two
// interquartile ranges overlap, medians can differ substantially on small
// samples purely by chance. A NoisyChange verdict tells the agent to
// collect more data instead of celebrating.
func Compare(base, variant *Result) *Comparison {
	c := &Comparison{}
	if base == nil || variant == nil || len(base.Iterations) < 2 || len(variant.Iterations) < 2 {
		c.Verdict = Inconclusive
		c.Reason = "need at least 2 measured iterations per side"
		return c
	}
	c.BaseMedianNS = base.MedianNS
	c.VariantMedianNS = variant.MedianNS
	c.BaseQ1NS, c.BaseQ3NS = base.Q1NS, base.Q3NS
	c.VariantQ1NS, c.VariantQ3NS = variant.Q1NS, variant.Q3NS
	c.DeltaNS = int64(variant.MedianNS) - int64(base.MedianNS)
	if base.MedianNS > 0 {
		c.DeltaPct = float64(c.DeltaNS) / float64(base.MedianNS) * 100
	}

	switch {
	case c.VariantQ3NS < c.BaseQ1NS:
		c.Verdict = Improved
		c.Reason = fmt.Sprintf("variant IQR [%d..%d] sits entirely below baseline IQR [%d..%d]",
			c.VariantQ1NS, c.VariantQ3NS, c.BaseQ1NS, c.BaseQ3NS)
	case c.VariantQ1NS > c.BaseQ3NS:
		c.Verdict = Regressed
		c.Reason = fmt.Sprintf("variant IQR [%d..%d] sits entirely above baseline IQR [%d..%d]",
			c.VariantQ1NS, c.VariantQ3NS, c.BaseQ1NS, c.BaseQ3NS)
	case c.withinNoiseBand():
		c.Verdict = Equivalent
		c.Reason = "median delta is smaller than the combined noise band"
	default:
		c.Verdict = NoisyChange
		c.Reason = fmt.Sprintf("medians differ by %.1f%% but IQRs overlap ([%d..%d] vs [%d..%d]); collect more iterations before concluding",
			abs(c.DeltaPct), c.VariantQ1NS, c.VariantQ3NS, c.BaseQ1NS, c.BaseQ3NS)
	}
	return c
}

// withinNoiseBand reports whether the median delta is inside a quarter of
// the combined IQR width — a conservative equivalence zone. When both IQRs
// are zero (perfectly stable runs), any nonzero median delta is a real
// change, so the band is treated as one part in a thousand.
func (c *Comparison) withinNoiseBand() bool {
	noise := (c.baseIQRWidth() + c.variantIQRWidth()) / 4
	if noise == 0 {
		noise = c.BaseMedianNS / 1000
	}
	delta := c.DeltaNS
	if delta < 0 {
		delta = -delta
	}
	return uint64(delta) <= noise
}

func (c *Comparison) baseIQRWidth() uint64 { return c.BaseQ3NS - c.BaseQ1NS }

func (c *Comparison) variantIQRWidth() uint64 { return c.VariantQ3NS - c.VariantQ1NS }

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// JSON renders the comparison for agent consumption.
func (c *Comparison) JSON() ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}
