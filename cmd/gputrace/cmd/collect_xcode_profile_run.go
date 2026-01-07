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

	windowAX, err := waitForWindow(appAX, 30*time.Second)
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

	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf(Colorize("\nDone! Output saved to: %s\n", ColorGreen), outputPath)
	} else {
		fmt.Print(Colorize("\nNote: Output file not found at expected location.\n", ColorYellow))
		fmt.Printf("Check Xcode for the exported file.\n")
	}
	return nil
}


func waitForWindow(appAX uintptr, timeout time.Duration) (uintptr, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		windowAX := GetFirstWindow(appAX)
		if windowAX != 0 {
			return windowAX, nil
		}
		time.Sleep(1 * time.Second)
	}
	return 0, fmt.Errorf("could not find main Xcode window")
}

func clickReplayButton(windowAX uintptr) error {
	// First, try to find a "Profile" button (preferred - starts profiling directly)
	profileBtn := findButtonBFS(windowAX, "Profile", 500)
	if profileBtn != 0 && IsElementEnabled(profileBtn) {
		if err := axAction(profileBtn, "AXPress"); err != nil {
			return fmt.Errorf("failed to click Profile button: %w", err)
		}
		fmt.Println("    Clicked Profile button successfully")
		return nil
	}

	// Check for Stop button (replay already in progress)
	if FindStopButton(windowAX) != 0 {
		fmt.Println("    (Stop button found, replay already in progress?)")
		return nil
	}

	// Try Replay button
	replayBtn := FindReplayButton(windowAX)
	if replayBtn != 0 && IsElementEnabled(replayBtn) {
		if err := axAction(replayBtn, "AXPress"); err != nil {
			return fmt.Errorf("failed to click Replay button: %w", err)
		}
		fmt.Println("    Clicked Replay button successfully")
		return nil
	}

	// Try "Run" button as fallback (newer Xcode versions may use this name)
	// Look for Run button near GPU trace controls
	runBtn := FindRunButton(windowAX)
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
		replayBtn = FindReplayButton(windowAX)
		if replayBtn != 0 && IsElementEnabled(replayBtn) {
			if err := axAction(replayBtn, "AXPress"); err != nil {
				return fmt.Errorf("failed to click Replay button: %w", err)
			}
			fmt.Println("    Clicked Replay button successfully")
			return nil
		}
		runBtn = FindRunButton(windowAX)
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
	for time.Since(start) < timeout {
		// Check for completion indicators:
		// 1. Replay button is enabled (can replay again)
		rBtn := FindReplayButton(windowAX)
		if rBtn != 0 && IsElementEnabled(rBtn) {
			verboseLog("waitForReplayComplete: Replay button enabled - complete")
			return nil
		}

		// 2. Show Performance button appears (profiling complete, ready to view)
		showPerfBtn := FindShowPerformanceButton(windowAX)
		if showPerfBtn != 0 && IsElementEnabled(showPerfBtn) {
			verboseLog("waitForReplayComplete: Show Performance button found - complete")
			return nil
		}

		// 3. No Stop button means replay finished (fallback check)
		stopBtn := FindStopButton(windowAX)
		if stopBtn == 0 {
			// Double-check by looking for Export button
			exportBtn := FindExportButton(windowAX)
			if exportBtn != 0 {
				verboseLog("waitForReplayComplete: No Stop button, Export available - complete")
				return nil
			}
		}

		elapsed := time.Since(start).Seconds()
		status := "running"
		if stopBtn == 0 {
			status = "no stop button"
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

	// Set the filename using AX APIs
	// Note: This saves to whatever directory the save dialog is currently showing
	// For full path control, the user should specify the output path and we'll set just the basename
	outputName := filepath.Base(outputPath)

	if collectProfileDebug {
		DebugTextFields(windowAX)
	}

	// Find the "Save As" text field by identifier (saveAsNameTextField)
	// Note: We only set the basename - macOS save dialogs don't accept full paths in the filename field
	saveNameField := FindSaveAsTextField(windowAX)
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

	// Click Save button - get completely fresh app and window references
	fmt.Println("    Saving...")
	freshApp, err := FindXcodeApp()
	if err != nil {
		if collectProfileDebug {
			fmt.Printf("    [DEBUG] Could not get fresh app: %v\n", err)
		}
	} else {
		defer cfRelease(freshApp)
		freshWindow := GetFirstWindow(freshApp)
		if freshWindow != 0 {
			saveBtn := findButtonBFS(freshWindow, "Save", 1000)
			if saveBtn != 0 {
				if err := axAction(saveBtn, "AXPress"); err != nil {
					if collectProfileDebug {
						fmt.Printf("    [DEBUG] Save click failed: %v\n", err)
					}
				}
			} else if collectProfileDebug {
				fmt.Println("    [DEBUG] Save button not found")
			}
		} else if collectProfileDebug {
			fmt.Println("    [DEBUG] Could not get fresh window")
		}
	}

	time.Sleep(2 * time.Second)
	return nil
}

