package cmd

import (
	"bytes"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace/internal/trace"
)

type dependenciesOptions struct {
	verbose bool
	limit   int
	all     bool
}

var dependenciesCmd = newDependenciesCommand(&dependenciesOptions{limit: defaultHumanLimit})

func newDependenciesCommand(opts *dependenciesOptions) *cobra.Command {
	if opts.limit == 0 {
		opts.limit = defaultHumanLimit
	}
	cmd := &cobra.Command{
		Use:    "dependencies <trace_path>",
		Short:  "Generate a dependency graph of operations",
		Hidden: true,
		Long: `Generate a Graphviz DOT graph from decoded buffer dependency events.
Missing record types can make the graph incomplete. Human-readable DOT output
is bounded by default; use --all for the complete decoded graph.

Example:
  gputrace dependencies trace.gputrace | dot -Tpng -o graph.png`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDependencies(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", false, "Show detailed parsing information")
	cmd.Flags().IntVar(&opts.limit, "limit", opts.limit, "Maximum nodes and edges in DOT output")
	cmd.Flags().BoolVar(&opts.all, "all", opts.all, "Show the complete dependency graph")
	return cmd
}

func runDependencies(cmd *cobra.Command, args []string, opts *dependenciesOptions) error {
	t, err := trace.Open(args[0])
	if err != nil {
		return err
	}

	if opts.verbose {
		fmt.Fprintf(cmd.ErrOrStderr(), "Capture data size: %d bytes\n", len(t.CaptureData))

		events, err := t.ParseDependencyEvents()
		if err != nil {
			return fmt.Errorf("parse events: %w", err)
		}

		csCnt, bindCnt, useCnt := 0, 0, 0
		for _, ev := range events {
			switch ev.Type {
			case trace.EventCS:
				csCnt++
			case trace.EventBind:
				bindCnt++
			case trace.EventUse:
				useCnt++
			}
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Events: %d total (CS=%d, Bind=%d, Use=%d)\n",
			len(events), csCnt, bindCnt, useCnt)
	}

	graph, err := t.BuildDependencyGraph()
	if err != nil {
		return err
	}

	if opts.verbose {
		fmt.Fprintf(cmd.ErrOrStderr(), "Graph: %d nodes, %d edges\n",
			len(graph.Nodes), len(graph.Edges))
	}

	limit, err := resolveHumanLimit(opts.limit, opts.all)
	if err != nil {
		return err
	}
	return writeDependencyGraphDOTLimited(cmd.OutOrStdout(), graph, limit)
}

func writeDependencyGraphDOT(w io.Writer, graph *trace.DependencyGraph) error {
	return writeDependencyGraphDOTLimited(w, graph, -1)
}

func writeDependencyGraphDOTLimited(w io.Writer, graph *trace.DependencyGraph, limit int) error {
	var buf bytes.Buffer
	fmt.Fprintln(&buf, "digraph G {")
	fmt.Fprintln(&buf, "  rankdir=LR;")
	fmt.Fprintln(&buf, "  node [shape=box, style=filled, fontname=\"Helvetica\"];")
	fmt.Fprintln(&buf, "  edge [fontname=\"Helvetica\", fontsize=10];")
	fmt.Fprintln(&buf, "  // Decoded dependency events; missing record types can make this graph incomplete.")

	nodeCount := limitedCount(len(graph.Nodes), limit)
	included := make(map[int]bool, nodeCount)
	for _, node := range graph.Nodes[:nodeCount] {
		included[node.ID] = true
		label := node.Label
		if len(label) > 50 {
			label = label[:47] + "..."
		}
		fmt.Fprintf(&buf, "  n%d [label=%q];\n", node.ID, label)
	}

	edgeCount := 0
	eligibleEdges := 0
	for _, edge := range graph.Edges {
		if !included[edge.From] || !included[edge.To] {
			continue
		}
		eligibleEdges++
		if limit >= 0 && edgeCount >= limit {
			continue
		}
		label := fmt.Sprintf("%s (%s)", edge.Buffer, edge.Hazard)
		fmt.Fprintf(&buf, "  n%d -> n%d [label=%q];\n", edge.From, edge.To, label)
		edgeCount++
	}
	if nodeCount < len(graph.Nodes) {
		fmt.Fprintf(&buf, "  // %d nodes and their incident edges omitted; use --all for the complete decoded graph.\n", len(graph.Nodes)-nodeCount)
	}
	if edgeCount < eligibleEdges {
		fmt.Fprintf(&buf, "  // %d additional edges between shown nodes omitted; use --all for the complete decoded graph.\n", eligibleEdges-edgeCount)
	}

	fmt.Fprintln(&buf, "}")
	_, err := w.Write(buf.Bytes())
	return err
}

func init() {
	rootCmd.AddCommand(dependenciesCmd)
}
