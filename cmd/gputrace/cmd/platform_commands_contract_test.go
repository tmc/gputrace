package cmd

import "testing"

func TestXcodeProfileCommandMetadata(t *testing.T) {
	for _, path := range [][]string{
		{"open"},
		{"close"},
		{"export"},
		{"run-profile"},
		{"wait-profile"},
		{"check-status"},
		{"check-permissions"},
		{"select-tab"},
		{"xcode-export-counters"},
		{"xcode-export-memory"},
		{"vertex-output"},
		{"list-windows"},
		{"list-menus"},
		{"click-menu"},
		{"list-buttons"},
		{"open-export"},
		{"screenshot"},
		{"debug-tree"},
		{"ensure-checked"},
		{"performance", "show"},
		{"performance", "status"},
		{"performance", "summary"},
		{"performance", "memory"},
		{"navigator"},
	} {
		cmd, _, err := collectXcodeProfileCmd.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		if cmd.Long == "" {
			t.Errorf("%v Long is empty", path)
		}
	}

	for _, name := range []string{"run", "vertex-output"} {
		cmd, _, err := collectXcodeProfileCmd.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		if !cmd.SilenceUsage {
			t.Errorf("%s SilenceUsage = false, want true", name)
		}
	}
}

func TestXcodeBindingsPreservesArbitraryArgs(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"xcode-bindings"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Args != nil {
		t.Fatal("xcode-bindings gained positional argument validation")
	}
}
