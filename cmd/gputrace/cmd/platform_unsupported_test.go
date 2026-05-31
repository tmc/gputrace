//go:build !darwin

package cmd

import (
	"strings"
	"testing"
)

func TestDarwinOnlyXcodeProfileSurface(t *testing.T) {
	for _, args := range [][]string{
		{"run", "trace.gputrace"},
		{"open", "trace.gputrace"},
		{"close"},
		{"export"},
		{"run-profile"},
		{"wait-profile"},
		{"check-status"},
		{"check-permissions"},
		{"select-tab", "Summary"},
		{"show-performance"},
		{"show-summary"},
		{"show-counters"},
		{"show-memory"},
		{"show-dependencies"},
		{"xcode-export-counters"},
		{"xcode-export-memory"},
		{"vertex-output", "trace.gputrace"},
		{"performance"},
		{"performance", "show"},
		{"performance", "status"},
		{"performance", "counters"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			command, remaining, err := collectXcodeProfileCmd.Find(args)
			if err != nil {
				t.Fatalf("find %v: %v", args, err)
			}
			if len(remaining) > 0 {
				t.Fatalf("remaining args = %v, want none", remaining)
			}
			if command == nil || command.RunE == nil {
				t.Fatalf("command %v missing RunE", args)
			}
			if err := command.RunE(command, nil); err == nil || !strings.Contains(err.Error(), darwinOnly) {
				t.Fatalf("RunE error = %v, want darwin-only error", err)
			}
		})
	}
}

func TestDarwinOnlyXcodeProfileFlags(t *testing.T) {
	for _, name := range []string{
		"timeout",
		"debug",
		"verbose",
		"no-bundle",
		"background",
		"no-prompt",
		"json",
		"wait",
		"force",
		"pprof",
	} {
		if collectXcodeProfileCmd.PersistentFlags().Lookup(name) == nil {
			t.Fatalf("persistent flag %q not registered", name)
		}
	}
	if collectXcodeProfileCmd.Flags().Lookup("output") == nil {
		t.Fatal("output flag not registered")
	}
}
