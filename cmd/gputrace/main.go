// Command gputrace provides tools for analyzing and converting GPU trace files.
//
// Usage:
//
//	gputrace [command] [flags]
//
// Use "gputrace [command] --help" for more information about a command.
package main

import (
	"os"

	"github.com/tmc/gputrace/cmd/gputrace/cmd"
)

func main() {
	// Ensure macgo cleanup happens on exit for fast parent process termination
	defer cleanupMacgo()

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
