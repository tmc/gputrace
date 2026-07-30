//go:build darwin

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	gputraceTrace "github.com/tmc/gputrace/internal/trace"
	"github.com/tmc/gputrace/internal/tracebundle"
)

type standaloneExportRecovery struct {
	Enabled    bool
	CheckOnly  bool
	SourcePath string
	SourceUUID string
	Identity   xcodeProcessIdentity
}

type standaloneRecoveryWindow struct {
	xcodeAXWindow
	PID             int
	PerformanceView bool
}

type depthElement struct {
	Element uintptr
	Depth   int
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

	recovery, err := standaloneExportRecoveryFromFlags(cmd)
	if err != nil {
		return err
	}

	var appAX uintptr
	var identity xcodeProcessIdentity
	if recovery.Enabled {
		identity = recovery.Identity
		appAX, err = reacquireXcodeApp(identity)
		if err != nil {
			return fmt.Errorf("cannot establish recovery Xcode selection: %w", err)
		}
		fmt.Fprintf(status, "Recovering source: %s\n", recovery.SourcePath)
		fmt.Fprintf(status, "Source trace UUID: %s\n", recovery.SourceUUID)
		fmt.Fprintf(status, "Bound Xcode: PID %d app %s\n", identity.PID, identity.AppPath)
		fmt.Fprintln(status, "Recovery requires a shallow Performance group; Stop/activity progress is advisory")
	} else {
		requestedApp := requestedXcodeAppPath()
		appAX, identity, err = findSingleXcodeApp(cmd.Context(), requestedApp, 10*time.Second)
		if err != nil {
			return fmt.Errorf("cannot establish exact Xcode selection: %w", err)
		}
	}
	defer cfRelease(appAX)

	var windowAX uintptr
	var doc string
	if recovery.Enabled {
		windowAX, err = waitForStandaloneRecoveryWindow(cmd.Context(), appAX, recovery, 10*time.Second)
		if err != nil {
			return err
		}
		doc = recovery.SourcePath
	} else {
		windowAX, doc, err = waitForStandaloneExportWindow(cmd.Context(), appAX, identity, 10*time.Second)
		if err != nil {
			return err
		}
	}

	if err := requireStandaloneExportTarget(doc); err != nil {
		return err
	}
	if recovery.CheckOnly {
		fmt.Fprintln(status, "Recovery target verified; no UI action performed")
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action:           "check_recovery",
			Target:           recovery.SourcePath,
			Source:           recovery.SourcePath,
			SourceUUID:       recovery.SourceUUID,
			XcodePID:         recovery.Identity.PID,
			XcodeApp:         recovery.Identity.AppPath,
			Phase:            "untitled Performance window verified",
			Evidence:         "exact PID/app and shallow Performance group stable across two samples",
			TargetBound:      boolPointer(true),
			SelectedTitle:    "",
			SelectedDocument: "",
		})
	}
	// If no output path specified, try to infer from window document
	if outputPath == "" {
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

	if err := exportTrace(cmd.Context(), appAX, windowAX, outputPath); err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	candidates := exportCandidatePaths(doc, outputPath)
	finalPath, err := waitForExportedTrace(cmd.Context(), []string{outputPath}, exportWaitTimeout())
	if err != nil {
		if alternates := existingExportCandidates(candidates, outputPath); len(alternates) > 0 {
			return fmt.Errorf("export did not appear at requested location %s; Xcode wrote candidate output at %s; preserving it for recovery: %w",
				outputPath, strings.Join(alternates, ", "), err)
		}
		return err
	}
	payload, err := finalizeStandaloneExport(status, doc, finalPath)
	if err != nil {
		return err
	}
	if err := requireExportedTrace(outputPath); err != nil {
		return err
	}
	fmt.Fprintf(status, Colorize("Exported to: %s\n", ColorGreen), outputPath)
	actionOutput := xcodeProfileActionOutput{
		Action: "export",
		Target: doc,
		Output: outputPath,
	}
	if recovery.Enabled {
		actionOutput.Source = recovery.SourcePath
		actionOutput.SourceUUID = recovery.SourceUUID
		actionOutput.XcodePID = recovery.Identity.PID
		actionOutput.XcodeApp = recovery.Identity.AppPath
		actionOutput.Evidence = "explicit untitled-window recovery; exported UUID verified against source"
		actionOutput.TargetBound = boolPointer(true)
	}
	applyXcodePayload(&actionOutput, payload)
	return writeXcodeProfileActionOutput(actionOutput)
}

