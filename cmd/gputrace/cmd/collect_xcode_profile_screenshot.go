//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

type screenshotOptions struct {
	output   string
	noPrompt bool
}

func runScreenshot(cmd *cobra.Command, args []string, opts *screenshotOptions) error {
	// Handle --no-prompt: trigger TCC entry without prompting
	if opts.noPrompt {
		return triggerScreenRecordingTCC()
	}

	// Get trace file from positional arg
	traceFile := ""
	if len(args) > 0 {
		traceFile = args[0]
	}

	outputPath, err := resolveScreenshotOutputPath(opts.output, time.Now())
	if err != nil {
		return err
	}

	// Get Xcode window info using AX
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	// Find the target window (prefer trace windows)
	windowAX, err := findTargetWindow(appAX, traceFile)
	if err != nil {
		return err
	}

	// Get window title for feedback
	title := axString(windowAX, "AXTitle")
	if title == "" {
		title = "Xcode"
	}

	status := xcodeProfileStatusWriter()
	fmt.Fprintf(status, "Capturing screenshot of: %s\n", title)

	// Capture using CoreGraphics (captures by window ID, doesn't need window in front)
	if err := CaptureWindowToFile(windowAX, outputPath); err != nil {
		return fmt.Errorf("capture failed: %w", err)
	}

	// Verify file was created
	if _, err := os.Stat(outputPath); err != nil {
		return fmt.Errorf("screenshot file not created")
	}

	fmt.Fprintf(status, "Screenshot saved to: %s\n", outputPath)
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "screenshot",
		Target: traceFile,
		Output: outputPath,
	})
}

func resolveScreenshotOutputPath(output string, now time.Time) (string, error) {
	if output == "" {
		output = fmt.Sprintf("/tmp/xcode-screenshot-%s.png", now.Format("20060102-150405"))
	}
	if commandOutputPathIsStdout(output) {
		return "", fmt.Errorf("screenshot output must be a file path, not stdout")
	}
	outputPath, err := filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("invalid output path: %w", err)
	}
	return outputPath, nil
}

// triggerScreenRecordingTCC calls CGDisplayCreateImage to create a TCC
// database entry for Screen Recording permission without prompting the user.
func triggerScreenRecordingTCC() error {
	// Don't call setupMacgo() - we just need CoreGraphics, not Accessibility

	// First check current status
	hasPermission := cgPreflightScreenCaptureAccess()

	// CGDisplayCreateImage triggers TCC registration for screen recording.
	// This should create the TCC entry. The image will be null if permission denied.
	displayID := cgMainDisplayID()
	image := cgDisplayCreateImage(displayID)
	if image != 0 {
		cgImageRelease(image)
		hasPermission = true
	}

	if hasPermission {
		fmt.Fprintln(xcodeProfileStatusWriter(), "Screen Recording permission: granted")
	} else {
		fmt.Fprintln(xcodeProfileStatusWriter(), "Screen Recording permission: not granted (TCC entry triggered)")
	}
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "screenshot-no-prompt",
	})
}
