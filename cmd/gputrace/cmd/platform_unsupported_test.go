//go:build !darwin

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
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
		{"list-windows"},
		{"list-tabs"},
		{"navigator", "summary"},
		{"navigator", "dependencies"},
		{"navigator", "performance"},
		{"navigator", "memory"},
		{"list-menus"},
		{"click-menu", "File", "Export"},
		{"list-buttons"},
		{"click-button", "Save"},
		{"click-cancel"},
		{"click-replace"},
		{"open-export"},
		{"click-save"},
		{"send-key", "escape"},
		{"check-goto-folder"},
		{"debug-file-browser"},
		{"set-export-path", "/tmp/out.gputrace"},
		{"set-export-filename", "out.gputrace"},
		{"send-enter"},
		{"screenshot"},
		{"debug-tree"},
		{"ensure-checked", "Profile after replay"},
		{"toggle-checkbox", "Profile after replay"},
		{"performance"},
		{"performance", "show"},
		{"performance", "status"},
		{"performance", "overview"},
		{"performance", "timeline"},
		{"performance", "shaders"},
		{"performance", "counters"},
		{"performance", "cost-graph"},
		{"performance", "heat-map"},
		{"performance", "encoders"},
		{"performance", "cost"},
		{"performance", "summary"},
		{"performance", "memory"},
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

func TestDarwinOnlyXcodeProfileHiddenCommands(t *testing.T) {
	for _, args := range [][]string{
		{"list-windows"},
		{"list-tabs"},
		{"navigator"},
		{"list-menus"},
		{"click-menu"},
		{"list-buttons"},
		{"click-button"},
		{"click-cancel"},
		{"click-replace"},
		{"open-export"},
		{"click-save"},
		{"send-key"},
		{"check-goto-folder"},
		{"debug-file-browser"},
		{"set-export-path"},
		{"set-export-filename"},
		{"send-enter"},
		{"screenshot"},
		{"debug-tree"},
		{"ensure-checked"},
		{"toggle-checkbox"},
		{"performance", "summary"},
		{"performance", "memory"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			command := findXcodeProfileCommand(t, args)
			if !command.Hidden {
				t.Fatalf("%v Hidden = false, want true", args)
			}
		})
	}
}

func TestDarwinOnlyXcodeProfileUseStrings(t *testing.T) {
	want := map[string]string{
		"run-profile":           "run-profile [trace_file]",
		"wait-profile":          "wait-profile [trace_file]",
		"check-status":          "check-status [trace_file]",
		"xcode-export-counters": "xcode-export-counters [trace_file]",
		"xcode-export-memory":   "xcode-export-memory [trace_file]",
	}
	for name, wantUse := range want {
		command := findXcodeProfileCommand(t, []string{name})
		if command.Use != wantUse {
			t.Fatalf("%s Use = %q, want %q", name, command.Use, wantUse)
		}
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

func TestDarwinOnlyXcodeProfileChildFlags(t *testing.T) {
	for _, test := range []struct {
		args []string
		flag string
	}{
		{[]string{"run"}, "output"},
		{[]string{"open"}, "foreground"},
		{[]string{"check-status"}, "debug"},
		{[]string{"xcode-export-counters"}, "force"},
		{[]string{"xcode-export-memory"}, "force"},
		{[]string{"screenshot"}, "output"},
		{[]string{"screenshot"}, "no-prompt"},
		{[]string{"debug-tree"}, "verbose"},
		{[]string{"ensure-checked"}, "trace"},
		{[]string{"toggle-checkbox"}, "trace"},
	} {
		t.Run(strings.Join(test.args, "_")+"_"+test.flag, func(t *testing.T) {
			command := findXcodeProfileCommand(t, test.args)
			if command.Flags().Lookup(test.flag) == nil {
				t.Fatalf("%v flag %q not registered", test.args, test.flag)
			}
		})
	}
}

func findXcodeProfileCommand(t *testing.T, args []string) *cobra.Command {
	t.Helper()

	command, _, err := collectXcodeProfileCmd.Find(args)
	if err != nil {
		t.Fatalf("find %v: %v", args, err)
	}
	if command == nil {
		t.Fatalf("find %v returned nil command", args)
	}
	return command
}
