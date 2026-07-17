//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// TabInfo represents a tab in the UI.
type TabInfo struct {
	Name     string `json:"name"`
	Selected bool   `json:"selected"`
}

// ListTabsOutput represents the JSON output for list-tabs.
type ListTabsOutput struct {
	Tabs          []TabInfo    `json:"tabs"`
	ActionButtons []ButtonInfo `json:"action_buttons,omitempty"`
}

func runSelectTab(cmd *cobra.Command, args []string) error {
	tabName := args[0]
	status := xcodeProfileStatusWriter()

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	// Try to find a trace window first (empty string falls back to first available)
	windowAX, err := findTargetWindow(appAX, "")
	if err != nil {
		return err
	}

	// Find and click the tab
	tab := findTabByName(windowAX, tabName)
	if tab != 0 {
		fmt.Fprintf(status, "Selecting tab: %s\n", tabName)
		if err := axAction(tab, "AXPress"); err != nil {
			return fmt.Errorf("failed to click tab: %w", err)
		}
		fmt.Fprintln(status, "Done")
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action: "select_tab",
			Target: tabName,
			Method: "tab",
		})
	}

	// Try as an outline row (navigator items like Summary, Dependencies, etc.)
	row := findOutlineRowByName(windowAX, tabName)
	if row != 0 {
		fmt.Fprintf(status, "Selecting navigator item: %s\n", tabName)
		if err := axAction(row, "AXPress"); err != nil {
			return fmt.Errorf("failed to select: %w", err)
		}
		fmt.Fprintln(status, "Done")
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action: "select_tab",
			Target: tabName,
			Method: "navigator",
		})
	}

	// Try as a button (some tabs appear as buttons)
	btn := findButtonByNameInsensitive(windowAX, tabName)
	if btn != 0 {
		fmt.Fprintf(status, "Selecting: %s\n", tabName)
		if err := axAction(btn, "AXPress"); err != nil {
			return fmt.Errorf("failed to click: %w", err)
		}
		fmt.Fprintln(status, "Done")
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action: "select_tab",
			Target: tabName,
			Method: "button",
		})
	}

	return fmt.Errorf("tab %q not found", tabName)
}

// runSelectNavigatorItem selects an item in the Debug navigator by name.
func runSelectNavigatorItem(name string) error {
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
		return err
	}

	// The navigator items have specific capitalization
	displayName := strings.Title(name)

	// Find the element - could be outline row, cell, or static text
	el := findOutlineRowByName(windowAX, displayName)
	if el == 0 {
		el = findCellByName(windowAX, displayName)
	}

	if el == 0 {
		return fmt.Errorf("navigator item %q not found", displayName)
	}

	role := axString(el, "AXRole")
	actions := axActionNames(el)
	if collectProfileOpts.debug {
		fmt.Fprintf(os.Stderr, "Found element: role=%s, actions=%v\n", role, actions)
	}

	// If we found a text element, find its parent row for actions
	targetEl := el
	if role == "AXStaticText" || role == "AXCell" {
		parent := findParentOutlineRow(el)
		if parent != 0 {
			targetEl = parent
			role = axString(targetEl, "AXRole")
			actions = axActionNames(targetEl)
			if collectProfileOpts.debug {
				fmt.Fprintf(os.Stderr, "Using parent: role=%s, actions=%v\n", role, actions)
			}
		}
	}

	fmt.Fprintf(status, "Selecting navigator item: %s\n", displayName)

	// Try AXOpen first (double-click to open)
	if err := axAction(targetEl, "AXOpen"); err == nil {
		fmt.Fprintln(status, "Done")
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action: "select_navigator",
			Target: displayName,
			Method: "AXOpen",
		})
	}

	// Try AXPress
	if err := axAction(targetEl, "AXPress"); err == nil {
		fmt.Fprintln(status, "Done")
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action: "select_navigator",
			Target: displayName,
			Method: "AXPress",
		})
	}

	// Try setting AXSelected on the element, then double-click
	if selectElement(targetEl) {
		// Also try double-click via CGEvent
		if err := doubleClickElement(targetEl); err == nil {
			fmt.Fprintln(status, "Done")
			return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
				Action: "select_navigator",
				Target: displayName,
				Method: "select_double_click",
			})
		}
	}

	// Last resort: just double-click on the element
	if err := doubleClickElement(targetEl); err == nil {
		fmt.Fprintln(status, "Done")
		return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
			Action: "select_navigator",
			Target: displayName,
			Method: "double_click",
		})
	}

	return fmt.Errorf("could not select %s (element found but selection failed)", displayName)
}

