//go:build darwin

package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gputraceTrace "github.com/tmc/gputrace/internal/trace"
	"github.com/tmc/gputrace/internal/tracebundle"
)

var xcodeProfileAutomationStartHook = func() {}

func runCollectXcodeProfileFull(cmd *cobra.Command, args []string) error {
	inputPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("invalid input path: %w", err)
	}
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("trace file does not exist: %s", inputPath)
	}
	payload, err := tracebundle.InspectPayload(inputPath)
	if err != nil {
		return err
	}

	status := xcodeProfileStatusWriter()
	if payload.HasProfilerStream {
		profilerDir := findProfilerDir(inputPath)
		if profilerDir == "" && filepath.Ext(inputPath) == ".gpuprofiler_raw" {
			profilerDir = inputPath
		}
		if _, err := readExportTraceSignature(inputPath, profilerDir); err != nil {
			return fmt.Errorf("verify embedded performance data: %w", err)
		}
		fmt.Fprintln(status, "Performance data already embedded; verified non-empty streamData.")
		fmt.Fprintf(status, "Using existing trace: %s\n", inputPath)
		writeXcodePayloadStatus(status, payload)
		output := xcodeProfileActionOutput{
			Action: "run",
			Input:  inputPath,
			Output: inputPath,
			Source: inputPath,
			Reused: true,
		}
		applyXcodePayload(&output, payload)
		return writeXcodeProfileActionOutput(output)
	}
	if err := validateTraceBundle(inputPath); err != nil {
		return err
	}

	xcodeProfileAutomationStartHook()
	automationCtx, cleanupCancel := StartAutomationCancelListener(cmd.Context(), true)
	defer cleanupCancel()
	ctx, cancel := context.WithTimeout(automationCtx, collectProfileOpts.timeout)
	defer cancel()

	output := collectProfileOpts.output
	if output == "" {
		output = defaultXcodeProfileOutputPath(inputPath)
	}
	outputPath, err := resolveXcodeProfileTraceOutputPath(output)
	if err != nil {
		return err
	}

	// Acquire lock to prevent concurrent profiling
	unlock, err := acquireProfileLock(ctx)
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

	crashReportDir := diagnosticReportDirectory()
	crashBaseline, err := snapshotXcodeCrashReports(crashReportDir)
	if err != nil {
		return fmt.Errorf("snapshot Xcode crash reports: %w", err)
	}
	requestedXcode := requestedXcodeAppPath()
	crashScope := newXcodeCrashScope(requestedXcode, time.Now())
	// A normal "open" request is delivered to the sole existing instance.
	// Observe it before opening so a crash during document loading is still
	// attributable even if LaunchServices immediately relaunches Xcode.
	if existing := xcodeProcessesForApp(requestedXcode); len(existing) == 1 {
		crashScope.observe(existing[0])
	}
	crashContext, stopCrashMonitor := startXcodeCrashMonitor(ctx, crashReportDir, crashBaseline, crashScope)
	defer stopCrashMonitor()
	ctx = crashContext

	fmt.Fprint(status, Colorize("Collect Profile: Automating Xcode GPU trace...\n", ColorBold))
	fmt.Fprintf(status, "  Input:  %s\n", inputPath)
	fmt.Fprintf(status, "  Output: %s\n", outputPath)

	// Step 1: Open File in Xcode
	fmt.Fprintln(status, "  Step 1: Opening trace in Xcode...")

	openCmd := exec.CommandContext(ctx, "open", append(xcodeOpenArgs(), inputPath)...)
	if output, err := openCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to open trace in Xcode: %w\n    output: %s", err, string(output))
	}
	if err := waitForAutomation(ctx, 2*time.Second); err != nil {
		return err
	}

	if err := checkAutomationCanceled(ctx); err != nil {
		return err
	}

	// Handle any startup dialogs (Reopen, etc.)
	if err := dismissStartupDialogs(); err != nil {
		verboseLog("dismissStartupDialogs: %v", err)
	}

	// Step 2: Wait for Xcode window via AX
	fmt.Fprintln(status, "  Step 2: Waiting for Xcode window...")
	appAX, xcodeIdentity, err := findSelectedXcodeApp(ctx, requestedXcode)
	if err != nil {
		return fmt.Errorf("selected Xcode app not found via AX: %w", err)
	}
	defer cfRelease(appAX)
	crashScope.bind(xcodeIdentity)
	fmt.Fprintf(status, "    Xcode process: PID %d, app %s, bundle %s\n",
		xcodeIdentity.PID, xcodeIdentity.AppPath, xcodeIdentity.BundleID)

	windowAX, err := waitForWindow(ctx, appAX, inputPath, 30*time.Second)
	if err != nil {
		crashScope.refreshProcesses()
		if crashScope.crashSuspected() {
			if crashErr := waitForXcodeCrashReport(ctx, xcodeCrashReportGrace); crashErr != nil {
				return crashErr
			}
		}
		return fmt.Errorf("Xcode window not found: %w", err)
	}
	traceGeometryKey := recoveryGeometryKeyForElement(windowAX, xcodeIdentity.PID)

	if err := checkAutomationCanceled(ctx); err != nil {
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
		if err := waitForReplayComplete(ctx, appAX, inputPath, windowAX, collectProfileOpts.timeout); err != nil {
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
		if err := waitForReplayComplete(ctx, appAX, inputPath, windowAX, collectProfileOpts.timeout); err != nil {
			return fmt.Errorf("replay wait failed: %w", err)
		}
		fmt.Fprintln(status, "    Replay completed")
	}

	if err := checkAutomationCanceled(ctx); err != nil {
		return err
	}

	// Verify performance data is actually available after replay.
	if !alreadyHasPerfData {
		freshWindow, err := waitForBoundTraceWindowAfterReplay(
			ctx, appAX, xcodeIdentity, inputPath, traceGeometryKey, true, false, 10*time.Second,
		)
		if err != nil {
			return fmt.Errorf("reacquire completed trace window: %w", err)
		}
		windowAX = freshWindow
		if !hasShowPerformance(windowAX) {
			return fmt.Errorf("replay completed but performance data is not available — the trace may not contain enough GPU work to profile")
		}
	}

	freshWindow, err := waitForBoundTraceWindowAfterReplay(
		ctx, appAX, xcodeIdentity, inputPath, traceGeometryKey, true, false, 10*time.Second,
	)
	if err != nil {
		return fmt.Errorf("reacquire trace window before Show Performance: %w", err)
	}
	windowAX = freshWindow
	if shown, err := showPerformanceBeforeExport(windowAX); err != nil {
		return fmt.Errorf("show performance before export: %w", err)
	} else if shown {
		// Xcode only enables "Embed performance data" after the Performance view
		// has been opened. Give the view time to settle before opening Export.
		if err := waitForAutomation(ctx, time.Second); err != nil {
			return err
		}
	}

	// Export step
	fmt.Fprintln(status, "  Exporting trace...")
	freshWindow, err = waitForBoundTraceWindowAfterReplay(
		ctx, appAX, xcodeIdentity, inputPath, traceGeometryKey, false, true, 15*time.Second,
	)
	if err != nil {
		return fmt.Errorf("reacquire trace window after Show Performance: %w", err)
	}
	windowAX = freshWindow
	transitionRecovery := standaloneExportRecovery{
		Enabled:    true,
		Finalize:   true,
		SourcePath: inputPath,
		Identity:   xcodeIdentity,
	}
	recovery := recoveryWindows(appAX)
	performanceWindow, err := transitionedRecoveryPerformanceTarget(
		recovery, transitionRecovery, traceGeometryKey,
	)
	if err != nil {
		for i, candidate := range recovery {
			verboseLog("post-replay window[%d]: pid=%d geometry=%q title=%q document=%q performance=%t summary=%t sheet=%t stop=%d enabled=%t show=%d enabled=%t",
				i, candidate.PID, standaloneRecoveryGeometryKey(candidate), candidate.Title, candidate.Document,
				candidate.PerformanceView, candidate.SummaryView, candidate.SheetOpen,
				candidate.StopCount, candidate.StopEnabled, candidate.ShowCount, candidate.ShowEnabled)
		}
		return fmt.Errorf("verify post-replay Performance state: %w", err)
	}
	if performanceWindow.StopCount > 1 {
		return fmt.Errorf("verify post-replay Performance state: multiple Stop GPU workload controls")
	}
	if performanceWindow.StopCount == 1 && performanceWindow.StopEnabled {
		windowAX, err = finalizeRecoveredWorkload(
			ctx, appAX, windowAX, transitionRecovery, 2*time.Minute,
		)
		if err != nil {
			return fmt.Errorf("finalize post-replay Performance: %w", err)
		}
	}
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

	if err := exportTrace(ctx, appAX, windowAX, outputPath); err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	verboseLog("exportTrace: searching for output in: %v", candidatePaths)

	finalPath, err := waitForExportedTrace(ctx, candidatePaths, exportWaitTimeout())
	if err != nil {
		return err
	}
	if err := verifyExportTraceIdentity(inputPath, finalPath); err != nil {
		if removeErr := os.RemoveAll(finalPath); removeErr != nil {
			return fmt.Errorf("%w; remove mismatched export: %v", err, removeErr)
		}
		return err
	}
	exportedPayload, err := tracebundle.InspectPayload(finalPath)
	if err != nil {
		return fmt.Errorf("inspect exported trace payload: %w", err)
	}
	if err := requireSelfContainedExport(finalPath, exportedPayload); err != nil {
		writeXcodePayloadStatus(status, exportedPayload)
		return err
	}
	writeXcodePayloadStatus(status, exportedPayload)

	// Close the Xcode window after export completes
	// Re-fetch window reference since it may have become stale during export
	// (window title may change or become empty after profiling)
	if freshWindow := findTraceWindowByButtons(appAX); freshWindow != 0 {
		closeXcodeWindow(freshWindow)
	} else if freshWindow := getPreferredTraceWindow(appAX, inputPath); freshWindow != 0 {
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
			actionOutput := xcodeProfileActionOutput{
				Action:          "run",
				Input:           inputPath,
				Output:          finalPath,
				RequestedOutput: outputPath,
				Warning:         warning,
			}
			applyXcodePayload(&actionOutput, exportedPayload)
			return writeXcodeProfileActionOutput(actionOutput)
		}
		fmt.Fprintf(status, Colorize("\nDone! Output saved to: %s (copied from %s)\n", ColorGreen), outputPath, finalPath)
		actionOutput := xcodeProfileActionOutput{
			Action: "run",
			Input:  inputPath,
			Output: outputPath,
			Source: finalPath,
			Copied: true,
		}
		applyXcodePayload(&actionOutput, exportedPayload)
		return writeXcodeProfileActionOutput(actionOutput)
	}
	fmt.Fprintf(status, Colorize("\nDone! Output saved to: %s\n", ColorGreen), outputPath)
	actionOutput := xcodeProfileActionOutput{
		Action: "run",
		Input:  inputPath,
		Output: outputPath,
	}
	applyXcodePayload(&actionOutput, exportedPayload)
	return writeXcodeProfileActionOutput(actionOutput)
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
func closeAllXcodeWindows(ctx context.Context) error {
	appAX, err := FindXcodeApp()
	if err != nil {
		return nil
	}
	defer cfRelease(appAX)

	windows := GetAllWindows(appAX)
	verboseLog("closeAllXcodeWindows: closing %d windows", len(windows))
	for _, w := range windows {
		closeXcodeWindow(w)
		if err := waitForAutomation(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
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

func waitForWindow(ctx context.Context, appAX uintptr, traceFileName string, timeout time.Duration) (uintptr, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := checkAutomationCanceled(ctx); err != nil {
			return 0, err
		}
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
		if err := waitForAutomation(ctx, time.Second); err != nil {
			return 0, err
		}
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

func waitForBoundTraceWindow(ctx context.Context, appAX uintptr, identity xcodeProcessIdentity, traceFileName string, timeout time.Duration) (uintptr, error) {
	deadline := time.Now().Add(timeout)
	var candidate uintptr
	stable := 0
	for {
		bound, err := xcodeIdentityForAX(appAX)
		if err != nil || bound.PID != identity.PID ||
			filepath.Clean(bound.AppPath) != filepath.Clean(identity.AppPath) {
			return 0, fmt.Errorf("lost Xcode binding: want PID %d app %s", identity.PID, identity.AppPath)
		}
		if window := getPreferredTraceWindow(appAX, traceFileName); window != 0 {
			var pid int32
			if axUIElementGetPid(window, &pid) == kAXErrorSuccess && int(pid) == identity.PID {
				if window == candidate {
					stable++
				} else {
					candidate = window
					stable = 1
				}
				if stable >= 2 {
					return window, nil
				}
			}
		} else {
			candidate = 0
			stable = 0
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("bound Xcode PID %d app %s did not expose the trace window for %s within %s",
				identity.PID, identity.AppPath, traceFileName, timeout.Round(time.Second))
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return 0, err
		}
	}
}

func waitForBoundTraceWindowAfterReplay(
	ctx context.Context,
	appAX uintptr,
	identity xcodeProcessIdentity,
	traceFileName, geometryKey string,
	allowSummary, allowPerformance bool,
	timeout time.Duration,
) (uintptr, error) {
	deadline := time.Now().Add(timeout)
	recovery := standaloneExportRecovery{
		Enabled:    true,
		SourcePath: filepath.Clean(traceFileName),
		Identity:   identity,
	}
	var candidateKey string
	stable := 0
	var lastErr error
	for {
		bound, err := xcodeIdentityForAX(appAX)
		if err != nil || bound.PID != identity.PID ||
			filepath.Clean(bound.AppPath) != filepath.Clean(identity.AppPath) {
			return 0, fmt.Errorf("lost Xcode binding: want PID %d app %s", identity.PID, identity.AppPath)
		}

		var window standaloneRecoveryWindow
		element := getPreferredTraceWindow(appAX, traceFileName)
		if element != 0 && !selectionForWindow(traceFileName, element).Bound {
			lastErr = fmt.Errorf("GPU window lacks exact title or AXDocument source binding")
			element = 0
		}
		if element != 0 && allowPerformance && !hasShallowPerformanceGroup(element) {
			lastErr = fmt.Errorf("source-bound trace window has not entered Performance")
			element = 0
		}
		if element != 0 {
			window = standaloneRecoveryWindow{
				xcodeAXWindow: xcodeAXWindow{
					Element:  element,
					Title:    axString(element, "AXTitle"),
					Document: axString(element, "AXDocument"),
				},
				PID: identity.PID,
			}
			window.X, window.Y = axPosition(element)
			window.Width, window.Height = axSize(element)
		} else {
			windows := recoveryWindows(appAX)
			switch {
			case allowSummary:
				window, err = summaryRecoveryTarget(windows, recovery, geometryKey)
			case allowPerformance:
				window, err = transitionedRecoveryPerformanceTarget(windows, recovery, geometryKey)
			default:
				err = fmt.Errorf("no post-replay transition state is allowed")
			}
			if err != nil {
				lastErr = err
			}
		}

		if window.Element != 0 {
			key := standaloneRecoveryWindowKey(window)
			if key == candidateKey {
				stable++
			} else {
				candidateKey = key
				stable = 1
			}
			if stable >= 2 {
				return window.Element, nil
			}
		} else {
			candidateKey = ""
			stable = 0
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("bound Xcode PID %d app %s did not expose the trace or allowed post-replay state for %s within %s: %w",
				identity.PID, identity.AppPath, traceFileName, timeout.Round(time.Second), lastErr)
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return 0, err
		}
	}
}

// getPreferredTraceWindow finds the best matching window for a trace filename.
// When multiple windows match (e.g., document window + trace viewer), prefer the one
// with GPU trace UI elements (Replay button, profiling status).
// uiIdentifiedTraceWindow is the window that getPreferredTraceWindow last
// accepted solely because it was the only one carrying GPU trace UI. Xcode
// clears a trace window's title and AXDocument during replay, so that
// uniqueness is the only remaining evidence that the window is the requested
// trace. selectionForWindow consults it. The Xcode automation is single
// threaded, so a package-level value is sufficient.
var uiIdentifiedTraceWindow uintptr

func getPreferredTraceWindow(appAX uintptr, traceFileName string) uintptr {
	uiIdentifiedTraceWindow = 0
	traceIdentity := strings.ToLower(filepath.Clean(traceFileName))
	traceBase := strings.ToLower(filepath.Base(traceFileName))
	allWindows := deduplicateAXWindows(GetAllWindows(appAX))
	for _, window := range allWindows {
		verboseLog("getPreferredTraceWindow: visible window: title=%q doc=%q", window.Title, window.Document)
	}
	verboseLog("getPreferredTraceWindow: %d total Xcode windows, looking for %q", len(allWindows), traceFileName)

	exactWindows := exactTraceWindows(allWindows, traceIdentity)
	var matchingWindows []uintptr
	if len(exactWindows) == 0 {
		for _, window := range allWindows {
			child := window.Element
			windowTitle := strings.ToLower(window.Title)
			windowDoc := strings.ToLower(filepath.Clean(window.Document))
			if traceBase != "" && strings.Contains(windowTitle, traceBase) {
				matchingWindows = append(matchingWindows, child)
				continue
			}
			if traceBase != "" && strings.Contains(windowDoc, traceBase) {
				matchingWindows = append(matchingWindows, child)
			}
		}
	} else {
		matchingWindows = exactWindows
	}

	// Second pass: try matching without extension (Xcode sometimes strips it)
	if len(matchingWindows) == 0 {
		baseName := strings.TrimSuffix(traceBase, filepath.Ext(traceBase))
		if baseName != traceBase {
			for _, window := range allWindows {
				child := window.Element
				windowTitle := strings.ToLower(window.Title)
				if strings.Contains(windowTitle, baseName) {
					matchingWindows = append(matchingWindows, child)
					continue
				}
				windowDoc := strings.ToLower(window.Document)
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
		for _, window := range allWindows {
			child := window.Element
			title := window.Title
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
		if len(matchingWindows) == 1 {
			uiIdentifiedTraceWindow = matchingWindows[0]
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

	// Multiple matches - prefer a uniquely active profiling window. Do not
	// choose an arbitrary untitled Summary window: it may belong to another
	// trace and carry a stale Show Performance sentinel.
	var activeWindows []uintptr
	for _, w := range matchingWindows {
		if stopBtn := findButtonBFS(w, "Stop GPU workload", 500); stopBtn != 0 && IsElementEnabled(stopBtn) {
			activeWindows = append(activeWindows, w)
		}
	}
	if len(activeWindows) == 1 {
		verboseLog("getPreferredTraceWindow: selected unique active GPU window %q", axString(activeWindows[0], "AXTitle"))
		return activeWindows[0]
	}
	if len(activeWindows) > 1 {
		verboseLog("getPreferredTraceWindow: %d active GPU windows are ambiguous", len(activeWindows))
		return 0
	}

	verboseLog("getPreferredTraceWindow: multiple inactive GPU windows are ambiguous")
	return 0
}

type xcodeAXWindow struct {
	Element  uintptr
	Title    string
	Document string
	X        int
	Y        int
	Width    int
	Height   int
}

func deduplicateAXWindows(elements []uintptr) []xcodeAXWindow {
	windows := make([]xcodeAXWindow, 0, len(elements))
	for _, element := range elements {
		x, y := axPosition(element)
		width, height := axSize(element)
		windows = append(windows, xcodeAXWindow{
			Element:  element,
			Title:    axString(element, "AXTitle"),
			Document: axString(element, "AXDocument"),
			X:        x,
			Y:        y,
			Width:    width,
			Height:   height,
		})
	}
	return deduplicateXcodeWindows(windows)
}

func deduplicateXcodeWindows(windows []xcodeAXWindow) []xcodeAXWindow {
	seenElements := make(map[uintptr]bool)
	seenLogical := make(map[string]bool)
	out := make([]xcodeAXWindow, 0, len(windows))
	for _, window := range windows {
		if window.Element != 0 && seenElements[window.Element] {
			continue
		}
		if window.Element != 0 {
			seenElements[window.Element] = true
		}

		title := strings.ToLower(strings.TrimSpace(window.Title))
		document := strings.ToLower(filepath.Clean(window.Document))
		if window.Document == "" {
			document = ""
		}
		logical := fmt.Sprintf("%s\x00%s\x00%d,%d,%d,%d",
			title, document, window.X, window.Y, window.Width, window.Height)
		hasLogicalIdentity := title != "" || document != "" ||
			window.X != 0 || window.Y != 0 || window.Width != 0 || window.Height != 0
		if hasLogicalIdentity && seenLogical[logical] {
			continue
		}
		if hasLogicalIdentity {
			seenLogical[logical] = true
		}
		out = append(out, window)
	}
	return out
}

func exactTraceWindows(windows []xcodeAXWindow, traceIdentity string) []uintptr {
	if traceIdentity == "" || traceIdentity == "." {
		return nil
	}
	var matches []uintptr
	for _, window := range windows {
		document := strings.ToLower(filepath.Clean(window.Document))
		if document != "." && (document == traceIdentity || strings.Contains(document, traceIdentity)) {
			matches = append(matches, window.Element)
		}
	}
	return matches
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

// hasGPUTraceUI checks whether a window contains GPU trace UI elements.
func hasGPUTraceUI(windowAX uintptr) bool {
	for _, name := range gpuTraceStateButtonNames() {
		if btn := findButtonBFS(windowAX, name, 500); btn != 0 {
			return true
		}
	}
	return false
}

func gpuTraceStateButtonNames() []string {
	return []string{
		"Stop GPU workload",
		"Capture GPU workload",
		"Replay",
		"Profile",
		"Export",
		"Show Performance",
	}
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
	if collectProfileOpts.timeout > 30*time.Second {
		return collectProfileOpts.timeout
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

func waitForExportedTrace(ctx context.Context, candidatePaths []string, timeout time.Duration) (string, error) {
	return waitForExportedTraceWithReader(ctx, candidatePaths, timeout, readExportTraceSignature)
}

type exportCandidate struct {
	Path     string
	Identity string
	info     os.FileInfo
}

func canonicalExportCandidates(paths []string) []exportCandidate {
	var candidates []exportCandidate
	for _, path := range uniquePaths(paths) {
		path = filepath.Clean(path)
		resolved := path
		if target, err := filepath.EvalSymlinks(path); err == nil {
			resolved = filepath.Clean(target)
		}
		info, _ := os.Stat(path)

		duplicate := false
		for _, candidate := range candidates {
			if resolved == candidate.Identity ||
				(info != nil && candidate.info != nil && os.SameFile(info, candidate.info)) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		candidates = append(candidates, exportCandidate{
			Path:     path,
			Identity: resolved,
			info:     info,
		})
	}
	return candidates
}

type exportCandidateStability struct {
	signature exportTraceSignature
	samples   int
	set       bool
}

func waitForExportedTraceWithReader(
	ctx context.Context,
	candidatePaths []string,
	timeout time.Duration,
	readSignature func(string, string) (exportTraceSignature, error),
) (string, error) {
	deadline := time.Now().Add(timeout)
	var foundWithoutProfiler []string
	var foundIncomplete []string
	stability := make(map[string]exportCandidateStability)
	for {
		if err := checkAutomationCanceled(ctx); err != nil {
			return "", err
		}
		for _, candidate := range canonicalExportCandidates(candidatePaths) {
			p := candidate.Path
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			if !info.IsDir() {
				delete(stability, candidate.Identity)
				continue
			}
			profilerDir := findProfilerDir(p)
			if profilerDir == "" {
				foundWithoutProfiler = append(foundWithoutProfiler, p)
				delete(stability, candidate.Identity)
				continue
			}
			signature, err := readSignature(p, profilerDir)
			if err != nil {
				foundIncomplete = append(foundIncomplete, p)
				delete(stability, candidate.Identity)
				continue
			}
			state := stability[candidate.Identity]
			if state.set && signature == state.signature {
				state.samples++
				if state.samples >= 2 {
					return p, nil
				}
			} else {
				state.samples = 0
			}
			state.signature = signature
			state.set = true
			stability[candidate.Identity] = state
		}
		if time.Now().After(deadline) {
			break
		}
		if err := waitForAutomation(ctx, 250*time.Millisecond); err != nil {
			return "", err
		}
	}

	if len(foundIncomplete) > 0 {
		return "", fmt.Errorf("export profiler data did not stabilize with non-empty streamData: %s", strings.Join(uniquePaths(foundIncomplete), ", "))
	}
	if len(foundWithoutProfiler) > 0 {
		return "", fmt.Errorf("export wrote a bundle without .gpuprofiler_raw: %s; Xcode did not embed performance data", strings.Join(uniquePaths(foundWithoutProfiler), ", "))
	}
	return "", fmt.Errorf("export did not write a perfdata bundle within %s; checked: %s", timeout.Round(time.Second), strings.Join(candidatePaths, ", "))
}

type exportTraceSignature struct {
	Files          int
	Bytes          int64
	StreamDataSize int64
}

type exportSheetState struct {
	Filename            string
	DirectoryCandidates []string
	SaveEnabled         bool
	GoToFolderSheetOpen bool
	GoToFolderPath      string
}

func readExportSheetState(window uintptr) exportSheetState {
	var state exportSheetState
	if field := FindSaveAsTextField(window); field != 0 {
		state.Filename = axString(field, "AXValue")
	}
	if save := findButtonBFS(window, "Save", 500); save != 0 {
		state.SaveEnabled = IsElementEnabled(save)
	}
	goToSheet := findElementBounded(window, 600, func(element uintptr) bool {
		return axString(element, "AXRole") == "AXSheet" &&
			axString(element, "AXIdentifier") == "GoToWindow"
	})
	state.GoToFolderSheetOpen = goToSheet != 0
	if goToSheet != 0 {
		if pathField := findElementBounded(goToSheet, 200, func(element uintptr) bool {
			return axString(element, "AXRole") == "AXTextField" &&
				axString(element, "AXIdentifier") == "PathTextField"
		}); pathField != 0 {
			state.GoToFolderPath = strings.TrimSpace(axString(pathField, "AXValue"))
		}
	}
	findElementBounded(window, 1000, func(element uintptr) bool {
		role := axString(element, "AXRole")
		subrole := axString(element, "AXSubrole")
		identifier := strings.ToLower(axString(element, "AXIdentifier"))
		description := strings.ToLower(axString(element, "AXDescription"))
		// The GoToWindow input is a requested path, not evidence that the
		// parent save panel committed that directory.
		if identifier == "pathtextfield" {
			return false
		}
		isLocation := role == "AXPopUpButton" || subrole == "AXPathButton" ||
			strings.Contains(identifier, "path") || strings.Contains(identifier, "location") ||
			strings.Contains(description, "where") || strings.Contains(description, "location")
		// AXURL and AXDocument are useful full-path evidence even when Xcode
		// does not identify the owning element as a location control.
		for _, attribute := range []string{"AXURL", "AXDocument"} {
			value := strings.TrimSpace(axString(element, attribute))
			if value != "" {
				state.DirectoryCandidates = append(state.DirectoryCandidates, value)
			}
		}
		if !isLocation {
			return false
		}
		for _, attribute := range []string{"AXValue", "AXTitle"} {
			value := strings.TrimSpace(axString(element, attribute))
			if value != "" {
				state.DirectoryCandidates = append(state.DirectoryCandidates, value)
			}
		}
		return false
	})
	state.DirectoryCandidates = uniquePaths(state.DirectoryCandidates)
	return state
}

func exportSheetDirectoryMatches(state exportSheetState, directory string) bool {
	want := filepath.Clean(directory)
	for _, candidate := range state.DirectoryCandidates {
		value := candidate
		if strings.HasPrefix(value, "file://") {
			if parsed, err := url.Parse(value); err == nil {
				value = parsed.Path
			}
		}
		if decoded, err := url.PathUnescape(value); err == nil {
			value = decoded
		}
		if filepath.IsAbs(value) && filepath.Clean(value) == want {
			return true
		}
	}
	return false
}

func needsDirectExportLocation(remainingPath string, state exportSheetState, directory string) bool {
	return remainingPath != "" || !exportSheetDirectoryMatches(state, directory)
}

func goToFolderNavigationComplete(state exportSheetState, directory string) bool {
	return !state.GoToFolderSheetOpen && state.SaveEnabled &&
		exportSheetDirectoryMatches(state, directory)
}

// goToFolderNavigationCompleteAfterExactEntry accepts a basename-only save
// panel location only after the caller observed the exact absolute path in the
// open Go To Folder field and then committed it. The ordered proof
// distinguishes identical basenames such as /Users/tmc/tmp and /private/tmp.
func goToFolderNavigationCompleteAfterExactEntry(state exportSheetState, directory string) bool {
	if goToFolderNavigationComplete(state, directory) {
		return true
	}
	if state.GoToFolderSheetOpen || !state.SaveEnabled {
		return false
	}
	wantBase := filepath.Base(filepath.Clean(directory))
	for _, candidate := range state.DirectoryCandidates {
		if !filepath.IsAbs(candidate) && filepath.Clean(candidate) == wantBase {
			return true
		}
	}
	return false
}

func goToFolderConfirmationReady(state exportSheetState, directory string) bool {
	if !state.GoToFolderSheetOpen {
		return exportSheetDirectoryMatches(state, directory)
	}
	return filepath.IsAbs(state.GoToFolderPath) &&
		filepath.Clean(state.GoToFolderPath) == filepath.Clean(directory)
}

func waitForExportDirectoryState(ctx context.Context, window uintptr, directory string, timeout time.Duration) (exportSheetState, error) {
	deadline := time.Now().Add(timeout)
	var state exportSheetState
	for {
		state = readExportSheetState(window)
		if exportSheetDirectoryMatches(state, directory) {
			return state, nil
		}
		if time.Now().After(deadline) {
			return state, fmt.Errorf("save sheet did not expose requested directory %q", directory)
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return state, err
		}
	}
}

func formatExportSheetState(state exportSheetState) string {
	return fmt.Sprintf("filename=%q directory_candidates=%q save_enabled=%t go_to_folder_open=%t go_to_folder_path=%q",
		state.Filename, state.DirectoryCandidates, state.SaveEnabled,
		state.GoToFolderSheetOpen, state.GoToFolderPath)
}

func readExportTraceSignature(bundle, profilerDir string) (exportTraceSignature, error) {
	streamInfo, err := os.Stat(filepath.Join(profilerDir, "streamData"))
	if err != nil {
		return exportTraceSignature{}, err
	}
	if streamInfo.Size() == 0 {
		return exportTraceSignature{}, fmt.Errorf("streamData is empty")
	}

	var signature exportTraceSignature
	signature.StreamDataSize = streamInfo.Size()
	err = filepath.WalkDir(bundle, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		signature.Files++
		signature.Bytes += info.Size()
		return nil
	})
	if err != nil {
		return exportTraceSignature{}, err
	}
	return signature, nil
}

func verifyExportTraceIdentity(inputPath, outputPath string) error {
	input, err := gputraceTrace.ReadMetadata(inputPath)
	if err != nil {
		return fmt.Errorf("read input trace identity: %w", err)
	}
	output, err := gputraceTrace.ReadMetadata(outputPath)
	if err != nil {
		return fmt.Errorf("read exported trace identity: %w", err)
	}
	if input.UUID == "" || output.UUID == "" {
		return fmt.Errorf("trace identity is missing (input UUID %q, exported UUID %q)", input.UUID, output.UUID)
	}
	if input.UUID != output.UUID {
		return fmt.Errorf("exported trace UUID %s does not match requested trace UUID %s", output.UUID, input.UUID)
	}
	return nil
}

func windowMatchesTraceFile(window uintptr, traceFileName string) bool {
	return selectionForWindow(traceFileName, window).Bound
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

func waitForReplayComplete(ctx context.Context, appAX uintptr, traceFileName string, initialWindowAX uintptr, timeout time.Duration) error {
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
	var targetPID int32
	_ = axUIElementGetPid(appAX, &targetPID)

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
		freshApp := uintptr(0)
		crashScope := xcodeCrashScopeFromContext(ctx)
		targetAppPath := ""
		if targetPID != 0 {
			targetAppPath = xcodeProcessPath(int(targetPID))
		}
		if targetAppPath != "" &&
			(crashScope == nil || filepath.Clean(targetAppPath) == crashScope.appPath) {
			freshApp = axCreateApplication(targetPID)
		}
		if freshApp == 0 {
			verboseLog("waitForReplayComplete: failed to re-fetch target Xcode PID %d; checking exact-app replacements", targetPID)
			if crashScope != nil {
				crashScope.refreshProcesses()
				for _, identity := range xcodeProcessesForApp(crashScope.appPath) {
					replacementApp := axCreateApplication(int32(identity.PID))
					if replacementApp == 0 {
						continue
					}
					replacementWindow := getPreferredTraceWindow(replacementApp, traceFileName)
					if replacementWindow == 0 {
						cfRelease(replacementApp)
						continue
					}
					verboseLog("waitForReplayComplete: rebound exact trace window to Xcode PID %d", identity.PID)
					crashScope.observe(identity)
					targetPID = int32(identity.PID)
					currentWindow = replacementWindow
					freshApp = replacementApp
					break
				}
			}
			if freshApp == 0 {
				return 0, false
			}
		}
		defer cfRelease(freshApp)
		consecutiveXcodeFailures = 0
		allWindows := GetAllWindows(freshApp)

		// When a trace identity was supplied, never fall through to an
		// unrelated untitled GPU window. A stale completed Summary window may
		// expose the same app-global controls and Show Performance sentinel.
		passes := 1
		if traceFileName == "" {
			passes = 2
		}
		for pass := range passes {
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
				if crashScope := xcodeCrashScopeFromContext(ctx); crashScope != nil {
					crashScope.refreshProcesses()
					if crashScope.crashSuspected() {
						if crashErr := waitForXcodeCrashReport(ctx, xcodeCrashReportGrace); crashErr != nil {
							return 0, crashErr
						}
					}
				}
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
		if err := checkAutomationCanceled(ctx); err != nil {
			return err
		}
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
		if err := waitForAutomation(ctx, 500*time.Millisecond); err != nil {
			return err
		}
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
		if err := waitForAutomation(ctx, sleepTime); err != nil {
			return err
		}
	}

	// Now wait for profiling to complete
	lastStatus := ""
	for time.Since(start) < timeout {
		if err := checkAutomationCanceled(ctx); err != nil {
			return err
		}
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
			if err := waitForAutomation(ctx, 2*time.Second); err != nil {
				return err
			}
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
			if err := waitForAutomation(ctx, 2*time.Second); err != nil {
				return err
			}
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
		if err := waitForAutomation(ctx, 2*time.Second); err != nil {
			return err
		}
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

func exportTrace(ctx context.Context, appAX, windowAX uintptr, outputPath string) error {
	if err := checkAutomationCanceled(ctx); err != nil {
		return err
	}
	identity, err := xcodeIdentityForAX(appAX)
	if err != nil {
		return fmt.Errorf("establish export Xcode identity: %w", err)
	}
	var windowPID int32
	if axUIElementGetPid(windowAX, &windowPID) != kAXErrorSuccess || int(windowPID) != identity.PID {
		return fmt.Errorf("export window is not owned by bound Xcode PID %d app %s", identity.PID, identity.AppPath)
	}
	status := xcodeProfileStatusWriter()
	axAction(windowAX, "AXRaise")
	time.Sleep(300 * time.Millisecond)
	if sheet := findElement(windowAX, func(el uintptr) bool {
		return axString(el, "AXRole") == "AXSheet"
	}); sheet != 0 {
		return fmt.Errorf("selected export window already has an open sheet; refusing to reuse stale UI")
	}

	// Try clicking Export button in Summary panel first
	exportBtn := FindExportButton(windowAX)
	if exportBtn != 0 {
		if !IsElementEnabled(exportBtn) {
			return fmt.Errorf("Export button is disabled; Xcode workload is not finalized")
		}
		fmt.Fprintln(status, "    Found Export button in Summary panel")
		if err := axPressWithFallback(exportBtn); err != nil {
			fmt.Fprintf(status, "    Warning: Failed to click Export button: %v\n", err)
		}
	} else {
		found, enabled, err := fileExportMenuState(appAX, windowAX)
		if err != nil {
			return fmt.Errorf("check File > Export readiness: %w", err)
		}
		if !found {
			return fmt.Errorf("File > Export menu item not found")
		}
		if !enabled {
			return fmt.Errorf("File > Export is disabled; Xcode workload is not finalized")
		}
		bound, err := xcodeIdentityForAX(appAX)
		if err != nil || bound.PID != identity.PID ||
			filepath.Clean(bound.AppPath) != filepath.Clean(identity.AppPath) {
			return fmt.Errorf("bound Xcode identity changed while checking File > Export")
		}
		// Fall back to the menu. The readiness probe above already logged
		// everything the debug probe used to discover; reopening File here
		// introduces a second, stateful menu transaction.
		if err := clickMenuItemForWindow(appAX, windowAX, []string{"File", "Export..."}); err != nil {
			return fmt.Errorf("failed to click Export menu: %w", err)
		}
	}

	fmt.Fprintln(status, "    Waiting for export sheet...")
	if err := waitForAutomation(ctx, 500*time.Millisecond); err != nil {
		return err
	}

	// Refresh app reference since the UI might have changed
	freshApp, err := reacquireXcodeApp(identity)
	if err != nil {
		return fmt.Errorf("bound Xcode not accessible after clicking Export: %w", err)
	}
	defer cfRelease(freshApp)

	// The export sheet must descend from the selected trace window. Searching
	// every Xcode window can bind a stale sheet from another trace.
	sheetFound := false
	for i := 0; i < 30; i++ {
		if err := checkAutomationCanceled(ctx); err != nil {
			return err
		}
		sheet := findElement(windowAX, func(el uintptr) bool {
			return axString(el, "AXRole") == "AXSheet"
		})
		if sheet != 0 {
			sheetFound = true
			break
		}
		if err := waitForAutomation(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}

	if !sheetFound {
		if collectProfileOpts.debug {
			fmt.Fprintf(os.Stderr, "    Debug: selected window title=%q document=%q\n",
				axString(windowAX, "AXTitle"), axString(windowAX, "AXDocument"))
		}
		return fmt.Errorf("export sheet did not appear under the selected trace window")
	}

	fmt.Fprintln(status, "    Export sheet detected")

	findInExportWindow := func(finder func(uintptr) uintptr) uintptr {
		return finder(windowAX)
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

	if collectProfileOpts.debug {
		DebugTextFields(windowAX)
	}

	// Try the shallow path popup first. Nested file-browser crawling is
	// intentionally avoided because large AX trees can stall for minutes.
	remainingPath := ""
	if outputDir != "" && outputDir != "." {
		fmt.Fprintf(status, "    Navigating to directory: %s\n", outputDir)
		var popupErr error
		remainingPath, popupErr = navigateViaPathPopup(windowAX, outputDir)
		if popupErr != nil {
			verboseLog("exportTrace: path popup navigation failed: %v", popupErr)
		} else if remainingPath != "" {
			verboseLog("exportTrace: navigated partially, remaining path: %s", remainingPath)
		}
	}

	// A popup result is not proof of the destination: the control may display
	// only "tmp" after partial navigation. Unless the sheet exposes the exact
	// absolute directory, use the bounded direct-location fallback.
	directoryState := readExportSheetState(windowAX)
	directoryVerifiedByExactEntry := false
	if needsDirectExportLocation(remainingPath, directoryState, outputDir) {
		fmt.Fprintf(status, "    Using direct location for: %s\n", outputDir)
		if err := NavigateToFolderInSaveDialog(windowAX, outputDir); err != nil {
			return fmt.Errorf("establish export directory %s: %w; sheet state: %s",
				outputDir, err, formatExportSheetState(readExportSheetState(windowAX)))
		}
		directoryVerifiedByExactEntry = true
	}
	if directoryVerifiedByExactEntry {
		if !goToFolderNavigationCompleteAfterExactEntry(readExportSheetState(windowAX), outputDir) {
			return fmt.Errorf("export directory lost exact-entry proof; sheet state: %s",
				formatExportSheetState(readExportSheetState(windowAX)))
		}
	} else {
		directoryState, err = waitForExportDirectoryState(ctx, windowAX, outputDir, 2*time.Second)
		if err != nil {
			return fmt.Errorf("export directory was not established: %w; sheet state: %s",
				err, formatExportSheetState(directoryState))
		}
	}
	fmt.Fprintf(status, "    Verified export directory: %s\n", outputDir)

	// Set just the filename (never include path prefix - macOS converts "/" to ":")
	fmt.Fprintf(status, "    Setting filename: %s\n", outputName)
	saveNameField := findInExportWindow(FindSaveAsTextField)
	if saveNameField != 0 {
		if err := setSaveName(saveNameField, outputName); err != nil {
			return err
		}
		if collectProfileOpts.debug {
			fmt.Fprintln(os.Stderr, "    [DEBUG] Set and verified filename via AX (saveAsNameTextField)")
		}
	} else {
		return fmt.Errorf("saveAsNameTextField not found")
	}
	time.Sleep(300 * time.Millisecond)
	finalSheetState := readExportSheetState(windowAX)
	directoryVerified := exportSheetDirectoryMatches(finalSheetState, outputDir)
	if directoryVerifiedByExactEntry {
		directoryVerified = goToFolderNavigationCompleteAfterExactEntry(finalSheetState, outputDir)
	}
	if finalSheetState.Filename != outputName || !directoryVerified {
		return fmt.Errorf("export destination verification failed: requested_directory=%q requested_filename=%q; sheet state: %s",
			outputDir, outputName, formatExportSheetState(finalSheetState))
	}
	fmt.Fprintf(status, "    Verified export filename: %s\n", outputName)

	// Debug: dump the export sheet state so we can see exactly what's happening
	if collectProfileOpts.debug {
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
		if collectProfileOpts.debug {
			fmt.Fprintln(os.Stderr, "    [DEBUG] Export sheet state (Save disabled):")
			dumpExportSheetState(windowAX)
		}
		return fmt.Errorf("Save button disabled in export sheet: %s",
			formatExportSheetState(readExportSheetState(windowAX)))
	}

	// Click Save button
	fmt.Fprintln(status, "    Saving...")
	if err := axPressWithFallback(saveBtn); err != nil {
		return fmt.Errorf("failed to click Save: %w", err)
	}
	replaced, err := pressReplaceIfPresent(ctx, windowAX, 5*time.Second)
	if err != nil {
		return fmt.Errorf("confirm replace: %w", err)
	}
	if replaced {
		fmt.Fprintln(status, "    Confirmed replacement")
	}

	if err := waitForExportSheetDismissed(ctx, windowAX, 5*time.Second); err != nil {
		return err
	}
	fmt.Fprintln(status, "    Export accepted; assembling bundle...")

	// Check if file was saved to expected location
	if _, err := os.Stat(outputPath); err == nil {
		return nil // File found at expected path
	}

	// Return nil to let caller handle searching alternate locations
	// Caller is responsible for finding and copying the file
	return nil
}

func setSaveName(field uintptr, name string) error {
	for attempt := 0; attempt < 3; attempt++ {
		if err := axSetValue(field, name); err != nil {
			if attempt == 2 {
				return fmt.Errorf("set export filename: %w", err)
			}
			continue
		}
		axAction(field, "AXConfirm")
		time.Sleep(150 * time.Millisecond)
		if got := axString(field, "AXValue"); got == name {
			return nil
		}
	}
	return fmt.Errorf("export filename did not update to %q (still %q)", name, axString(field, "AXValue"))
}

func waitForExportSheetDismissed(ctx context.Context, window uintptr, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		sheet := findElement(window, func(el uintptr) bool {
			return axString(el, "AXRole") == "AXSheet" &&
				(axString(el, "AXIdentifier") == "save-panel" || axString(el, "AXDescription") == "export")
		})
		if sheet == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			save := findButtonBFS(sheet, "Save", 500)
			if save != 0 && IsElementEnabled(save) {
				return fmt.Errorf("export save sheet is still open with Save enabled")
			}
			return fmt.Errorf("export save sheet did not dismiss")
		}
		if err := waitForAutomation(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
}

func pressReplaceIfPresent(ctx context.Context, windowAX uintptr, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := checkAutomationCanceled(ctx); err != nil {
			return false, err
		}
		replaceBtn := findButtonBFS(windowAX, "Replace", 3000)
		if replaceBtn != 0 {
			if !IsElementEnabled(replaceBtn) {
				return false, fmt.Errorf("Replace button disabled")
			}
			if err := axPressWithFallback(replaceBtn); err != nil {
				return false, err
			}
			if err := waitForAutomation(ctx, 500*time.Millisecond); err != nil {
				return false, err
			}
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return false, err
		}
	}
}

// navigateViaPathPopup tries to navigate to a folder using the path popup button
// in the save dialog. This is the breadcrumb-style path shown at the top of the dialog.
// Returns the remaining path components that couldn't be navigated (to include in filename),
// and an error if the popup couldn't be opened at all.
func navigateViaPathPopup(windowAX uintptr, targetPath string) (remainingPath string, err error) {
	// Look for a path control or popup button that shows the current location
	// Common identifiers: "Where:" popup, path bar, location dropdown
	pathPopup := findElementBounded(windowAX, 600, func(el uintptr) bool {
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
		pathPopup = findElementBounded(windowAX, 600, func(el uintptr) bool {
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
		findElementBounded(popupMenu, 300, func(el uintptr) bool {
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
				// Do not crawl the file browser for nested components. Large
				// save-panel AX trees can make that search take minutes, and a
				// double-click does not prove the location changed. Return the
				// remainder so the caller uses the bounded direct-location
				// fallback.
				return strings.Join(remainingParts, "/"), nil
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

func findElementBounded(root uintptr, maxVisit int, match func(uintptr) bool) uintptr {
	queue := []uintptr{root}
	for visited := 0; len(queue) > 0 && visited < maxVisit; visited++ {
		element := queue[0]
		queue = queue[1:]
		if match(element) {
			return element
		}
		queue = append(queue, axChildren(element)...)
	}
	return 0
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
