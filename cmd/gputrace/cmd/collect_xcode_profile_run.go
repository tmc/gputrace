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

func init() {
	runCmd := &cobra.Command{
		Use:          "run <trace_file>",
		Short:        "Run full automation (open, replay, export)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE:         runCollectXcodeProfileFull,
	}
	runCmd.Flags().StringVarP(&collectProfileOutput, "output", "o", "", "Output path for the exported trace")
	collectXcodeProfileCmd.AddCommand(runCmd)
}

func runCollectXcodeProfileFull(cmd *cobra.Command, args []string) error {
	cleanupCancel := StartAutomationCancelListener(true)
	defer cleanupCancel()

	inputPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("invalid input path: %w", err)
	}

	output := collectProfileOutput
	if output == "" {
		output = defaultXcodeProfileOutputPath(inputPath)
	}
	outputPath, err := resolveXcodeProfileTraceOutputPath(output)
	if err != nil {
		return err
	}

	// Acquire lock to prevent concurrent profiling
	unlock, err := acquireProfileLock()
	if err != nil {
		return err
	}
	defer unlock()

	if err := setupMacgo(); err != nil {
		return err
	}
	if err := checkPermissions(); err != nil {
		return err
	}

	status := xcodeProfileStatusWriter()
	fmt.Fprint(status, Colorize("Collect Profile: Automating Xcode GPU trace...\n", ColorBold))
	fmt.Fprintf(status, "  Input:  %s\n", inputPath)
	fmt.Fprintf(status, "  Output: %s\n", outputPath)

	ctx, cancel := context.WithTimeout(automationContext(), collectProfileTimeout)
	defer cancel()

	// Validate trace bundle before opening in Xcode
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("trace file does not exist: %s", inputPath)
	}
	if err := validateTraceBundle(inputPath); err != nil {
		return err
	}

	// Step 1: Open File in Xcode
	fmt.Fprintln(status, "  Step 1: Opening trace in Xcode...")

	openCmd := exec.CommandContext(ctx, "open", append(xcodeOpenArgs(), inputPath)...)
	if output, err := openCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to open trace in Xcode: %w\n    output: %s", err, string(output))
	}
	time.Sleep(2 * time.Second)

	if err := CheckCancelAndReturn(); err != nil {
		return err
	}

	// Handle any startup dialogs (Reopen, etc.)
	if err := dismissStartupDialogs(); err != nil {
		verboseLog("dismissStartupDialogs: %v", err)
	}

	// Step 2: Wait for Xcode window via AX
	fmt.Fprintln(status, "  Step 2: Waiting for Xcode window...")
	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not found via AX: %w", err)
	}
	defer cfRelease(appAX)

	traceFileName := filepath.Base(inputPath)
	windowAX, err := waitForWindow(appAX, traceFileName, 30*time.Second)
	if err != nil {
		return fmt.Errorf("Xcode window not found: %w", err)
	}

	if err := CheckCancelAndReturn(); err != nil {
		return err
	}

	// Check if trace already has performance data (Show Performance button visible)
	alreadyHasPerfData := hasShowPerformance(windowAX)
	// Check if profiling is actually in progress. In Xcode's "Profile after
	// replay" flow the Replay button can disappear while profiler data is still
	// being prepared, so Stop alone is enough to mean "keep waiting" here.
	profilingInProgress := false
	if !alreadyHasPerfData {
		stopBtn := FindStopButton(windowAX)
		replayBtn := FindReplayButton(windowAX)
		if stopBtn != 0 && IsElementEnabled(stopBtn) {
			profilingInProgress = true
		} else if replayBtn != 0 && !IsElementEnabled(replayBtn) {
			profilingInProgress = true
		}
	}

	if alreadyHasPerfData {
		fmt.Fprintln(status, "  Trace already has performance data, skipping replay...")
	} else if profilingInProgress {
		// Profiling already running (e.g., from a prior attempt or --force) — just wait for it
		fmt.Fprintln(status, "  Profiling already in progress, waiting for completion...")
		if err := waitForReplayComplete(appAX, traceFileName, windowAX, collectProfileTimeout); err != nil {
			return fmt.Errorf("replay wait failed: %w", err)
		}
		fmt.Fprintln(status, "    Profiling completed")
	} else {
		// Step 3: Start replay
		fmt.Fprintln(status, "  Step 3: Starting replay...")
		if err := clickReplayButton(windowAX); err != nil {
			return fmt.Errorf("failed to start replay: %w", err)
		}

		// Step 4: Wait for replay
		fmt.Fprintln(status, "  Step 4: Waiting for replay to complete...")
		if err := waitForReplayComplete(appAX, traceFileName, windowAX, collectProfileTimeout); err != nil {
			return fmt.Errorf("replay wait failed: %w", err)
		}
		fmt.Fprintln(status, "    Replay completed")
	}

	if err := CheckCancelAndReturn(); err != nil {
		return err
	}

	// Verify performance data is actually available after replay.
	if !alreadyHasPerfData {
		if freshWindow := getPreferredTraceWindow(appAX, traceFileName); freshWindow != 0 {
			windowAX = freshWindow
		} else if freshWindow := findTraceWindowByButtons(appAX); freshWindow != 0 {
			windowAX = freshWindow
		}
		if !hasShowPerformance(windowAX) {
			return fmt.Errorf("replay completed but performance data is not available — the trace may not contain enough GPU work to profile")
		}
	}

	if freshWindow := getPreferredTraceWindow(appAX, traceFileName); freshWindow != 0 {
		windowAX = freshWindow
	} else if freshWindow := findTraceWindowByButtons(appAX); freshWindow != 0 {
		windowAX = freshWindow
	}
	if shown, err := showPerformanceBeforeExport(windowAX); err != nil {
		return fmt.Errorf("show performance before export: %w", err)
	} else if shown {
		// Xcode only enables "Embed performance data" after the Performance view
		// has been opened. Give the view time to settle before opening Export.
		time.Sleep(1 * time.Second)
	}

	// Export step
	fmt.Fprintln(status, "  Exporting trace...")
	if freshWindow := getPreferredTraceWindow(appAX, traceFileName); freshWindow != 0 {
		windowAX = freshWindow
	} else if freshWindow := findTraceWindowByButtons(appAX); freshWindow != 0 {
		windowAX = freshWindow
	}
	activateXcodeQuick()
	axAction(windowAX, "AXRaise")
	time.Sleep(300 * time.Millisecond)

	candidatePaths := exportCandidatePaths(inputPath, outputPath)
	// Remove existing destinations to avoid "file exists" dialogs and stale
	// fallback-path exports being mistaken for the result of this run.
	for _, p := range candidatePaths {
		if _, err := os.Stat(p); err == nil {
			verboseLog("removing existing output path: %s", p)
			if err := os.RemoveAll(p); err != nil {
				return fmt.Errorf("failed to remove existing output %s: %w", p, err)
			}
		}
	}

	if err := exportTrace(appAX, windowAX, outputPath); err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	verboseLog("exportTrace: searching for output in: %v", candidatePaths)

	finalPath, err := waitForExportedTrace(candidatePaths, exportWaitTimeout())
	if err != nil {
		return err
	}

	// Close the Xcode window after export completes
	// Re-fetch window reference since it may have become stale during export
	// (window title may change or become empty after profiling)
	if freshWindow := findTraceWindowByButtons(appAX); freshWindow != 0 {
		closeXcodeWindow(freshWindow)
	} else if freshWindow := getPreferredTraceWindow(appAX, traceFileName); freshWindow != 0 {
		closeXcodeWindow(freshWindow)
	} else {
		closeXcodeWindow(windowAX) // Try original reference as fallback
	}

	// Check if file was saved
	if finalPath != outputPath {
		// Copy from alternate location to expected output path
		if err := copyPath(finalPath, outputPath); err != nil {
			warning := fmt.Sprintf("file saved to %s; copy to %s failed: %v", finalPath, outputPath, err)
			fmt.Fprintf(status, Colorize("\nNote: File saved to %s (copy to %s failed: %v)\n", ColorYellow), finalPath, outputPath, err)
			return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
				Action:          "run",
				Input:           inputPath,
				Output:          finalPath,
				RequestedOutput: outputPath,
				Warning:         warning,
			})
		}
		fmt.Fprintf(status, Colorize("\nDone! Output saved to: %s (copied from %s)\n", ColorGreen), outputPath, finalPath)
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action: "run",
			Input:  inputPath,
			Output: outputPath,
			Source: finalPath,
			Copied: true,
		})
	}
	fmt.Fprintf(status, Colorize("\nDone! Output saved to: %s\n", ColorGreen), outputPath)
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "run",
		Input:  inputPath,
		Output: outputPath,
	})
}

