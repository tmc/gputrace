//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/tracebundle"
)

type openTraceOptions struct {
	foreground bool
}

func runOpenTrace(cmd *cobra.Command, args []string, opts *openTraceOptions) error {
	inputPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("invalid input path: %w", err)
	}

	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("trace file does not exist: %s", inputPath)
	}
	if err := requireXcodeOpenableTrace(inputPath); err != nil {
		return err
	}

	status := xcodeProfileStatusWriter()
	fmt.Fprintf(status, "Opening trace in Xcode: %s\n", inputPath)

	// Use -g to open in background by default (doesn't steal focus)
	openArgs := xcodeOpenArgs()
	if !opts.foreground {
		openArgs = append(openArgs, "-g")
	}
	openArgs = append(openArgs, inputPath)

	openCmd := exec.Command("open", openArgs...)
	if output, err := openCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to open trace in Xcode: %w\n  output: %s", err, string(output))
	}

	fmt.Fprintln(status, "Waiting for Xcode window...")

	// Wait for Xcode using AX polling (doesn't steal focus).
	deadline := time.Now().Add(30 * time.Second)
	var appAX uintptr
	var axErr error
	for time.Now().Before(deadline) {
		appAX, axErr = FindXcodeApp()
		if axErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if axErr != nil {
		return fmt.Errorf("Xcode not accessible: %w", axErr)
	}
	defer cfRelease(appAX)

	// Handle startup dialogs (Reopen, etc.) before binding the requested trace
	// window; a modal dialog can hide the document's AX attributes.
	if err := dismissStartupDialogs(); err != nil {
		verboseLog("dismissStartupDialogs: %v", err)
	}

	windowAX, err := waitForWindow(cmd.Context(), appAX, inputPath, 30*time.Second)
	if err != nil {
		return fmt.Errorf("find requested trace window: %w", err)
	}
	selection := selectionForWindow(inputPath, windowAX)
	if err := requireBoundSelection(selection); err != nil {
		return fmt.Errorf("Xcode opened a GPU trace window, but %w", err)
	}

	// Ensure the selected window is on-screen (it may have been restored to a
	// disconnected monitor).
	x, y := axPosition(windowAX)
	_, h := axSize(windowAX)
	if y < 0 || y > 2000 || x < -500 {
		verboseLog("Window at (%d,%d) appears off-screen, repositioning", x, y)
		setWindowPosition(windowAX, 100, 100)
		time.Sleep(200 * time.Millisecond)
	}
	if h < 100 {
		verboseLog("Window height %d too small, may be minimized", h)
	}

	// Ensure the Debug navigator is shown using AX menu click
	if err := ClickMenuItem(appAX, []string{"View", "Navigators", "Debug"}); err != nil {
		if collectProfileOpts.debug {
			fmt.Fprintf(os.Stderr, "Warning: could not show Debug navigator via menu: %v\n", err)
		}
	}

	fmt.Fprint(status, Colorize("Xcode opened the requested trace window.\n", ColorGreen))
	if selection.Document != "" {
		fmt.Fprintf(status, "  Selected document: %s\n", selection.Document)
	}
	if selection.Title != "" {
		fmt.Fprintf(status, "  Selected window: %s\n", selection.Title)
	}
	fmt.Fprintf(status, "  Evidence: %s\n", selection.Evidence)
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action:           "open",
		Input:            inputPath,
		RequestedTrace:   inputPath,
		SelectedTitle:    selection.Title,
		SelectedDocument: selection.Document,
		Phase:            "trace window ready",
		Evidence:         selection.Evidence,
		TargetBound:      boolPointer(selection.Bound),
	})
}

func requireXcodeOpenableTrace(path string) error {
	payload, err := tracebundle.InspectPayload(path)
	if err != nil {
		return fmt.Errorf("inspect trace before opening in Xcode: %w", err)
	}
	if payload.Class == tracebundle.PayloadProfilerOnly {
		return fmt.Errorf("cannot open %s in Xcode: profiler-only .gpuprofiler_raw data has no capture or index; use gputrace profiler/timing, or rerun profile-replay without --profiler-only", path)
	}
	return nil
}

func xcodeOpenArgs() []string {
	if app := os.Getenv("GPUTRACE_XCODE_APP"); app != "" {
		return []string{"-a", app}
	}
	return []string{"-a", "Xcode"}
}
