package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Global automation cancel state
var automationCanceled atomic.Bool

// automationContext is used to cancel automation via context
var automationCtx context.Context
var automationCancel context.CancelFunc

// StartAutomationCancelListener starts a Ctrl+C listener for automation.
func StartAutomationCancelListener(showOverlay bool) (cleanup func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	return startAutomationCancelListener(showOverlay, signals, func() {
		signal.Stop(signals)
	})
}

// startAutomationCancelListener prepares for cancellation support.
// If showOverlay is true, shows a macOS notification with cancel instructions.
// Returns a cleanup function that should be called when automation is done.
func startAutomationCancelListener(showOverlay bool, signals <-chan os.Signal, stop func()) (cleanup func()) {
	// Reset cancel state
	automationCanceled.Store(false)

	// Create cancellable context
	automationCtx, automationCancel = context.WithCancel(context.Background())

	if showOverlay {
		_ = ShowAutomationOverlay("Automation running. Press Ctrl+C to cancel.")
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			automationCanceled.Store(true)
			automationCancel()
		case <-done:
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			if stop != nil {
				stop()
			}
			if automationCancel != nil {
				automationCancel()
			}
		})
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
