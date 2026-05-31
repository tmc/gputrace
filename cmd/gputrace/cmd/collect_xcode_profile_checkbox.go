package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var ensureCheckedTrace string

func rejectUnsupportedXcodeProfileJSON(command string) error {
	if !collectProfileJSON {
		return nil
	}
	return fmt.Errorf("%s does not support --json", command)
}

func unsupportedXcodeProfileJSONArgs(command string, validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := rejectUnsupportedXcodeProfileJSON(command); err != nil {
			return err
		}
		return validate(cmd, args)
	}
}

func init() {
	ensureCheckedCmd := &cobra.Command{
		Use:    "ensure-checked <checkbox_title>",
		Short:  "Ensure a checkbox is checked",
		Hidden: true,
		Long: `Finds a checkbox by title in an Xcode window and ensures it is checked.

Example:
  gputrace collect-xcode-profile ensure-checked "Profile after replay"
  gputrace collect-xcode-profile ensure-checked "Profile after replay" --trace my.gputrace`,
		Args: unsupportedXcodeProfileJSONArgs("ensure-checked", cobra.ExactArgs(1)),
		RunE: runEnsureChecked,
	}
	ensureCheckedCmd.Flags().StringVar(&ensureCheckedTrace, "trace", "", "Target window by trace filename")
	collectXcodeProfileCmd.AddCommand(ensureCheckedCmd)

	toggleCheckboxCmd := &cobra.Command{
		Use:    "toggle-checkbox <checkbox_title>",
		Short:  "Toggle a checkbox",
		Hidden: true,
		Args:   unsupportedXcodeProfileJSONArgs("toggle-checkbox", cobra.ExactArgs(1)),
		RunE:   runToggleCheckbox,
	}
	toggleCheckboxCmd.Flags().StringVar(&ensureCheckedTrace, "trace", "", "Target window by trace filename")
	collectXcodeProfileCmd.AddCommand(toggleCheckboxCmd)
}

func runEnsureChecked(cmd *cobra.Command, args []string) error {
	if err := rejectUnsupportedXcodeProfileJSON("ensure-checked"); err != nil {
		return err
	}

	title := args[0]

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, ensureCheckedTrace)
	if err != nil {
		return err
	}

	// Find checkbox by title
	checkbox := findCheckboxByTitle(windowAX, title)
	if checkbox == 0 {
		return fmt.Errorf("checkbox %q not found", title)
	}

	// Check if already checked
	if IsCheckboxChecked(checkbox) {
		fmt.Printf("Checkbox %q is already checked\n", title)
		return nil
	}

	// Click to check it
	if err := axPressWithFallback(checkbox); err != nil {
		return fmt.Errorf("failed to click checkbox: %w", err)
	}

	// Verify it's now checked
	time.Sleep(200 * time.Millisecond)
	if IsCheckboxChecked(checkbox) {
		fmt.Printf(Colorize("Checkbox %q is now checked\n", ColorGreen), title)
	} else {
		fmt.Print(Colorize("Warning: checkbox may not have been checked\n", ColorYellow))
	}

	return nil
}

func runToggleCheckbox(cmd *cobra.Command, args []string) error {
	if err := rejectUnsupportedXcodeProfileJSON("toggle-checkbox"); err != nil {
		return err
	}

	title := args[0]

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, ensureCheckedTrace)
	if err != nil {
		return err
	}

	checkbox := findCheckboxByTitle(windowAX, title)
	if checkbox == 0 {
		return fmt.Errorf("checkbox %q not found", title)
	}

	wasBefore := IsCheckboxChecked(checkbox)
	fmt.Printf("Checkbox %q was: %v\n", title, wasBefore)

	if err := axPressWithFallback(checkbox); err != nil {
		return fmt.Errorf("failed to click checkbox: %w", err)
	}

	time.Sleep(200 * time.Millisecond)
	isAfter := IsCheckboxChecked(checkbox)
	fmt.Printf("Checkbox %q now: %v\n", title, isAfter)
	return nil
}

