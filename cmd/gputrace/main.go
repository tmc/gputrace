// Command gputrace provides tools for analyzing and converting GPU trace files.
//
// Usage:
//
//	gputrace [command] [flags]
//
// Use "gputrace [command] --help" for more information about a command.
package main

import (
	"fmt"
	"os"

	"github.com/tmc/gputrace/cmd/gputrace/cmd"
)

func main() {
	// Ensure macgo cleanup happens on exit for fast parent process termination
	defer cleanupMacgo()

	if err := cmd.Execute(); err != nil {
		if !cmd.ErrorAlreadyReported(err) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(cmd.ExitCode(err))
	}
}
