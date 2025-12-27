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

Key Features:
- Parse and analyze Metal GPU capture data
- Recovery of kernel names from Metal Library (MTLB) sidecar files
- Generate pprof profiles for deep performance analysis
- Inspect raw trace records and shader performance metrics

Command Groups:

Basic Information:
  stats            - Display comprehensive trace statistics
  api-calls        - Display API call sequences
  dump             - Dump raw API call sequences
  encoders         - List compute command encoders
  buffers          - List buffers and their properties

Shader Analysis:
  shaders          - Shader performance metrics (Xcode Instruments format)

Command Buffer Analysis:
  command-buffers  - Detailed command buffer analysis

Examples:
  # Show trace statistics
  gputrace stats trace.gputrace

  # List API calls
  gputrace api-calls trace.gputrace

  # Show encoders
  gputrace encoders trace.gputrace

  # Shader performance analysis
  gputrace shaders trace.gputrace

For more information about a specific command:
  gputrace [command] --help`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.CompletionOptions.DisableDefaultCmd = true
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
