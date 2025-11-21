package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tmc/mlx-go/experiments/gputrace"
	"github.com/tmc/mlx-go/experiments/gputrace/internal/graph"
)

var (
	graphFormat     string
	graphType       string
	graphOutput     string
	graphShowTiming bool
	graphShowMemory bool
)

var graphCmd = &cobra.Command{
	Use:   "graph <trace.gputrace>",
	Short: "Generate graph visualization of GPU trace structure",
	Long: `Generate graph visualizations showing relationships between GPU trace entities.

Supported formats:
  - dot: Graphviz DOT format (default)
  - mermaid: Mermaid diagram format

Graph types:
  - flow: Execution flow (temporal order) - default
  - hierarchy: Command buffer → encoder → shader hierarchy
  - resources: Resource usage and buffer allocations

Examples:
  gputrace graph trace.gputrace
  gputrace graph trace.gputrace --format mermaid
  gputrace graph trace.gputrace --type hierarchy --show-timing
  gputrace graph trace.gputrace -o graph.dot`,
	Args: cobra.ExactArgs(1),
	RunE: runGraph,
}

func init() {
	rootCmd.AddCommand(graphCmd)

	graphCmd.Flags().StringVar(&graphFormat, "format", "dot", "Output format: dot, mermaid")
	graphCmd.Flags().StringVar(&graphType, "type", "hierarchy", "Graph type: hierarchy, flow, resources")
	graphCmd.Flags().StringVarP(&graphOutput, "output", "o", "", "Output file (default: stdout)")
	graphCmd.Flags().BoolVar(&graphShowTiming, "show-timing", false, "Include timing information in nodes")
	graphCmd.Flags().BoolVar(&graphShowMemory, "show-memory", false, "Include memory usage information")
}

func runGraph(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Create graph generator based on format
	var generator graph.Generator
	switch graphFormat {
	case "dot":
		generator = graph.NewDOTGenerator()
	case "mermaid":
		generator = graph.NewMermaidGenerator()
	default:
		return fmt.Errorf("unsupported format: %s (supported: dot, mermaid)", graphFormat)
	}

	// Configure generator
	config := &graph.Config{
		Type:       graphType,
		ShowTiming: graphShowTiming,
		ShowMemory: graphShowMemory,
	}

	// Generate graph
	output, err := generator.Generate(trace, config)
	if err != nil {
		return fmt.Errorf("failed to generate graph: %w", err)
	}

	// Write output
	if graphOutput != "" {
		if err := os.WriteFile(graphOutput, []byte(output), 0644); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStderr(), "Graph written to %s\n", graphOutput)
	} else {
		fmt.Fprint(cmd.OutOrStdout(), output)
	}

	return nil
}
