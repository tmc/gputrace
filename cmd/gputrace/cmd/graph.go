package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/graph"
)

var graphCmd = newGraphCommand(&graphOptions{
	format:    "dot",
	graphType: "hierarchy",
})

func newGraphCommand(opts *graphOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph <trace.gputrace>",
		Short: "Generate graph visualization of GPU trace structure",
		Long: `Generate graph visualizations showing relationships between GPU trace entities.

Supported formats:
  - dot: Graphviz DOT format (default)
  - mermaid: Mermaid diagram format

Graph types:
  - hierarchy: Command buffer → encoder → shader hierarchy (default)
  - flow: Execution flow (temporal order)
  - resources: Resource usage and buffer allocations

Examples:
  gputrace graph trace.gputrace
  gputrace graph trace.gputrace --format mermaid
  gputrace graph trace.gputrace --type hierarchy --show-timing
  gputrace graph trace.gputrace -o graph.dot`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGraph(cmd, args, opts)
		},
	}

	cmd.Flags().StringVar(&opts.format, "format", opts.format, "Output format: dot, mermaid")
	cmd.Flags().StringVar(&opts.graphType, "type", opts.graphType, "Graph type: hierarchy, flow, resources")
	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "Output file (default: stdout)")
	cmd.Flags().BoolVar(&opts.showTiming, "show-timing", opts.showTiming, "Include timing information in nodes")
	cmd.Flags().BoolVar(&opts.showMemory, "show-memory", opts.showMemory, "Include memory usage information")
	return cmd
}

func init() {
	rootCmd.AddCommand(graphCmd)
}

func runGraph(cmd *cobra.Command, args []string, opts *graphOptions) error {
	valid, err := validateGraphOptions(opts.format, opts.graphType)
	if err != nil {
		return err
	}

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
	switch opts.format {
	case "dot":
		generator = graph.NewDOTGenerator()
	case "mermaid":
		generator = graph.NewMermaidGenerator()
	}

	// Configure generator
	config := &graph.Config{
		Type:       valid.graphType,
		ShowTiming: opts.showTiming,
		ShowMemory: opts.showMemory,
	}

	writer, closeOutput, err := openGraphOutput(cmd, opts.output)
	if err != nil {
		return err
	}
	if err := generator.Generate(writer, trace, config); err != nil {
		if closeOutput != nil {
			_ = closeOutput()
		}
		return fmt.Errorf("failed to generate graph: %w", err)
	}
	return closeGraphOutput(cmd, opts.output, closeOutput)
}

type graphOptions struct {
	format     string
	graphType  string
	output     string
	showTiming bool
	showMemory bool
}

func validateGraphOptions(format, graphType string) (graphOptions, error) {
	format, err := normalizeGraphFormat(format)
	if err != nil {
		return graphOptions{}, err
	}
	graphType, err = normalizeGraphType(graphType)
	if err != nil {
		return graphOptions{}, err
	}
	return graphOptions{format: format, graphType: graphType}, nil
}

func normalizeGraphFormat(format string) (string, error) {
	switch format {
	case "dot", "mermaid":
		return format, nil
	default:
		return "", fmt.Errorf("invalid graph format %q (must be dot or mermaid)", format)
	}
}

func normalizeGraphType(graphType string) (string, error) {
	switch graphType {
	case "hierarchy", "flow", "resources":
		return graphType, nil
	default:
		return "", fmt.Errorf("invalid graph type %q (must be hierarchy, flow, or resources)", graphType)
	}
}

func writeGraphOutput(cmd *cobra.Command, outputPath, output string) error {
	writer, closeOutput, err := openGraphOutput(cmd, outputPath)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(writer, output); err != nil {
		if closeOutput != nil {
			_ = closeOutput()
		}
		return fmt.Errorf("failed to write output: %w", err)
	}
	return closeGraphOutput(cmd, outputPath, closeOutput)
}

func openGraphOutput(cmd *cobra.Command, outputPath string) (io.Writer, func() error, error) {
	if outputPath == "" {
		return cmd.OutOrStdout(), nil, nil
	}

	writer, closeOutput, err := createCommandOutput(outputPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create output: %w", err)
	}
	return writer, closeOutput, nil
}

func closeGraphOutput(cmd *cobra.Command, outputPath string, closeOutput func() error) error {
	if closeOutput != nil {
		if err := closeOutput(); err != nil {
			return fmt.Errorf("failed to close output: %w", err)
		}
	}
	if !commandOutputPathIsStdout(outputPath) {
		fmt.Fprintf(cmd.ErrOrStderr(), "Graph written to %s\n", outputPath)
	}
	return nil
}
