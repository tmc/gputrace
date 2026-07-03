package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var shaderSourceCmd = newShaderSourceCommand(&shaderSourceOptions{
	format: "text",
	hints:  true,
})

type shaderSourceOptions struct {
	format string
	output string
	hints  bool
}

func newShaderSourceCommand(opts *shaderSourceOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shader-source <trace.gputrace> <shader-name>",
		Short: "Show source-level performance attribution for a Metal shader",
		Long: `Analyze shader performance at the source code level, similar to 'go tool pprof -list'.

This command maps performance metrics (execution time, ALU utilization, memory bandwidth)
to individual source code lines, enabling precise identification of expensive operations.

Features:
  - Line-by-line performance attribution showing GPU time percentage
  - Hot spot identification (top 20% most expensive lines)
  - Instruction type classification (compute, memory, control)
  - Optimization hints for expensive operations
  - Multiple output formats (text, HTML, JSON)

The analysis uses:
  - Shader performance metrics from trace (timing, invocations, occupancy)
  - Metal shader source files (.metal) from indexed locations
  - Static analysis to estimate relative cost of each line
  - Heuristics to classify instruction types

Output Formats:
  - text: Annotated source with metrics (default, similar to 'perf annotate')
  - html: Interactive HTML view with syntax highlighting
  - json: Structured data for programmatic analysis

Examples:
  # Show annotated source for a shader
  gputrace shader-source trace.gputrace rope_single_freqs

  # Hide optimization hints
  gputrace shader-source trace.gputrace rope_single_freqs --hints=false

  # Generate HTML view
  gputrace shader-source trace.gputrace affine_qmm_t --format html -o shader.html

  # Export as JSON
  gputrace shader-source trace.gputrace vv_Multiply --format json -o analysis.json

Interpreting Results:
  - Time%: Percentage of this shader's GPU time attributed to the line
  - ALU%: Estimated ALU utilization for this line (0-100%)
  - Type: Instruction classification (c=compute, m=memory, o=other)
  - Lines marked with '>' are hot spots (top 20% by cost)

Note: Per-line attribution uses static analysis heuristics. For precise measurements,
consider using Metal's shader profiler or GPU frame capture tools.

See also:
  - gputrace shaders: List all shaders with aggregate metrics
  - gputrace profiler: Show profiler timing and shader cost breakdowns
  - go tool pprof -list: Similar concept for CPU profiles`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShaderSource(cmd, args, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.format, "format", "f", opts.format,
		"Output format: text, html, json")
	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output,
		"Output file (default: stdout)")
	cmd.Flags().BoolVar(&opts.hints, "hints", opts.hints,
		"Show optimization hints for expensive lines")
	return cmd
}

func init() {
	rootCmd.AddCommand(shaderSourceCmd)
}

func runShaderSource(cmd *cobra.Command, args []string, opts *shaderSourceOptions) error {
	tracePath := args[0]
	shaderName := args[1]

	format, err := validateShaderSourceFormat(opts.format)
	if err != nil {
		return err
	}

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Extract shader source attribution
	attribution, err := gputrace.ExtractShaderSourceAttribution(trace, shaderName)
	if err != nil {
		return fmt.Errorf("failed to extract shader source attribution: %w", err)
	}

	// Generate output based on format
	var output string
	var data interface{}

	switch format {
	case "text":
		output = gputrace.FormatShaderSourceAttribution(attribution, opts.hints)

	case "html":
		output = gputrace.FormatShaderSourceAttributionHTML(attribution)

	case "json":
		data = attribution
	}

	writer, closeOutput, err := createCommandOutput(opts.output)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	if closeOutput != nil {
		defer closeOutput()
	}

	if output != "" {
		if _, err := io.WriteString(writer, output); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}
	} else {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(data); err != nil {
			return fmt.Errorf("failed to write JSON: %w", err)
		}
	}
	if opts.output != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "✓ Written to: %s\n", opts.output)
	}

	return nil
}

func validateShaderSourceFormat(format string) (string, error) {
	switch format {
	case "text", "html", "json":
		return format, nil
	default:
		return "", fmt.Errorf("invalid shader-source format %q (must be text, html, or json)", format)
	}
}
