package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace/internal/trace"
)

var dependenciesVerbose bool

var dependenciesCmd = &cobra.Command{
	Use:   "dependencies <trace_path>",
	Short: "Generate a dependency graph of operations",
	Long: `Analyze buffer usage to generate a dependency graph of operations/encoders.
The output is in Graphviz DOT format.

Example:
  gputrace dependencies trace.gputrace | dot -Tpng -o graph.png`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := trace.Open(args[0])
		if err != nil {
			return err
		}

		if dependenciesVerbose {
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

		if dependenciesVerbose {
			fmt.Fprintf(cmd.ErrOrStderr(), "Graph: %d nodes, %d edges\n",
				len(graph.Nodes), len(graph.Edges))
		}

		fmt.Println("digraph G {")
		fmt.Println("  rankdir=LR;")
		fmt.Println("  node [shape=box, style=filled, fontname=\"Helvetica\"];")
		fmt.Println("  edge [fontname=\"Helvetica\", fontsize=10];")

		for _, node := range graph.Nodes {
			// Clean up label if it's too long?
			// For now, keep as is or truncate
			label := node.Label
			if len(label) > 50 {
				label = label[:47] + "..."
			}
			fmt.Printf("  n%d [label=\"%s\"];\n", node.ID, label)
		}

		for _, edge := range graph.Edges {
			// Include hazard type in edge label for clarity
			fmt.Printf("  n%d -> n%d [label=\"%s (%s)\"];\n", edge.From, edge.To, edge.Buffer, edge.Hazard)
		}

		fmt.Println("}")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(dependenciesCmd)
	dependenciesCmd.Flags().BoolVarP(&dependenciesVerbose, "verbose", "v", false, "Show detailed parsing information")
}
