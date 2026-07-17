package cmd

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestAutomationCancelListenerCancelsOnSignal(t *testing.T) {
	signals := make(chan os.Signal, 1)
	ctx, cleanup := startAutomationCancelListener(context.Background(), false, signals, nil)
	defer cleanup()

	signals <- os.Interrupt

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("listener did not cancel automation context")
	}

	if err := checkAutomationCanceled(ctx); !errors.Is(err, errAutomationCanceled) {
		t.Fatalf("checkAutomationCanceled = %v, want %v", err, errAutomationCanceled)
	}
}

func TestAutomationCancelListenerPropagatesParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancelCause(context.Background())
	ctx, cleanup := startAutomationCancelListener(parent, false, make(chan os.Signal), nil)
	defer cleanup()

	want := errors.New("parent canceled")
	cancel(want)
	<-ctx.Done()
	if err := checkAutomationCanceled(ctx); !errors.Is(err, want) {
		t.Fatalf("checkAutomationCanceled = %v, want %v", err, want)
	}
}

func TestWaitForAutomationCancellation(t *testing.T) {
	want := errors.New("canceled while waiting")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(want)

	if err := waitForAutomation(ctx, time.Hour); !errors.Is(err, want) {
		t.Fatalf("waitForAutomation = %v, want %v", err, want)
	}
}
