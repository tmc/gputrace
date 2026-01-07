package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	runCmd := &cobra.Command{
		Use:   "run <trace_file>",
		Short: "Run full automation (open, replay, export)",
		Args:  cobra.ExactArgs(1),
		RunE:  runCollectXcodeProfileFull,
	}
	runCmd.Flags().StringVarP(&collectProfileOutput, "output", "o", "", "Output path for the exported trace")
	collectXcodeProfileCmd.AddCommand(runCmd)
}

func runCollectXcodeProfileFull(cmd *cobra.Command, args []string) error {
	inputPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("invalid input path: %w", err)
	}

	if collectProfileOutput == "" {
		ext := filepath.Ext(inputPath)
		base := inputPath[:len(inputPath)-len(ext)]
		collectProfileOutput = base + "-perfdata" + ext
	}
	outputPath, err := filepath.Abs(collectProfileOutput)
	if err != nil {
		return fmt.Errorf("invalid output path: %w", err)
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

	fmt.Print(Colorize("Collect Profile: Automating Xcode GPU trace...\n", ColorBold))
	fmt.Printf("  Input:  %s\n", inputPath)
	fmt.Printf("  Output: %s\n", outputPath)

	ctx, cancel := context.WithTimeout(context.Background(), collectProfileTimeout)
	defer cancel()

	// Step 1: Open File in Xcode
	fmt.Println("  Step 1: Opening trace in Xcode...")
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("trace file does not exist: %s", inputPath)
	}

	openCmd := exec.CommandContext(ctx, "open", "-a", "Xcode", inputPath)
	if output, err := openCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to open trace in Xcode: %w\n    output: %s", err, string(output))
	}
	time.Sleep(3 * time.Second)

	// Step 2: Wait for Xcode window via AX
	fmt.Println("  Step 2: Waiting for Xcode window...")
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

	// Step 3: Start replay
	fmt.Println("  Step 3: Starting replay...")
	if err := clickReplayButton(windowAX); err != nil {
		return fmt.Errorf("failed to start replay: %w", err)
	}

	// Step 4: Wait for replay
	fmt.Println("  Step 4: Waiting for replay to complete...")
	if err := waitForReplayComplete(windowAX, 5*time.Minute); err != nil {
		return fmt.Errorf("replay wait failed: %w", err)
	}
	fmt.Println("    Replay completed")

	// Step 5: Export
	fmt.Println("  Step 5: Exporting trace...")
	if err := exportTrace(appAX, windowAX, outputPath); err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	// Check if file was saved - try multiple locations
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf(Colorize("\nDone! Output saved to: %s\n", ColorGreen), outputPath)
		return nil
	}

	// Try the input file's directory (common Xcode default)
	outputName := filepath.Base(outputPath)
	inputDir := filepath.Dir(inputPath)
	altPath := filepath.Join(inputDir, outputName)
	if altPath != outputPath {
		if _, err := os.Stat(altPath); err == nil {
			fmt.Printf(Colorize("\nDone! Output saved to: %s\n", ColorGreen), altPath)
			return nil
		}
	}

	// Try user's Downloads folder
	if home, err := os.UserHomeDir(); err == nil {
		downloadsPath := filepath.Join(home, "Downloads", outputName)
		if _, err := os.Stat(downloadsPath); err == nil {
			fmt.Printf(Colorize("\nDone! Output saved to: %s\n", ColorGreen), downloadsPath)
			return nil
		}
	}

	fmt.Print(Colorize("\nNote: Output file not found at expected location.\n", ColorYellow))
	fmt.Printf("  Expected: %s\n", outputPath)
	fmt.Printf("  Also checked: %s\n", inputDir)
	fmt.Printf("Check Xcode's save dialog for the actual location.\n")
	return nil
}


func waitForWindow(appAX uintptr, traceFileName string, timeout time.Duration) (uintptr, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Try to find window by trace file name first
		if traceFileName != "" {
			windowAX := GetWindowByTitle(appAX, traceFileName)
			if windowAX != 0 {
				return windowAX, nil
			}
		}
		// Fallback to first window
		windowAX := GetFirstWindow(appAX)
		if windowAX != 0 {
			return windowAX, nil
		}
		time.Sleep(1 * time.Second)
	}
	return 0, fmt.Errorf("could not find Xcode window for %s", traceFileName)
}

