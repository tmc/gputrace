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

var automationState automationCancelState

type automationCancelState struct {
	canceled atomic.Bool

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

func (s *automationCancelState) resetContext() {
	s.canceled.Store(false)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx, s.cancel = context.WithCancel(context.Background())
}

func (s *automationCancelState) context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *automationCancelState) cancelContext() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

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
	automationState.resetContext()

	if showOverlay {
		_ = ShowAutomationOverlay("Automation running. Press Ctrl+C to cancel.")
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			automationState.canceled.Store(true)
			automationState.cancelContext()
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
			automationState.cancelContext()
		})
	}
}

// IsAutomationCanceled returns true if the user has requested to cancel automation.
func IsAutomationCanceled() bool {
	return automationState.canceled.Load()
}

// ResetAutomationCancel resets the cancel flag.
func ResetAutomationCancel() {
	automationState.canceled.Store(false)
}

// CheckCancelAndReturn returns an error if automation was canceled.
func CheckCancelAndReturn() error {
	if automationState.canceled.Load() {
		return fmt.Errorf("automation canceled by user")
	}
	return nil
}

func automationContext() context.Context {
	return automationState.context()
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
