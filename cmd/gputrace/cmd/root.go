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
	Long: `gputrace provides tools for analyzing and converting GPU trace files.

The gputrace tool includes subcommands for various GPU trace analysis tasks:
  - gputrace2pprof: Convert .gputrace files to pprof format
  - stats: Display GPU trace statistics

Examples:
  gputrace gputrace2pprof trace.gputrace
  gputrace stats trace.gputrace`,
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