// findTraceWindowByButtons finds an Xcode window with trace-related buttons
// (Export + Show Performance indicates a completed profiling session)
func findTraceWindowByButtons(appAX uintptr) uintptr {
	for _, child := range GetAllWindows(appAX) {
		// Look for windows with both Export and Show Performance buttons
		hasExport := findButtonBFS(child, "Export", 200) != 0
		hasShowPerf := findButtonBFS(child, "Show Performance", 200) != 0
		if hasExport && hasShowPerf {
			verboseLog("findTraceWindowByButtons: found window with Export + Show Performance")
			return child
		}
	}
	return 0
}

// closeXcodeWindow closes the specified Xcode window
// closeAllXcodeWindows closes all open Xcode windows to clear stale GPU trace sessions.
func closeAllXcodeWindows() {
	appAX, err := FindXcodeApp()
	if err != nil {
		return
	}
	defer cfRelease(appAX)

	windows := GetAllWindows(appAX)
	verboseLog("closeAllXcodeWindows: closing %d windows", len(windows))
	for _, w := range windows {
		closeXcodeWindow(w)
		time.Sleep(500 * time.Millisecond)
	}
}

func closeXcodeWindow(windowAX uintptr) {
	if windowAX == 0 {
		return
	}

	// Try AXCloseButton attribute (standard macOS window close button)
	var closeBtn uintptr
	key := mkString("AXCloseButton")
	defer cfRelease(key)
	if axCopyAttributeValue(windowAX, key, &closeBtn) == kAXErrorSuccess && closeBtn != 0 {
		verboseLog("closeXcodeWindow: clicking AXCloseButton")
		// Try AXPress action directly on the close button
		pressKey := mkString("AXPress")
		defer cfRelease(pressKey)
		if axPerformAction(closeBtn, pressKey) == kAXErrorSuccess {
			verboseLog("closeXcodeWindow: window closed successfully")
			return
		}
		verboseLog("closeXcodeWindow: AXPress failed, trying fallback")
		if err := axPressWithFallback(closeBtn); err != nil {
			verboseLog("closeXcodeWindow: fallback also failed: %v", err)
		}
		return
	}
	verboseLog("closeXcodeWindow: AXCloseButton not found")
}

func waitForWindow(appAX uintptr, traceFileName string, timeout time.Duration) (uintptr, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var windowAX uintptr
		// Try to find window by trace file name first
		if traceFileName != "" {
			// Get ALL matching windows and prefer ones with Replay button
			// (multiple windows can have same trace filename)
			// getPreferredTraceWindow includes title match, basename match,
			// and GPU-trace-UI heuristic (third pass).
			windowAX = getPreferredTraceWindow(appAX, traceFileName)
		}
		// Only fall back to GetFirstWindow when no traceFileName was given.
		// When we have a filename, falling back to an arbitrary window
		// (e.g., a source editor) causes the automation to operate on the
		// wrong window. getPreferredTraceWindow already includes a UI
		// heuristic fallback for windows with GPU trace controls.
		if windowAX == 0 && traceFileName == "" {
			windowAX = GetFirstWindow(appAX)
		}
		if windowAX != 0 {
			// Check for off-screen position and reposition if needed
			// (required for CGEvent fallback path which uses screen coordinates)
			x, y := axPosition(windowAX)
			if x < 0 || y < 0 || y > 5000 {
				verboseLog("waitForWindow: window at (%d,%d) is off-screen, repositioning to (100,100)", x, y)
				if err := setWindowPosition(windowAX, 100, 100); err != nil {
					verboseLog("waitForWindow: failed to reposition window: %v", err)
				} else {
					time.Sleep(200 * time.Millisecond)
				}
			}
			// Do not raise the window here. AXPress works without the
			// window being frontmost. If CGEvent fallback is needed later,
			// axPressWithFallbackWindow will raise on demand.
			return windowAX, nil
		}
		time.Sleep(1 * time.Second)
	}
	// Collect diagnostic info about what windows exist
	children := GetAllWindows(appAX)
	var windowInfo []string
	for _, child := range children {
		title := axString(child, "AXTitle")
		doc := axString(child, "AXDocument")
		if title != "" || doc != "" {
			windowInfo = append(windowInfo, fmt.Sprintf("title=%q doc=%q", title, doc))
		}
	}
	if len(windowInfo) > 0 {
		return 0, fmt.Errorf("could not find Xcode window for %s; found windows: %s", traceFileName, strings.Join(windowInfo, "; "))
	}
	if diagnostic := xcodeWindowVisibilityDiagnostic(appAX); diagnostic != "" {
		return 0, fmt.Errorf("could not find AX-visible Xcode window for %s (%s)", traceFileName, diagnostic)
	}
	return 0, fmt.Errorf("could not find Xcode window for %s (no Xcode windows found - check Accessibility permissions)", traceFileName)
}

// getPreferredTraceWindow finds the best matching window for a trace filename.
// When multiple windows match (e.g., document window + trace viewer), prefer the one
// with GPU trace UI elements (Replay button, profiling status).
func getPreferredTraceWindow(appAX uintptr, traceFileName string) uintptr {
	titleLower := strings.ToLower(traceFileName)
	allWindows := GetAllWindows(appAX)
	for _, child := range allWindows {
		title := axString(child, "AXTitle")
		doc := axString(child, "AXDocument")
		verboseLog("getPreferredTraceWindow: visible window: title=%q doc=%q", title, doc)
	}
	verboseLog("getPreferredTraceWindow: %d total Xcode windows, looking for %q", len(allWindows), traceFileName)

	var matchingWindows []uintptr
	for _, child := range allWindows {
		// Check AXTitle
		windowTitle := strings.ToLower(axString(child, "AXTitle"))
		if strings.Contains(windowTitle, titleLower) {
			matchingWindows = append(matchingWindows, child)
			continue
		}
		// Check AXDocument (file path)
		windowDoc := strings.ToLower(axString(child, "AXDocument"))
		if strings.Contains(windowDoc, titleLower) {
			matchingWindows = append(matchingWindows, child)
		}
	}

	// Second pass: try matching without extension (Xcode sometimes strips it)
	if len(matchingWindows) == 0 {
		baseName := strings.ToLower(strings.TrimSuffix(traceFileName, filepath.Ext(traceFileName)))
		if baseName != titleLower {
			for _, child := range allWindows {
				windowTitle := strings.ToLower(axString(child, "AXTitle"))
				if strings.Contains(windowTitle, baseName) {
					matchingWindows = append(matchingWindows, child)
					continue
				}
				windowDoc := strings.ToLower(axString(child, "AXDocument"))
				if strings.Contains(windowDoc, baseName) {
					matchingWindows = append(matchingWindows, child)
				}
			}
			if len(matchingWindows) > 0 {
				verboseLog("getPreferredTraceWindow: matched %d windows using base name %q", len(matchingWindows), baseName)
			}
		}
	}

	// Third pass: if still no match, look for any window with GPU trace UI elements.
	// Xcode may title the window differently than the filename (e.g., showing a
	// descriptive name or abbreviated path). A window with Replay/Profile/Export
	// buttons is almost certainly our trace window.
	if len(matchingWindows) == 0 {
		verboseLog("getPreferredTraceWindow: no title/doc match, scanning for windows with GPU trace UI elements")
		for _, child := range allWindows {
			title := axString(child, "AXTitle")
			// Skip windows that are clearly source editors (common extensions)
			titleLow := strings.ToLower(title)
			if isSourceEditorWindow(titleLow) {
				verboseLog("getPreferredTraceWindow: skipping source-editor window %q", title)
				continue
			}
			if hasGPUTraceUI(child) {
				verboseLog("getPreferredTraceWindow: window %q has GPU trace UI elements, accepting", title)
				matchingWindows = append(matchingWindows, child)
			}
		}
		if len(matchingWindows) > 0 {
			verboseLog("getPreferredTraceWindow: matched %d windows by GPU trace UI heuristic", len(matchingWindows))
		}
	}

	verboseLog("getPreferredTraceWindow: found %d windows matching %q", len(matchingWindows), traceFileName)

	if len(matchingWindows) == 0 {
		return 0
	}

	// If only one match, return it
	if len(matchingWindows) == 1 {
		return matchingWindows[0]
	}

	// Multiple matches - prefer windows with GPU trace UI (Replay button)
	for _, w := range matchingWindows {
		title := axString(w, "AXTitle")
		// Check for Replay button (fast shallow search)
		replayBtn := findButtonBFS(w, "Replay", 500)
		if replayBtn != 0 {
			verboseLog("getPreferredTraceWindow: selected window %q (has Replay button)", title)
			return w
		}
		// Check for Export button (indicates profiling data ready)
		exportBtn := findButtonBFS(w, "Export", 500)
		if exportBtn != 0 {
			verboseLog("getPreferredTraceWindow: selected window %q (has Export button)", title)
			return w
		}
		// Check for Show Performance button
		showPerfBtn := findButtonBFS(w, "Show Performance", 500)
		if showPerfBtn != 0 {
			verboseLog("getPreferredTraceWindow: selected window %q (has Show Performance button)", title)
			return w
		}
	}

	// No window with trace UI found - return first match
	verboseLog("getPreferredTraceWindow: no window with trace UI, using first match")
	return matchingWindows[0]
}

