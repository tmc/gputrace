// Package cmd implements the gputrace CLI commands.
package cmd

import (
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
  stats            - Comprehensive trace statistics
  api-calls        - API call sequences
  dump             - Raw API call dump

Kernel & Shader Analysis:
  shaders          - Shader performance metrics
  kernels          - Kernel functions and pipeline mappings
  shader-source    - Source-level performance attribution

Timing & Profiling:
  timing           - Timing metrics export
  profiler         - GPU profiler data extraction
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
  insights         - Actionable performance insights

Capture & Automation:
  capture          - Capture GPU trace from a command
  xcode-profile    - Xcode GPU profiler automation
  xcode-bindings   - Inspect private Xcode GTShaderProfiler bindings
  xcode-parity     - Audit Xcode metric parity for a trace

Utilities:
  mtlb             - Metal Library Binary inspection
  clear-buffers    - Zero out buffers to reduce trace size
  version          - Print gputrace build version

For more information about a specific command:
  gputrace [command] --help`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
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
