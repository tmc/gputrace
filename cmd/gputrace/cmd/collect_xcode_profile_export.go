package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	exportCmd := &cobra.Command{
		Use:   "export <output_path>",
		Short: "Export the trace from Xcode",
		Long: `Triggers File > Export in Xcode and saves to the specified path.
If no path is specified, it defaults to the trace file path with -perfdata suffix, inferred from the Xcode window.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runExport,
	}
	collectXcodeProfileCmd.AddCommand(exportCmd)

	openExportCmd := &cobra.Command{
		Use:    "open-export [output_path]",
		Short:  "Open the export dialog and set the output path",
		Hidden: true,
		Long: `Opens the Export dialog in Xcode and sets the output path.

If output_path is specified, navigates to that directory and sets the filename.
If no path specified, uses the original trace name with -perfdata suffix.

Examples:
  gputrace xp open-export                              # Uses -perfdata suffix in current dialog location
  gputrace xp open-export /tmp/my-trace.gputrace       # Navigates to /tmp and sets filename`,
		Args: cobra.MaximumNArgs(1),
		RunE: runOpenExport,
	}
	collectXcodeProfileCmd.AddCommand(openExportCmd)
}

func rejectXcodeProfileJSON(command string) error {
	if !collectProfileJSON {
		return nil
	}
	return fmt.Errorf("%s does not support --json", command)
}

func runExport(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	var outputPath string
	if len(args) > 0 {
		var err error
		outputPath, err = resolveXcodeProfileTraceOutputPath(args[0])
		if err != nil {
			return err
		}
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	// Try AX-based approach first
	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("AX not available: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := waitForWindow(appAX, "", 10*time.Second)
	if err != nil {
		return fmt.Errorf("Xcode window not found: %w", err)
	}

	doc := axString(windowAX, "AXDocument")
	// If no output path specified, try to infer from window document
	if outputPath == "" {
		if doc == "" {
			return fmt.Errorf("output path not specified and could not be inferred from Xcode window (AXDocument empty)")
		}
		// e.g. /path/to/trace.gputrace -> /path/to/trace-perfdata.gputrace
		ext := filepath.Ext(doc) // .gputrace
		if ext == "" {
			ext = ".gputrace"
		}
		base := strings.TrimSuffix(doc, ext)
		outputPath = base + "-perfdata" + ext
		fmt.Fprintf(status, "Inferred output path: %s\n", outputPath)
	}

	fmt.Fprintf(status, "Exporting trace to: %s\n", outputPath)
	if doc != "" {
		verboseLog("runExport: window AXDocument=%q", doc)
	}

	if err := exportTrace(appAX, windowAX, outputPath); err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	warning := ""
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Fprintf(status, Colorize("Exported to: %s\n", ColorGreen), outputPath)
	} else {
		warning = "output file not found at expected location"
		fmt.Fprint(status, Colorize("Note: Output file not found at expected location.\n", ColorYellow))
	}
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action:  "export",
		Target:  doc,
		Output:  outputPath,
		Warning: warning,
	})
}

// isExportDialogOpen checks if an export/save dialog is already open on the window.
func isExportDialogOpen(window uintptr) bool {
	saveBtn := findButtonBFS(window, "Save", 500) // Export sheet is shallow
	return saveBtn != 0
}

