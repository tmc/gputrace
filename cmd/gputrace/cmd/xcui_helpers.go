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