// isSourceEditorWindow returns true if the window title looks like a source code editor
// (e.g., "GatedDelta.swift", "main.cpp") rather than a trace document.
func isSourceEditorWindow(titleLower string) bool {
	sourceExts := []string{".swift", ".m", ".mm", ".c", ".cpp", ".h", ".hpp", ".metal", ".py", ".js", ".ts"}
	for _, ext := range sourceExts {
		if strings.HasSuffix(titleLower, ext) {
			return true
		}
	}
	return false
}

// hasGPUTraceUI checks whether a window contains GPU trace UI elements
// (Replay, Profile, Export, or Show Performance buttons).
func hasGPUTraceUI(windowAX uintptr) bool {
	for _, name := range []string{"Replay", "Profile", "Export", "Show Performance"} {
		if btn := findButtonBFS(windowAX, name, 500); btn != 0 {
			return true
		}
	}
	return false
}

// validateTraceBundle checks whether a .gputrace bundle contains enough data
// to be worth profiling. An empty capture (header-only MTSP file, ≤8 bytes)
// means the original Metal capture recorded no GPU commands.
func validateTraceBundle(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("trace bundle: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("trace bundle is not a directory: %s", path)
	}

	// Check capture file size — an 8-byte capture is just the MTSP header
	// with no GPU command data.
	capturePath := filepath.Join(path, "capture")
	capInfo, err := os.Stat(capturePath)
	if err == nil && capInfo.Size() <= 8 {
		// Also check for unsorted-capture as an alternative
		unsortedPath := filepath.Join(path, "unsorted-capture")
		if _, unsortedErr := os.Stat(unsortedPath); os.IsNotExist(unsortedErr) {
			return fmt.Errorf("trace capture is empty (capture file is %d bytes with no unsorted-capture): %s\n    This trace contains no GPU commands — the Metal capture may have recorded an empty frame", capInfo.Size(), path)
		}
	}
	if os.IsNotExist(err) {
		// No capture file at all — check for unsorted-capture or store0 (newer Xcode format)
		unsortedPath := filepath.Join(path, "unsorted-capture")
		store0Path := filepath.Join(path, "store0")
		if _, unsortedErr := os.Stat(unsortedPath); os.IsNotExist(unsortedErr) {
			if _, store0Err := os.Stat(store0Path); os.IsNotExist(store0Err) {
				return fmt.Errorf("trace bundle has no capture data (missing capture, unsorted-capture, and store0): %s", path)
			}
			// store0 exists — newer Xcode format, valid for Xcode replay
		}
	}
	return nil
}

func exportWaitTimeout() time.Duration {
	if collectProfileTimeout > 30*time.Second {
		return collectProfileTimeout
	}
	return 30 * time.Second
}