func standaloneExportRecoveryFromFlags(cmd *cobra.Command) (standaloneExportRecovery, error) {
	enabled, _ := cmd.Flags().GetBool("recover-untitled")
	checkOnly, _ := cmd.Flags().GetBool("check-recovery")
	source, _ := cmd.Flags().GetString("source")
	pid, _ := cmd.Flags().GetInt("xcode-pid")
	app, _ := cmd.Flags().GetString("xcode-app")

	any := enabled || checkOnly || source != "" || pid != 0 || app != ""
	if !any {
		return standaloneExportRecovery{}, nil
	}
	if !enabled || source == "" || pid <= 0 || app == "" {
		return standaloneExportRecovery{}, fmt.Errorf(
			"untitled recovery requires --recover-untitled, --source, --xcode-pid, and --xcode-app",
		)
	}
	if !filepath.IsAbs(app) {
		return standaloneExportRecovery{}, fmt.Errorf("--xcode-app must be an absolute .app path")
	}
	app = filepath.Clean(app)
	if !strings.HasSuffix(strings.ToLower(app), ".app") {
		return standaloneExportRecovery{}, fmt.Errorf("--xcode-app must name an .app bundle")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return standaloneExportRecovery{}, fmt.Errorf("resolve recovery source: %w", err)
	}
	source = filepath.Clean(source)
	if !strings.HasSuffix(strings.ToLower(source), ".gputrace") {
		return standaloneExportRecovery{}, fmt.Errorf("--source must name a .gputrace bundle")
	}
	payload, err := tracebundle.InspectPayload(source)
	if err != nil {
		return standaloneExportRecovery{}, fmt.Errorf("inspect recovery source: %w", err)
	}
	if payload.Class != tracebundle.PayloadFull {
		return standaloneExportRecovery{}, fmt.Errorf("recovery source is not self-contained: %s", source)
	}
	metadata, err := gputraceTrace.ReadMetadata(source)
	if err != nil {
		return standaloneExportRecovery{}, fmt.Errorf("read recovery source metadata: %w", err)
	}
	if metadata.UUID == "" {
		return standaloneExportRecovery{}, fmt.Errorf("recovery source has no trace UUID: %s", source)
	}
	identity := xcodeProcessIdentity{PID: pid, AppPath: app, BundleID: "com.apple.dt.Xcode"}
	if err := validateStandaloneRecoveryIdentity(identity, xcodeProcessPath(pid)); err != nil {
		return standaloneExportRecovery{}, err
	}
	return standaloneExportRecovery{
		Enabled:    true,
		CheckOnly:  checkOnly,
		SourcePath: source,
		SourceUUID: metadata.UUID,
		Identity:   identity,
	}, nil
}

func validateStandaloneRecoveryIdentity(identity xcodeProcessIdentity, actualApp string) error {
	actualApp = filepath.Clean(actualApp)
	if identity.PID <= 0 || actualApp != filepath.Clean(identity.AppPath) {
		return fmt.Errorf("Xcode PID %d runs from %s, not requested app %s",
			identity.PID, actualApp, identity.AppPath)
	}
	return nil
}

