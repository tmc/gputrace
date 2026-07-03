//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	collectXcodeProfileCmd.AddCommand(newXcodeExportMemoryCommand(&xcodeExportMemoryOptions{}))
}

type xcodeExportMemoryOptions struct {
	force bool
}

func newXcodeExportMemoryCommand(opts *xcodeExportMemoryOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "xcode-export-memory [trace_file]",
		Short: "Export memory report from Xcode's Performance view",
		Long: `Triggers Xcode's Export Memory Report dialog and accepts the default save location.

This command:
1. Finds the Xcode window for the specified trace (or first window)
2. Clicks Editor > Export Memory Report... menu via AX
3. Clicks Save to accept the default filename/location

The file is saved to wherever Xcode's save dialog defaults to.

Use --force to automatically replace existing files.

Example:
  gputrace xp xcode-export-memory
  gputrace xp xcode-export-memory MyTrace.gputrace
  gputrace xp xcode-export-memory --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runXcodeExportMemory(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.force, "force", "f", opts.force, "Replace existing file if it exists")
	return cmd
}

func runXcodeExportMemory(cmd *cobra.Command, args []string, opts *xcodeExportMemoryOptions) error {
	status := xcodeProfileStatusWriter()
	traceFile := ""
	if len(args) > 0 {
		traceFile = args[0]
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	// Save cursor position and restore when done
	origCursorX, origCursorY := getCursorPosition()
	defer func() {
		if origCursorX != 0 || origCursorY != 0 {
			time.Sleep(100 * time.Millisecond)
			moveCursor(origCursorX, origCursorY)
		}
	}()

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	// Find trace window and navigate to Memory view
	fmt.Fprintln(status, "Finding trace window and navigating to Memory view...")
	windowAX, err := findTraceWindowFast(appAX, traceFile)
	if err != nil {
		return fmt.Errorf("could not find trace window: %w", err)
	}

	// Raise the window
	axAction(windowAX, "AXRaise")
	time.Sleep(100 * time.Millisecond)

	// Use targeted search: find editor area first, then search within it
	editorArea := findGroupByTitle(windowAX, "editor area", 100)
	if editorArea == 0 {
		editorArea = windowAX // Fall back to window if editor area not found
	}

	// First try to click "Show Performance" if we're in Summary view
	showPerfBtn := findButtonBFS(editorArea, "Show Performance", 2000)
	if showPerfBtn != 0 {
		fmt.Fprintln(status, "Clicking Show Performance...")
		if err := axAction(showPerfBtn, "AXPress"); err != nil {
			verboseLog("Failed to click Show Performance: %v", err)
		} else {
			time.Sleep(500 * time.Millisecond)
		}
	}

	// Try to click Memory tab (search with reasonable limit)
	memoryBtn := findButtonBFS(editorArea, "Memory", 3000)
	if memoryBtn != 0 {
		fmt.Fprintln(status, "Clicking Memory tab...")
		if err := axAction(memoryBtn, "AXPress"); err != nil {
			verboseLog("Failed to click Memory: %v", err)
		} else {
			time.Sleep(300 * time.Millisecond)
		}
	} else {
		verboseLog("Memory button not found, trying menu anyway...")
	}

	// Activate Xcode and focus the trace window
	activateXcodeQuick()
	time.Sleep(100 * time.Millisecond)
	// Also perform AXRaise again after activation to ensure this window is focused
	axAction(windowAX, "AXRaise")
	time.Sleep(200 * time.Millisecond)

	// Click Editor > Export Memory Report menu item via AX
	fmt.Fprintln(status, "Opening Editor > Export Memory Report...")

	menuNames := []string{
		"Export Memory Report…", // Unicode ellipsis
		"Export Memory Report...",
		"Export Memory Report",
	}

	var menuErr error
	for _, menuName := range menuNames {
		if collectProfileDebug {
			fmt.Fprintf(os.Stderr, "Trying menu: Editor > %s\n", menuName)
		}
		if err := ClickMenuItem(appAX, []string{"Editor", menuName}); err == nil {
			menuErr = nil
			break
		} else {
			menuErr = err
		}
	}

	if menuErr != nil {
		return fmt.Errorf("failed to open export dialog: %w\n\nHint: Make sure you're in the Memory view in Xcode's Performance section.\nNavigate to: GPU trace window → Performance → Memory tab", menuErr)
	}

	// Wait for save dialog to appear
	time.Sleep(500 * time.Millisecond)

	// Find and click Save button with retries (AX references can go stale)
	var clickErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}

		windows := GetAllWindows(appAX)
		for _, w := range windows {
			// Try Save button (export sheet is shallow)
			if btn := findButtonBFS(w, "Save", 500); btn != 0 {
				fmt.Fprintln(status, "Clicking Save...")
				if err := axAction(btn, "AXPress"); err != nil {
					clickErr = err
					verboseLog("Save click attempt %d failed: %v", attempt+1, err)
					continue
				}
				clickErr = nil
				goto saveClicked
			}
			// Try Export button (export sheet is shallow)
			if btn := findButtonBFS(w, "Export", 500); btn != 0 {
				fmt.Fprintln(status, "Clicking Export...")
				if err := axAction(btn, "AXPress"); err != nil {
					clickErr = err
					verboseLog("Export click attempt %d failed: %v", attempt+1, err)
					continue
				}
				clickErr = nil
				goto saveClicked
			}
		}
	}

	if clickErr != nil {
		return fmt.Errorf("failed to click Save after retries: %w", clickErr)
	}
	return fmt.Errorf("Save button not found in dialog")

saveClicked:

	// Wait for potential Replace dialog
	time.Sleep(300 * time.Millisecond)

	// Check if a Replace dialog appeared (file already exists)
	replaceWindows := GetAllWindows(appAX)
	for _, w := range replaceWindows {
		if btn := findButtonBFS(w, "Replace", 200); btn != 0 {
			if opts.force {
				fmt.Fprintln(status, "File exists, clicking Replace...")
				if err := axAction(btn, "AXPress"); err != nil {
					return fmt.Errorf("failed to click Replace: %w", err)
				}
			} else {
				// Cancel the operation
				if cancelBtn := findButtonBFS(w, "Cancel", 200); cancelBtn != 0 {
					axAction(cancelBtn, "AXPress")
				}
				return fmt.Errorf("file already exists - use --force to replace")
			}
			break
		}
	}

	time.Sleep(500 * time.Millisecond)
	fmt.Fprintln(status, "Export complete")
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "xcode-export-memory",
		Target: traceFile,
	})
}