// findTargetWindow finds the appropriate Xcode window based on trace filename.
// If traceFile is empty, it looks for the first .gputrace window.
func findTargetWindow(appAX uintptr, traceFile string) (uintptr, error) {
	if traceFile != "" {
		// Extract just the filename for matching
		baseName := filepath.Base(traceFile)
		windowAX := GetWindowByTitle(appAX, baseName)
		if windowAX == 0 {
			if diagnostic := xcodeWindowVisibilityDiagnostic(appAX); diagnostic != "" {
				return 0, fmt.Errorf("no AX-visible Xcode window found for trace %q (%s)", baseName, diagnostic)
			}
			return 0, fmt.Errorf("no Xcode window found for trace %q", baseName)
		}
		return windowAX, nil
	}

	// Try to find first trace window (*.gputrace)
	windowAX := findFirstTraceWindow(appAX)
	if windowAX != 0 {
		return windowAX, nil
	}

	// Fall back to waiting for any window
	windowAX, err := waitForWindow(appAX, "", 10*time.Second)
	if err != nil {
		return 0, err
	}
	return windowAX, nil
}

// findFirstTraceWindow finds the first window with .gputrace in its title or document,
// or a window that has GPU trace UI elements (Replay, Show Performance, etc).
func findFirstTraceWindow(appAX uintptr) uintptr {
	windows := GetAllWindows(appAX)
	verboseLog("findFirstTraceWindow: found %d windows", len(windows))
	for i, w := range windows {
		title := axString(w, "AXTitle")
		doc := axString(w, "AXDocument")
		verboseLog("  Window %d: title=%q doc=%q", i+1, title, doc)
		// Match by file extension
		if strings.HasSuffix(title, ".gputrace") || strings.Contains(doc, ".gputrace") {
			verboseLog("  -> matched by .gputrace extension")
			return w
		}
	}
	// Try to find by GPU trace UI elements (Replay, Show Performance)
	verboseLog("findFirstTraceWindow: checking for GPU trace UI elements")
	for i, w := range windows {
		verboseLog("  Checking window %d for trace UI...", i+1)
		if isGPUTraceWindow(w) {
			verboseLog("  -> Window %d is GPU trace window", i+1)
			return w
		}
	}
	verboseLog("findFirstTraceWindow: no trace window found")
	return 0
}

// isGPUTraceWindow checks if a window appears to be a GPU trace viewer
// by looking for characteristic buttons or profiling status.
func isGPUTraceWindow(w uintptr) bool {
	// Fast check first: look for GPU trace UI landmarks (shallow traversal)
	if isGPUTraceWindowFast(w) {
		verboseLog("    isGPUTraceWindow: fast check passed")
		return true
	}

	// Full check: get profiling status (slower, traverses up to 2000 nodes)
	status := getProfilingStatus(w)
	verboseLog("    isGPUTraceWindow: status=%q", status)
	if status == "complete" || status == "running" || status == "replay-ready" {
		return true
	}
	// Fall back to button search
	showPerfBtn := findShowPerformanceButton(w)
	replayBtn := FindReplayButton(w)
	verboseLog("    isGPUTraceWindow: showPerfBtn=%v replayBtn=%v", showPerfBtn != 0, replayBtn != 0)
	return replayBtn != 0 || showPerfBtn != 0
}

// isGPUTraceWindowFast does a quick check for GPU trace window landmarks.
// Uses shallow traversal (~300 nodes max) to avoid 7+ second delays.
func isGPUTraceWindowFast(w uintptr) bool {
	// Look for "editor area" group (GPU trace windows have this)
	editorArea := findGroupByTitle(w, "editor area", 100)
	if editorArea == 0 {
		return false
	}

	// Look for "Summary" group inside editor area (trace viewer has this)
	summary := findGroupByTitle(editorArea, "Summary", 200)
	if summary != 0 {
		return true
	}

	// Also check for Export button (another strong indicator)
	exportBtn := findButtonBFS(editorArea, "Export", 200)
	return exportBtn != 0
}

// findCheckboxByTitle finds a checkbox element by its title (case-insensitive partial match).
func findCheckboxByTitle(window uintptr, title string) uintptr {
	titleLower := strings.ToLower(title)
	return findElement(window, func(el uintptr) bool {
		if axString(el, "AXRole") != "AXCheckBox" {
			return false
		}
		cbTitle := strings.ToLower(axString(el, "AXTitle"))
		cbDesc := strings.ToLower(axString(el, "AXDescription"))
		return strings.Contains(cbTitle, titleLower) || strings.Contains(cbDesc, titleLower)
	})
}
