//go:build darwin

package cmd

import (
	"fmt"

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
	fmt.Fprintln(status, "Waiting for GPU replay and performance profiling...")

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not found: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(cmd.Context(), appAX, traceFile)
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}
	selection := selectionForWindow(traceFile, windowAX)
	if err := requireBoundSelection(selection); err != nil {
		return err
	}
	fmt.Fprintln(status, "  Phase: performance profiling running or pending")
	if selection.Document != "" {
		fmt.Fprintf(status, "  Selected document: %s\n", selection.Document)
	}
	if selection.Title != "" {
		fmt.Fprintf(status, "  Selected window: %s\n", selection.Title)
	}
	fmt.Fprintf(status, "  Evidence: %s\n", selection.Evidence)

	if err := waitForReplayComplete(cmd.Context(), appAX, traceFile, windowAX, collectProfileOpts.timeout); err != nil {
		return fmt.Errorf("wait for performance profiling: %w", err)
	}

	fmt.Fprint(status, Colorize("Performance data became available after GPU replay; export identity is not yet verified.\n", ColorGreen))
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action:           "wait-profile",
		Target:           traceFile,
		RequestedTrace:   traceFile,
		SelectedTitle:    selection.Title,
		SelectedDocument: selection.Document,
		Phase:            "performance data available",
		Evidence:         "Xcode exposed a completion-ready performance control for the bound trace window; export identity is not yet verified",
		TargetBound:      boolPointer(selection.Bound),
	})
}
