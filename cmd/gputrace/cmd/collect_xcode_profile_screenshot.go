package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

var screenshotOutput string
var screenshotNoPrompt bool

func init() {
	screenshotCmd := &cobra.Command{
		Use:    "screenshot [trace_file]",
		Short:  "Capture a screenshot of the Xcode window",
		Hidden: true,
		Long: `Captures a screenshot of the current Xcode GPU trace window.

Uses CoreGraphics APIs to capture the specific window by ID, so the window
does not need to be in front or visible on screen.

If no output path is specified, saves to /tmp/xcode-screenshot-<timestamp>.png

Use --no-prompt to trigger a TCC database entry for Screen Recording permission
without prompting the user. This is useful for pre-registering the app so permission
can be granted later via System Preferences or MDM.

Examples:
  gputrace xp screenshot
  gputrace xp screenshot MyTrace.gputrace
  gputrace xp screenshot MyTrace.gputrace -o ~/Desktop/trace-view.png
  gputrace xp screenshot --no-prompt
`,
		Args: cobra.MaximumNArgs(1),
		RunE: runScreenshot,
	}
	screenshotCmd.Flags().StringVarP(&screenshotOutput, "output", "o", "", "Output path for screenshot")
	screenshotCmd.Flags().BoolVar(&screenshotNoPrompt, "no-prompt", false, "Trigger TCC entry without prompting")
	collectXcodeProfileCmd.AddCommand(screenshotCmd)
}

func runScreenshot(cmd *cobra.Command, args []string) error {
	// Handle --no-prompt: trigger TCC entry without prompting
	if screenshotNoPrompt {
		return triggerScreenRecordingTCC()
	}

	// Get trace file from positional arg
	traceFile := ""
	if len(args) > 0 {
		traceFile = args[0]
	}

	// Determine output path from -o flag
	outputPath := screenshotOutput
	if outputPath == "" {
		outputPath = fmt.Sprintf("/tmp/xcode-screenshot-%s.png", time.Now().Format("20060102-150405"))
	}

	// Make absolute path
	var err error
	outputPath, err = filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("invalid output path: %w", err)
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

	fmt.Printf("Capturing screenshot of: %s\n", title)

	// Capture using CoreGraphics (captures by window ID, doesn't need window in front)
	if err := CaptureWindowToFile(windowAX, outputPath); err != nil {
		return fmt.Errorf("capture failed: %w", err)
	}

	// Verify file was created
	if _, err := os.Stat(outputPath); err != nil {
		return fmt.Errorf("screenshot file not created")
	}

	fmt.Printf("Screenshot saved to: %s\n", outputPath)
	return nil
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
		fmt.Println("Screen Recording permission: granted")
	} else {
		fmt.Println("Screen Recording permission: not granted (TCC entry triggered)")
	}
	return nil
}
