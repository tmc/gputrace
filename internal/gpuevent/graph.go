package gpuevent

import "sort"

// GraphNodeStats aggregates every execution of one node in one CUDA graph.
// The node ID is stable across launches of the same instantiated graph, so
// it identifies a position in the graph even though the kernel name alone
// may appear at many positions.
type GraphNodeStats struct {
	// Index orders the nodes of one graph by node ID, giving a short
	// stable handle for reports; NodeID is the identity CUPTI reported.
	Index    int     `json:"index"`
	NodeID   uint64  `json:"node_id"`
	Name     string  `json:"name"`
	Count    int     `json:"count"`
	TotalNS  uint64  `json:"total_ns"`
	MeanNS   uint64  `json:"mean_ns"`
	SharePct float64 `json:"share_pct"`
}

// GraphStats aggregates every kernel launched from one CUDA graph.
type GraphStats struct {
	GraphID  uint32           `json:"graph_id"`
	Launches int              `json:"launches"` // executions of the whole graph
	Kernels  int              `json:"kernels"`  // kernel node executions
	Nodes    int              `json:"nodes"`    // distinct kernel nodes
	TotalNS  uint64           `json:"total_ns"`
	SharePct float64          `json:"share_pct"` // of all kernel time in the capture
	TopNodes []GraphNodeStats `json:"top_nodes,omitempty"`
}

// GraphAnalysis reports how much of a capture ran through CUDA graphs.
//
// Attribution is by graph and node ID [V], both read from the activity
// record. Mapping a node back to the source operation that created it is
// not possible from activity records alone — CUPTI reports the node's
// identity, not its provenance — so this stops at "node 7 of graph 3 runs
// this kernel for 40% of the graph's time".
type GraphAnalysis struct {
	Graphs        []GraphStats `json:"graphs"`
	GraphKernels  int          `json:"graph_kernels"`
	DirectKernels int          `json:"direct_kernels"`
	GraphNS       uint64       `json:"graph_ns"`
	GraphSharePct float64      `json:"graph_share_pct"` // graph kernel time / all kernel time
}

// topGraphNodeCount bounds the per-graph node list kept for reporting.
const topGraphNodeCount = 5

// AnalyzeGraphs groups kernels by the CUDA graph and node that launched
// them. Kernels with no graph ID are counted as direct launches.
func AnalyzeGraphs(events []Event) *GraphAnalysis {
	out := &GraphAnalysis{}
	type nodeKey struct {
		graph uint32
		node  uint64
	}
	type nodeAgg struct {
		name    string
		count   int
		totalNS uint64
	}
	graphs := map[uint32]*GraphStats{}
	nodes := map[nodeKey]*nodeAgg{}
	// A graph launch is one execution of the whole graph; every node fires
	// once per launch, so the busiest node's count is the launch count.
	nodeCountsPerGraph := map[uint32]map[uint64]int{}

	var allKernelNS uint64
	for _, e := range events {
		if e.Kind != KindKernel {
			continue
		}
		allKernelNS += e.DurationNS()
		if e.GraphID == 0 {
			out.DirectKernels++
			continue
		}
		out.GraphKernels++
		out.GraphNS += e.DurationNS()
		g := graphs[e.GraphID]
		if g == nil {
			g = &GraphStats{GraphID: e.GraphID}
			graphs[e.GraphID] = g
			nodeCountsPerGraph[e.GraphID] = map[uint64]int{}
		}
		g.Kernels++
		g.TotalNS += e.DurationNS()
		nodeCountsPerGraph[e.GraphID][e.GraphNodeID]++

		key := nodeKey{e.GraphID, e.GraphNodeID}
		n := nodes[key]
		if n == nil {
			n = &nodeAgg{name: activityLabel(e)}
			nodes[key] = n
		}
		n.count++
		n.totalNS += e.DurationNS()
	}
	if len(graphs) == 0 {
		return out
	}
	for gid, counts := range nodeCountsPerGraph {
		g := graphs[gid]
		g.Nodes = len(counts)
		for _, c := range counts {
			if c > g.Launches {
				g.Launches = c
			}
		}
	}
	for key, n := range nodes {
		g := graphs[key.graph]
		stats := GraphNodeStats{
			NodeID:  key.node,
			Name:    n.name,
			Count:   n.count,
			TotalNS: n.totalNS,
		}
		if n.count > 0 {
			stats.MeanNS = n.totalNS / uint64(n.count)
		}
		if g.TotalNS > 0 {
			stats.SharePct = 100 * float64(n.totalNS) / float64(g.TotalNS)
		}
		g.TopNodes = append(g.TopNodes, stats)
	}
	for _, g := range graphs {
		// Index by node ID first, so the label of a node does not depend
		// on how much time it happened to take in this capture.
		sort.Slice(g.TopNodes, func(i, j int) bool { return g.TopNodes[i].NodeID < g.TopNodes[j].NodeID })
		for i := range g.TopNodes {
			g.TopNodes[i].Index = i
		}
		sort.Slice(g.TopNodes, func(i, j int) bool {
			if g.TopNodes[i].TotalNS != g.TopNodes[j].TotalNS {
				return g.TopNodes[i].TotalNS > g.TopNodes[j].TotalNS
			}
			return g.TopNodes[i].NodeID < g.TopNodes[j].NodeID
		})
		if len(g.TopNodes) > topGraphNodeCount {
			g.TopNodes = g.TopNodes[:topGraphNodeCount]
		}
		if allKernelNS > 0 {
			g.SharePct = 100 * float64(g.TotalNS) / float64(allKernelNS)
		}
		out.Graphs = append(out.Graphs, *g)
	}
	sort.Slice(out.Graphs, func(i, j int) bool {
		if out.Graphs[i].TotalNS != out.Graphs[j].TotalNS {
			return out.Graphs[i].TotalNS > out.Graphs[j].TotalNS
		}
		return out.Graphs[i].GraphID < out.Graphs[j].GraphID
	})
	if allKernelNS > 0 {
		out.GraphSharePct = 100 * float64(out.GraphNS) / float64(allKernelNS)
	}
	return out
}
