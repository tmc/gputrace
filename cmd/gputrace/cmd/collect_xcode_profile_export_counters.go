package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var exportCountersForce bool

func init() {
	xcodeExportCountersCmd := &cobra.Command{
		Use:   "xcode-export-counters [trace_file]",
		Short: "Export GPU counters from Xcode's Performance view to CSV",
		Long: `Triggers Xcode's Export GPU Counters dialog and accepts the default save location.

This command:
1. Finds the Xcode window for the specified trace (or first window)
2. Clicks Editor > Export GPU Counters... menu via AX
3. Clicks Save to accept the default filename/location

The file is saved to wherever Xcode's save dialog defaults to.

Use --force to automatically replace existing files.

Example:
  gputrace xp xcode-export-counters
  gputrace xp xcode-export-counters MyTrace.gputrace
  gputrace xp xcode-export-counters --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: runXcodeExportCounters,
	}
	xcodeExportCountersCmd.Flags().BoolVarP(&exportCountersForce, "force", "f", false, "Replace existing file if it exists")
	collectXcodeProfileCmd.AddCommand(xcodeExportCountersCmd)
}

func runXcodeExportCounters(cmd *cobra.Command, args []string) error {
	traceFile := ""
	if len(args) > 0 {
		traceFile = args[0]
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	// Save cursor position and restore when done
	ensureXCUI()
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
	fmt.Println("Finding trace window and navigating to Counters view...")
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
		fmt.Println("Clicking Show Performance...")
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
		fmt.Println("Clicking Counters tab...")
		if err := axAction(countersBtn, "AXPress"); err != nil {
			verboseLog("Failed to click Counters: %v", err)
		} else {
			time.Sleep(300 * time.Millisecond)
		}
	} else {
		verboseLog("Counters button not found, trying menu anyway...")
	}

	// Activate Xcode and focus the trace window
	activateXcodeQuick()
	time.Sleep(100 * time.Millisecond)
	// Also perform AXRaise again after activation to ensure this window is focused
	axAction(windowAX, "AXRaise")
	time.Sleep(200 * time.Millisecond)

	// Click Editor > Export GPU Counters menu item via AX
	fmt.Println("Opening Editor > Export GPU Counters...")

	menuNames := []string{
		"Export GPU Counters…", // Unicode ellipsis
		"Export GPU Counters...",
		"Export GPU Counters",
	}

	var menuErr error
	for _, menuName := range menuNames {
		if collectProfileDebug {
			fmt.Printf("Trying menu: Editor > %s\n", menuName)
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
				fmt.Println("Clicking Save...")
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
				fmt.Println("Clicking Export...")
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
			if exportCountersForce {
				fmt.Println("File exists, clicking Replace...")
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
	fmt.Println("Export complete")
	return nil
}

// activateXcodeQuick activates Xcode using osascript with a timeout.
// Respects --background flag and does nothing if set.
func activateXcodeQuick() {
	if collectProfileBackground {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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

// TODO: Consider removing these helper functions if no longer needed after simplification.

// selectAllInTable finds the table and selects all rows.
func selectAllInTable(window uintptr) error {
	// Find the table in the Counters view
	table := findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		return role == "AXTable"
	})

	if table == 0 {
		return fmt.Errorf("table not found")
	}

	// Find all rows in the table and select them
	rows := findAllTableRows(table)
	if len(rows) == 0 {
		return fmt.Errorf("no rows found in table")
	}

	// Select all rows by setting AXSelectedRows
	// First, click on the first row to focus the table
	if len(rows) > 0 {
		if err := doubleClickElement(rows[0]); err != nil {
			axAction(rows[0], "AXPress")
		}
	}

	return nil
}

// findAllTableRows finds all rows in a table.
func findAllTableRows(table uintptr) []uintptr {
	var rows []uintptr
	queue := []uintptr{table}
	visited := make(map[uintptr]bool)

	for len(queue) > 0 && len(rows) < 200 {
		el := queue[0]
		queue = queue[1:]

		if visited[el] {
			continue
		}
		visited[el] = true

		role := axString(el, "AXRole")
		if role == "AXRow" {
			rows = append(rows, el)
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}

	return rows
}

// findRadioButtonDeep finds a radio button tab by name with deep search.
func findRadioButtonDeep(root uintptr, name string) uintptr {
	nameLower := strings.ToLower(name)
	queue := []uintptr{root}
	visited := 0
	maxVisit := 15000 // Higher limit for deep search

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]
		visited++

		role := axString(el, "AXRole")
		if role == "AXRadioButton" || role == "AXTab" || role == "AXButton" {
			title := strings.ToLower(axString(el, "AXTitle"))
			desc := strings.ToLower(axString(el, "AXDescription"))
			if title == nameLower || desc == nameLower ||
				strings.Contains(title, nameLower) ||
				strings.Contains(desc, nameLower) {
				return el
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}
	return 0
}

// handleSaveDialog enters the filename in the save dialog and clicks Save.
func handleSaveDialog(app uintptr, filePath string) error {
	// Wait for save sheet to appear
	time.Sleep(500 * time.Millisecond)

	// Find the save sheet - it's typically a sheet or window with "Export" in title
	var sheet uintptr

	// Debug: list all top-level elements
	if collectProfileDebug {
		fmt.Println("[DEBUG] Searching for save dialog...")
		windows := GetAllWindows(app)
		for i, w := range windows {
			role := axString(w, "AXRole")
			subrole := axString(w, "AXSubrole")
			title := axString(w, "AXTitle")
			fmt.Printf("[DEBUG] Window %d: role=%s subrole=%s title=%q\n", i+1, role, subrole, title)

			// Check for sheets attached to this window
			children := axChildren(w)
			for _, c := range children {
				childRole := axString(c, "AXRole")
				if childRole == "AXSheet" {
					fmt.Printf("[DEBUG]   Found AXSheet!\n")
					sheet = c
				}
			}
		}
	}

	if sheet == 0 {
		sheet = findElement(app, func(el uintptr) bool {
			role := axString(el, "AXRole")
			if role == "AXSheet" {
				if collectProfileDebug {
					fmt.Printf("[DEBUG] Found AXSheet element\n")
				}
				return true
			}
			if role == "AXWindow" {
				title := axString(el, "AXTitle")
				subrole := axString(el, "AXSubrole")
				if collectProfileDebug && subrole == "AXDialog" {
					fmt.Printf("[DEBUG] Found dialog window: %q\n", title)
				}
				if strings.Contains(title, "Export") || strings.Contains(title, "Save") ||
					subrole == "AXDialog" {
					return true
				}
			}
			return false
		})
	}

	if sheet == 0 {
		return fmt.Errorf("save dialog not found")
	}

	// Find the filename text field - look for "Save As:" combo box or text field
	textField := findElement(sheet, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXComboBox" || role == "AXTextField" {
			// The Save As field is usually the first editable text field
			// or has identifier related to filename
			id := axString(el, "AXIdentifier")
			if strings.Contains(strings.ToLower(id), "name") ||
				strings.Contains(strings.ToLower(id), "save") {
				return true
			}
		}
		return false
	})

	if textField == 0 {
		// Try to find any combo box (Save As field is typically a combo box)
		textField = findElement(sheet, func(el uintptr) bool {
			return axString(el, "AXRole") == "AXComboBox"
		})
	}

	if textField == 0 {
		// Fall back to any text field
		textField = findElement(sheet, func(el uintptr) bool {
			return axString(el, "AXRole") == "AXTextField"
		})
	}

	if textField == 0 {
		return fmt.Errorf("filename text field not found in save dialog")
	}

	// Set the filename
	filename := filepath.Base(filePath)

	// Set the value
	key := mkString("AXValue")
	filenameStr := mkString(filename)

	if ret := axSetAttributeValue(textField, key, filenameStr); ret != kAXErrorSuccess {
		// Try focusing and using keyboard input instead
		axAction(textField, "AXFocus")
		time.Sleep(100 * time.Millisecond)
		// The value set might have failed, continue anyway
	}

	cfRelease(key)
	cfRelease(filenameStr)

	time.Sleep(200 * time.Millisecond)

	// Find and click the Save button
	saveBtn := findElement(sheet, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXButton" {
			title := axString(el, "AXTitle")
			return title == "Save" || title == "Export" || title == "OK"
		}
		return false
	})

	if saveBtn == 0 {
		return fmt.Errorf("Save button not found")
	}

	if err := axAction(saveBtn, "AXPress"); err != nil {
		return fmt.Errorf("failed to click Save: %w", err)
	}

	// Wait for dialog to close and file to be written
	time.Sleep(1000 * time.Millisecond)

	return nil
}
