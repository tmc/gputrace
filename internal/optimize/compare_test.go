package optimize

import (
	"testing"
)

func mkResult(walls ...uint64) *Result {
	res := &Result{}
	for _, w := range walls {
		res.Iterations = append(res.Iterations, Iteration{WallNS: w})
	}
	res.MedianNS = median(walls)
	res.Q1NS, res.Q3NS = quartiles(walls)
	return res
}

func TestCompareClearImprovement(t *testing.T) {
	base := mkResult(1000, 1010, 990, 1005)     // median 1002
	variant := mkResult(800, 810, 795, 802)     // median ~806: far outside base IQR
	v := Compare(base, variant)
	if v.Verdict != Improved {
		t.Fatalf("verdict = %v (%s)", v.Verdict, v.Reason)
	}
	if v.BaseMedianNS == 0 || v.VariantMedianNS == 0 {
		t.Error("medians not reported")
	}
	if v.DeltaPct >= 0 {
		t.Errorf("delta = %.1f%%, want negative for improvement", v.DeltaPct)
	}
}

func TestCompareClearRegression(t *testing.T) {
	base := mkResult(1000, 1010, 990, 1005)
	variant := mkResult(1300, 1310, 1295, 1302)
	v := Compare(base, variant)
	if v.Verdict != Regressed {
		t.Fatalf("verdict = %v", v.Verdict)
	}
}

func TestCompareNoisyOverlap(t *testing.T) {
	// IQRs overlap but the median delta (12.5%) exceeds the noise band:
	// a real-looking delta the evidence cannot cleanly separate.
	base := mkResult(1000, 1200, 900, 1400)   // median 1100, IQR ~[975,1250]
	variant := mkResult(850, 1000, 800, 1150) // median 925,  IQR ~[812,1087]
	v := Compare(base, variant)
	if v.Verdict != NoisyChange {
		t.Fatalf("verdict = %v (%s), want noisy", v.Verdict, v.Reason)
	}
}

func TestCompareEquivalent(t *testing.T) {
	base := mkResult(1000, 1001, 999, 1000)
	variant := mkResult(1000, 1001, 999, 1002)
	v := Compare(base, variant)
	if v.Verdict != Equivalent {
		t.Fatalf("verdict = %v (%s)", v.Verdict, v.Reason)
	}
}

func TestCompareInsufficientSamples(t *testing.T) {
	v := Compare(mkResult(10), mkResult(20))
	if v.Verdict != Inconclusive {
		t.Fatalf("single-sample compare verdict = %v, want inconclusive", v.Verdict)
	}
	if v.Reason == "" {
		t.Error("inconclusive verdict carries no reason")
	}
}

func TestVerdictJSON(t *testing.T) {
	v := Compare(mkResult(1000, 1010, 990, 1005), mkResult(800, 810, 795, 802))
	data, err := v.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("empty JSON")
	}
}
