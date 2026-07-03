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
}

var dependenciesCmd = newDependenciesCommand(&dependenciesOptions{})

func newDependenciesCommand(opts *dependenciesOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "dependencies <trace_path>",
		Short:  "Generate a dependency graph of operations",
		Hidden: true,
		Long: `Analyze buffer usage to generate a dependency graph of operations/encoders.
The output is in Graphviz DOT format.

Example:
  gputrace dependencies trace.gputrace | dot -Tpng -o graph.png`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDependencies(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", false, "Show detailed parsing information")
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

	return writeDependencyGraphDOT(cmd.OutOrStdout(), graph)
}

func writeDependencyGraphDOT(w io.Writer, graph *trace.DependencyGraph) error {
	var buf bytes.Buffer
	fmt.Fprintln(&buf, "digraph G {")
	fmt.Fprintln(&buf, "  rankdir=LR;")
	fmt.Fprintln(&buf, "  node [shape=box, style=filled, fontname=\"Helvetica\"];")
	fmt.Fprintln(&buf, "  edge [fontname=\"Helvetica\", fontsize=10];")

	for _, node := range graph.Nodes {
		label := node.Label
		if len(label) > 50 {
			label = label[:47] + "..."
		}
		fmt.Fprintf(&buf, "  n%d [label=%q];\n", node.ID, label)
	}

	for _, edge := range graph.Edges {
		label := fmt.Sprintf("%s (%s)", edge.Buffer, edge.Hazard)
		fmt.Fprintf(&buf, "  n%d -> n%d [label=%q];\n", edge.From, edge.To, label)
	}

	fmt.Fprintln(&buf, "}")
	_, err := w.Write(buf.Bytes())
	return err
}

func init() {
	rootCmd.AddCommand(dependenciesCmd)
}
