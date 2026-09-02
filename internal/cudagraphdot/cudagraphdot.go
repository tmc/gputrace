// Package cudagraphdot parses the CUDA-graph dumps MLX writes when
// MLX_SAVE_CUDA_GRAPHS_DOT_FILE is set: one DOT file per graph commit,
// holding a hierarchy of `subgraph cluster_N` blocks.
//
// The dump declares work rather than measuring it, which is what makes it
// worth reading. The same libmlx code writes it from either language
// binding, so two dumps compare with no cross-stack calibration, no GPU
// hold, and no run-to-run variance — a single dump per side is the whole
// measurement, unlike timing.
//
// The one structural subtlety: a node drawn as shape="rectangle" whose
// label names another graph is a child-graph node, not a kernel. Counting
// it as a kernel undercounts the graph it stands for and overcounts by one;
// Kernels flattens it instead, which is why the counts here agree with the
// hand-validated dotdepth.py.
package cudagraphdot

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// nodeRE matches one node declaration. The label carries the node index on
// its first line and the payload — a kernel symbol, or a child graph name —
// on the second.
var nodeRE = regexp.MustCompile(`"(graph_\d+)_node_(\d+)"\[style="([^"]*)" shape="([^"]*)" label="(\d+)\n([^"]*)\n?"\]`)

// edgeRE matches one dependency edge. DOT only ever draws edges within one
// cluster, so cross-cluster matches are dropped rather than trusted.
var edgeRE = regexp.MustCompile(`"(graph_\d+)_node_(\d+)" -> "(graph_\d+)_node_(\d+)"`)

// Node is one node of one graph.
type Node struct {
	Index   int
	Style   string
	Shape   string
	Payload string // kernel symbol, or the name of the child graph
}

// ChildGraph returns the graph this node launches, or "" when the node is
// a kernel.
func (n Node) ChildGraph() string {
	if n.Shape == "rectangle" && strings.HasPrefix(n.Payload, "graph_") {
		return n.Payload
	}
	return ""
}

// Edge is one intra-graph dependency, from node index to node index.
type Edge struct{ From, To int }

// Graph is one `subgraph cluster_N` block.
type Graph struct {
	Name  string
	Nodes []Node // ordered by index
	Edges []Edge
}

// File is one parsed dump.
type File struct {
	Path   string
	Graphs map[string]*Graph
	// Roots names the graphs no other graph launches, sorted. Each is an
	// independently committed graph.
	Roots []string
}

// KernelInstance is one leaf kernel position in a flattened graph: the
// chain of graph names from the committed root down to the graph holding
// the node, and the kernel symbol.
type KernelInstance struct {
	Path   []string
	Symbol string
}

// Parse reads one dump.
func Parse(path string, data []byte) (*File, error) {
	text := string(data)
	f := &File{Path: path, Graphs: map[string]*Graph{}}
	for _, m := range nodeRE.FindAllStringSubmatch(text, -1) {
		name, index, style, shape, payload := m[1], m[2], m[3], m[4], m[6]
		i, err := strconv.Atoi(index)
		if err != nil {
			continue
		}
		g := f.Graphs[name]
		if g == nil {
			g = &Graph{Name: name}
			f.Graphs[name] = g
		}
		g.Nodes = append(g.Nodes, Node{
			Index:   i,
			Style:   style,
			Shape:   shape,
			Payload: strings.TrimSpace(payload),
		})
	}
	if len(f.Graphs) == 0 {
		return nil, fmt.Errorf("cudagraphdot: %s declares no graph nodes", path)
	}
	for _, m := range edgeRE.FindAllStringSubmatch(text, -1) {
		if m[1] != m[3] {
			continue
		}
		g := f.Graphs[m[1]]
		if g == nil {
			continue
		}
		from, err1 := strconv.Atoi(m[2])
		to, err2 := strconv.Atoi(m[4])
		if err1 != nil || err2 != nil {
			continue
		}
		g.Edges = append(g.Edges, Edge{From: from, To: to})
	}
	for _, g := range f.Graphs {
		sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].Index < g.Nodes[j].Index })
	}

	referenced := map[string]bool{}
	for _, g := range f.Graphs {
		for _, n := range g.Nodes {
			if child := n.ChildGraph(); child != "" {
				referenced[child] = true
			}
		}
	}
	for name := range f.Graphs {
		if !referenced[name] {
			f.Roots = append(f.Roots, name)
		}
	}
	sort.Strings(f.Roots)
	return f, nil
}

// ParseFile reads one dump from disk.
func ParseFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

// ParseDir reads every *.dot file in a directory, in name order. Files
// that declare no nodes are skipped; a directory holding none is an error,
// since a silently empty structure profile is indistinguishable from a
// graph with no kernels.
func ParseDir(dir string) ([]*File, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.dot"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	files := make([]*File, 0, len(matches))
	for _, m := range matches {
		f, err := ParseFile(m)
		if err != nil {
			continue // a dump truncated by exit still counts what it holds
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("cudagraphdot: no readable *.dot dumps in %s", dir)
	}
	return files, nil
}

// Kernels flattens every committed graph into its leaf kernel launches,
// counting a kernel once per instantiation: a child graph launched from
// two nodes contributes its kernels twice, because the device runs them
// twice.
func (f *File) Kernels() []KernelInstance {
	var out []KernelInstance
	for _, root := range f.Roots {
		out = append(out, f.flatten(root, nil, map[string]bool{})...)
	}
	return out
}

// flatten walks one graph, descending into child-graph nodes. visiting
// guards against a cycle: MLX graphs are acyclic, and a malformed dump
// should stop rather than recurse forever.
func (f *File) flatten(name string, path []string, visiting map[string]bool) []KernelInstance {
	if visiting[name] {
		return nil
	}
	g := f.Graphs[name]
	if g == nil {
		return nil
	}
	visiting[name] = true
	defer delete(visiting, name)

	path = append(path, name)
	var out []KernelInstance
	for _, n := range g.Nodes {
		if child := n.ChildGraph(); child != "" {
			out = append(out, f.flatten(child, path, visiting)...)
			continue
		}
		out = append(out, KernelInstance{
			Path:   append([]string(nil), path...),
			Symbol: n.Payload,
		})
	}
	return out
}

// KernelCount reports how many kernel launches the committed graphs
// declare, without materializing them.
func (f *File) KernelCount() int {
	memo := map[string]int{}
	total := 0
	for _, root := range f.Roots {
		total += f.count(root, memo, map[string]bool{})
	}
	return total
}

func (f *File) count(name string, memo map[string]int, visiting map[string]bool) int {
	if n, ok := memo[name]; ok {
		return n
	}
	if visiting[name] {
		return 0
	}
	g := f.Graphs[name]
	if g == nil {
		return 0
	}
	visiting[name] = true
	total := 0
	for _, n := range g.Nodes {
		if child := n.ChildGraph(); child != "" {
			total += f.count(child, memo, visiting)
			continue
		}
		total++
	}
	delete(visiting, name)
	memo[name] = total
	return total
}
