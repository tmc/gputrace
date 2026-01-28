package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"sync/atomic"
	"time"
)

// Global automation cancel state
var automationCanceled atomic.Bool

// automationContext is used to cancel automation via context
var automationCtx context.Context
var automationCancel context.CancelFunc

// StartAutomationCancelListener prepares for cancellation support.
// If showOverlay is true, shows a transparent overlay (not yet implemented).
// Returns a cleanup function that should be called when automation is done.
//
// TODO: Implement full-screen transparent overlay like iOS's
// "Automation Running - Hold both volume buttons to stop"
// For macOS, could use "Hold Escape for 2 seconds to stop" or similar.
func StartAutomationCancelListener(showOverlay bool) (cleanup func()) {
	// Reset cancel state
	automationCanceled.Store(false)

	// Create cancellable context
	automationCtx, automationCancel = context.WithCancel(context.Background())

	// TODO: Show transparent overlay with cancel instructions
	// For now, this is a no-op placeholder
	if showOverlay {
		// Future: create NSWindow overlay at floating level
		// with message "Automation Running - Hold Escape to stop"
	}

	return func() {
		if automationCancel != nil {
			automationCancel()
		}
	}
}

// IsAutomationCanceled returns true if the user has requested to cancel automation.
func IsAutomationCanceled() bool {
	return automationCanceled.Load()
}

// ResetAutomationCancel resets the cancel flag.
func ResetAutomationCancel() {
	automationCanceled.Store(false)
}

// CheckCancelAndReturn returns an error if automation was canceled.
func CheckCancelAndReturn() error {
	if automationCanceled.Load() {
		return fmt.Errorf("automation canceled by user")
	}
	return nil
}

// ShowAutomationOverlay shows a notification that automation is running.
// Uses AppleScript to show a system notification.
func ShowAutomationOverlay(message string) error {
	script := fmt.Sprintf(`display notification %q with title "gputrace" subtitle "Press Ctrl+C to cancel"`, message)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	return cmd.Run()
}
