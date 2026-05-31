package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestCLICommandContract(t *testing.T) {
	seen := map[string]bool{}
	walkCLI(t, rootCmd, func(t *testing.T, c *cobra.Command) {
		t.Helper()

		path := c.CommandPath()
		if seen[path] {
			t.Fatalf("duplicate command path: %q", path)
		}
		seen[path] = true

		if strings.TrimSpace(c.Use) == "" {
			t.Fatalf("empty Use for %q", path)
		}
		fields := strings.Fields(c.Use)
		if len(fields) == 0 || fields[0] != c.Name() {
			t.Fatalf("Use %q must start with command name %q", c.Use, c.Name())
		}
		if path != rootCmd.CommandPath() && !c.Hidden && strings.TrimSpace(c.Short) == "" {
			t.Fatalf("empty Short for %q", path)
		}

		if len(c.Commands()) == 0 && c.Run == nil && c.RunE == nil {
			t.Fatalf("leaf command %q has no Run/RunE", path)
		}

		assertNoDuplicateShorthand(t, path, c.LocalFlags())
		assertNoDuplicateShorthand(t, path+" (persistent)", c.PersistentFlags())
	})
}

func TestMTLBUsageMarksRequiredTraceOperands(t *testing.T) {
	checks := []*cobra.Command{
		mtlbCmd,
		mtlbListCmd,
		mtlbInfoCmd,
		mtlbFunctionsCmd,
		mtlbStatsCmd,
		mtlbExtractCmd,
		mtlbExportFunctionsCmd,
	}
	for _, cmd := range checks {
		if strings.Contains(cmd.Use, "[trace") {
			t.Fatalf("%s usage marks required trace operand optional: %q", cmd.CommandPath(), cmd.Use)
		}
		if !strings.Contains(cmd.Use, "<trace") {
			t.Fatalf("%s usage does not mark required trace operand: %q", cmd.CommandPath(), cmd.Use)
		}
		if err := cmd.Args(cmd, nil); err == nil {
			t.Fatalf("%s accepts missing required trace operand", cmd.CommandPath())
		}
	}
}

func walkCLI(t *testing.T, c *cobra.Command, fn func(*testing.T, *cobra.Command)) {
	t.Helper()
	fn(t, c)
	for _, sub := range c.Commands() {
		walkCLI(t, sub, fn)
	}
}

func assertNoDuplicateShorthand(t *testing.T, path string, fs *pflag.FlagSet) {
	t.Helper()
	seen := map[string]string{}
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Shorthand == "" {
			return
		}
		if prev, ok := seen[f.Shorthand]; ok {
			t.Fatalf("duplicate shorthand -%s for %q and %q on %s", f.Shorthand, prev, f.Name, path)
		}
		seen[f.Shorthand] = f.Name
	})
}