func runOpenExport(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	var outputPath string
	if len(args) > 0 {
		var err error
		outputPath, err = resolveXcodeProfileTraceOutputPath(args[0])
		if err != nil {
			return err
		}
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}
	target := axString(windowAX, "AXDocument")
	var outputFilename string
	var warning string
	if outputPath != "" {
		outputFilename = filepath.Base(outputPath)
	}
	writeOutput := func() error {
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action:          "open-export",
			Target:          target,
			Output:          outputFilename,
			RequestedOutput: outputPath,
			Warning:         warning,
		})
	}

	// Check if export dialog is already open
	if isExportDialogOpen(windowAX) {
		fmt.Fprintln(status, "Export dialog already open")
	} else {
		fmt.Fprintln(status, "Opening export dialog...")

		// Try clicking Export button in Summary panel first
		exportBtn := FindExportButton(windowAX)
		if exportBtn != 0 {
			fmt.Fprintln(status, "  Found Export button in Summary panel")
			if err := axAction(exportBtn, "AXPress"); err != nil {
				return fmt.Errorf("failed to click Export button: %w", err)
			}
		} else {
			// Fall back to menu
			fmt.Fprintln(status, "  Using File > Export menu...")
			if err := ClickMenuItem(appAX, []string{"File", "Export..."}); err != nil {
				return fmt.Errorf("failed to click Export menu: %w", err)
			}
		}

		// Wait for dialog to appear
		fmt.Fprintln(status, "  Waiting for export sheet...")
		sheetFound := false
		for i := 0; i < 30; i++ {
			saveBtn := findButtonBFS(windowAX, "Save", 500) // Export sheet is shallow
			if saveBtn != 0 {
				sheetFound = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}

		if !sheetFound {
			return fmt.Errorf("export dialog did not appear")
		}

		fmt.Fprintln(status, "  Export dialog opened")

		// Check "Embed performance data" checkbox if not already checked
		embedCheckbox := findCheckboxByName(windowAX, "Embed performance data")
		if embedCheckbox != 0 {
			if !IsCheckboxChecked(embedCheckbox) {
				fmt.Fprintln(status, "  Enabling 'Embed performance data'")
				axAction(embedCheckbox, "AXPress")
				time.Sleep(300 * time.Millisecond)
			}
		}
	}

	// Re-fetch window to get fresh reference after sheet appeared
	// Use findTargetWindow to get the trace window, not just any window
	freshWindow, err := findTargetWindow(appAX, "")
	if err != nil || freshWindow == 0 {
		warning = "could not get window reference"
		fmt.Fprintln(status, "  Warning: could not get window reference")
		fmt.Fprint(status, Colorize("Export dialog ready. Use Save button to complete export.\n", ColorGreen))
		return writeOutput()
	}
	if doc := axString(freshWindow, "AXDocument"); doc != "" {
		target = doc
	}

	saveNameField := FindSaveAsTextField(freshWindow)
	if saveNameField == 0 {
		warning = "save as field not found"
		fmt.Fprintln(status, "  Warning: Save As field not found")
		fmt.Fprint(status, Colorize("Export dialog ready. Use Save button to complete export.\n", ColorGreen))
		return writeOutput()
	}

	if outputPath != "" {
		// User specified path - use just the basename
		// (folder navigation via Cmd+Shift+G isn't reliable, so we only set the filename)
		dir := filepath.Dir(outputPath)
		if dir != "." && dir != "/" {
			fmt.Fprintf(status, "  Note: Navigate to %s manually (folder navigation not yet supported)\n", dir)
		}
	} else {
		// Generate -perfdata suffix from current filename
		currentName := axString(saveNameField, "AXValue")
		if currentName != "" && strings.HasSuffix(currentName, ".gputrace") {
			// Don't add -perfdata if it's already there
			if strings.Contains(currentName, "-perfdata") {
				outputFilename = currentName // Keep as-is
			} else {
				ext := filepath.Ext(currentName)
				base := strings.TrimSuffix(currentName, ext)
				outputFilename = base + "-perfdata" + ext
			}
		}
	}

	// Set the filename (need to re-find the field after navigation)
	if outputFilename != "" {
		// Re-find the save field after navigation
		saveNameField = FindSaveAsTextField(freshWindow)
		if saveNameField != 0 {
			fmt.Fprintf(status, "  Setting filename: %s\n", outputFilename)
			if err := axSetValue(saveNameField, outputFilename); err != nil {
				warning = fmt.Sprintf("could not set filename: %v", err)
				fmt.Fprintf(status, "  Warning: could not set filename: %v\n", err)
			}
			// Focus out of the field to commit the value (Tab key)
			time.Sleep(200 * time.Millisecond)
			if err := axAction(saveNameField, "AXConfirm"); err != nil {
				// AXConfirm may not be supported, that's OK
			}
		}
	}

	fmt.Fprint(status, Colorize("Export dialog ready. Use Save button to complete export.\n", ColorGreen))
	return writeOutput()
}

