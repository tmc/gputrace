package gpuevent

import "testing"

func graphKernel(graphID uint32, nodeID uint64, name string, start, end uint64) Event {
	return Event{Kind: KindKernel, Name: name, StartNS: start, EndNS: end, GraphID: graphID, GraphNodeID: nodeID}
}

func TestAnalyzeGraphs(t *testing.T) {
	var events []Event
	// One graph of two nodes launched three times, plus two direct launches.
	for launch := 0; launch < 3; launch++ {
		base := uint64(launch) * 10_000
		events = append(events,
			graphKernel(7, 200, "slow", base, base+900),
			graphKernel(7, 100, "fast", base+1000, base+1100),
		)
	}
	events = append(events,
		Event{Kind: KindKernel, Name: "direct", StartNS: 100_000, EndNS: 100_400},
		Event{Kind: KindKernel, Name: "direct", StartNS: 200_000, EndNS: 200_400},
	)

	got := AnalyzeGraphs(events)
	if len(got.Graphs) != 1 {
		t.Fatalf("Graphs = %d, want 1", len(got.Graphs))
	}
	g := got.Graphs[0]
	if g.GraphID != 7 || g.Launches != 3 || g.Nodes != 2 || g.Kernels != 6 {
		t.Errorf("graph = id %d, %d launches, %d nodes, %d kernels; want 7, 3, 2, 6",
			g.GraphID, g.Launches, g.Nodes, g.Kernels)
	}
	if got.GraphKernels != 6 || got.DirectKernels != 2 {
		t.Errorf("graph/direct kernels = %d/%d, want 6/2", got.GraphKernels, got.DirectKernels)
	}
	if g.TotalNS != 3000 {
		t.Errorf("graph total = %d ns, want 3000", g.TotalNS)
	}
	// Node index follows node ID, so it does not shuffle with timing;
	// ordering within TopNodes follows time.
	if g.TopNodes[0].Name != "slow" || g.TopNodes[0].Index != 1 {
		t.Errorf("hottest node = %+v, want the 'slow' node at index 1", g.TopNodes[0])
	}
	if got, want := g.TopNodes[0].SharePct, 90.0; got != want {
		t.Errorf("hottest node share = %v, want %v", got, want)
	}
}

func TestAnalyzeGraphsWithoutGraphs(t *testing.T) {
	got := AnalyzeGraphs([]Event{{Kind: KindKernel, StartNS: 0, EndNS: 100}})
	if len(got.Graphs) != 0 || got.DirectKernels != 1 || got.GraphSharePct != 0 {
		t.Errorf("AnalyzeGraphs() = %+v, want no graphs and one direct launch", got)
	}
}
