package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

// ListButtonsOutput represents the JSON output for list-buttons.
type ListButtonsOutput struct {
	Buttons []ButtonInfo `json:"buttons"`
}

func init() {
	listButtonsCmd := &cobra.Command{
		Use:   "list-buttons",
		Short: "List buttons using XCUIAutomation and AX",
		Long:  `Lists all buttons in Xcode using both XCUIAutomation framework and Accessibility APIs.`,
		RunE:  runListButtons,
	}
	collectXcodeProfileCmd.AddCommand(listButtonsCmd)

	clickButtonCmd := &cobra.Command{
		Use:   "click-button <name>",
		Short: "Click a button by name in any Xcode window/dialog",
		Long: `Finds and clicks a button by name in any Xcode window or dialog.

Useful for dismissing dialogs or clicking UI elements.

Example:
  gputrace xp click-button Cancel
  gputrace xp click-button Replace
  gputrace xp click-button Save`,
		Args: cobra.ExactArgs(1),
		RunE: runClickButton,
	}
	collectXcodeProfileCmd.AddCommand(clickButtonCmd)

	// Convenience shortcuts for common dialog buttons
	clickCancelCmd := &cobra.Command{
		Use:   "click-cancel",
		Short: "Click Cancel button in any Xcode dialog",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClickButton(cmd, []string{"Cancel"})
		},
	}
	collectXcodeProfileCmd.AddCommand(clickCancelCmd)

	clickReplaceCmd := &cobra.Command{
		Use:   "click-replace",
		Short: "Click Replace button in any Xcode dialog",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClickButton(cmd, []string{"Replace"})
		},
	}
	collectXcodeProfileCmd.AddCommand(clickReplaceCmd)

	clickSaveCmd := &cobra.Command{
		Use:   "click-save",
		Short: "Click Save button in any Xcode dialog",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClickButton(cmd, []string{"Save"})
		},
	}
	collectXcodeProfileCmd.AddCommand(clickSaveCmd)
}

