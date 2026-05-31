//go:build !darwin

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const darwinOnly = "only supported on darwin"

func darwinOnlyRun(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("%s is %s", cmd.CommandPath(), darwinOnly)
}

var collectXcodeProfileCmd = &cobra.Command{
	Use:     "xcode-profile [trace_file]",
	Aliases: []string{"xp", "collect-xcode-profile"},
	Short:   "Interact with Xcode GPU trace viewer",
	Long: `Control and extract information from Xcode's GPU trace viewer.

This command is only supported on darwin.

Data Export:
  export                 Export the trace from Xcode
  vertex-output          Extract vertex shader output from Xcode GPU debugger`,
	RunE: darwinOnlyRun,
}

var performanceCmd = &cobra.Command{
	Use:   "performance",
	Short: "Performance data commands",
	Long: `Performance data commands.

This command is only supported on darwin.

Subcommands:
  counters  Select the Counters tab`,
	RunE: darwinOnlyRun,
}

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "capture -- command [args...]",
		Short: "Capture GPU trace from a command",
		Long:  "Capture GPU trace from a command. This command is only supported on darwin.",
		RunE:  darwinOnlyRun,
	})

	collectXcodeProfileCmd.AddCommand(&cobra.Command{
		Use:   "export [output_path]",
		Short: "Export the trace from Xcode",
		Args:  cobra.MaximumNArgs(1),
		RunE:  darwinOnlyRun,
	})
	collectXcodeProfileCmd.AddCommand(&cobra.Command{
		Use:   "vertex-output <trace.gputrace>",
		Short: "Extract vertex shader output from Xcode GPU debugger",
		Args:  cobra.ExactArgs(1),
		RunE:  darwinOnlyRun,
	})
	collectXcodeProfileCmd.AddCommand(performanceCmd)
	rootCmd.AddCommand(collectXcodeProfileCmd)

	rootCmd.AddCommand(&cobra.Command{
		Use:   "xcode-bindings",
		Short: "Inspect private Xcode GTShaderProfiler bindings",
		Long:  "Inspect private Xcode GTShaderProfiler bindings. This command is only supported on darwin.",
		RunE:  darwinOnlyRun,
	})
	rootCmd.AddCommand(&cobra.Command{
		Use:   "xcode-parity <trace.gputrace>",
		Short: "Audit Xcode metric parity for a trace",
		Long:  "Audit Xcode metric parity for a trace. This command is only supported on darwin.",
		Args:  cobra.ExactArgs(1),
		RunE:  darwinOnlyRun,
	})
}
