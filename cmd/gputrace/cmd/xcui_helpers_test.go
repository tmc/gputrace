//go:build darwin

package cmd

import (
	"errors"
	"strings"
	"testing"
)

func TestRunFileExportProbeClosesDisabledMenu(t *testing.T) {
	open := false
	closed := 0
	found, enabled, err := runFileExportProbe(fileExportProbeOps{
		open: func() error {
			open = true
			return nil
		},
		state: func() (bool, bool, error) {
			if !open {
				t.Fatal("menu was not open during probe")
			}
			return true, false, nil
		},
		close: func() error {
			closed++
			open = false
			return nil
		},
	})
	if err != nil || !found || enabled {
		t.Fatalf("probe = (%t, %t, %v), want (true, false, nil)", found, enabled, err)
	}
	if open || closed != 1 {
		t.Fatalf("menu open=%t close calls=%d, want false, 1", open, closed)
	}
}

func TestRunFileExportProbeReportsCloseFailure(t *testing.T) {
	closeErr := errors.New("menu_open=true")
	found, enabled, err := runFileExportProbe(fileExportProbeOps{
		open:  func() error { return nil },
		state: func() (bool, bool, error) { return true, false, nil },
		close: func() error { return closeErr },
	})
	if found || enabled || !errors.Is(err, closeErr) || !strings.Contains(err.Error(), "menu_open=true") {
		t.Fatalf("probe = (%t, %t, %v), want closed-state failure", found, enabled, err)
	}
}

func TestRunFileExportProbeClosesAfterOpenError(t *testing.T) {
	openErr := errors.New("AXPress failed after opening File")
	open := false
	closed := 0
	found, enabled, err := runFileExportProbe(fileExportProbeOps{
		open: func() error {
			open = true
			return openErr
		},
		state: func() (bool, bool, error) {
			t.Fatal("state called after open error")
			return false, false, nil
		},
		close: func() error {
			closed++
			open = false
			return nil
		},
	})
	if found || enabled || !errors.Is(err, openErr) {
		t.Fatalf("probe = (%t, %t, %v), want open error", found, enabled, err)
	}
	if open || closed != 1 {
		t.Fatalf("menu open=%t close calls=%d, want false, 1", open, closed)
	}
}

func TestCloseMenuWithOpsFallsBackToScopedEscape(t *testing.T) {
	cancelErr := errors.New("AXCancel failed")
	expanded := true
	cancelCalls := 0
	escapeCalls := 0
	err := closeMenuWithOps(menuCloseOps{
		expanded: func() (bool, error) { return expanded, nil },
		cancel: func() error {
			cancelCalls++
			return cancelErr
		},
		escape: func() error {
			escapeCalls++
			expanded = false
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if expanded || cancelCalls != 1 || escapeCalls != 1 {
		t.Fatalf("expanded=%t cancel calls=%d escape calls=%d, want false, 1, 1",
			expanded, cancelCalls, escapeCalls)
	}
}
