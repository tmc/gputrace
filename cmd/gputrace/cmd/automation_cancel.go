package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var errAutomationCanceled = errors.New("automation canceled by user")

// StartAutomationCancelListener starts a Ctrl+C listener for automation.
func StartAutomationCancelListener(parent context.Context, showOverlay bool) (context.Context, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	return startAutomationCancelListener(parent, showOverlay, signals, func() {
		signal.Stop(signals)
	})
}

// startAutomationCancelListener prepares for cancellation support.
// If showOverlay is true, shows a macOS notification with cancel instructions.
// Returns a cleanup function that should be called when automation is done.
func startAutomationCancelListener(parent context.Context, showOverlay bool, signals <-chan os.Signal, stop func()) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)

	if showOverlay {
		_ = ShowAutomationOverlay(ctx, "Automation running. Press Ctrl+C to cancel.")
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			cancel(errAutomationCanceled)
		case <-done:
		case <-parent.Done():
		}
	}()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			close(done)
			if stop != nil {
				stop()
			}
			cancel(nil)
		})
	}
	return ctx, cleanup
}

func checkAutomationCanceled(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
		return nil
	}
}

func waitForAutomation(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

// ShowAutomationOverlay shows a notification that automation is running.
// Uses AppleScript to show a system notification.
func ShowAutomationOverlay(parent context.Context, message string) error {
	script := fmt.Sprintf(`display notification %q with title "gputrace" subtitle "Press Ctrl+C to cancel"`, message)
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	return cmd.Run()
}