func clickReplayButton(windowAX uintptr) error {
	windowTitle := axString(windowAX, "AXTitle")
	verboseLog("clickReplayButton: window=%d title=%q", windowAX, windowTitle)

	// Activate Xcode and raise the target window before clicking
	if err := ActivateXcode(); err != nil {
		verboseLog("clickReplayButton: ActivateXcode failed: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// Raise the specific trace window
	if err := axAction(windowAX, "AXRaise"); err != nil {
		verboseLog("clickReplayButton: AXRaise failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

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

	// Try "Capture GPU workload" button (GPU trace specific - starts profiling)
	captureBtn := findButtonInAllWindows("Capture GPU workload")
	verboseLog("clickReplayButton: Capture GPU workload button=%d enabled=%v", captureBtn, captureBtn != 0 && IsElementEnabled(captureBtn))
	if captureBtn != 0 && IsElementEnabled(captureBtn) {
		if err := axAction(captureBtn, "AXPress"); err != nil {
			return fmt.Errorf("failed to click Capture GPU workload button: %w", err)
		}
		fmt.Println("    Clicked Capture GPU workload button successfully")
		return nil
	}

	// Try "Profile" button (alternate name)
	profileBtn := findButtonInAllWindows("Profile")
	verboseLog("clickReplayButton: Profile button=%d enabled=%v", profileBtn, profileBtn != 0 && IsElementEnabled(profileBtn))
	if profileBtn != 0 && IsElementEnabled(profileBtn) {
		if err := axAction(profileBtn, "AXPress"); err != nil {
			return fmt.Errorf("failed to click Profile button: %w", err)
		}
		fmt.Println("    Clicked Profile button successfully")
		return nil
	}

	// Try Replay button
	replayBtn := findButtonInAllWindows("Replay")
	verboseLog("clickReplayButton: Replay button=%d enabled=%v", replayBtn, replayBtn != 0 && IsElementEnabled(replayBtn))
	if replayBtn != 0 && IsElementEnabled(replayBtn) {
		if err := axAction(replayBtn, "AXPress"); err != nil {
			return fmt.Errorf("failed to click Replay button: %w", err)
		}
		fmt.Println("    Clicked Replay button successfully")
		return nil
	}

	// Retry a few times
	for i := 0; i < 5; i++ {
		time.Sleep(1 * time.Second)
		captureBtn = findButtonInAllWindows("Capture GPU workload")
		if captureBtn != 0 && IsElementEnabled(captureBtn) {
			if err := axAction(captureBtn, "AXPress"); err != nil {
				return fmt.Errorf("failed to click Capture GPU workload button: %w", err)
			}
			fmt.Println("    Clicked Capture GPU workload button successfully")
			return nil
		}
		replayBtn = findButtonInAllWindows("Replay")
		if replayBtn != 0 && IsElementEnabled(replayBtn) {
			if err := axAction(replayBtn, "AXPress"); err != nil {
				return fmt.Errorf("failed to click Replay button: %w", err)
			}
			fmt.Println("    Clicked Replay button successfully")
			return nil
		}
	}

	return fmt.Errorf("Capture GPU workload/Replay button not found in AX tree")
}

func waitForReplayComplete(windowAX uintptr, timeout time.Duration) error {
	start := time.Now()
	windowTitle := axString(windowAX, "AXTitle")
	verboseLog("waitForReplayComplete: waiting for profiling in window %q", windowTitle)

	// Helper to find a button in the TARGET window only (not all windows)
	// This prevents false positives from other Xcode windows
	findButton := func(name string) uintptr {
		return findButtonBFS(windowAX, name, 500)
	}

	// First, wait for profiling to actually start
	// "Capture GPU workload" disabled OR "Stop GPU workload" enabled = profiling running
	profilingStarted := false
	for time.Since(start) < 30*time.Second {
		captureBtn := findButton("Capture GPU workload")
		stopBtn := findButton("Stop GPU workload")

		captureEnabled := captureBtn != 0 && IsElementEnabled(captureBtn)
		stopEnabled := stopBtn != 0 && IsElementEnabled(stopBtn)

		verboseLog("waitForReplayComplete: checking start state - Capture=%v(enabled=%v) Stop=%v(enabled=%v)",
			captureBtn != 0, captureEnabled, stopBtn != 0, stopEnabled)

		// Profiling started if: Capture is disabled OR Stop GPU workload is enabled
		if (captureBtn != 0 && !captureEnabled) || stopEnabled {
			profilingStarted = true
			verboseLog("waitForReplayComplete: profiling started")
			break
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
		showPerfBtn := findButton("Show Performance")
		if showPerfBtn != 0 && IsElementEnabled(showPerfBtn) {
			verboseLog("waitForReplayComplete: Show Performance button found - complete")
			return nil
		}

		// 2. Export button appears (indicates performance data is ready)
		exportBtn := findButton("Export")
		if exportBtn != 0 && IsElementEnabled(exportBtn) {
			verboseLog("waitForReplayComplete: Export button found - complete")
			return nil
		}

		// 3. Stop GPU workload button is disabled/absent AND Capture is enabled
		captureBtn := findButton("Capture GPU workload")
		stopBtn := findButton("Stop GPU workload")
		captureEnabled := captureBtn != 0 && IsElementEnabled(captureBtn)
		stopEnabled := stopBtn != 0 && IsElementEnabled(stopBtn)

		if !stopEnabled && captureEnabled {
			// Additional check: wait for Export or Show Performance to appear
			// before declaring complete (prevents early false positive)
			time.Sleep(2 * time.Second)
			exportBtn = findButton("Export")
			showPerfBtn = findButton("Show Performance")
			if exportBtn != 0 || showPerfBtn != 0 {
				verboseLog("waitForReplayComplete: Stop disabled, Capture enabled, Export/ShowPerf available - complete")
				return nil
			}
			// If still no Export button, continue waiting
			verboseLog("waitForReplayComplete: Stop disabled, Capture enabled but no Export yet")
		}

		elapsed := time.Since(start).Seconds()
		status := "running"
		if stopBtn == 0 {
			status = "no stop button"
		} else if !stopEnabled {
			status = "stop disabled"
		}

		// Only print if status changed
		if status != lastStatus {
			fmt.Printf("    Profiling... (%.0fs, status: %s)\n", elapsed, status)
			lastStatus = status
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for replay completion")
}

func exportTrace(appAX, windowAX uintptr, outputPath string) error {
	// Try clicking Export button in Summary panel first
	exportBtn := FindExportButton(windowAX)
	if exportBtn != 0 {
		fmt.Println("    Found Export button in Summary panel")
		if err := axAction(exportBtn, "AXPress"); err != nil {
			fmt.Printf("    Warning: Failed to click Export button: %v\n", err)
		}
	} else {
		// Fall back to menu
		if err := debugCheckExportMenu(appAX); err != nil {
			fmt.Printf("    Debug: Export menu check failed: %v\n", err)
		}
		if err := ClickMenuItem(appAX, []string{"File", "Export..."}); err != nil {
			return fmt.Errorf("failed to click Export menu: %w", err)
		}
	}

	fmt.Println("    Waiting for export sheet...")
	sheetFound := false

	// Wait for Save button to appear (indicates sheet is showing)
	for i := 0; i < 30; i++ {
		saveBtn := findButtonBFS(windowAX, "Save", 500)
		if saveBtn != 0 {
			sheetFound = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !sheetFound {
		fmt.Printf("    Debug: Window children roles: %v\n", getRoles(windowAX))
		fmt.Printf("    Debug: App children roles: %v\n", getRoles(appAX))
		return fmt.Errorf("export sheet did not appear")
	}

	fmt.Println("    Export sheet detected")

	// Check "Embed performance data" checkbox if not already checked
	embedCheckbox := findCheckboxByName(windowAX, "Embed performance data")
	if embedCheckbox != 0 {
		if !IsCheckboxChecked(embedCheckbox) {
			fmt.Println("    Enabling 'Embed performance data'")
			axAction(embedCheckbox, "AXPress")
			time.Sleep(300 * time.Millisecond)
		}
	}

	outputDir := filepath.Dir(outputPath)
	outputName := filepath.Base(outputPath)

	if collectProfileDebug {
		DebugTextFields(windowAX)
	}

	// Get app reference to search all windows
	freshApp, _ := FindXcodeApp()
	if freshApp != 0 {
		defer cfRelease(freshApp)
	}

	// Helper to find element across all windows
	findInAllWindows := func(finder func(uintptr) uintptr) uintptr {
		if freshApp == 0 {
			return finder(windowAX)
		}
		windows := GetAllWindows(freshApp)
		for _, w := range windows {
			if el := finder(w); el != 0 {
				return el
			}
		}
		return 0
	}

	// Try to navigate to the output directory using Cmd+Shift+G
	navigatedToDir := false
	if outputDir != "" && outputDir != "." {
		fmt.Printf("    Navigating to directory: %s\n", outputDir)
		if err := NavigateToFolderInSaveDialog(windowAX, outputDir); err != nil {
			verboseLog("exportTrace: directory navigation failed: %v", err)
			// Navigation failed - this is common with Xcode dialogs
			// Continue with just setting the filename
		} else {
			navigatedToDir = true
			verboseLog("exportTrace: navigated to directory successfully")
		}
	}

	// Find the "Save As" text field across all windows and set the filename
	saveNameField := findInAllWindows(FindSaveAsTextField)
	fmt.Printf("    Setting filename: %s\n", outputName)
	if saveNameField != 0 {
		if err := axSetValue(saveNameField, outputName); err != nil {
			fmt.Printf("    Warning: SetValue failed: %v (using default filename)\n", err)
		} else if collectProfileDebug {
			fmt.Printf("    [DEBUG] Set filename via AX (saveAsNameTextField)\n")
		}
	} else {
		fmt.Println("    Warning: saveAsNameTextField not found (using default filename)")
	}
	time.Sleep(300 * time.Millisecond)

	// Click Save button - search all windows
	fmt.Println("    Saving...")
	saveBtn := findInAllWindows(func(w uintptr) uintptr {
		return findButtonBFS(w, "Save", 1000)
	})
	if saveBtn != 0 {
		if err := axAction(saveBtn, "AXPress"); err != nil {
			if collectProfileDebug {
				fmt.Printf("    [DEBUG] Save click failed: %v\n", err)
			}
		}
	} else if collectProfileDebug {
		fmt.Println("    [DEBUG] Save button not found")
	}

	// Wait for save to complete
	time.Sleep(2 * time.Second)

	// Check if file was saved to expected location
	if _, err := os.Stat(outputPath); err == nil {
		return nil // File found at expected path
	}

	// If we didn't navigate, suggest where the file might be
	if !navigatedToDir {
		verboseLog("exportTrace: file not at %s, may be in Xcode's default export location", outputPath)
	}

	return nil
}