func standaloneExportTarget(windows []xcodeAXWindow) (uintptr, string, error) {
	var matches []xcodeAXWindow
	for _, window := range windows {
		doc := filepath.Clean(strings.TrimSpace(window.Document))
		if doc == "." || !filepath.IsAbs(doc) || !strings.HasSuffix(strings.ToLower(doc), ".gputrace") {
			continue
		}
		matches = append(matches, window)
	}
	switch len(matches) {
	case 0:
		return 0, "", fmt.Errorf("cannot establish standalone export target: no AXDocument-bound .gputrace window")
	case 1:
		return matches[0].Element, matches[0].Document, nil
	default:
		var docs []string
		for _, match := range matches {
			docs = append(docs, match.Document)
		}
		return 0, "", fmt.Errorf("cannot establish standalone export target: multiple .gputrace windows are open: %s",
			strings.Join(docs, ", "))
	}
}

func waitForStandaloneExportWindow(ctx context.Context, appAX uintptr, identity xcodeProcessIdentity, timeout time.Duration) (uintptr, string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		bound, err := xcodeIdentityForAX(appAX)
		if err != nil || bound.PID != identity.PID || filepath.Clean(bound.AppPath) != filepath.Clean(identity.AppPath) {
			return 0, "", fmt.Errorf("standalone export lost exact Xcode PID/app binding: want PID %d app %s",
				identity.PID, identity.AppPath)
		}
		window, doc, err := standaloneExportTarget(deduplicateAXWindows(GetAllWindows(appAX)))
		if err == nil {
			var pid int32
			if axUIElementGetPid(window, &pid) != kAXErrorSuccess || int(pid) != identity.PID {
				return 0, "", fmt.Errorf("standalone export target window is not owned by bound Xcode PID %d", identity.PID)
			}
			return window, doc, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return 0, "", fmt.Errorf("standalone export target not established for PID %d app %s: %w",
				identity.PID, identity.AppPath, lastErr)
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return 0, "", err
		}
	}
}

func standaloneRecoveryTarget(windows []standaloneRecoveryWindow, recovery standaloneExportRecovery) (standaloneRecoveryWindow, error) {
	var matches []standaloneRecoveryWindow
	seen := make(map[uintptr]bool)
	for _, window := range windows {
		if window.PID != recovery.Identity.PID || window.Document != "" ||
			strings.TrimSpace(window.Title) != "" || !window.PerformanceView {
			continue
		}
		if seen[window.Element] {
			continue
		}
		seen[window.Element] = true
		matches = append(matches, window)
	}
	switch len(matches) {
	case 0:
		return standaloneRecoveryWindow{}, fmt.Errorf("no untitled Performance window is bound to Xcode PID %d app %s",
			recovery.Identity.PID, recovery.Identity.AppPath)
	case 1:
		return matches[0], nil
	default:
		var elements []string
		for _, match := range matches {
			elements = append(elements, fmt.Sprintf("%d", match.Element))
		}
		return standaloneRecoveryWindow{}, fmt.Errorf("multiple untitled Performance windows are ambiguous for Xcode PID %d app %s: AX elements %s",
			recovery.Identity.PID, recovery.Identity.AppPath, strings.Join(elements, ", "))
	}
}

func standaloneRecoveryWindowKey(window standaloneRecoveryWindow) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%d,%d,%d,%d",
		window.PID,
		strings.TrimSpace(window.Title),
		filepath.Clean(window.Document),
		window.X,
		window.Y,
		window.Width,
		window.Height,
	)
}

func recoveryWindows(appAX uintptr) []standaloneRecoveryWindow {
	windows := deduplicateAXWindows(GetAllWindows(appAX))
	out := make([]standaloneRecoveryWindow, 0, len(windows))
	for _, window := range windows {
		var pid int32
		if axUIElementGetPid(window.Element, &pid) != kAXErrorSuccess {
			continue
		}
		out = append(out, standaloneRecoveryWindow{
			xcodeAXWindow:   window,
			PID:             int(pid),
			PerformanceView: hasShallowPerformanceGroup(window.Element),
		})
	}
	return out
}

func hasShallowPerformanceGroup(root uintptr) bool {
	return findElementAtDepth(
		root,
		4,
		128,
		axChildren,
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXOutline"
		},
		func(element uintptr) bool {
			role := axString(element, "AXRole")
			description := strings.TrimSpace(axString(element, "AXDescription"))
			return (role == "AXGroup" || role == "AXSplitGroup") && description == "Performance"
		},
	) != 0
}

