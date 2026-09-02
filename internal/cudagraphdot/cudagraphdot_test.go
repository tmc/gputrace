package cudagraphdot

import (
	"path/filepath"
	"strings"
	"testing"
)

// testdata/nested_graph.dot is a real MLX dump (kerndot-q7/g_37.dot) chosen
// because it is small enough to count by hand and still exercises the one
// structural trap: graph_130 holds eleven kernel nodes and one child-graph
// node, that child holds nothing but another child-graph node, and the
// grandchild holds the twelfth kernel. Counting rectangles as kernels gives
// 13 and misses the cuDNN flash-attention kernel entirely.
const nestedPath = "testdata/nested_graph.dot"

func TestParseFlattensChildGraphs(t *testing.T) {
	f, err := ParseFile(nestedPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got, want := len(f.Graphs), 3; got != want {
		t.Errorf("graphs = %d, want %d", got, want)
	}
	if got, want := f.Roots, []string{"graph_130"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("roots = %v, want %v", got, want)
	}
	if got, want := f.KernelCount(), 12; got != want {
		t.Errorf("KernelCount() = %d, want %d (dotdepth.py reports 12 for this file)", got, want)
	}

	kernels := f.Kernels()
	if got, want := len(kernels), 12; got != want {
		t.Fatalf("len(Kernels()) = %d, want %d", got, want)
	}
	// The grandchild kernel must arrive with the whole descent recorded,
	// which is what a pprof stack is built from.
	var deepest KernelInstance
	for _, k := range kernels {
		if len(k.Path) > len(deepest.Path) {
			deepest = k
		}
	}
	wantPath := []string{"graph_130", "graph_131", "graph_132"}
	if strings.Join(deepest.Path, "/") != strings.Join(wantPath, "/") {
		t.Errorf("deepest path = %v, want %v", deepest.Path, wantPath)
	}
	if !strings.Contains(deepest.Symbol, "sdpa") {
		t.Errorf("deepest symbol = %q, want the cuDNN sdpa kernel", deepest.Symbol)
	}
	// No kernel instance may be a child-graph reference.
	for _, k := range kernels {
		if strings.HasPrefix(k.Symbol, "graph_") {
			t.Errorf("child-graph node %q counted as a kernel", k.Symbol)
		}
	}
}

func TestKernelsAndKernelCountAgree(t *testing.T) {
	f, err := ParseFile(nestedPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got, want := len(f.Kernels()), f.KernelCount(); got != want {
		t.Errorf("len(Kernels()) = %d, KernelCount() = %d; the two must not diverge", got, want)
	}
}

func TestChildGraphOnlyRecognizesGraphLabeledRectangles(t *testing.T) {
	tests := []struct {
		name string
		node Node
		want string
	}{
		{"child graph", Node{Shape: "rectangle", Payload: "graph_131"}, "graph_131"},
		{"kernel", Node{Shape: "octagon", Payload: "_Z5saxpyifPfS_"}, ""},
		// A kernel whose symbol happens to start with graph_ is still a
		// kernel: the shape is what distinguishes them.
		{"kernel named like a graph", Node{Shape: "octagon", Payload: "graph_reduce"}, ""},
		// A rectangle that is not a graph reference is not a descent.
		{"rectangle with a symbol", Node{Shape: "rectangle", Payload: "_Z5saxpyifPfS_"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.node.ChildGraph(); got != tt.want {
				t.Errorf("ChildGraph() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseCountsRepeatedChildGraphPerInstantiation(t *testing.T) {
	// A child graph launched from two nodes runs twice on the device, so
	// its kernels count twice. Memoizing the count must not collapse them.
	const dump = `digraph dot {
subgraph cluster_1 {
label="graph_1" graph[style="dashed"];
"graph_1_node_0"[style="solid" shape="rectangle" label="0
graph_2
"];
"graph_1_node_1"[style="solid" shape="rectangle" label="1
graph_2
"];
}
subgraph cluster_2 {
label="graph_2" graph[style="dashed"];
"graph_2_node_0"[style="bold" shape="octagon" label="0
_Z5saxpyifPfS_
"];
"graph_2_node_1"[style="bold" shape="octagon" label="1
_Z4axpyifPfS_
"];
}
}`
	f, err := Parse("repeat.dot", []byte(dump))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := f.KernelCount(), 4; got != want {
		t.Errorf("KernelCount() = %d, want %d (two instantiations of a two-kernel graph)", got, want)
	}
	if got, want := len(f.Kernels()), 4; got != want {
		t.Errorf("len(Kernels()) = %d, want %d", got, want)
	}
}

func TestParseRejectsDumpWithoutNodes(t *testing.T) {
	if _, err := Parse("empty.dot", []byte("digraph dot {\n}\n")); err == nil {
		t.Fatal("Parse accepted a dump with no nodes; an empty structure profile is indistinguishable from a graph with no kernels")
	}
}

func TestParseSurvivesCycle(t *testing.T) {
	// MLX graphs are acyclic. A malformed dump must stop rather than
	// recurse until the stack runs out.
	const dump = `digraph dot {
subgraph cluster_1 {
label="graph_1" graph[style="dashed"];
"graph_1_node_0"[style="solid" shape="rectangle" label="0
graph_2
"];
}
subgraph cluster_2 {
label="graph_2" graph[style="dashed"];
"graph_2_node_0"[style="solid" shape="rectangle" label="0
graph_1
"];
"graph_2_node_1"[style="bold" shape="octagon" label="1
_Z5saxpyifPfS_
"];
}
}`
	f, err := Parse("cycle.dot", []byte(dump))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Every graph is referenced, so nothing is a root and the walk has no
	// entry point; the guard is that this returns rather than hanging.
	if got := f.KernelCount(); got != 0 {
		t.Errorf("KernelCount() = %d, want 0 for a fully-referenced cycle", got)
	}
}

func TestParseDirReadsEveryDump(t *testing.T) {
	files, err := ParseDir("testdata")
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("ParseDir returned %d files, want 1", len(files))
	}
	if got, want := files[0].Path, filepath.Join("testdata", "nested_graph.dot"); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestParseDirRejectsDirectoryWithNoDumps(t *testing.T) {
	if _, err := ParseDir(t.TempDir()); err == nil {
		t.Fatal("ParseDir accepted a directory with no dumps")
	}
}
