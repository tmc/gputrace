package gpuevent

import (
	"strings"
	"testing"
)

func feed(t *testing.T, lines ...string) []Event {
	t.Helper()
	cap, err := DecodeJSONL(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	return cap.Events
}

func TestAnalyzeRanksByTotalTime(t *testing.T) {
	events := feed(t,
		`{"kind":"kernel","name":"big","start_ns":0,"end_ns":900000,"grid":"100x1x1","block":"256x1x1","registers":32}`,
		`{"kind":"kernel","name":"big","start_ns":1000000,"end_ns":1900000,"grid":"100x1x1","block":"256x1x1","registers":32}`,
		`{"kind":"kernel","name":"small","start_ns":2000000,"end_ns":2100000,"grid":"8x1x1","block":"64x1x1","registers":16}`,
	)
	rep := Analyze(events, nil)
	if len(rep.Kernels) != 2 {
		t.Fatalf("kernels = %d, want 2", len(rep.Kernels))
	}
	if rep.Kernels[0].Name != "big" {
		t.Errorf("top kernel = %q, want big", rep.Kernels[0].Name)
	}
	if rep.TotalKernelNS != 1900000 {
		t.Errorf("total = %d", rep.TotalKernelNS)
	}
	if got := rep.Kernels[0].SharePct; got < 94 || got > 95 {
		t.Errorf("share = %.1f%%, want ~94.7", got)
	}
}

func TestAnalyzeClassifiesBound(t *testing.T) {
	// Large grid relative to duration => compute-bound; tiny grid with long
	// duration => latency-bound; mid shape dominated by bytes => memory-bound.
	events := feed(t,
		// 18992 blocks x 256 threads for 400us: plenty of parallel work.
		`{"kind":"kernel","name":"compute","start_ns":0,"end_ns":400000,"grid":"18992x1x1","block":"32x8x1","registers":40}`,
		// 1 block x 32 threads for 300us: nothing to hide latency behind.
		`{"kind":"kernel","name":"latency","start_ns":500000,"end_ns":800000,"grid":"1x1x1","block":"32x1x1","registers":24}`,
		// 4 blocks copying 256 MiB in 500us: bandwidth-dominated shape.
		`{"kind":"kernel","name":"copy","start_ns":900000,"end_ns":1400000,"grid":"4x1x1","block":"256x1x1","bytes":268435456}`,
	)
	rep := Analyze(events, nil)
	bound := map[string]Bound{}
	for _, k := range rep.Kernels {
		bound[k.Name] = k.Bound
	}
	if bound["latency"] != BoundLatency {
		t.Errorf("latency classified %v, want %v", bound["latency"], BoundLatency)
	}
	if bound["compute"] != BoundCompute {
		t.Errorf("compute classified %v, want %v", bound["compute"], BoundCompute)
	}
	if bound["copy"] != BoundMemory {
		t.Errorf("copy classified %v, want %v", bound["copy"], BoundMemory)
	}
}

func TestAnalyzeProducesDominanceFinding(t *testing.T) {
	lines := []string{}
	for i := 0; i < 10; i++ {
		lines = append(lines,
			`{"kind":"kernel","name":"hot","start_ns":0,"end_ns":100000,"grid":"100x1x1","block":"128x1x1"}`)
	}
	for i := 0; i < 10; i++ {
		lines = append(lines,
			`{"kind":"kernel","name":"cold","start_ns":200000,"end_ns":202000,"grid":"4x1x1","block":"64x1x1"}`)
	}
	rep := Analyze(feed(t, lines...), nil)
	found := false
	for _, f := range rep.Findings {
		if f.Kind == FindingDominance && strings.Contains(f.Subject, "hot") {
			found = true
			if f.Hypothesis == "" {
				t.Error("dominance finding carries no hypothesis")
			}
			if len(f.Evidence) == 0 {
				t.Error("dominance finding carries no evidence")
			}
		}
	}
	if !found {
		t.Fatalf("no dominance finding; got %+v", rep.Findings)
	}
}

func TestAnalyzeLatencyFindingForTinyGrids(t *testing.T) {
	events := feed(t,
		`{"kind":"kernel","name":"tiny","start_ns":0,"end_ns":250000,"grid":"1x1x1","block":"32x1x1","registers":8}`,
	)
	rep := Analyze(events, nil)
	found := false
	for _, f := range rep.Findings {
		if f.Kind == FindingLaunchShape {
			found = true
		}
	}
	if !found {
		t.Fatalf("no launch-shape finding; got %+v", rep.Findings)
	}
}

func TestAnalyzeIgnoresNonKernelsAndEmpty(t *testing.T) {
	if rep := Analyze(nil, nil); len(rep.Kernels) != 0 || len(rep.Findings) != 0 {
		t.Errorf("empty analysis = %+v", rep)
	}
	events := feed(t, `{"kind":"memcpy","start_ns":0,"end_ns":100,"bytes":10}`)
	if rep := Analyze(events, nil); len(rep.Kernels) != 0 {
		t.Errorf("memcpy counted as kernel: %+v", rep.Kernels)
	}
}

func TestStatsPercentiles(t *testing.T) {
	events := feed(t,
		`{"kind":"kernel","name":"k","start_ns":0,"end_ns":10}`,
		`{"kind":"kernel","name":"k","start_ns":20,"end_ns":40}`,
		`{"kind":"kernel","name":"k","start_ns":60,"end_ns":90}`,
		`{"kind":"kernel","name":"k","start_ns":100,"end_ns":140}`,
		`{"kind":"kernel","name":"k","start_ns":200,"end_ns":300}`,
	)
	rep := Analyze(events, nil)
	if len(rep.Kernels) != 1 {
		t.Fatalf("kernels = %d", len(rep.Kernels))
	}
	s := rep.Kernels[0]
	if s.Count != 5 || s.TotalNS != 200 {
		t.Errorf("count/total = %d/%d", s.Count, s.TotalNS)
	}
	if s.MeanNS != 40 {
		t.Errorf("mean = %d", s.MeanNS)
	}
	if s.P95NS != 100 {
		t.Errorf("p95 = %d, want 100 (max duration of the five launches)", s.P95NS)
	}
	if s.MaxNS != 100 {
		t.Errorf("max = %d", s.MaxNS)
	}
}