func runClickButton(cmd *cobra.Command, args []string) error {
	buttonName := args[0]

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	// Activate Xcode first
	activateXcodeQuick()

	// Search all windows for the button (dialogs may be separate windows)
	windows := GetAllWindows(appAX)
	if len(windows) == 0 {
		return fmt.Errorf("no Xcode windows found")
	}

	// Search windows in parallel with shallow depth first
	var btn uintptr
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Phase 1: Shallow parallel search (good for dialog buttons)
	for _, w := range windows {
		wg.Add(1)
		go func(window uintptr) {
			defer wg.Done()
			if b := findButtonBFSFast(window, buttonName, 500); b != 0 {
				mu.Lock()
				if btn == 0 {
					btn = b
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	// Phase 2: Deep search if not found
	if btn == 0 {
		for _, w := range windows {
			wg.Add(1)
			go func(window uintptr) {
				defer wg.Done()
				if b := findButtonBFSFast(window, buttonName, 5000); b != 0 {
					mu.Lock()
					if btn == 0 {
						btn = b
					}
					mu.Unlock()
				}
			}(w)
		}
		wg.Wait()
	}

	if btn == 0 {
		return fmt.Errorf("button %q not found in any Xcode window", buttonName)
	}

	fmt.Printf("Clicking button: %s\n", buttonName)
	if err := axAction(btn, "AXPress"); err != nil {
		return fmt.Errorf("failed to click: %w", err)
	}
	fmt.Println("Done")
	return nil
}

func runListButtons(cmd *cobra.Command, args []string) error {
	start := time.Now()
	if err := setupMacgo(); err != nil {
		return err
	}
	verboseLog("setupMacgo: %v", time.Since(start))

	t1 := time.Now()
	appAX, err := FindXcodeApp()
	if err != nil {
		if collectProfileJSON {
			return outputJSONError("XCODE_NOT_RUNNING", "Xcode not running", "Start Xcode first")
		}
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)
	verboseLog("FindXcodeApp: %v", time.Since(t1))

	// Search all windows in parallel for better performance
	t2 := time.Now()
	windows := GetAllWindows(appAX)
	verboseLog("GetAllWindows: %v (%d windows)", time.Since(t2), len(windows))
	if len(windows) == 0 {
		if collectProfileJSON {
			return outputJSONError("NO_WINDOWS", "no Xcode windows found", "Open a trace file first")
		}
		return fmt.Errorf("no Xcode windows found")
	}

	// Collect buttons from all windows in parallel
	t3 := time.Now()
	var allButtons []ButtonInfo
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, w := range windows {
		wg.Add(1)
		go func(window uintptr) {
			defer wg.Done()
			buttons := findAllButtonsFast(window, 2000)
			if len(buttons) > 0 {
				mu.Lock()
				allButtons = append(allButtons, buttons...)
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	verboseLog("findAllButtonsFast: %v (%d buttons)", time.Since(t3), len(allButtons))

	if collectProfileJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ListButtonsOutput{Buttons: allButtons})
	}

	fmt.Printf("Found %d buttons across %d windows:\n", len(allButtons), len(windows))
	for i, btn := range allButtons {
		status := ""
		if !btn.Enabled {
			status = " [disabled]"
		}
		fmt.Printf("  %d. %s%s\n", i+1, btn.Name, status)
	}

	return nil
}

// Cached CFStrings for common AX attributes (initialized once)
var (
	axCachedStrings     map[string]uintptr
	axCachedStringsOnce sync.Once
)

func getAXCachedString(name string) uintptr {
	axCachedStringsOnce.Do(func() {
		axCachedStrings = map[string]uintptr{
			"AXRole":        mkString("AXRole"),
			"AXChildren":    mkString("AXChildren"),
			"AXTitle":       mkString("AXTitle"),
			"AXDescription": mkString("AXDescription"),
			"AXEnabled":     mkString("AXEnabled"),
		}
	})
	if s, ok := axCachedStrings[name]; ok {
		return s
	}
	// Fallback for uncached strings
	return mkString(name)
}

// axStringFast gets a string attribute using cached key
func axStringFast(ax uintptr, attr string) string {
	key := getAXCachedString(attr)
	var ptr uintptr
	ret := axCopyAttributeValue(ax, key, &ptr)
	if ret != kAXErrorSuccess {
		return ""
	}
	defer cfRelease(ptr)
	return cfToString(ptr)
}

// axChildrenFast gets children using cached key
func axChildrenFast(ax uintptr) []uintptr {
	key := getAXCachedString("AXChildren")
	var ptr uintptr
	ret := axCopyAttributeValue(ax, key, &ptr)
	if ret != kAXErrorSuccess {
		return nil
	}
	defer cfRelease(ptr)
	count := cfArrayGetCount(ptr)
	res := make([]uintptr, count)
	for i := 0; i < count; i++ {
		val := cfArrayGetValueAtIndex(ptr, i)
		res[i] = cfRetain(val)
	}
	return res
}

// axBatchInfo holds info fetched in one batch call
type axBatchInfo struct {
	Role     string
	Title    string
	Desc     string
	Children []uintptr
}

// axGetBatchInfo fetches role, title, description, and children.
// Uses cached CFStrings for efficiency.
func axGetBatchInfo(ax uintptr) axBatchInfo {
	return axBatchInfo{
		Role:     axStringFast(ax, "AXRole"),
		Title:    axStringFast(ax, "AXTitle"),
		Desc:     axStringFast(ax, "AXDescription"),
		Children: axChildrenFast(ax),
	}
}

// findButtonBFSFast finds a button by name using cached CFStrings.
// Returns the button element or 0 if not found.
func findButtonBFSFast(root uintptr, name string, maxVisit int) uintptr {
	stack := []uintptr{root}
	visited := 0

	for len(stack) > 0 && visited < maxVisit {
		el := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visited++

		role := axStringFast(el, "AXRole")
		if role == "AXButton" {
			title := axStringFast(el, "AXTitle")
			if title == "" {
				title = axStringFast(el, "AXDescription")
			}
			if title == name {
				return el
			}
		}

		children := axChildrenFast(el)
		for i := len(children) - 1; i >= 0; i-- {
			stack = append(stack, children[i])
		}
	}
	return 0
}

// findAllButtonsFast returns ButtonInfo for all buttons using optimized traversal.
// Uses batch API to fetch multiple attributes in one IPC call.
func findAllButtonsFast(root uintptr, maxVisit int) []ButtonInfo {
	var buttons []ButtonInfo
	stack := []uintptr{root} // DFS uses less memory than BFS
	visited := 0

	for len(stack) > 0 && visited < maxVisit {
		// Pop from stack (DFS)
		el := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visited++

		// Batch fetch: role, title, desc, children in one IPC call
		info := axGetBatchInfo(el)

		if info.Role == "AXButton" {
			title := info.Title
			if title == "" {
				title = info.Desc
			}
			if title != "RuntimeIssue" && title != "" {
				enabled := IsElementEnabled(el)
				buttons = append(buttons, ButtonInfo{
					Name:    title,
					Enabled: enabled,
				})
			}
		}

		// Add children in reverse order so first child is processed first
		for i := len(info.Children) - 1; i >= 0; i-- {
			stack = append(stack, info.Children[i])
		}
	}
	return buttons
}
