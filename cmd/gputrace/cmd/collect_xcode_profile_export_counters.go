//go:build darwin

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type xcodeExportCountersOptions struct {
	force bool
}

func runXcodeExportCounters(cmd *cobra.Command, args []string, opts *xcodeExportCountersOptions) error {
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

	// Find trace window and navigate to Counters view
	fmt.Fprintln(status, "Finding trace window and navigating to Counters view...")
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
		verboseLog("editor area not found, searching whole window")
		editorArea = windowAX // Fall back to window if editor area not found
	} else {
		verboseLog("Found editor area, searching within it")
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
	} else {
		verboseLog("Show Performance button not found (may already be in Performance view)")
	}

	// Try to click Counters tab (search with reasonable limit)
	countersBtn := findButtonBFS(editorArea, "Counters", 3000)
	if countersBtn != 0 {
		fmt.Fprintln(status, "Clicking Counters tab...")
		if err := axAction(countersBtn, "AXPress"); err != nil {
			verboseLog("Failed to click Counters: %v", err)
		} else {
			time.Sleep(300 * time.Millisecond)
		}
	} else {
		verboseLog("Counters button not found, trying menu anyway...")
	}

	// Activate Xcode and focus the trace window
	activateXcodeQuick(cmd.Context())
	time.Sleep(100 * time.Millisecond)
	// Also perform AXRaise again after activation to ensure this window is focused
	axAction(windowAX, "AXRaise")
	time.Sleep(200 * time.Millisecond)

	// Click Editor > Export GPU Counters menu item via AX
	fmt.Fprintln(status, "Opening Editor > Export GPU Counters...")

	menuNames := []string{
		"Export GPU Counters…", // Unicode ellipsis
		"Export GPU Counters...",
		"Export GPU Counters",
	}

	var menuErr error
	for _, menuName := range menuNames {
		if collectProfileOpts.debug {
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
		return fmt.Errorf("failed to open export dialog: %w\n\nHint: Make sure you're in the Counters view in Xcode's Performance section.\nNavigate to: GPU trace window → Performance → Counters tab", menuErr)
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
		Action: "xcode-export-counters",
		Target: traceFile,
	})
}

// activateXcodeQuick activates Xcode using osascript with a timeout.
// Respects --background flag and does nothing if set.
func activateXcodeQuick(parent context.Context) {
	if collectProfileOpts.background {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", `tell application "Xcode" to activate`)
	_ = cmd.Run() // Ignore errors - best effort
}

// findTraceWindowFast finds a GPU trace window using quick heuristics.
// It checks for .gputrace in title/doc first, then looks for trace-specific buttons.
func findTraceWindowFast(appAX uintptr, traceFile string) (uintptr, error) {
	windows := GetAllWindows(appAX)
	if len(windows) == 0 {
		return 0, fmt.Errorf("no Xcode windows found")
	}

	// If trace file specified, look for it by name
	if traceFile != "" {
		baseName := filepath.Base(traceFile)
		for _, w := range windows {
			title := axString(w, "AXTitle")
			doc := axString(w, "AXDocument")
			if strings.Contains(title, baseName) || strings.Contains(doc, baseName) {
				return w, nil
			}
		}
	}

	// Look for .gputrace in title or document
	for _, w := range windows {
		title := axString(w, "AXTitle")
		doc := axString(w, "AXDocument")
		if strings.HasSuffix(title, ".gputrace") || strings.Contains(doc, ".gputrace") {
			return w, nil
		}
	}

	// Look for windows with trace-specific buttons (quick search)
	for _, w := range windows {
		// Check for Counters, Timeline, or Encoders buttons (Performance view indicators)
		if findButtonBFS(w, "Counters", 500) != 0 ||
			findButtonBFS(w, "Timeline", 500) != 0 ||
			findButtonBFS(w, "Encoders", 500) != 0 {
			return w, nil
		}
		// Check for Show Performance button (Summary view indicator)
		if findButtonBFS(w, "Show Performance", 500) != 0 {
			return w, nil
		}
		// Check for Replay button (trace loaded indicator)
		if findButtonBFS(w, "Replay", 500) != 0 {
			return w, nil
		}
	}

	// Fall back to first window with empty title (trace windows often have no title)
	for _, w := range windows {
		title := axString(w, "AXTitle")
		if title == "" {
			return w, nil
		}
	}

	return 0, fmt.Errorf("no GPU trace window found - open a .gputrace file first")
}
