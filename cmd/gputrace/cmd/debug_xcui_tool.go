package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var debugXCUICmd = &cobra.Command{
	Use:   "debug-xcui",
	Short:  "Debug XCUI",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		DebugXCUI()
	},
}

func init() {
	rootCmd.AddCommand(debugXCUICmd)
}

func DebugXCUI() {
	fmt.Println("Debugging XCUI...")

	app, err := FindXcodeApp()
	if err != nil {
		fmt.Printf("Error finding Xcode: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Found Xcode AX: %d\n", app)

	window := GetFirstWindow(app)
	if window == 0 {
		fmt.Printf("No main window found. Dumping app children:\n")
		dumpChildren(app, 0)
		os.Exit(1)
	}
	fmt.Printf("Found Main Window: %d\n", window)

	fmt.Println("Searching for Replay button...")
	btn := FindReplayButton(window)
	if btn != 0 {
		fmt.Printf("Found Replay Button: %d\n", btn)
		fmt.Printf("  Enabled: %v\n", IsElementEnabled(btn))

		// Dump actions
		// (We need binding for AXUIElementCopyActionNames if we want to debug actions)
	} else {
		fmt.Println("Replay Button NOT found.")

		fmt.Println("Dumping window children:")
		dumpChildren(window, 0)
	}
}

func dumpChildren(root uintptr, depth int) {
	if depth > 3 {
		return
	}
	children := axChildren(root)
	prefix := ""
	for i := 0; i < depth; i++ {
		prefix += "  "
	}

	for _, child := range children {
		role := axString(child, "AXRole")
		title := axString(child, "AXTitle")
		fmt.Printf("%s[%s] '%s'\n", prefix, role, title)
		dumpChildren(child, depth+1)
	}
}
