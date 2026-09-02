package gpuevent

import "testing"

func TestDims(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want uint64
	}{
		{"three dims", "1x320x1", 320},
		{"square", "32x8x1", 256},
		{"single", "64", 64},
		{"empty is unknown", "", 0},
		{"unparseable is unknown", "1xNx1", 0},
		{"a zero dimension is unknown, not zero work", "1x0x1", 0},
		{"spaces tolerated", "2 x 4 x 8", 64},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := dims(tt.in); got != tt.want {
				t.Errorf("dims(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestAnalyzeLaunchShapesSeparatesGeometries is the regression this file
// exists for. One symbol launched at two geometries whose durations differ
// by 20x must produce two populations, not one mean sitting between them.
func TestAnalyzeLaunchShapesSeparatesGeometries(t *testing.T) {
	shapes := AnalyzeLaunchShapes(eventsFromLines(t,
		`{"kind":"kernel","name":"qmv","start_ns":0,"end_ns":5000,"grid":"1x32x1","block":"32x8x1"}`,
		`{"kind":"kernel","name":"qmv","start_ns":10000,"end_ns":15000,"grid":"1x32x1","block":"32x8x1"}`,
		`{"kind":"kernel","name":"qmv","start_ns":20000,"end_ns":120000,"grid":"1x1280x1","block":"32x8x1"}`,
	))
	if len(shapes) != 2 {
		t.Fatalf("shapes = %d, want 2 (one symbol, two geometries)", len(shapes))
	}
	byGrid := map[string]LaunchShapeStats{}
	for _, s := range shapes {
		byGrid[s.Grid] = s
	}
	small, big := byGrid["1x32x1"], byGrid["1x1280x1"]
	if small.Count != 2 || small.MeanNS != 5000 {
		t.Errorf("small shape = count %d mean %d, want 2 / 5000", small.Count, small.MeanNS)
	}
	if big.Count != 1 || big.MeanNS != 100000 {
		t.Errorf("big shape = count %d mean %d, want 1 / 100000", big.Count, big.MeanNS)
	}
	if small.Blocks != 32 || big.Blocks != 1280 {
		t.Errorf("blocks = %d / %d, want 32 / 1280", small.Blocks, big.Blocks)
	}
	if small.ThreadsPerLaunch != 32*256 {
		t.Errorf("threads per launch = %d, want %d", small.ThreadsPerLaunch, 32*256)
	}
	// The symbol-level mean over these three launches is 36666ns, which
	// describes none of them. That is the number this grouping exists to
	// stop anyone quoting.
	if n := ShapeCountsByName(shapes)["qmv"]; n != 2 {
		t.Errorf("shape count for qmv = %d, want 2", n)
	}
}

func TestAnalyzeLaunchShapesKeysOnRegistersAndSharedMem(t *testing.T) {
	shapes := AnalyzeLaunchShapes(eventsFromLines(t,
		`{"kind":"kernel","name":"k","start_ns":0,"end_ns":1000,"grid":"1x1x1","block":"32x1x1","registers":32}`,
		`{"kind":"kernel","name":"k","start_ns":2000,"end_ns":3000,"grid":"1x1x1","block":"32x1x1","registers":64}`,
		`{"kind":"kernel","name":"k","start_ns":4000,"end_ns":5000,"grid":"1x1x1","block":"32x1x1","registers":64,"shared_mem":2048}`,
	))
	if len(shapes) != 3 {
		t.Fatalf("shapes = %d, want 3: registers and shared memory change residency "+
			"and so belong in the key", len(shapes))
	}
}

// TestCompareWithholdsHeterogeneousMean pins the behaviour that a symbol
// spanning several geometries reports a shape count rather than letting a
// mean-to-mean delta stand. Two geometries here move in opposite
// directions, so any single mean is not merely imprecise but misleading.
func TestCompareWithholdsHeterogeneousMean(t *testing.T) {
	base := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"qmv","start_ns":0,"end_ns":5000,"grid":"1x32x1","block":"32x8x1"}`,
		`{"kind":"kernel","name":"qmv","start_ns":10000,"end_ns":110000,"grid":"1x1280x1","block":"32x8x1"}`,
	), nil)
	variant := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"qmv","start_ns":0,"end_ns":9000,"grid":"1x32x1","block":"32x8x1"}`,
		`{"kind":"kernel","name":"qmv","start_ns":10000,"end_ns":105000,"grid":"1x1280x1","block":"32x8x1"}`,
	), nil)

	c := CompareCaptures(base, variant)
	if len(c.KernelDeltas) != 1 {
		t.Fatalf("kernel deltas = %d, want 1", len(c.KernelDeltas))
	}
	d := c.KernelDeltas[0]
	if !d.Heterogeneous() {
		t.Fatalf("qmv spans two geometries but Heterogeneous() is false (ShapeCount=%d)", d.ShapeCount)
	}
	if d.ShapeCount != 2 {
		t.Errorf("shape count = %d, want 2", d.ShapeCount)
	}
	if len(c.HeterogeneousKernels) != 1 || c.HeterogeneousKernels[0] != "qmv" {
		t.Errorf("heterogeneous kernels = %v, want [qmv]", c.HeterogeneousKernels)
	}

	if len(c.ShapeDeltas) != 2 {
		t.Fatalf("shape deltas = %d, want 2", len(c.ShapeDeltas))
	}
	byGrid := map[string]LaunchShapeDelta{}
	for _, s := range c.ShapeDeltas {
		byGrid[s.Grid] = s
	}
	// The small geometry got 80% slower; the large one got 5% faster. The
	// symbol-level mean delta is -9%, which reports the opposite of what
	// happened to most launches.
	if got := byGrid["1x32x1"].DeltaPct; got < 79 || got > 81 {
		t.Errorf("small geometry delta = %.1f%%, want ~+80%%", got)
	}
	if got := byGrid["1x1280x1"].DeltaPct; got > -4 || got < -6 {
		t.Errorf("large geometry delta = %.1f%%, want ~-5%%", got)
	}
	if !byGrid["1x32x1"].CountsMatch {
		t.Error("counts match on both sides but CountsMatch is false")
	}
	if byGrid["1x1280x1"].Blocks != 1280 {
		t.Errorf("blocks = %d, want 1280", byGrid["1x1280x1"].Blocks)
	}
}

