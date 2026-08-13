// Package cmd implements the gputrace CLI commands.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "gputrace",
	Short: "Tools for analyzing and converting GPU trace files",
	Long: `gputrace provides tools for analyzing and converting GPU trace files (.gputrace bundles).

Command Groups:

Trace Overview:
  summary          - One-screen structure, timing, and evidence report
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
	// Cancel the command context on interrupt so commands that launch external
	// processes can tear them down. Without this, Go's default SIGINT handling
	// ends the process before any Go code runs, and profile-replay leaves the
	// MTLReplayer it started behind: LaunchServices, not gputrace, is that
	// process's parent, so nothing else reaps it.
	//
	// Unregistering on the first signal is what keeps a second interrupt able
	// to kill gputrace outright. NotifyContext on its own leaves the handler
	// installed after it fires, so every later interrupt is swallowed too, and
	// a command that does not watch its context becomes unkillable by Ctrl+C --
	// worse than the leak this exists to fix. Verified both ways with a
	// sleeping test binary.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	return rootCmd.ExecuteContext(ctx)
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
