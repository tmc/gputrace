//go:build darwin

package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

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
	document := axString(windowAX, "AXDocument")
	initialWindows := deduplicateAXWindows(GetAllWindows(appAX))
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
	if err := waitForClosedTraceWindow(cmd.Context(), appAX, title, document, len(initialWindows), 5*time.Second); err != nil {
		return err
	}

	fmt.Fprintln(xcodeProfileStatusWriter(), "Trace window closed (verified absent from Xcode window list).")
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action:           "close",
		Target:           traceFile,
		SelectedTitle:    title,
		SelectedDocument: document,
		Phase:            "closed",
		Evidence:         "selected trace window is absent from the Xcode AX window list",
	})
}

func waitForClosedTraceWindow(ctx context.Context, appAX uintptr, title, document string, initialCount int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		windows := deduplicateAXWindows(GetAllWindows(appAX))
		if !windowSnapshotContainsTarget(windows, title, document, initialCount) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("close trace window: selected window is still present (title %q, document %q)", title, document)
		}
		if err := waitForAutomation(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}

func windowSnapshotContainsTarget(windows []xcodeAXWindow, title, document string, initialCount int) bool {
	if document != "" {
		want := strings.ToLower(filepath.Clean(document))
		for _, window := range windows {
			if strings.ToLower(filepath.Clean(window.Document)) == want {
				return true
			}
		}
		return false
	}
	if title != "" {
		want := strings.ToLower(strings.TrimSpace(title))
		for _, window := range windows {
			if strings.ToLower(strings.TrimSpace(window.Title)) == want {
				return true
			}
		}
		return false
	}
	return len(windows) >= initialCount
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
