//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/spf13/cobra"
)

// WindowInfo represents information about an Xcode window for JSON output.
type WindowInfo struct {
	Index      int             `json:"index"`
	Title      string          `json:"title"`
	Document   string          `json:"document,omitempty"`
	Role       string          `json:"role"`
	Subrole    string          `json:"subrole,omitempty"`
	Status     string          `json:"status"`
	Buttons    []ButtonInfo    `json:"buttons,omitempty"`
	Checkboxes []CheckboxInfo  `json:"checkboxes,omitempty"`
	TextFields []TextFieldInfo `json:"text_fields,omitempty"`
}

// ButtonInfo represents a button in the UI.
type ButtonInfo struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// CheckboxInfo represents a checkbox in the UI.
type CheckboxInfo struct {
	Name    string `json:"name"`
	Checked bool   `json:"checked"`
	Enabled bool   `json:"enabled"`
}

// TextFieldInfo represents a text field in the UI.
type TextFieldInfo struct {
	Identifier string `json:"identifier,omitempty"`
	Value      string `json:"value"`
	Editable   bool   `json:"editable"`
}

// ListWindowsOutput is the JSON output for list-windows.
type ListWindowsOutput struct {
	Windows []WindowInfo `json:"windows"`
}

func runListWindows(cmd *cobra.Command, args []string) error {
	traceFile := ""
	if len(args) > 0 {
		traceFile = args[0]
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		if collectProfileOpts.json {
			return outputJSONError("XCODE_NOT_RUNNING", "Xcode not running or not accessible", "Start Xcode first")
		}
		return fmt.Errorf("Xcode not running or not accessible: %w", err)
	}
	defer cfRelease(appAX)

	var windowPtrs []uintptr
	if traceFile != "" {
		w, err := findTargetWindow(appAX, traceFile)
		if err != nil {
			if collectProfileOpts.json {
				return outputJSONError("WINDOW_NOT_FOUND", err.Error(), "Check if the trace file is open")
			}
			return err
		}
		windowPtrs = []uintptr{w}
	} else {
		windowPtrs = GetAllWindows(appAX)
	}

	if len(windowPtrs) == 0 {
		if collectProfileOpts.json {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(ListWindowsOutput{Windows: []WindowInfo{}})
		}
		fmt.Println("Xcode Windows:")
		fmt.Println("==============")
		fmt.Println("  (no windows found)")
		return nil
	}

	// Collect window info
	windowInfos := make([]WindowInfo, len(windowPtrs))
	for i, w := range windowPtrs {
		title := axString(w, "AXTitle")
		role := axString(w, "AXRole")
		subrole := axString(w, "AXSubrole")
		doc := axString(w, "AXDocument")

		// Run all searches in parallel
		var wg sync.WaitGroup
		var checkboxPtrs, buttonPtrs, textFieldPtrs []uintptr
		var status string
		var showPerfBtn uintptr

		wg.Add(1)
		go func() {
			defer wg.Done()
			checkboxPtrs, buttonPtrs, textFieldPtrs = findCheckboxesButtonsAndTextFields(w)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			status = getProfilingStatus(w)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			showPerfBtn = findShowPerformanceButton(w)
		}()

		wg.Wait()

		if showPerfBtn != 0 {
			buttonPtrs = append(buttonPtrs, showPerfBtn)
		}

		// Convert to info structs
		var buttons []ButtonInfo
		for _, btn := range buttonPtrs {
			name := axString(btn, "AXTitle")
			if name == "" {
				name = axString(btn, "AXDescription")
			}
			if name == "" {
				name = "(unnamed)"
			}
			buttons = append(buttons, ButtonInfo{
				Name:    name,
				Enabled: IsElementEnabled(btn),
			})
		}

		var checkboxes []CheckboxInfo
		for _, cb := range checkboxPtrs {
			if cb == 0 {
				continue
			}
			name := axString(cb, "AXTitle")
			if name == "" {
				name = axString(cb, "AXDescription")
			}
			if name == "" {
				name = "(unnamed)"
			}
			checkboxes = append(checkboxes, CheckboxInfo{
				Name:    name,
				Checked: IsCheckboxChecked(cb),
				Enabled: IsElementEnabled(cb),
			})
		}

		var textFields []TextFieldInfo
		for _, tf := range textFieldPtrs {
			if tf == 0 {
				continue
			}
			identifier := axString(tf, "AXIdentifier")
			value := axString(tf, "AXValue")
			textFields = append(textFields, TextFieldInfo{
				Identifier: identifier,
				Value:      value,
				Editable:   IsElementEnabled(tf),
			})
		}

		windowInfos[i] = WindowInfo{
			Index:      i,
			Title:      title,
			Document:   doc,
			Role:       role,
			Subrole:    subrole,
			Status:     status,
			Buttons:    buttons,
			Checkboxes: checkboxes,
			TextFields: textFields,
		}
	}

	// Output
	if collectProfileOpts.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ListWindowsOutput{Windows: windowInfos})
	}

	// Text output
	fmt.Println("Xcode Windows:")
	fmt.Println("==============")

	for _, w := range windowInfos {
		fmt.Printf("\nWindow %d:\n", w.Index+1)
		fmt.Printf("  Title:   %s\n", w.Title)
		if w.Document != "" {
			fmt.Printf("  Document: %s\n", w.Document)
		}
		fmt.Printf("  Role:    %s\n", w.Role)
		if w.Subrole != "" {
			fmt.Printf("  Subrole: %s\n", w.Subrole)
		}

		if len(w.Checkboxes) > 0 {
			fmt.Printf("  Checkboxes (%d):\n", len(w.Checkboxes))
			for j, cb := range w.Checkboxes {
				checked := ""
				if cb.Checked {
					checked = " [checked]"
				}
				st := ""
				if !cb.Enabled {
					st = " [disabled]"
				}
				fmt.Printf("    %d. %s%s%s\n", j+1, cb.Name, checked, st)
			}
		}

		if len(w.Buttons) > 0 {
			fmt.Printf("  Buttons (%d):\n", len(w.Buttons))
			for j, btn := range w.Buttons {
				st := ""
				if !btn.Enabled {
					st = " [disabled]"
				}
				fmt.Printf("    %d. %s%s\n", j+1, btn.Name, st)
			}
		}

		switch w.Status {
		case "complete":
			fmt.Printf("  Status:  %s\n", Colorize("COMPLETE (Show Performance available)", ColorGreen))
		case "running":
			fmt.Printf("  Status:  %s\n", Colorize("PROFILING IN PROGRESS", ColorYellow))
		case "replay-ready":
			fmt.Printf("  Status:  %s\n", Colorize("REPLAY READY", ColorGreen))
		case "initializing":
			fmt.Printf("  Status:  %s\n", Colorize("Initializing (Replay disabled)", ColorYellow))
		default:
			fmt.Printf("  Status:  %s\n", Colorize("Unknown", ColorRed))
		}
	}

	return nil
}

