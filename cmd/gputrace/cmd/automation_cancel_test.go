package cmd

import (
	"os"
	"testing"
	"time"
)

func TestAutomationCancelListenerCancelsOnSignal(t *testing.T) {
	signals := make(chan os.Signal, 1)
	cleanup := startAutomationCancelListener(false, signals, nil)
	defer cleanup()

	signals <- os.Interrupt

	deadline := time.After(2 * time.Second)
	for !IsAutomationCanceled() {
		select {
		case <-deadline:
			t.Fatal("listener did not mark automation canceled")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	select {
	case <-automationCtx.Done():
	case <-deadline:
		t.Fatal("listener did not cancel automation context")
	}

	if err := CheckCancelAndReturn(); err == nil {
		t.Fatal("CheckCancelAndReturn succeeded after cancellation")
	}
}
