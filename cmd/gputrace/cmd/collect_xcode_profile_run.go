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

	// Get app reference to search all windows (Run button may be in toolbar, not document window)
	appAX, _ := FindXcodeApp()
	if appAX != 0 {
		defer cfRelease(appAX)
	}

	// Helper to search all windows for a button
	findButtonInAllWindows := func(name string) uintptr {
		if appAX == 0 {
			return findButtonBFS(windowAX, name, 500)
		}
		windows := GetAllWindows(appAX)
		for _, w := range windows {
			if btn := findButtonBFS(w, name, 500); btn != 0 {
				return btn
			}
		}
		return 0
	}

	// First, try to find a "Profile" button (preferred - starts profiling directly)
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

	// Try "Run" button as fallback (newer Xcode versions may use this name)
	runBtn := findButtonInAllWindows("Run")
	verboseLog("clickReplayButton: Run button=%d enabled=%v", runBtn, runBtn != 0 && IsElementEnabled(runBtn))
	if runBtn != 0 && IsElementEnabled(runBtn) {
		if err := axAction(runBtn, "AXPress"); err != nil {
			return fmt.Errorf("failed to click Run button: %w", err)
		}
		fmt.Println("    Clicked Run button successfully")
		return nil
	}

	// Retry a few times
	for i := 0; i < 5; i++ {
		time.Sleep(1 * time.Second)
		replayBtn = findButtonInAllWindows("Replay")
		if replayBtn != 0 && IsElementEnabled(replayBtn) {
			if err := axAction(replayBtn, "AXPress"); err != nil {
				return fmt.Errorf("failed to click Replay button: %w", err)
			}
			fmt.Println("    Clicked Replay button successfully")
			return nil
		}
		runBtn = findButtonInAllWindows("Run")
		if runBtn != 0 && IsElementEnabled(runBtn) {
			if err := axAction(runBtn, "AXPress"); err != nil {
				return fmt.Errorf("failed to click Run button: %w", err)
			}
			fmt.Println("    Clicked Run button successfully")
			return nil
		}
	}

	return fmt.Errorf("Replay/Run button not found in AX tree")
}

func waitForReplayComplete(windowAX uintptr, timeout time.Duration) error {
	start := time.Now()

	// Get app reference to search all windows (buttons may be in different windows)
	appAX, _ := FindXcodeApp()
	if appAX != 0 {
		defer cfRelease(appAX)
	}

	// Helper to find a button across all windows
	findButtonInAllWindows := func(name string) uintptr {
		if appAX == 0 {
			return findButtonBFS(windowAX, name, 500)
		}
		windows := GetAllWindows(appAX)
		for _, w := range windows {
			if btn := findButtonBFS(w, name, 500); btn != 0 {
				return btn
			}
		}
		return 0
	}

	// First, wait for profiling to actually start (Run button disabled OR Stop button enabled)
	profilingStarted := false
	for time.Since(start) < 30*time.Second {
		runBtn := findButtonInAllWindows("Run")
		stopBtn := findButtonInAllWindows("Stop GPU workload")

		// Profiling started if: Run is disabled OR Stop GPU workload is enabled
		if (runBtn != 0 && !IsElementEnabled(runBtn)) || (stopBtn != 0 && IsElementEnabled(stopBtn)) {
			profilingStarted = true
			verboseLog("waitForReplayComplete: profiling started (Run disabled or Stop enabled)")
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !profilingStarted {
		verboseLog("waitForReplayComplete: WARNING - could not confirm profiling started")
		// Continue anyway, maybe the state changed too quickly
	}

	// Now wait for profiling to complete
	for time.Since(start) < timeout {
		// Check for completion indicators:
		// 1. Replay/Run button is enabled (can replay again)
		rBtn := findButtonInAllWindows("Replay")
		if rBtn != 0 && IsElementEnabled(rBtn) {
			verboseLog("waitForReplayComplete: Replay button enabled - complete")
			return nil
		}
		runBtn := findButtonInAllWindows("Run")
		if runBtn != 0 && IsElementEnabled(runBtn) {
			verboseLog("waitForReplayComplete: Run button enabled - complete")
			return nil
		}

		// 2. Show Performance button appears (profiling complete, ready to view)
		showPerfBtn := findButtonInAllWindows("Show Performance")
		if showPerfBtn != 0 && IsElementEnabled(showPerfBtn) {
			verboseLog("waitForReplayComplete: Show Performance button found - complete")
			return nil
		}

		// 3. Stop GPU workload button is disabled or absent means replay finished
		stopBtn := findButtonInAllWindows("Stop GPU workload")
		if stopBtn == 0 || !IsElementEnabled(stopBtn) {
			// Double-check by looking for Export button
			exportBtn := findButtonInAllWindows("Export")
			if exportBtn != 0 {
				verboseLog("waitForReplayComplete: Stop button disabled/absent, Export available - complete")
				return nil
			}
		}

		elapsed := time.Since(start).Seconds()
		status := "running"
		if stopBtn == 0 {
			status = "no stop button"
		} else if !IsElementEnabled(stopBtn) {
			status = "stop button disabled"
		}
		time.Sleep(2 * time.Second)
		fmt.Printf("    Still waiting... (%.0fs, status: %s)\n", elapsed, status)
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