// JSONError represents a structured error for JSON output.
type JSONError struct {
	Error      bool   `json:"error"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// outputJSONError outputs a JSON error and returns nil (to avoid duplicate error output).
func outputJSONError(code, message, suggestion string) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(JSONError{
		Error:      true,
		Code:       code,
		Message:    message,
		Suggestion: suggestion,
	})
	return nil
}

// keyButtons are the button names we care about for automation.
var keyButtons = map[string]bool{
	"Profile":             true,
	"Replay":              true,
	"Replay GPU Workload": true,
	"Show Performance":    true,
	"Open Performance":    true,
	"Export":              true,
	"Save":                true,
	"Timeline":            true,
	"Encoders":            true,
	"Summary":             true,
	"Counters":            true,
	"Memory":              true,
}

// findCheckboxesButtonsAndTextFields does a single BFS pass to find checkboxes, key buttons, and text fields.
// Only returns buttons that are relevant for automation (Replay, Stop, Export, etc.)
func findCheckboxesButtonsAndTextFields(root uintptr) (checkboxes, buttons, textFields []uintptr) {
	queue := []uintptr{root}
	visited := 0
	maxVisit := 500 // Reduced limit - we only need shallow UI elements

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]
		visited++

		role := axString(el, "AXRole")

		switch role {
		case "AXCheckBox":
			checkboxes = append(checkboxes, el)
		case "AXButton":
			name := axString(el, "AXTitle")
			if name == "" {
				name = axString(el, "AXDescription")
			}
			// Only include key buttons we care about
			if keyButtons[name] {
				buttons = append(buttons, el)
			}
		case "AXTextField":
			textFields = append(textFields, el)
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}
	return
}

// GetAllWindows returns all windows for an application.
func GetAllWindows(app uintptr) []uintptr {
	var windows []uintptr

	addWindow := func(el uintptr) {
		if el == 0 || axString(el, "AXRole") != "AXWindow" {
			return
		}
		for _, w := range windows {
			if w == el {
				return
			}
		}
		windows = append(windows, el)
	}

	for _, attr := range []string{"AXWindows", "AXVisibleChildren", "AXChildren"} {
		els, ret := axArrayAttributeWithError(app, attr)
		if ret != kAXErrorSuccess && collectProfileOpts.debug {
			verboseLog("GetAllWindows: %s failed: AXError %d", attr, ret)
		}
		for _, el := range els {
			addWindow(el)
		}
	}

	for _, attr := range []string{"AXFocusedWindow", "AXMainWindow"} {
		var el uintptr
		key := mkString(attr)
		ret := axCopyAttributeValue(app, key, &el)
		cfRelease(key)
		if ret == kAXErrorSuccess {
			addWindow(el)
		} else if collectProfileOpts.debug {
			verboseLog("GetAllWindows: %s failed: AXError %d", attr, ret)
		}
	}
	if len(windows) == 0 {
		for _, w := range axWindowsFromCGHitTest(app) {
			addWindow(w)
		}
	}
	return windows
}

// findShowPerformanceButton does targeted traversal to find "Show Performance" or "Open Performance" button.
// Path: window > editor area > Summary > Show/Open Performance
// Also falls back to searching the whole window if not found in Summary.
func findShowPerformanceButton(window uintptr) uintptr {
	// Try targeted path first
	editorArea := findGroupByTitle(window, "editor area", 100)
	verboseLog("findShowPerformanceButton: editorArea=%v", editorArea != 0)
	if editorArea != 0 {
		summary := findGroupByTitle(editorArea, "Summary", 500)
		verboseLog("findShowPerformanceButton: summary=%v", summary != 0)
		if summary != 0 {
			if btn := findButtonBFS(summary, "Show Performance", 500); btn != 0 {
				verboseLog("findShowPerformanceButton: found in Summary")
				return btn
			}
			if btn := findButtonBFS(summary, "Open Performance", 500); btn != 0 {
				verboseLog("findShowPerformanceButton: found Open in Summary")
				return btn
			}
		}
	}

	// Fallback: search the whole window for either button
	verboseLog("findShowPerformanceButton: trying fallback BFS on window")
	if btn := findButtonBFS(window, "Show Performance", 1000); btn != 0 {
		verboseLog("findShowPerformanceButton: found via fallback")
		return btn
	}
	if btn := findButtonBFS(window, "Open Performance", 1000); btn != 0 {
		verboseLog("findShowPerformanceButton: found Open via fallback")
		return btn
	}
	verboseLog("findShowPerformanceButton: not found")
	return 0
}