func findElementAtDepth(
	root uintptr,
	maxDepth, maxVisit int,
	children func(uintptr) []uintptr,
	prune, match func(uintptr) bool,
) uintptr {
	if root == 0 || maxDepth < 0 || maxVisit <= 0 {
		return 0
	}
	queue := []depthElement{{Element: root}}
	seen := make(map[uintptr]bool)
	visited := 0
	for len(queue) > 0 && visited < maxVisit {
		item := queue[0]
		queue = queue[1:]
		if item.Element == 0 || seen[item.Element] {
			continue
		}
		seen[item.Element] = true
		visited++
		if match(item.Element) {
			return item.Element
		}
		if item.Depth >= maxDepth || prune(item.Element) {
			continue
		}
		for _, child := range children(item.Element) {
			queue = append(queue, depthElement{Element: child, Depth: item.Depth + 1})
		}
	}
	return 0
}

func waitForStandaloneRecoveryWindow(ctx context.Context, appAX uintptr, recovery standaloneExportRecovery, timeout time.Duration) (uintptr, error) {
	deadline := time.Now().Add(timeout)
	var lastKey string
	stable := 0
	var lastErr error
	for {
		bound, err := xcodeIdentityForAX(appAX)
		if err != nil || bound.PID != recovery.Identity.PID ||
			filepath.Clean(bound.AppPath) != filepath.Clean(recovery.Identity.AppPath) {
			return 0, fmt.Errorf("untitled recovery lost exact Xcode PID/app binding: want PID %d app %s",
				recovery.Identity.PID, recovery.Identity.AppPath)
		}
		window, err := standaloneRecoveryTarget(recoveryWindows(appAX), recovery)
		if err == nil {
			key := standaloneRecoveryWindowKey(window)
			if key == lastKey {
				stable++
			} else {
				lastKey = key
				stable = 1
				lastErr = fmt.Errorf("untitled Performance window identity is not yet stable")
			}
			if stable >= 2 {
				return window.Element, nil
			}
		} else {
			lastKey = ""
			stable = 0
			lastErr = err
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("untitled recovery target not established: %w", lastErr)
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return 0, err
		}
	}
}

func finalizeStandaloneExport(w io.Writer, targetPath, outputPath string) (tracebundle.Payload, error) {
	if err := requireStandaloneExportTarget(targetPath); err != nil {
		return tracebundle.Payload{}, err
	}
	if err := verifyExportTraceIdentity(targetPath, outputPath); err != nil {
		return tracebundle.Payload{}, err
	}
	payload, err := tracebundle.InspectPayload(outputPath)
	if err != nil {
		return tracebundle.Payload{}, fmt.Errorf("inspect exported trace payload: %w", err)
	}
	writeXcodePayloadStatus(w, payload)
	if err := requireSelfContainedExport(outputPath, payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func requireStandaloneExportTarget(targetPath string) error {
	if targetPath != "" {
		return nil
	}
	return fmt.Errorf(
		"cannot verify standalone export identity: selected Xcode window has no AXDocument binding; use a combined xp run or explicitly bind the source trace before export",
	)
}

func existingExportCandidates(candidates []string, requested string) []string {
	var found []string
	requested = filepath.Clean(requested)
	for _, candidate := range uniquePaths(candidates) {
		if filepath.Clean(candidate) == requested {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			found = append(found, candidate)
		}
	}
	return found
}

func requireExportedTrace(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("export completed but output not found at expected location %s: %w", path, err)
	}
	return nil
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

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
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
	freshWindow, err := findTargetWindow(cmd.Context(), appAX, "")
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

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
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

func runSendKey(cmd *cobra.Command, args []string) error {
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
	status := xcodeProfileStatusWriter()
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
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

func runDebugFileBrowser(cmd *cobra.Command, args []string) error {
	status := xcodeProfileStatusWriter()
	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
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

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
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

	windowAX, err := findTargetWindow(cmd.Context(), appAX, "")
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
