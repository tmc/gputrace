// Package cmd implements the gputrace CLI commands.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "gputrace",
	Short: "Tools for analyzing and converting GPU trace files",
	Long: `gputrace provides tools for analyzing and converting GPU trace files (.gputrace bundles).

Command Groups:

Trace Overview:
  stats            - Capture structure, resources, and profiler availability
  api-calls        - API call sequences
  dump             - Raw API call dump

Kernel & Shader Analysis:
  shaders          - Shader performance metrics
  kernels          - Kernel functions and pipeline mappings
  shader-source    - Source-level performance attribution

Timing & Profiling:
  timing           - Timing metrics with measured/estimated provenance
  profiler         - Profiler spans, active time, dispatches, and pipelines
  pprof            - pprof format export
  correlate        - Correlate timing with hardware metrics

Command Buffers & Encoders:
  command-buffers  - Command buffer analysis
  encoders         - Compute encoder listing

Buffer Analysis:
  buffers          - Buffer listing and properties
  buffer-access    - Buffer access patterns
  buffer-timeline  - Buffer allocation timeline

Visualization & Export:
  timeline         - Text timeline and Chrome/Perfetto export
  graph            - Graph visualization
  tree             - Execution tree view
  diff             - Compare two traces
  brief            - Compact comparison brief
  insights         - Diagnostic performance hypotheses

Capture & Automation:
  capture          - Run a Metal workload under the capture interposer
  profile-replay   - Replay a capture under the profiler to add timing
  xcode-profile    - Xcode GPU profiler automation
  xcode-bindings   - Inspect private Xcode GTShaderProfiler bindings
  xcode-parity     - Audit Xcode metric parity for a trace

Utilities:
  mtlb             - Metal Library Binary inspection
  clear-buffers    - Destructively zero captured buffers
  version          - Print gputrace build version

Hidden commands are runnable but omitted from Available Commands because their
output is experimental or heuristic: counters, replay-counters, dependencies,
fences, export-counters, perfcounters-validate.

For more information about a specific command:
  gputrace [command] --help`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

type alreadyReportedError interface {
	error
	alreadyReported()
}

// ErrorAlreadyReported reports whether err has already been written as
// structured command output.
func ErrorAlreadyReported(err error) bool {
	var reported alreadyReportedError
	return errors.As(err, &reported)
}

func init() {
	// The command entry point prints ordinary errors. Some commands write a
	// structured JSON error before returning, so Cobra must not independently
	// print either the error or command usage.
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	initColorFlag(rootCmd)
}

// checkTraceFile verifies that a trace file path exists and is a valid .gputrace directory.
func checkTraceFile(tracePath string) error {
	info, err := os.Stat(tracePath)
	if os.IsNotExist(err) {
		return fmt.Errorf("trace file not found: %s", tracePath)
	}
	if err != nil {
		return fmt.Errorf("error accessing trace file: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("trace path must be a .gputrace directory bundle, got file: %s", tracePath)
	}

	return nil
}