// TestCompareKeepsMeanForSingleShapeKernel checks the withholding is
// targeted: a symbol launched at one geometry still reports its mean, since
// there is nothing for it to be averaged across.
func TestCompareKeepsMeanForSingleShapeKernel(t *testing.T) {
	base := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"norm","start_ns":0,"end_ns":1000,"grid":"1x1x1","block":"64x1x1"}`,
	), nil)
	variant := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"norm","start_ns":0,"end_ns":1200,"grid":"1x1x1","block":"64x1x1"}`,
	), nil)

	c := CompareCaptures(base, variant)
	d := c.KernelDeltas[0]
	if d.Heterogeneous() {
		t.Fatalf("single-geometry kernel marked heterogeneous (ShapeCount=%d)", d.ShapeCount)
	}
	if d.BaseMeanNS != 1000 || d.VariantMeanNS != 1200 {
		t.Errorf("means = %d -> %d, want 1000 -> 1200", d.BaseMeanNS, d.VariantMeanNS)
	}
	if len(c.HeterogeneousKernels) != 0 {
		t.Errorf("heterogeneous kernels = %v, want none", c.HeterogeneousKernels)
	}
}

// TestCompareShapeDeltaMarksUnequalCounts pins the second half of the
// interpretation problem: when the arms launched a geometry a different
// number of times, the total delta mixes per-launch cost with work done.
func TestCompareShapeDeltaMarksUnequalCounts(t *testing.T) {
	base := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"k","start_ns":0,"end_ns":1000,"grid":"1x8x1","block":"32x1x1"}`,
	), nil)
	variant := Analyze(eventsFromLines(t,
		`{"kind":"kernel","name":"k","start_ns":0,"end_ns":1000,"grid":"1x8x1","block":"32x1x1"}`,
		`{"kind":"kernel","name":"k","start_ns":2000,"end_ns":3000,"grid":"1x8x1","block":"32x1x1"}`,
	), nil)

	c := CompareCaptures(base, variant)
	if len(c.ShapeDeltas) != 1 {
		t.Fatalf("shape deltas = %d, want 1", len(c.ShapeDeltas))
	}
	d := c.ShapeDeltas[0]
	if d.CountsMatch {
		t.Error("counts are 1 and 2 but CountsMatch is true")
	}
	if d.DeltaPct != 0 {
		t.Errorf("per-launch delta = %.1f%%, want 0: each launch costs the same, "+
			"there are simply more of them", d.DeltaPct)
	}
	if d.TotalDeltaNS != 1000 {
		t.Errorf("total delta = %d, want 1000", d.TotalDeltaNS)
	}
}