// findCellByName finds a cell or static text element by name.
func findCellByName(root uintptr, name string) uintptr {
	nameLower := strings.ToLower(name)
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXCell" || role == "AXStaticText" || role == "AXGroup" {
			title := strings.ToLower(axString(el, "AXTitle"))
			desc := strings.ToLower(axString(el, "AXDescription"))
			value := strings.ToLower(axString(el, "AXValue"))
			if title == nameLower || desc == nameLower || value == nameLower {
				return true
			}
		}
		return false
	})
}

// selectElement tries to select an element by setting AXSelected attribute.
func selectElement(el uintptr) bool {
	key := mkString("AXSelected")
	defer cfRelease(key)

	if kCFBooleanTrue == 0 {
		return false
	}
	ret := axSetAttributeValue(el, key, kCFBooleanTrue)
	return ret == kAXErrorSuccess
}

// findParentOutlineRow finds the parent AXOutlineRow of an element.
func findParentOutlineRow(el uintptr) uintptr {
	key := mkString("AXParent")
	defer cfRelease(key)

	var parent uintptr
	for i := 0; i < 10; i++ { // Max depth to prevent infinite loop
		if axCopyAttributeValue(el, key, &parent) != kAXErrorSuccess {
			return 0
		}
		role := axString(parent, "AXRole")
		if role == "AXOutlineRow" || role == "AXRow" {
			return parent
		}
		cfRelease(el)
		el = parent
	}
	return 0
}

func runListTabs(cmd *cobra.Command, args []string) error {
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
			return outputJSONError("XCODE_NOT_RUNNING", "Xcode not running", "Start Xcode first")
		}
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	windowAX, err := findTargetWindow(appAX, traceFile)
	if err != nil {
		if collectProfileOpts.json {
			return outputJSONError("NO_WINDOWS", err.Error(), "Open a trace file first")
		}
		return err
	}

	// Find navigator items (Summary, Dependencies, Performance, Memory)
	navigatorItems := findNavigatorItems(windowAX)
	var tabs []TabInfo
	for _, item := range navigatorItems {
		tabs = append(tabs, TabInfo{
			Name:     item.name,
			Selected: item.selected,
		})
	}

	// Find relevant buttons (Replay, Export, Show Performance, etc.)
	relevantBtnPtrs := findRelevantButtons(windowAX)
	var actionButtons []ButtonInfo
	for _, btn := range relevantBtnPtrs {
		title := axString(btn, "AXTitle")
		desc := axString(btn, "AXDescription")
		name := title
		if name == "" {
			name = desc
		}
		actionButtons = append(actionButtons, ButtonInfo{
			Name:    name,
			Enabled: IsElementEnabled(btn),
		})
	}

	if collectProfileOpts.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ListTabsOutput{
			Tabs:          tabs,
			ActionButtons: actionButtons,
		})
	}

	// Text output
	fmt.Println("Available tabs and buttons:")
	fmt.Println("===========================")

	if len(tabs) > 0 {
		fmt.Println("\nTabs:")
		for i, tab := range tabs {
			selected := ""
			if tab.Selected {
				selected = " [selected]"
			}
			fmt.Printf("  %d. %s%s\n", i+1, tab.Name, selected)
		}
	}

	if len(actionButtons) > 0 {
		fmt.Println("\nAction buttons:")
		for i, btn := range actionButtons {
			status := ""
			if !btn.Enabled {
				status = " [disabled]"
			}
			fmt.Printf("  %d. %s%s\n", i+1, btn.Name, status)
		}
	}

	return nil
}

func runShowPerformance(cmd *cobra.Command, args []string) error {
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
		return err
	}

	// Find Show Performance button
	btn := findButtonByNameInsensitive(windowAX, "Show Performance")
	if btn == 0 {
		return fmt.Errorf("Show Performance button not found (replay may not be complete)")
	}

	if !IsElementEnabled(btn) {
		return fmt.Errorf("Show Performance button is disabled")
	}

	fmt.Fprintln(status, "Clicking Show Performance...")
	if err := axAction(btn, "AXPress"); err != nil {
		return fmt.Errorf("failed to click: %w", err)
	}
	fmt.Fprintln(status, "Done")
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "show_performance",
	})
}

// findTabByName finds a tab (AXRadioButton in tab group) by name.
func findTabByName(root uintptr, name string) uintptr {
	nameLower := strings.ToLower(name)
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXRadioButton" || role == "AXTab" {
			title := strings.ToLower(axString(el, "AXTitle"))
			desc := strings.ToLower(axString(el, "AXDescription"))
			value := strings.ToLower(axString(el, "AXValue"))
			if strings.Contains(title, nameLower) ||
				strings.Contains(desc, nameLower) ||
				strings.Contains(value, nameLower) {
				return true
			}
		}
		return false
	})
}

