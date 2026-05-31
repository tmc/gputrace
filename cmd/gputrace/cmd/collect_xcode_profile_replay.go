//go:build darwin

package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

var replayTraceFile string

func init() {
	runProfileCmd := &cobra.Command{
		Use:     "run-profile [trace_file]",
		Aliases: []string{"run-replay"},
		Short:   "Start profiling in Xcode",
		Long: `Clicks the Profile button if available, otherwise falls back to Replay button.
The Profile button starts profiling directly without needing additional checkboxes.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runReplay,
	}

	waitReplayCmd := &cobra.Command{
		Use:     "wait-profile [trace_file]",
		Aliases: []string{"wait-replay"},
		Short:   "Wait for profiling to complete",
		Long:    `Polls Xcode until profiling completes (Show Performance button appears or Replay re-enabled).`,
		Args:    cobra.MaximumNArgs(1),
		RunE:    runWaitReplay,
	}

	collectXcodeProfileCmd.AddCommand(runProfileCmd)
	collectXcodeProfileCmd.AddCommand(waitReplayCmd)
}

func runReplay(cmd *cobra.Command, args []string) error {
	traceFile := ""
	if len(args) > 0 {
		traceFile = args[0]
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	status := xcodeProfileStatusWriter()
	fmt.Fprintln(status, "Starting replay...")

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not found: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, traceFile)
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}

	if err := clickReplayButton(windowAX); err != nil {
		return fmt.Errorf("replay failed: %w", err)
	}

	fmt.Fprint(status, Colorize("Replay started\n", ColorGreen))
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "run-profile",
		Target: traceFile,
	})
}

func runWaitReplay(cmd *cobra.Command, args []string) error {
	traceFile := ""
	if len(args) > 0 {
		traceFile = args[0]
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	status := xcodeProfileStatusWriter()
	fmt.Fprintln(status, "Waiting for replay to complete...")

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not found: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, traceFile)
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}

	traceFileName := filepath.Base(traceFile)
	if err := waitForReplayComplete(appAX, traceFileName, windowAX, collectProfileTimeout); err != nil {
		return fmt.Errorf("wait failed: %w", err)
	}

	fmt.Fprint(status, Colorize("Replay completed\n", ColorGreen))
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "wait-profile",
		Target: traceFile,
	})
}
