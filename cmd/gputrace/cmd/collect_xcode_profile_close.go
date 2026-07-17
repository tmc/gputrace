//go:build darwin

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func runCloseTrace(cmd *cobra.Command, args []string) error {
	traceFile := ""
	if len(args) > 0 {
		traceFile = args[0]
	}

	if err := setupMacgo(); err != nil {
		return err
	}

	appAX, err := FindXcodeApp()
	if err != nil {
		return fmt.Errorf("Xcode not running: %w", err)
	}
	defer cfRelease(appAX)

	var windowAX uintptr
	windowAX, err = findTargetWindow(cmd.Context(), appAX, traceFile)
	if err != nil {
		return err
	}
	title := axString(windowAX, "AXTitle")
	if traceFile != "" {
		fmt.Fprintf(xcodeProfileStatusWriter(), "Closing window for: %s\n", traceFile)
	} else if title != "" {
		fmt.Fprintf(xcodeProfileStatusWriter(), "Closing window: %s\n", title)
	} else {
		fmt.Fprintln(xcodeProfileStatusWriter(), "Closing trace window")
	}

	// Close via AX close button
	closeBtn := findCloseButton(windowAX)
	if closeBtn == 0 {
		return fmt.Errorf("close button not found")
	}

	if err := axAction(closeBtn, "AXPress"); err != nil {
		return fmt.Errorf("failed to click close button: %w", err)
	}

	fmt.Fprintln(xcodeProfileStatusWriter(), "Done")
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "close",
		Target: traceFile,
	})
}

// findCloseButton finds the close button in a window.
func findCloseButton(window uintptr) uintptr {
	return findElement(window, func(el uintptr) bool {
		role := axString(el, "AXRole")
		if role == "AXButton" {
			subrole := axString(el, "AXSubrole")
			if subrole == "AXCloseButton" {
				return true
			}
			// Also check for title/description
			title := axString(el, "AXTitle")
			desc := axString(el, "AXDescription")
			if title == "close" || desc == "close" || title == "Close" || desc == "Close" {
				return true
			}
		}
		return false
	})
}
