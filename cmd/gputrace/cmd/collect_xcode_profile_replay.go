//go:build darwin

package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

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

	windowAX, err := findTargetWindow(cmd.Context(), appAX, traceFile)
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

	windowAX, err := findTargetWindow(cmd.Context(), appAX, traceFile)
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}

	traceFileName := filepath.Base(traceFile)
	if err := waitForReplayComplete(cmd.Context(), appAX, traceFileName, windowAX, collectProfileOpts.timeout); err != nil {
		return fmt.Errorf("wait failed: %w", err)
	}

	fmt.Fprint(status, Colorize("Replay completed\n", ColorGreen))
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "wait-profile",
		Target: traceFile,
	})
}