func init() {
	clickSaveCmd := &cobra.Command{
		Use:    "click-save",
		Short:  "Click the Save button in an open export dialog",
		Hidden: true,
		RunE:   runClickSave,
	}
	collectXcodeProfileCmd.AddCommand(clickSaveCmd)
}

func runClickSave(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}
	target := axString(windowAX, "AXDocument")

	if !isExportDialogOpen(windowAX) {
		return fmt.Errorf("export dialog not open")
	}

	saveBtn := findButtonBFS(windowAX, "Save", 500) // Export sheet is shallow
	if saveBtn == 0 {
		return fmt.Errorf("Save button not found")
	}

	// Get the filename being saved
	filename := ""
	saveField := FindSaveAsTextField(windowAX)
	if saveField != 0 {
		filename = axString(saveField, "AXValue")
		fmt.Fprintf(status, "Saving: %s\n", filename)
	}

	fmt.Fprintln(status, "Clicking Save...")
	if err := axAction(saveBtn, "AXPress"); err != nil {
		return fmt.Errorf("failed to click Save: %w", err)
	}

	// Wait briefly for save to complete
	time.Sleep(2 * time.Second)
	fmt.Fprintln(status, "Export initiated")
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "click-save",
		Target: target,
		Output: filename,
	})
}

func init() {
	sendKeyCmd := &cobra.Command{
		Use:    "send-key <key>",
		Short:  "Send a keyboard shortcut (for debugging)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   runSendKey,
	}
	collectXcodeProfileCmd.AddCommand(sendKeyCmd)

	checkGoToFolderCmd := &cobra.Command{
		Use:    "check-goto-folder",
		Short:  "Check if Go to Folder dialog is open",
		Hidden: true,
		RunE:   runCheckGoToFolder,
	}
	collectXcodeProfileCmd.AddCommand(checkGoToFolderCmd)
}

