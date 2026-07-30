//go:build darwin

package cmd

import "fmt"

func debugCheckExportMenu(app uintptr) error {
	menuBar := findElement(app, func(el uintptr) bool {
		return axString(el, "AXRole") == "AXMenuBar"
	})
	if menuBar == 0 {
		return fmt.Errorf("menubar not found")
	}

	// Find File menu
	fileMenu := findElement(menuBar, func(el uintptr) bool {
		return axString(el, "AXTitle") == "File"
	})
	if fileMenu == 0 {
		return fmt.Errorf("File menu not found")
	}

	// Click File to populate children (often needed for dynamic menus)
	if err := axAction(fileMenu, "AXPress"); err != nil {
		verboseLog("debugCheckExportMenu: failed to open File menu: %v", err)
	}

	// Find Export item
	exportItem := findElement(fileMenu, func(el uintptr) bool {
		t := axString(el, "AXTitle")
		return t == "Export..." || t == "Export…"
	})

	if exportItem == 0 {
		verboseLog("debugCheckExportMenu: Export item not found in File menu")
		// Dump all items
		children := axChildren(fileMenu)
		for _, child := range children {
			verboseLog("debugCheckExportMenu: menu item %q enabled=%v", axString(child, "AXTitle"), IsElementEnabled(child))
			cfRelease(child)
		}
		return nil
	}

	verboseLog("debugCheckExportMenu: Export item found, enabled=%v", IsElementEnabled(exportItem))
	return nil
}

func fileExportMenuState(app uintptr) (found, enabled bool, err error) {
	menuBar := findElementAtDepth(
		app,
		2,
		64,
		axChildren,
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXWindow"
		},
		func(element uintptr) bool {
			return axString(element, "AXRole") == "AXMenuBar"
		},
	)
	if menuBar == 0 {
		return false, false, fmt.Errorf("menubar not found")
	}
	fileMenu := findElementAtDepth(
		menuBar,
		2,
		64,
		axChildren,
		func(uintptr) bool { return false },
		func(element uintptr) bool {
			return axString(element, "AXTitle") == "File"
		},
	)
	if fileMenu == 0 {
		return false, false, fmt.Errorf("File menu not found")
	}
	if err := axAction(fileMenu, "AXPress"); err != nil {
		return false, false, fmt.Errorf("open File menu: %w", err)
	}
	defer axAction(fileMenu, "AXCancel")

	var matches []uintptr
	for _, item := range findAllMenuItems(fileMenu) {
		title := axString(item, "AXTitle")
		if title == "Export..." || title == "Export…" {
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 0:
		return false, false, nil
	case 1:
		return true, IsElementEnabled(matches[0]), nil
	default:
		return false, false, fmt.Errorf("multiple File > Export menu items found")
	}
}
