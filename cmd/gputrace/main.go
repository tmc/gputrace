// Command gputrace provides tools for analyzing and converting GPU trace files.
//
// gputrace includes subcommands for various GPU trace analysis tasks:
//   - gputrace2pprof: Convert .gputrace files to pprof format
//   - stats: Display GPU trace statistics
//
// Usage:
//
//	gputrace [command] [flags]
//
// Examples:
//
//	gputrace gputrace2pprof trace.gputrace
//	gputrace stats trace.gputrace
//
// Use "gputrace [command] --help" for more information about a command.
package main

import (
	"os"

	"github.com/tmc/gputrace/cmd/gputrace/cmd"
	"github.com/tmc/macgo"
)

func main() {
	// Ensure macgo cleanup happens on exit for fast parent process termination
	defer macgo.Cleanup()

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