func runSendKey(cmd *cobra.Command, args []string) error {
	if err := rejectXcodeProfileJSON("send-key"); err != nil {
		return err
	}
	status := xcodeProfileStatusWriter()
	if err := setupMacgo(); err != nil {
		return err
	}

	key := args[0]

	// Activate Xcode first
	fmt.Fprintln(status, "Activating Xcode...")
	if err := ActivateXcode(); err != nil {
		return fmt.Errorf("failed to activate Xcode: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	switch key {
	case "cmd-shift-g":
		fmt.Fprintln(status, "Sending Cmd+Shift+G...")
		if err := sendCmdShiftG(); err != nil {
			return fmt.Errorf("failed to send Cmd+Shift+G: %w", err)
		}
	case "escape":
		fmt.Fprintln(status, "Sending Escape...")
		if err := sendEscape(); err != nil {
			return fmt.Errorf("failed to send Escape: %w", err)
		}
	case "return":
		fmt.Fprintln(status, "Sending Return...")
		if err := sendReturn(); err != nil {
			return fmt.Errorf("failed to send Return: %w", err)
		}
	default:
		return fmt.Errorf("unknown key: %s (supported: cmd-shift-g, escape, return)", key)
	}

	fmt.Fprintln(status, "Key sent")
	return nil
}

func runCheckGoToFolder(cmd *cobra.Command, args []string) error {
	if err := rejectXcodeProfileJSON("check-goto-folder"); err != nil {
		return err
	}
	status := xcodeProfileStatusWriter()
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}

	// Look for "Go" button which indicates Go to Folder dialog
	goBtn := findButtonBFS(windowAX, "Go", 1000)
	if goBtn != 0 {
		fmt.Fprintln(status, "Go to Folder dialog: OPEN")
		// Try to find the path text field
		pathField := FindPathTextField(windowAX)
		if pathField != 0 {
			val := axString(pathField, "AXValue")
			fmt.Fprintf(status, "  Path field value: %q\n", val)
		}
	} else {
		fmt.Fprintln(status, "Go to Folder dialog: NOT OPEN")
	}

	// Also check for Save button to see overall dialog state
	saveBtn := findButtonBFS(windowAX, "Save", 500) // Export sheet is shallow
	if saveBtn != 0 {
		fmt.Fprintln(status, "Export dialog: OPEN")
		// Check if Save is enabled
		enabled := IsElementEnabled(saveBtn)
		fmt.Fprintf(status, "  Save button enabled: %v\n", enabled)
		// Show the save-as field value
		saveField := FindSaveAsTextField(windowAX)
		if saveField != 0 {
			val := axString(saveField, "AXValue")
			fmt.Fprintf(status, "  Filename: %q\n", val)
		} else {
			fmt.Fprintln(status, "  Filename field: NOT FOUND")
		}
		// Look for disclosure triangle or path control
		disclosure := findButtonBFS(windowAX, "disclosure", 500)
		if disclosure != 0 {
			fmt.Fprintln(status, "  Has disclosure button")
		}
		// Look for popup buttons (e.g., "Where" location selector)
		popup := findElement(windowAX, func(el uintptr) bool {
			role := axString(el, "AXRole")
			return role == "AXPopUpButton"
		})
		if popup != 0 {
			val := axString(popup, "AXValue")
			desc := axString(popup, "AXDescription")
			fmt.Fprintf(status, "  Popup button: value=%q desc=%q\n", val, desc)
		}
	} else {
		fmt.Fprintln(status, "Export dialog: NOT OPEN")
	}

	return nil
}

func init() {
	debugFileBrowserCmd := &cobra.Command{
		Use:    "debug-file-browser",
		Short:  "Debug: list file browser elements in export dialog",
		Hidden: true,
		RunE:   runDebugFileBrowser,
	}
	collectXcodeProfileCmd.AddCommand(debugFileBrowserCmd)
}

func runDebugFileBrowser(cmd *cobra.Command, args []string) error {
	if err := rejectXcodeProfileJSON("debug-file-browser"); err != nil {
		return err
	}
	status := xcodeProfileStatusWriter()
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}

	if !isExportDialogOpen(windowAX) {
		return fmt.Errorf("export dialog not open - use 'gputrace xp open-export' first")
	}

	fmt.Fprintln(status, "Scanning file browser elements...")
	fmt.Fprintln(status)

	// Look for browser/table/outline elements that might contain the file list
	count := 0
	findElement(windowAX, func(el uintptr) bool {
		role := axString(el, "AXRole")

		// Look for elements that might be file list items
		if role == "AXCell" || role == "AXRow" || role == "AXOutlineRow" ||
			role == "AXStaticText" || role == "AXTextField" || role == "AXGroup" ||
			role == "AXBrowser" || role == "AXTable" || role == "AXOutline" {

			title := axString(el, "AXTitle")
			value := axString(el, "AXValue")
			desc := axString(el, "AXDescription")
			identifier := axString(el, "AXIdentifier")

			// Only print if there's some content
			if title != "" || value != "" || desc != "" || identifier != "" {
				count++
				fmt.Fprintf(status, "[%d] Role=%s\n", count, role)
				if title != "" {
					fmt.Fprintf(status, "    Title: %q\n", title)
				}
				if value != "" {
					fmt.Fprintf(status, "    Value: %q\n", value)
				}
				if desc != "" {
					fmt.Fprintf(status, "    Desc: %q\n", desc)
				}
				if identifier != "" {
					fmt.Fprintf(status, "    ID: %q\n", identifier)
				}
				fmt.Fprintln(status)
			}
		}
		return false // Continue searching
	})

	fmt.Fprintf(status, "Found %d elements with content\n", count)
	return nil
}

