package cmd

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCommandHelpRenders(t *testing.T) {
	walkCommands(t, rootCmd)
}

func walkCommands(t *testing.T, command *cobra.Command) {
	t.Helper()

	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.Help(); err != nil {
		t.Fatalf("help failed for %q: %v", command.CommandPath(), err)
	}

	for _, sub := range command.Commands() {
		walkCommands(t, sub)
	}
}

func TestShadersHelpMarksHighRegisterSourceBacked(t *testing.T) {
	if !strings.Contains(shadersCmd.Long, "High Register, shown only when source-backed") {
		t.Fatalf("shaders help should not imply high register is always available:\n%s", shadersCmd.Long)
	}
}
