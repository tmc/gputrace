//go:build darwin

package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func runListMenus(cmd *cobra.Command, args []string) error {
	if err := rejectUnsupportedXcodeProfileJSON("list-menus"); err != nil {
		return err
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	// Find menu bar
	menuBar := findElement(appAX, func(el uintptr) bool {
		return axString(el, "AXRole") == "AXMenuBar"
	})

	if menuBar == 0 {
		return fmt.Errorf("menu bar not found")
	}

	// Get menu bar items
	children := axChildren(menuBar)

	if len(args) == 0 {
		// List all top-level menu items
		fmt.Println("Menu Bar Items:")
		for i, child := range children {
			title := axString(child, "AXTitle")
			if title != "" {
				fmt.Printf("  %d. %s\n", i+1, title)
			}
		}
		return nil
	}

	// Find and list items in the specified menu
	menuName := args[0]
	var targetMenu uintptr

	for _, child := range children {
		title := axString(child, "AXTitle")
		if strings.EqualFold(title, menuName) {
			targetMenu = child
			break
		}
	}

	if targetMenu == 0 {
		return fmt.Errorf("menu %q not found", menuName)
	}

	// Press the menu to open it
	if err := axAction(targetMenu, "AXPress"); err != nil {
		return fmt.Errorf("failed to open menu: %w", err)
	}

	// Give menu time to open
	// time.Sleep(200 * time.Millisecond)

	// Find the menu's children (menu items)
	// The menu items are in a child AXMenu element
	menuItems := findAllMenuItems(targetMenu)

	fmt.Printf("Menu '%s' Items:\n", menuName)
	for i, item := range menuItems {
		title := axString(item, "AXTitle")
		enabled := IsElementEnabled(item)
		status := ""
		if !enabled {
			status = " [disabled]"
		}
		if title != "" {
			fmt.Printf("  %d. %s%s\n", i+1, title, status)
		} else {
			fmt.Printf("  %d. ---separator---\n", i+1)
		}
	}

	// Press Escape to close menu
	axAction(targetMenu, "AXCancel")

	return nil
}

func runClickMenu(cmd *cobra.Command, args []string) error {
	if err := rejectUnsupportedXcodeProfileJSON("click-menu"); err != nil {
		return err
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	menuName := args[0]
	itemName := args[1]

	if err := ClickMenuItem(appAX, []string{menuName, itemName}); err != nil {
		return fmt.Errorf("failed to click menu item: %w", err)
	}

	fmt.Printf("Clicked: %s > %s\n", menuName, itemName)
	return nil
}

// findAllMenuItems returns all menu items in a menu.
func findAllMenuItems(menu uintptr) []uintptr {
	var items []uintptr
	queue := []uintptr{menu}
	visited := make(map[uintptr]bool)

	for len(queue) > 0 && len(items) < 100 {
		el := queue[0]
		queue = queue[1:]

		if visited[el] {
			continue
		}
		visited[el] = true

		role := axString(el, "AXRole")
		if role == "AXMenuItem" {
			items = append(items, el)
		}

		children := axChildren(el)
		queue = append(queue, children...)
	}

	return items
}