func init() {
	setExportPathCmd := &cobra.Command{
		Use:    "set-export-path <absolute_path>",
		Short:  "Set the export path (note: directory navigation limited)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   runSetExportPath,
	}
	collectXcodeProfileCmd.AddCommand(setExportPathCmd)

	setFilenameCmd := &cobra.Command{
		Use:    "set-export-filename <filename>",
		Short:  "Set the export filename (recommended)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   runSetExportFilename,
	}
	collectXcodeProfileCmd.AddCommand(setFilenameCmd)

	sendEnterCmd := &cobra.Command{
		Use:    "send-enter",
		Short:  "Send Enter key to Xcode",
		Hidden: true,
		RunE:   runSendEnter,
	}
	collectXcodeProfileCmd.AddCommand(sendEnterCmd)
}

func runSetExportPath(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	absPath := args[0]
	if !filepath.IsAbs(absPath) {
		return fmt.Errorf("path must be absolute: %s", absPath)
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}
	target := axString(windowAX, "AXDocument")

	// Check export dialog is open
	if !isExportDialogOpen(windowAX) {
		return fmt.Errorf("export dialog not open")
	}

	// Strategy: Set the full path in the filename field
	// macOS will interpret paths with / as directory navigation
	saveField := FindSaveAsTextField(windowAX)
	if saveField == 0 {
		return fmt.Errorf("save-as field not found")
	}

	fmt.Fprintf(status, "Setting export path: %s\n", absPath)
	if err := axSetValue(saveField, absPath); err != nil {
		return fmt.Errorf("failed to set path: %w", err)
	}

	// Note: When setting a full path in macOS save dialogs,
	// the system converts "/" to ":" in the filename (HFS path separator)
	// The correct approach is to:
	// 1. Set just the filename
	// 2. Navigate to the directory separately

	// For now, we set the full path and note that manual intervention
	// may be needed for directory navigation since Cmd+Shift+G doesn't
	// work in Xcode's export dialog.

	dir := filepath.Dir(absPath)
	base := filepath.Base(absPath)

	fmt.Fprintf(status, "  Directory: %s\n", dir)
	fmt.Fprintf(status, "  Filename: %s\n", base)
	fmt.Fprintln(status, "  Note: Xcode export dialog doesn't support Cmd+Shift+G")
	fmt.Fprintln(status, "  If directory navigation is needed, set filename only and navigate manually")

	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action:          "set-export-path",
		Target:          target,
		Output:          absPath,
		RequestedOutput: absPath,
	})
}

func runSetExportFilename(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	filename := args[0]

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		return fmt.Errorf("window not found: %w", err)
	}
	target := axString(windowAX, "AXDocument")

	if !isExportDialogOpen(windowAX) {
		return fmt.Errorf("export dialog not open")
	}

	saveField := FindSaveAsTextField(windowAX)
	if saveField == 0 {
		return fmt.Errorf("save-as field not found")
	}

	fmt.Fprintf(status, "Setting filename: %s\n", filename)
	if err := axSetValue(saveField, filename); err != nil {
		return fmt.Errorf("failed to set filename: %w", err)
	}

	fmt.Fprintln(status, "Filename set")
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "set-export-filename",
		Target: target,
		Output: filename,
	})
}

func runSendEnter(cmd *cobra.Command, args []string) error {
	// No setupMacgo needed - we just use AppleScript
	status := xcodeProfileStatusWriter()

	fmt.Fprintln(status, "Sending Enter to Xcode...")
	if err := sendReturn(); err != nil {
		return fmt.Errorf("failed to send Enter: %w", err)
	}

	fmt.Fprintln(status, "Enter sent")
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "send-enter",
		Target: "xcode",
	})
}
