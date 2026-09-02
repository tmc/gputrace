//go:build darwin

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportPathFromSheetState(t *testing.T) {
	state := exportSheetState{
		Filename:            "Counters.csv",
		DirectoryCandidates: []string{"tmp", "file:///Users/tmc/tmp/reports"},
	}
	got, err := exportPathFromSheetState(state)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/Users/tmc/tmp/reports/Counters.csv"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}

	for _, state := range []exportSheetState{
		{Filename: "Counters.csv", DirectoryCandidates: []string{"tmp"}},
		{DirectoryCandidates: []string{"/Users/tmc/tmp"}},
		{Filename: "../Counters.csv", DirectoryCandidates: []string{"/Users/tmc/tmp"}},
	} {
		if _, err := exportPathFromSheetState(state); err == nil {
			t.Fatalf("state %+v returned nil error", state)
		}
	}
}

func TestWaitForExportFileRequiresStableNonEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Counters.csv")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte("encoder,cost\n"), 0o644)
	}()
	if err := waitForExportFile(context.Background(), path, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(t.TempDir(), "missing.csv")
	err := waitForExportFile(context.Background(), missing, 0)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing file error = %v", err)
	}
}