func exportCandidatePaths(inputPath, outputPath string) []string {
	outputName := filepath.Base(outputPath)
	inputDir := filepath.Dir(inputPath)
	altPath := filepath.Join(inputDir, outputName)

	candidates := []string{outputPath}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(outputPath)); err == nil {
		candidates = append(candidates, filepath.Join(resolved, outputName))
	}
	if altPath != outputPath {
		candidates = append(candidates, altPath)
	}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(altPath)); err == nil {
		candidates = append(candidates, filepath.Join(resolved, outputName))
	}
	for _, dir := range []string{os.TempDir(), "/tmp", "/private/tmp"} {
		candidates = append(candidates, filepath.Join(dir, outputName))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, "Downloads", outputName),
			filepath.Join(home, "Desktop", outputName),
		)
	}
	return uniquePaths(candidates)
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func waitForExportedTrace(candidatePaths []string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var foundWithoutProfiler []string
	for {
		for _, p := range candidatePaths {
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			if !info.IsDir() {
				continue
			}
			if findProfilerDir(p) != "" {
				return p, nil
			}
			foundWithoutProfiler = append(foundWithoutProfiler, p)
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(1 * time.Second)
	}

	if len(foundWithoutProfiler) > 0 {
		return "", fmt.Errorf("export wrote a bundle without .gpuprofiler_raw: %s; Xcode did not embed performance data", strings.Join(uniquePaths(foundWithoutProfiler), ", "))
	}
	return "", fmt.Errorf("export did not write a perfdata bundle within %s; checked: %s", timeout.Round(time.Second), strings.Join(candidatePaths, ", "))
}

func windowMatchesTraceFile(window uintptr, traceFileName string) bool {
	if traceFileName == "" {
		return true
	}
	name := strings.ToLower(traceFileName)
	title := strings.ToLower(axString(window, "AXTitle"))
	if strings.Contains(title, name) {
		return true
	}
	doc := strings.ToLower(axString(window, "AXDocument"))
	return strings.Contains(doc, name)
}

func clickReplayButton(windowAX uintptr) error {
	windowTitle := axString(windowAX, "AXTitle")
	verboseLog("clickReplayButton: window=%d title=%q", windowAX, windowTitle)

	// Do NOT raise the window upfront. AXPress works without the window
	// being frontmost, so we avoid stealing focus. The CGEvent fallback
	// path in axPressWithFallbackWindow will raise only if AXPress fails.

	// Get app reference to search all windows (Run button may be in toolbar, not document window)
	appAX, _ := FindXcodeApp()
	if appAX != 0 {
		defer cfRelease(appAX)
	}

	// Helper to search windows for a button - prioritize the target window
	findButtonInAllWindows := func(name string) uintptr {
		// First check the target window
		if btn := findButtonBFS(windowAX, name, 500); btn != 0 {
			return btn
		}
		// Fall back to other windows
		if appAX == 0 {
			return 0
		}
		windows := GetAllWindows(appAX)
		for _, w := range windows {
			if btn := findButtonBFS(w, name, 500); btn != 0 {
				return btn
			}
		}
		return 0
	}

	// For trace files, prioritize "Replay" button in the TARGET window first
	// "Capture GPU workload" is for capturing new traces from running apps
	replayBtn := findButtonBFS(windowAX, "Replay", 500)
	verboseLog("clickReplayButton: Replay button (target window)=%d enabled=%v", replayBtn, replayBtn != 0 && IsElementEnabled(replayBtn))
	if replayBtn != 0 && IsElementEnabled(replayBtn) {
		if err := axPressWithFallbackWindow(replayBtn, windowAX); err != nil {
			// Button reference may be stale after window repositioning - retry with fresh reference
			verboseLog("clickReplayButton: first attempt failed (%v), waiting and retrying", err)
			time.Sleep(500 * time.Millisecond)
			replayBtn = findButtonBFS(windowAX, "Replay", 500)
			if replayBtn != 0 && IsElementEnabled(replayBtn) {
				if err := axPressWithFallbackWindow(replayBtn, windowAX); err != nil {
					return fmt.Errorf("failed to click Replay button: %w", err)
				}
			} else {
				return fmt.Errorf("failed to click Replay button: %w (and retry failed to find button)", err)
			}
		}
		fmt.Fprintln(os.Stderr, "    Clicked Replay button successfully")
		return nil
	}

	// Try "Profile" button in target window
	profileBtn := findButtonBFS(windowAX, "Profile", 500)
	verboseLog("clickReplayButton: Profile button=%d enabled=%v", profileBtn, profileBtn != 0 && IsElementEnabled(profileBtn))
	if profileBtn != 0 && IsElementEnabled(profileBtn) {
		if err := axPressWithFallbackWindow(profileBtn, windowAX); err != nil {
			return fmt.Errorf("failed to click Profile button: %w", err)
		}
		fmt.Fprintln(os.Stderr, "    Clicked Profile button successfully")
		return nil
	}

	// Fall back to "Capture GPU workload" button (for capturing new traces)
	captureBtn := findButtonInAllWindows("Capture GPU workload")
	verboseLog("clickReplayButton: Capture GPU workload button=%d enabled=%v", captureBtn, captureBtn != 0 && IsElementEnabled(captureBtn))
	if captureBtn != 0 && IsElementEnabled(captureBtn) {
		if err := axPressWithFallbackWindow(captureBtn, windowAX); err != nil {
			return fmt.Errorf("failed to click Capture GPU workload button: %w", err)
		}
		fmt.Fprintln(os.Stderr, "    Clicked Capture GPU workload button successfully")
		return nil
	}

	// Retry with wait-for-enabled — compute-only traces may need extra time
	// for Xcode to prepare the replay infrastructure before the button enables.
	foundDisabled := replayBtn != 0 || profileBtn != 0 || captureBtn != 0
	waitTime := 5
	if foundDisabled {
		waitTime = 20 // longer wait when button exists but is disabled
		verboseLog("clickReplayButton: button found but disabled, waiting up to %ds for it to enable", waitTime)
	}
	for i := 0; i < waitTime; i++ {
		time.Sleep(1 * time.Second)
		replayBtn = findButtonBFS(windowAX, "Replay", 500)
		if replayBtn != 0 && IsElementEnabled(replayBtn) {
			if err := axPressWithFallbackWindow(replayBtn, windowAX); err != nil {
				return fmt.Errorf("failed to click Replay button: %w", err)
			}
			fmt.Fprintln(os.Stderr, "    Clicked Replay button successfully")
			return nil
		}
		captureBtn = findButtonInAllWindows("Capture GPU workload")
		if captureBtn != 0 && IsElementEnabled(captureBtn) {
			if err := axPressWithFallbackWindow(captureBtn, windowAX); err != nil {
				return fmt.Errorf("failed to click Capture GPU workload button: %w", err)
			}
			fmt.Fprintln(os.Stderr, "    Clicked Capture GPU workload button successfully")
			return nil
		}
		if i > 0 && i%5 == 0 {
			verboseLog("clickReplayButton: still waiting for button to enable (%ds)...", i)
		}
	}

	return fmt.Errorf("Replay/Capture GPU workload button not found or disabled")
}

func showPerformanceBeforeExport(windowAX uintptr) (bool, error) {
	showPerfBtn := findShowPerformanceButton(windowAX)
	if showPerfBtn == 0 {
		return false, nil
	}
	if !IsElementEnabled(showPerfBtn) {
		return false, fmt.Errorf("Show Performance button is disabled")
	}
	fmt.Fprintln(xcodeProfileStatusWriter(), "  Showing performance data...")
	if err := axPressWithFallbackWindow(showPerfBtn, windowAX); err != nil {
		time.Sleep(500 * time.Millisecond)
		showPerfBtn = findShowPerformanceButton(windowAX)
		if showPerfBtn == 0 || !IsElementEnabled(showPerfBtn) {
			return false, fmt.Errorf("Show Performance button unavailable after retry")
		}
		if err := axPressWithFallbackWindow(showPerfBtn, windowAX); err != nil {
			return false, fmt.Errorf("click Show Performance: %w", err)
		}
	}
	return true, nil
}

// targetedShowPerformanceFound is a found-only marker for hasShowPerformance.
// That traversal confirms the button is present but does not return an AX
// element handle, so callers must not pass this value to IsElementEnabled or
// AXPress.
const targetedShowPerformanceFound uintptr = 1

func isTargetedShowPerformanceFound(button uintptr) bool {
	return button == targetedShowPerformanceFound
}

func waitForReplayComplete(appAX uintptr, traceFileName string, initialWindowAX uintptr, timeout time.Duration) error {
	start := time.Now()
	currentWindow := initialWindowAX
	windowTitle := axString(currentWindow, "AXTitle")
	verboseLog("waitForReplayComplete: waiting for profiling in window %q", windowTitle)

	// Track consecutive failures to detect Xcode crash/exit
	consecutiveXcodeFailures := 0
	const maxXcodeFailures = 1

	// Helper to find a button - tries current window first, then re-fetches window if needed
	// Returns (button, xcodeRunning)
	// Note: depth of 2000 required for deep UI hierarchies (e.g., Show Performance in summary panel)
	const buttonSearchDepth = 5000

	// tryWindowForButton checks a single window for a button (or Show Performance via targeted traversal).
	tryWindowForButton := func(w uintptr, name string) uintptr {
		if w == 0 {
			return 0
		}
		if name == "Show Performance" && hasShowPerformance(w) {
			return targetedShowPerformanceFound
		}
		return findButtonBFS(w, name, buttonSearchDepth)
	}

	findButton := func(name string) (uintptr, bool) {
		// 1. Try the current window reference directly (fastest path)
		if btn := tryWindowForButton(currentWindow, name); btn != 0 {
			consecutiveXcodeFailures = 0
			return btn, true
		}
		// 2. Try re-fetching the window by title match
		if newWindow := getPreferredTraceWindow(appAX, traceFileName); newWindow != 0 && newWindow != currentWindow {
			verboseLog("waitForReplayComplete: window reference updated (old=%v, new=%v)", currentWindow, newWindow)
			currentWindow = newWindow
			if btn := tryWindowForButton(currentWindow, name); btn != 0 {
				consecutiveXcodeFailures = 0
				return btn, true
			}
		}
		// 3. Re-fetch Xcode app and search all windows (handles stale appAX and title changes)
		freshApp, err := FindXcodeApp()
		if err != nil {
			verboseLog("waitForReplayComplete: failed to re-fetch Xcode app: %v", err)
			return 0, false
		}
		consecutiveXcodeFailures = 0
		allWindows := GetAllWindows(freshApp)

		// First pass: title-matched windows. Second pass: all windows.
		for pass := range 2 {
			for _, w := range allWindows {
				if pass == 0 && !windowMatchesTraceFile(w, traceFileName) {
					continue
				}
				if btn := tryWindowForButton(w, name); btn != 0 {
					newTitle := axString(w, "AXTitle")
					verboseLog("waitForReplayComplete: found %q in window %q (pass=%d)", name, newTitle, pass)
					currentWindow = w
					return btn, true
				}
			}
		}
		return 0, true
	}

	// Wrapper that checks for Xcode being down
	findButtonOrFail := func(name string) (uintptr, error) {
		btn, xcodeRunning := findButton(name)
		if !xcodeRunning {
			consecutiveXcodeFailures++
			if consecutiveXcodeFailures >= maxXcodeFailures {
				return 0, fmt.Errorf("Xcode exited while waiting for replay completion")
			}
		}
		return btn, nil
	}

	// Re-validate the window reference before checking start state.
	// This prevents detecting stale completion indicators from a prior run
	// when running multiple traces sequentially.
	if freshWindow := getPreferredTraceWindow(appAX, traceFileName); freshWindow != 0 {
		if freshWindow != currentWindow {
			verboseLog("waitForReplayComplete: refreshed window reference before start detection")
			currentWindow = freshWindow
		}
	}

	// First, wait for replay/profiling to actually start
	// For trace replay: Replay button becomes disabled
	// For GPU capture: "Capture GPU workload" disabled OR "Stop GPU workload" enabled
	profilingStarted := false
	// Track whether we ever saw the Replay button enabled, so we can require
	// the enabled→disabled transition (not just "is disabled", which could be
	// stale state from a prior run).
	sawReplayEnabled := false
	for time.Since(start) < 30*time.Second {
		replayBtn, err := findButtonOrFail("Replay")
		if err != nil {
			return err
		}
		captureBtn, err := findButtonOrFail("Capture GPU workload")
		if err != nil {
			return err
		}
		stopBtn, err := findButtonOrFail("Stop GPU workload")
		if err != nil {
			return err
		}

		replayEnabled := replayBtn != 0 && IsElementEnabled(replayBtn)
		captureEnabled := captureBtn != 0 && IsElementEnabled(captureBtn)
		stopEnabled := stopBtn != 0 && IsElementEnabled(stopBtn)

		if replayEnabled {
			sawReplayEnabled = true
		}

		verboseLog("waitForReplayComplete: checking start state - Replay=%v(enabled=%v) Capture=%v(enabled=%v) Stop=%v(enabled=%v) sawReplayEnabled=%v",
			replayBtn != 0, replayEnabled, captureBtn != 0, captureEnabled, stopBtn != 0, stopEnabled, sawReplayEnabled)

		// Profiling started if:
		// - Replay button transitioned from enabled to disabled (requires sawReplayEnabled)
		// - OR Stop GPU workload is enabled (GPU capture running)
		// - OR Capture is disabled (GPU capture running)
		if (replayBtn != 0 && !replayEnabled && sawReplayEnabled) || stopEnabled || (captureBtn != 0 && !captureEnabled) {
			profilingStarted = true
			verboseLog("waitForReplayComplete: profiling/replay started")
			break
		}
		// If Replay is disabled but we never saw it enabled, it may be stale
		// from a prior run — keep polling to see the transition.
		if replayBtn != 0 && !replayEnabled && !sawReplayEnabled {
			verboseLog("waitForReplayComplete: Replay disabled but never saw enabled state, waiting for transition")
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !profilingStarted {
		verboseLog("waitForReplayComplete: WARNING - could not confirm profiling started")
		// Continue anyway, maybe the state changed too quickly
	}

	// Add minimum wait to ensure profiling has time to run
	// (prevents detecting completion before profiling actually happens)
	minWait := 5 * time.Second
	elapsed := time.Since(start)
	if elapsed < minWait {
		sleepTime := minWait - elapsed
		verboseLog("waitForReplayComplete: minimum wait %.1fs", sleepTime.Seconds())
		time.Sleep(sleepTime)
	}

	// Now wait for profiling to complete
	lastStatus := ""
	for time.Since(start) < timeout {
		// Check for completion indicators (only in target window):

		// 1. Show Performance button appears (most reliable - profiling complete, ready to view)
		// Use targeted traversal via hasShowPerformance (same as check-status) for reliability
		if currentWindow != 0 && hasShowPerformance(currentWindow) {
			verboseLog("waitForReplayComplete: Show Performance button found (targeted traversal) - complete")
			return nil
		}
		// Also try findButtonOrFail as fallback (searches all windows with deeper BFS)
		// findButton can return targetedShowPerformanceFound for this button.
		// That sentinel is not an AX element, so skip IsElementEnabled here.
		showPerfBtn, err := findButtonOrFail("Show Performance")
		if err != nil {
			return err
		}
		if showPerfBtn != 0 {
			if isTargetedShowPerformanceFound(showPerfBtn) {
				verboseLog("waitForReplayComplete: Show Performance button found (targeted sentinel) - complete")
			} else {
				verboseLog("waitForReplayComplete: Show Performance button found - complete")
			}
			return nil
		}

		// NOTE: Export button is NOT a reliable completion indicator - it's always
		// visible in the Summary panel even before profiling. Only Show Performance
		// or Replay button re-enabled indicates profiling is done.

		// 2. Replay button disappeared. This is not completion by itself:
		// Xcode may hide Replay while it is still preparing profiler data.
		replayBtn, err := findButtonOrFail("Replay")
		if err != nil {
			return err
		}
		replayEnabled := replayBtn != 0 && IsElementEnabled(replayBtn)
		if profilingStarted && replayBtn == 0 {
			verboseLog("waitForReplayComplete: Replay button gone, waiting for Show Performance")
		}
		if profilingStarted && replayEnabled {
			// Replay button re-enabled - wait for Show Performance to appear
			// (indicates profiler data is ready, not just that replay finished)
			time.Sleep(2 * time.Second)
			// Use targeted traversal first
			if currentWindow != 0 && hasShowPerformance(currentWindow) {
				verboseLog("waitForReplayComplete: Replay enabled, Show Performance available (targeted) - complete")
				return nil
			}
			showPerfBtn, err = findButtonOrFail("Show Performance")
			if err != nil {
				return err
			}
			if showPerfBtn != 0 {
				verboseLog("waitForReplayComplete: Replay enabled, Show Performance available - complete")
				return nil
			}
			verboseLog("waitForReplayComplete: Replay enabled but Show Performance not yet available, waiting...")
		}

		// 4. Stop GPU workload button is disabled/absent AND Capture is enabled
		captureBtn, err := findButtonOrFail("Capture GPU workload")
		if err != nil {
			return err
		}
		stopBtn, err := findButtonOrFail("Stop GPU workload")
		if err != nil {
			return err
		}
		captureEnabled := captureBtn != 0 && IsElementEnabled(captureBtn)
		stopEnabled := stopBtn != 0 && IsElementEnabled(stopBtn)

		if !stopEnabled && captureEnabled {
			// Additional check: wait for Show Performance button to appear
			// before declaring complete (indicates profiler data is ready)
			time.Sleep(2 * time.Second)
			showPerfBtn, err = findButtonOrFail("Show Performance")
			if err != nil {
				return err
			}
			if showPerfBtn != 0 {
				verboseLog("waitForReplayComplete: Stop disabled, Capture enabled, Show Performance available - complete")
				return nil
			}
			// Show Performance not available yet, continue waiting for profiler data
			verboseLog("waitForReplayComplete: Stop disabled, Capture enabled but Show Performance not yet available")
		}

		elapsed := time.Since(start).Seconds()
		status := "running"
		if replayBtn != 0 && !replayEnabled {
			status = "replay running"
		} else if stopBtn != 0 && stopEnabled {
			status = "capture running"
		} else if replayEnabled {
			status = "replay done, waiting for data"
		}

		// Only print if status changed
		if status != lastStatus {
			fmt.Fprintf(os.Stderr, "    Profiling... (%.0fs, status: %s)\n", elapsed, status)
			lastStatus = status
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for replay completion")
}

// findSaveButtonInSheet finds the save/export action button specifically within
// a sheet element, not the toolbar Export button.
func findSaveButtonInSheet() uintptr {
	appAX, err := FindXcodeApp()
	if err != nil {
		return 0
	}
	defer cfRelease(appAX)

	for _, w := range GetAllWindows(appAX) {
		// Find AXSheet elements within the window — the save dialog is a sheet
		sheet := findElement(w, func(el uintptr) bool {
			return axString(el, "AXRole") == "AXSheet"
		})
		if sheet != 0 {
			for _, name := range []string{"Save", "Export"} {
				btn := findButtonBFS(sheet, name, 2000)
				if btn != 0 {
					verboseLog("findSaveButtonInSheet: found %s in sheet (enabled=%v)", name, IsElementEnabled(btn))
					return btn
				}
			}
		}
		// Fallback for save panels that do not expose a distinct AXSheet
		// subtree. Do not accept a window-level Export button here: that is
		// usually the toolbar button that opened the sheet.
		btn := findButtonBFS(w, "Save", 3000)
		if btn != 0 {
			verboseLog("findSaveButtonInSheet: found Save in window (enabled=%v)", IsElementEnabled(btn))
			return btn
		}
	}
	return 0
}

// dumpExportSheetState prints the buttons, checkboxes, and text fields in the export dialog.
func dumpExportSheetState(windowAX uintptr) {
	appAX, _ := FindXcodeApp()
	if appAX != 0 {
		defer cfRelease(appAX)
	}

	// Search all windows for export-related elements
	searchWindows := []uintptr{windowAX}
	if appAX != 0 {
		for _, w := range GetAllWindows(appAX) {
			if w != windowAX {
				searchWindows = append(searchWindows, w)
			}
		}
	}

	for wi, w := range searchWindows {
		title := axString(w, "AXTitle")
		fmt.Fprintf(os.Stderr, "    [DEBUG] Window %d: %q\n", wi, title)

		findElement(w, func(el uintptr) bool {
			role := axString(el, "AXRole")
			switch role {
			case "AXButton":
				t := axString(el, "AXTitle")
				if t != "" {
					enabled := IsElementEnabled(el)
					fmt.Fprintf(os.Stderr, "    [DEBUG]   Button: %q enabled=%v\n", t, enabled)
				}
			case "AXCheckBox":
				t := axString(el, "AXTitle")
				desc := axString(el, "AXDescription")
				if t == "" {
					t = desc
				}
				checked := IsCheckboxChecked(el)
				enabled := IsElementEnabled(el)
				fmt.Fprintf(os.Stderr, "    [DEBUG]   Checkbox: %q checked=%v enabled=%v\n", t, checked, enabled)
			case "AXTextField":
				ident := axString(el, "AXIdentifier")
				val := axString(el, "AXValue")
				fmt.Fprintf(os.Stderr, "    [DEBUG]   TextField: id=%q value=%q\n", ident, val)
			}
			return false // keep searching
		})
	}
}

func exportTrace(appAX, windowAX uintptr, outputPath string) error {
	status := xcodeProfileStatusWriter()
	activateXcodeQuick()
	axAction(windowAX, "AXRaise")
	time.Sleep(300 * time.Millisecond)

	// Try clicking Export button in Summary panel first
	exportBtn := FindExportButton(windowAX)
	if exportBtn != 0 {
		fmt.Fprintln(status, "    Found Export button in Summary panel")
		if err := axPressWithFallback(exportBtn); err != nil {
			fmt.Fprintf(status, "    Warning: Failed to click Export button: %v\n", err)
		}
	} else {
		// Fall back to menu
		if freshApp, err := FindXcodeApp(); err == nil && freshApp != 0 {
			appAX = freshApp
		}
		if collectProfileDebug || collectProfileVerbose {
			if err := debugCheckExportMenu(appAX); err != nil {
				fmt.Fprintf(os.Stderr, "    Debug: Export menu check failed: %v\n", err)
			}
		}
		if err := ClickMenuItem(appAX, []string{"File", "Export..."}); err != nil {
			return fmt.Errorf("failed to click Export menu: %w", err)
		}
	}

	fmt.Fprintln(status, "    Waiting for export sheet...")
	time.Sleep(500 * time.Millisecond) // Give dialog time to appear

	// Refresh app reference since the UI might have changed
	freshApp, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not accessible after clicking Export: %w", err)
	}
	defer cfRelease(freshApp)

	// Search ALL windows for Save button (sheet might be in any window)
	var saveWindow uintptr
	sheetFound := false
	for i := 0; i < 30; i++ {
		windows := GetAllWindows(freshApp)
		for _, w := range windows {
			// Detect export sheet by looking for Save button or AXSheet role
			sheet := findElement(w, func(el uintptr) bool {
				return axString(el, "AXRole") == "AXSheet"
			})
			if sheet != 0 {
				sheetFound = true
				saveWindow = w
				break
			}
		}
		if sheetFound {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !sheetFound {
		if collectProfileDebug {
			windows := GetAllWindows(freshApp)
			fmt.Fprintf(os.Stderr, "    Debug: Found %d windows\n", len(windows))
			for i, w := range windows {
				title := axString(w, "AXTitle")
				fmt.Fprintf(os.Stderr, "    Debug: Window %d: %q\n", i+1, title)
			}
		}
		return fmt.Errorf("export sheet did not appear (Save button not found)")
	}

	fmt.Fprintln(status, "    Export sheet detected")
	// Use the window containing the Save button for subsequent operations
	windowAX = saveWindow

	// Helper to find element across all windows (using freshApp from above)
	findInAllWindows := func(finder func(uintptr) uintptr) uintptr {
		windows := GetAllWindows(freshApp)
		for _, w := range windows {
			if el := finder(w); el != 0 {
				return el
			}
		}
		return 0
	}

	// Check "Embed performance data" checkbox if available and enabled
	embedCheckbox := findCheckboxByName(windowAX, "Embed performance data")
	if embedCheckbox != 0 {
		if IsElementEnabled(embedCheckbox) {
			if !IsCheckboxChecked(embedCheckbox) {
				fmt.Fprintln(status, "    Enabling 'Embed performance data'")
				axPressWithFallback(embedCheckbox)
				time.Sleep(300 * time.Millisecond)
				if !IsCheckboxChecked(embedCheckbox) {
					return fmt.Errorf("failed to enable Embed performance data checkbox")
				}
			} else {
				fmt.Fprintln(status, "    'Embed performance data' already enabled")
			}
		} else {
			return fmt.Errorf("Embed performance data checkbox is disabled; profiler data is not available in Xcode")
		}
	}

	outputDir := filepath.Dir(outputPath)
	outputName := filepath.Base(outputPath)
	if outputDir != "" && outputDir != "." {
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	}

	if collectProfileDebug {
		DebugTextFields(windowAX)
	}

	// Try to navigate to the output directory using the path popup button
	navigatedToDir := false
	remainingPath := ""
	if outputDir != "" && outputDir != "." {
		fmt.Fprintf(status, "    Navigating to directory: %s\n", outputDir)
		// First try via path popup (more reliable than Cmd+Shift+G)
		var popupErr error
		remainingPath, popupErr = navigateViaPathPopup(windowAX, outputDir)
		if popupErr != nil {
			verboseLog("exportTrace: path popup navigation failed: %v", popupErr)
			// Fall back to Cmd+Shift+G
			if err := NavigateToFolderInSaveDialog(windowAX, outputDir); err != nil {
				verboseLog("exportTrace: Cmd+Shift+G navigation failed: %v", err)
				fmt.Fprintln(status, "    Note: Directory navigation failed, using default location")
			} else {
				navigatedToDir = true
			}
		} else {
			navigatedToDir = true
			if remainingPath != "" {
				verboseLog("exportTrace: navigated partially, remaining path: %s", remainingPath)
			} else {
				verboseLog("exportTrace: navigated to directory successfully")
			}
		}
	}

	// If there's a remaining path (couldn't fully navigate), try Cmd+Shift+G as final fallback
	// Note: putting "/" in filename creates ":"-named files due to macOS HFS legacy behavior
	if remainingPath != "" {
		fmt.Fprintf(status, "    Partial navigation, using Cmd+Shift+G to navigate to: %s\n", outputDir)
		// Ensure directory exists before trying to navigate
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			verboseLog("exportTrace: failed to create output directory: %v", err)
		}
		// Try Cmd+Shift+G to navigate to full path
		if err := NavigateToFolderInSaveDialog(windowAX, outputDir); err != nil {
			verboseLog("exportTrace: Cmd+Shift+G fallback also failed: %v", err)
			fmt.Fprintf(status, "    Warning: Could not navigate to %s, file may save to wrong location\n", outputDir)
		} else {
			navigatedToDir = true
			remainingPath = ""
			fmt.Fprintln(status, "    Successfully navigated via Cmd+Shift+G")
		}
	}

	// Set just the filename (never include path prefix - macOS converts "/" to ":")
	fmt.Fprintf(status, "    Setting filename: %s\n", outputName)
	saveNameField := findInAllWindows(FindSaveAsTextField)
	if saveNameField != 0 {
		if err := axSetValue(saveNameField, outputName); err != nil {
			fmt.Fprintf(status, "    Warning: SetValue failed: %v (using default filename)\n", err)
		} else if collectProfileDebug {
			fmt.Fprintln(os.Stderr, "    [DEBUG] Set filename via AX (saveAsNameTextField)")
		}
	} else {
		fmt.Fprintln(status, "    Warning: saveAsNameTextField not found (using default filename)")
	}
	time.Sleep(300 * time.Millisecond)

	// Debug: dump the export sheet state so we can see exactly what's happening
	if collectProfileDebug {
		fmt.Fprintln(os.Stderr, "    [DEBUG] Export sheet state after navigation:")
		dumpExportSheetState(windowAX)
	}

	// Find the action button. Depending on Xcode/macOS, the sheet may use
	// either "Save" or "Export".
	saveBtn := findSaveButtonInSheet()

	if saveBtn == 0 {
		return fmt.Errorf("Save button not found in export sheet")
	}

	if !IsElementEnabled(saveBtn) {
		// Save disabled — usually means a child sheet (e.g. Go to Folder) is still
		// open. Try dismissing any lingering sheets and re-querying.
		verboseLog("exportTrace: Save disabled, checking for lingering child sheets")
		dismissGoToFolderSheet(windowAX)
		time.Sleep(300 * time.Millisecond)
		saveBtn = findSaveButtonInSheet()
		if saveBtn == 0 || !IsElementEnabled(saveBtn) {
			if collectProfileDebug {
				fmt.Fprintln(os.Stderr, "    [DEBUG] Export sheet state (Save disabled):")
				dumpExportSheetState(windowAX)
			}
			return fmt.Errorf("Save button disabled in export sheet")
		}
	}

	// Click Save button
	fmt.Fprintln(status, "    Saving...")
	if err := axPressWithFallback(saveBtn); err != nil {
		return fmt.Errorf("failed to click Save: %w", err)
	}
	replaced, err := pressReplaceIfPresent(windowAX, 5*time.Second)
	if err != nil {
		return fmt.Errorf("confirm replace: %w", err)
	}
	if replaced {
		fmt.Fprintln(status, "    Confirmed replacement")
	}

	// Wait for export to complete — GPU trace exports can be large and slow
	fmt.Fprintln(status, "    Waiting for export to write...")
	time.Sleep(5 * time.Second)

	// Check if file was saved to expected location
	if _, err := os.Stat(outputPath); err == nil {
		return nil // File found at expected path
	}

	// If we didn't navigate, the file is likely in an alternate location
	// The caller will check alternate locations and copy if needed
	if !navigatedToDir {
		verboseLog("exportTrace: file not at %s, may be in Xcode's default export location", outputPath)
	}

	// Return nil to let caller handle searching alternate locations
	// Caller is responsible for finding and copying the file
	return nil
}

func pressReplaceIfPresent(windowAX uintptr, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		replaceBtn := findButtonBFS(windowAX, "Replace", 3000)
		if replaceBtn != 0 {
			if !IsElementEnabled(replaceBtn) {
				return false, fmt.Errorf("Replace button disabled")
			}
			if err := axPressWithFallback(replaceBtn); err != nil {
				return false, err
			}
			time.Sleep(500 * time.Millisecond)
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// navigateViaPathPopup tries to navigate to a folder using the path popup button
// in the save dialog. This is the breadcrumb-style path shown at the top of the dialog.
// Returns the remaining path components that couldn't be navigated (to include in filename),
// and an error if the popup couldn't be opened at all.
func navigateViaPathPopup(windowAX uintptr, targetPath string) (remainingPath string, err error) {
	// Look for a path control or popup button that shows the current location
	// Common identifiers: "Where:" popup, path bar, location dropdown
	pathPopup := findElement(windowAX, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXPopUpButton" {
			// Check if this is the "Where:" location popup
			desc := axString(el, "AXDescription")
			title := axString(el, "AXTitle")
			if strings.Contains(strings.ToLower(desc), "where") ||
				strings.Contains(strings.ToLower(title), "where") ||
				strings.Contains(strings.ToLower(desc), "location") {
				return true
			}
		}
		return false
	})

	if pathPopup == 0 {
		// Try to find any popup button that might be the location selector
		pathPopup = findElement(windowAX, func(el uintptr) bool {
			role := axString(el, "AXRole")
			subrole := axString(el, "AXSubrole")
			return role == "AXPopUpButton" && subrole == "AXPathButton"
		})
	}

	if pathPopup == 0 {
		return "", fmt.Errorf("path popup not found in save dialog")
	}

	// Check if we're already in the target directory
	currentValue := axString(pathPopup, "AXValue")
	targetBase := filepath.Base(targetPath)
	if currentValue != "" && (strings.Contains(currentValue, targetBase) || currentValue == targetBase) {
		verboseLog("navigateViaPathPopup: already in target directory %q (current=%q)", targetBase, currentValue)
		return "", nil // Already in the right place
	}

	// Click to open the popup menu
	if err := axPressWithFallback(pathPopup); err != nil {
		return "", fmt.Errorf("failed to click path popup: %w", err)
	}
	time.Sleep(500 * time.Millisecond) // Give menu time to appear

	// Find the popup menu - check direct children of the popup button first
	// macOS popup buttons expose their menu as a direct child when open
	var popupMenu uintptr
	directChildren := axChildren(pathPopup)
	for _, child := range directChildren {
		role := axString(child, "AXRole")
		if role == "AXMenu" {
			popupMenu = child
			verboseLog("navigateViaPathPopup: found menu as direct child of popup button")
			break
		}
	}

	// If not found as direct child, check the window for a floating menu
	// (Save dialogs sometimes create floating menus)
	if popupMenu == 0 {
		// Get the window containing the popup button
		windowChildren := axChildren(windowAX)
		for _, child := range windowChildren {
			role := axString(child, "AXRole")
			if role == "AXMenu" {
				popupMenu = child
				verboseLog("navigateViaPathPopup: found floating menu in window")
				break
			}
		}
	}

	verboseLog("navigateViaPathPopup: popupMenu=%v (directChildren=%d)", popupMenu, len(directChildren))

	// Collect all menu items with their element refs for later use
	type menuItemRef struct {
		title string
		el    uintptr
	}
	var allMenuItems []menuItemRef
	if popupMenu != 0 {
		findElement(popupMenu, func(el uintptr) bool {
			role := axString(el, "AXRole")
			if role == "AXMenuItem" {
				title := axString(el, "AXTitle")
				if title != "" {
					allMenuItems = append(allMenuItems, menuItemRef{title: title, el: el})
				}
			}
			return false // continue searching
		})
	}
	verboseLog("navigateViaPathPopup: found %d menu items", len(allMenuItems))

	// Helper to find menu items by title
	findMenuItem := func(title string) uintptr {
		for _, item := range allMenuItems {
			if item.title == title || strings.HasSuffix(item.title, "/"+title) {
				return item.el
			}
		}
		return 0
	}

	// First, try to find the target folder directly in the popup menu
	targetItem := findMenuItem(targetBase)
	if targetItem != 0 {
		verboseLog("navigateViaPathPopup: found target folder %q in popup menu", targetBase)
		if err := axAction(targetItem, "AXPress"); err != nil {
			return "", fmt.Errorf("failed to click target folder: %w", err)
		}
		time.Sleep(300 * time.Millisecond)
		return "", nil // Successfully navigated to exact target
	}

	// Try clicking parent directory components from the path
	// For /tmp/export_test, try "tmp" which navigates to /tmp
	// Then we'll navigate through the file browser for remaining components
	pathParts := strings.Split(strings.Trim(targetPath, "/"), "/")
	for i := len(pathParts) - 1; i >= 0; i-- {
		part := pathParts[i]
		if part == "" {
			continue
		}
		partItem := findMenuItem(part)
		if partItem != 0 {
			verboseLog("navigateViaPathPopup: clicking path component %q to navigate", part)
			if err := axPressWithFallback(partItem); err != nil {
				verboseLog("navigateViaPathPopup: failed to click %q: %v", part, err)
				continue
			}
			time.Sleep(500 * time.Millisecond)

			// Calculate remaining path components to navigate
			// We clicked pathParts[i], so we need to navigate pathParts[i+1:]
			remainingParts := pathParts[i+1:]
			if len(remainingParts) > 0 {
				verboseLog("navigateViaPathPopup: remaining path components: %v", remainingParts)
				// Try file browser navigation first (may work for some dialogs)
				if err := navigateThroughFileBrowser(windowAX, remainingParts); err != nil {
					verboseLog("navigateViaPathPopup: file browser navigation failed: %v", err)
					// Return the remaining path - caller will try Cmd+Shift+G as fallback
					remaining := strings.Join(remainingParts, "/")
					verboseLog("navigateViaPathPopup: returning remaining path %q for caller fallback", remaining)
					return remaining, nil
				}
				// File browser navigation succeeded
				return "", nil
			}
			return "", nil // We clicked something and no remaining parts
		}
	}

	// Look for "Other..." option which opens the folder browser
	otherItem := findMenuItem("Other...")
	if otherItem == 0 {
		otherItem = findMenuItem("Other…") // Unicode ellipsis
	}

	if otherItem != 0 {
		// Click "Other..." to open folder browser
		if err := axPressWithFallback(otherItem); err != nil {
			return "", fmt.Errorf("failed to click Other: %w", err)
		}
		time.Sleep(500 * time.Millisecond)

		// Now we should have a folder browser - try to navigate using Go to Folder
		err := NavigateToFolderInSaveDialog(windowAX, targetPath)
		return "", err
	}

	// Debug: list available menu items
	var menuItemTitles []string
	for _, item := range allMenuItems {
		menuItemTitles = append(menuItemTitles, item.title)
	}
	verboseLog("navigateViaPathPopup: popup menu items (%d): %v", len(menuItemTitles), menuItemTitles)

	// Close popup if we didn't find what we need
	sendEscape()
	return "", fmt.Errorf("could not find 'Other...' option in path popup (available: %v)", menuItemTitles)
}

// navigateThroughFileBrowser navigates through folders in a save dialog's file browser.
// It finds folders by name in the file list (table/outline view) and double-clicks to open them.
func navigateThroughFileBrowser(windowAX uintptr, folders []string) error {
	for _, folder := range folders {
		verboseLog("navigateThroughFileBrowser: looking for folder %q", folder)

		// Find the folder in the file browser
		// Save dialogs typically use AXTable, AXOutline, or AXBrowser for the file list
		folderElement := findFolderInFileBrowser(windowAX, folder)
		if folderElement == 0 {
			return fmt.Errorf("folder %q not found in file browser", folder)
		}

		// Double-click to open the folder
		verboseLog("navigateThroughFileBrowser: double-clicking folder %q", folder)
		if err := doubleClickElement(folderElement); err != nil {
			return fmt.Errorf("failed to double-click folder %q: %w", folder, err)
		}
		time.Sleep(500 * time.Millisecond) // Wait for navigation to complete
	}
	return nil
}

// findFolderInFileBrowser searches for a folder element in a save dialog's file browser.
func findFolderInFileBrowser(windowAX uintptr, folderName string) uintptr {
	// Search for elements with the folder name
	// Common patterns: AXCell, AXStaticText, AXRow, AXOutlineRow
	return findElement(windowAX, func(el uintptr) bool {
		role := axString(el, "AXRole")

		// Check if this is a file browser cell/row
		if role != "AXCell" && role != "AXStaticText" && role != "AXRow" && role != "AXOutlineRow" {
			return false
		}

		// Check various attributes for the folder name
		title := axString(el, "AXTitle")
		value := axString(el, "AXValue")
		desc := axString(el, "AXDescription")

		if title == folderName || value == folderName || desc == folderName {
			return true
		}

		// Also check text content of children (for cells containing text elements)
		if role == "AXCell" || role == "AXRow" || role == "AXOutlineRow" {
			children := axChildren(el)
			for _, child := range children {
				childRole := axString(child, "AXRole")
				if childRole == "AXStaticText" || childRole == "AXTextField" {
					childVal := axString(child, "AXValue")
					childTitle := axString(child, "AXTitle")
					if childVal == folderName || childTitle == folderName {
						return true
					}
				}
			}
		}

		return false
	})
}

// dismissStartupDialogs handles common Xcode startup dialogs like "Reopen windows".
// It checks for and dismisses these dialogs to allow automation to proceed.
func dismissStartupDialogs() error {
	appAX, err := FindXcodeApp()
	if err != nil {
		return err
	}
	defer cfRelease(appAX)

	// Check for startup dialogs up to 3 times with delays
	for attempt := 0; attempt < 3; attempt++ {
		windows := GetAllWindows(appAX)
		for _, w := range windows {
			// Look for "Reopen" or "Don't Reopen" buttons (startup dialog)
			reopenBtn := findButtonBFS(w, "Reopen", 200)
			dontReopenBtn := findButtonBFS(w, "Don't Reopen", 200)

			if reopenBtn != 0 || dontReopenBtn != 0 {
				// Found startup dialog - click "Reopen" to restore previous windows
				if reopenBtn != 0 {
					verboseLog("dismissStartupDialogs: clicking Reopen button")
					fmt.Fprintln(xcodeProfileStatusWriter(), "    Dismissing Xcode startup dialog...")
					if err := axPressWithFallback(reopenBtn); err != nil {
						verboseLog("dismissStartupDialogs: Reopen click failed: %v", err)
					}
					time.Sleep(2 * time.Second)
					return nil
				}
				// Fall back to "Don't Reopen" if Reopen not found
				if dontReopenBtn != 0 {
					verboseLog("dismissStartupDialogs: clicking Don't Reopen button")
					fmt.Fprintln(xcodeProfileStatusWriter(), "    Dismissing Xcode startup dialog...")
					if err := axPressWithFallback(dontReopenBtn); err != nil {
						verboseLog("dismissStartupDialogs: Don't Reopen click failed: %v", err)
					}
					time.Sleep(2 * time.Second)
					return nil
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	// No startup dialog found - that's fine
	verboseLog("dismissStartupDialogs: no startup dialog detected")
	return nil
}

// copyPath copies a file or directory from src to dst.
// For directories (like .gputrace bundles), it uses cp -R.
func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		// Use cp -R for directories (like .gputrace bundles)
		cmd := exec.Command("cp", "-R", src, dst)
		return cmd.Run()
	}

	// For regular files, use copyFile
	return copyFile(src, dst)
}
