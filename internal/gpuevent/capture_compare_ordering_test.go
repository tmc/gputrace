package gpuevent

import "testing"

// Ordering by GPU time moved, not by percentage: a huge percentage swing
// on a kernel that runs for a microsecond must not outrank a small swing
// on the kernel that owns the capture.
func TestCompareCapturesOrdersByTimeMoved(t *testing.T) {
	base := &Report{
		TotalKernelNS: 10_100_000,
		Kernels: []KernelStats{
			{Name: "hot", Count: 100, MeanNS: 100_000, TotalNS: 10_000_000},
			{Name: "trivial", Count: 100, MeanNS: 1_000, TotalNS: 100_000},
		},
	}
	variant := &Report{
		TotalKernelNS: 9_510_000,
		Kernels: []KernelStats{
			{Name: "hot", Count: 100, MeanNS: 95_000, TotalNS: 9_500_000}, // -5%, -500us
			{Name: "trivial", Count: 100, MeanNS: 100, TotalNS: 10_000},   // -90%, -90us
		},
	}
	got := CompareCaptures(base, variant)
	if got.KernelDeltas[0].Name != "hot" {
		t.Errorf("first row = %q, want the kernel that moved the most GPU time", got.KernelDeltas[0].Name)
	}
	if got.KernelDeltas[0].TotalDeltaNS != -500_000 {
		t.Errorf("TotalDeltaNS = %d, want -500000", got.KernelDeltas[0].TotalDeltaNS)
	}
}

func TestCompareCapturesReportsOnlyInSides(t *testing.T) {
	base := &Report{
		TotalKernelNS: 2000,
		Kernels: []KernelStats{
			{Name: "shared", Count: 1, MeanNS: 1000, TotalNS: 1000},
			{Name: "removed", Count: 1, MeanNS: 1000, TotalNS: 1000},
		},
	}
	variant := &Report{
		TotalKernelNS: 2000,
		Kernels: []KernelStats{
			{Name: "shared", Count: 1, MeanNS: 1000, TotalNS: 1000},
			{Name: "added", Count: 1, MeanNS: 1000, TotalNS: 1000},
		},
	}
	got := CompareCaptures(base, variant)
	if len(got.OnlyInBase) != 1 || got.OnlyInBase[0] != "removed" {
		t.Errorf("OnlyInBase = %v, want [removed]", got.OnlyInBase)
	}
	if len(got.OnlyInVariant) != 1 || got.OnlyInVariant[0] != "added" {
		t.Errorf("OnlyInVariant = %v, want [added]", got.OnlyInVariant)
	}
	for _, d := range got.KernelDeltas {
		switch d.Name {
		case "shared":
			if d.OnlyIn != "" {
				t.Errorf("shared kernel marked OnlyIn=%q", d.OnlyIn)
			}
		case "removed":
			if d.OnlyIn != "base" {
				t.Errorf("removed kernel OnlyIn=%q, want base", d.OnlyIn)
			}
		case "added":
			if d.OnlyIn != "variant" {
				t.Errorf("added kernel OnlyIn=%q, want variant", d.OnlyIn)
			}
		}
	}
}

// Utilization deltas separate "the kernels got faster" from "the device
// stopped waiting": identical kernel times, different idle budgets.
func TestCompareCapturesCarriesUtilization(t *testing.T) {
	base := &Report{
		TotalKernelNS: 1000,
		Kernels:       []KernelStats{{Name: "k", Count: 1, MeanNS: 1000, TotalNS: 1000}},
		Utilization:   Utilization{WallSpanNS: 10_000, BusyNS: 1000, IdleNS: 9000, OccupancyPct: 10, GapCount: 3},
	}
	variant := &Report{
		TotalKernelNS: 1000,
		Kernels:       []KernelStats{{Name: "k", Count: 1, MeanNS: 1000, TotalNS: 1000}},
		Utilization:   Utilization{WallSpanNS: 2000, BusyNS: 1000, IdleNS: 1000, OccupancyPct: 50, GapCount: 1},
	}
	got := CompareCaptures(base, variant).Utilization
	if got.BaseOccupancyPct != 10 || got.VariantOccupancyPct != 50 {
		t.Errorf("occupancy = %v -> %v, want 10 -> 50", got.BaseOccupancyPct, got.VariantOccupancyPct)
	}
	if got.BaseIdleNS != 9000 || got.VariantIdleNS != 1000 {
		t.Errorf("idle = %d -> %d, want 9000 -> 1000", got.BaseIdleNS, got.VariantIdleNS)
	}
}