// findButtonByNameInsensitive finds a button by name (case-insensitive).
func findButtonByNameInsensitive(root uintptr, name string) uintptr {
	nameLower := strings.ToLower(name)
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXButton" {
			title := strings.ToLower(axString(el, "AXTitle"))
			desc := strings.ToLower(axString(el, "AXDescription"))
			if strings.Contains(title, nameLower) || strings.Contains(desc, nameLower) {
				return true
			}
		}
		return false
	})
}

// findOutlineRowByName finds an outline row (navigator item) by name.
// In Xcode, the Debug navigator items (Summary, Dependencies, Performance, Memory)
// are typically AXOutlineRow, AXRow, or AXCell elements.
func findOutlineRowByName(root uintptr, name string) uintptr {
	nameLower := strings.ToLower(name)
	return findElement(root, func(el uintptr) bool {
		role := axString(el, "AXRole")
		// Check various row/cell types used in outline views
		if role == "AXOutlineRow" || role == "AXRow" || role == "AXCell" || role == "AXStaticText" {
			title := strings.ToLower(axString(el, "AXTitle"))
			desc := strings.ToLower(axString(el, "AXDescription"))
			value := strings.ToLower(axString(el, "AXValue"))
			// Exact match preferred for navigator items
			if title == nameLower || desc == nameLower || value == nameLower {
				return true
			}
			// Also try contains for partial matching
			if strings.Contains(title, nameLower) ||
				strings.Contains(desc, nameLower) ||
				strings.Contains(value, nameLower) {
				return true
			}
		}
		return false
	})
}

// findAllTabs finds all tab elements in the tree.
func findAllTabs(root uintptr, maxVisit int) []uintptr {
	var tabs []uintptr
	queue := []uintptr{root}
	visited := 0
	seen := make(map[uintptr]bool)

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if seen[el] {
			continue
		}
		seen[el] = true
		visited++

		role := axString(el, "AXRole")
		if role == "AXRadioButton" || role == "AXTab" {
			// Check if it looks like a tab (has a title)
			title := axString(el, "AXTitle")
			desc := axString(el, "AXDescription")
			if title != "" || desc != "" {
				tabs = append(tabs, el)
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}
	return tabs
}

// isTabSelected returns true if a tab is currently selected.
func isTabSelected(tab uintptr) bool {
	var val uintptr
	key := mkString("AXValue")
	defer cfRelease(key)

	if axCopyAttributeValue(tab, key, &val) == kAXErrorSuccess {
		defer cfRelease(val)
		// For radio buttons, AXValue is 1 when selected
		return cfBooleanGetValue(val)
	}
	return false
}

// navigatorItem represents a navigator panel item.
type navigatorItem struct {
	name     string
	selected bool
}

// findNavigatorItems finds navigator items in the Debug Navigator panel.
// These are the main navigation options: Summary, Dependencies, Performance, Memory.
func findNavigatorItems(window uintptr) []navigatorItem {
	knownItems := []string{"Summary", "Dependencies", "Performance", "Memory"}
	var items []navigatorItem

	for _, name := range knownItems {
		el := findOutlineRowByName(window, name)
		if el != 0 {
			items = append(items, navigatorItem{
				name:     name,
				selected: isElementSelected(el),
			})
		}
	}
	return items
}

// isElementSelected checks if an element is selected.
func isElementSelected(el uintptr) bool {
	var val uintptr
	key := mkString("AXSelected")
	defer cfRelease(key)

	if axCopyAttributeValue(el, key, &val) == kAXErrorSuccess {
		defer cfRelease(val)
		return cfBooleanGetValue(val)
	}
	return false
}

// findRelevantButtons finds buttons that are likely tab-related actions.
func findRelevantButtons(root uintptr) []uintptr {
	relevantNames := []string{
		"Show Performance",
		"Open Performance",
		"Show Summary",
		"Show Counters",
		"Show Memory",
		"Show Encoders",
		"Export",
		"Replay",
	}

	var buttons []uintptr
	queue := []uintptr{root}
	visited := 0
	seen := make(map[uintptr]bool)
	maxVisit := 1000

	for len(queue) > 0 && visited < maxVisit {
		el := queue[0]
		queue = queue[1:]

		if seen[el] {
			continue
		}
		seen[el] = true
		visited++

		role := axString(el, "AXRole")
		if role == "AXButton" {
			title := axString(el, "AXTitle")
			desc := axString(el, "AXDescription")
			name := title
			if name == "" {
				name = desc
			}
			for _, relevant := range relevantNames {
				if strings.EqualFold(name, relevant) {
					buttons = append(buttons, el)
					break
				}
			}
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}
	return buttons
}
