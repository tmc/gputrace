//go:build !darwin

package cmd

import (
	"fmt"
	"time"

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

Workflow:
  run               Run full automation (open, replay, export)
  open              Open a trace file in Xcode
  close             Close the trace window
  export            Export the trace with performance data
  run-profile       Start profiling in Xcode
  wait-profile      Wait for profiling to complete

Status:
  check-status      Check profiling status (ready, running, complete)
  check-permissions Check required permissions (Accessibility, Screen Recording)

Navigation:
  select-tab        Select a tab by name
  show-performance  Click Show Performance button
  show-summary      Select Summary tab
  show-counters     Select Counters tab
  show-memory       Select Memory tab
  show-dependencies Click Show Dependencies button

Data Export:
  xcode-export-counters  Export GPU counters from Performance view to CSV
  xcode-export-memory    Export memory report from Performance view
  vertex-output          Extract vertex shader output from Xcode GPU debugger
  performance            Performance data commands`,
	Args: cobra.MaximumNArgs(1),
	RunE: darwinOnlyRun,
}

var performanceCmd = &cobra.Command{
	Use:   "performance",
	Short: "Performance data commands",
	Long: `Performance data commands.

This command is only supported on darwin.

Subcommands:
  show      Click the Show Performance button
  status    Check if performance data is available
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

	collectXcodeProfileCmd.PersistentFlags().Duration("timeout", 5*time.Minute, "Timeout for the operation")
	collectXcodeProfileCmd.PersistentFlags().Bool("debug", false, "Print debug information")
	collectXcodeProfileCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose status information")
	collectXcodeProfileCmd.PersistentFlags().Bool("no-bundle", false, "Skip macgo app bundle (use Terminal's Accessibility permission)")
	collectXcodeProfileCmd.PersistentFlags().Bool("background", false, "Run without bringing Xcode to foreground")
	collectXcodeProfileCmd.PersistentFlags().Bool("no-prompt", false, "Don't prompt for permissions, exit with error instead")
	collectXcodeProfileCmd.PersistentFlags().Bool("json", false, "Output results in JSON format")
	collectXcodeProfileCmd.PersistentFlags().Duration("wait", 0, "Wait for lock release (0=no wait, e.g. 5m)")
	collectXcodeProfileCmd.PersistentFlags().Bool("force", false, "Override existing lock")
	collectXcodeProfileCmd.PersistentFlags().Bool("pprof", false, "Enable pprof debug endpoints (:6060 or :6061 in macgo)")
	collectXcodeProfileCmd.Flags().StringP("output", "o", "", "Output path for the exported trace (default: <input>-perfdata.gputrace)")

	addDarwinOnlyXcodeProfileCommands()
	collectXcodeProfileCmd.AddCommand(performanceCmd)
	addDarwinOnlyPerformanceCommands()
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

func addDarwinOnlyXcodeProfileCommands() {
	commands := []struct {
		use   string
		short string
		args  cobra.PositionalArgs
	}{
		{"run <trace_file>", "Run full automation (open, replay, export)", cobra.ExactArgs(1)},
		{"open <trace_file>", "Open a trace file in Xcode", cobra.ExactArgs(1)},
		{"close [trace_file]", "Close the trace window", cobra.MaximumNArgs(1)},
		{"export [output_path]", "Export the trace from Xcode", cobra.MaximumNArgs(1)},
		{"run-profile", "Start profiling in Xcode", cobra.NoArgs},
		{"wait-profile", "Wait for profiling to complete", cobra.NoArgs},
		{"check-status", "Check profiling status", cobra.NoArgs},
		{"check-permissions", "Check required permissions", cobra.NoArgs},
		{"select-tab <tab>", "Select a tab by name", cobra.ExactArgs(1)},
		{"show-performance", "Click Show Performance button", cobra.NoArgs},
		{"show-summary", "Select Summary tab", cobra.NoArgs},
		{"show-counters", "Select Counters tab", cobra.NoArgs},
		{"show-memory", "Select Memory tab", cobra.NoArgs},
		{"show-dependencies", "Click Show Dependencies button", cobra.NoArgs},
		{"xcode-export-counters [output.csv]", "Export GPU counters from Performance view to CSV", cobra.MaximumNArgs(1)},
		{"xcode-export-memory [output]", "Export memory report from Performance view", cobra.MaximumNArgs(1)},
		{"vertex-output <trace.gputrace>", "Extract vertex shader output from Xcode GPU debugger", cobra.ExactArgs(1)},
	}
	for _, c := range commands {
		collectXcodeProfileCmd.AddCommand(&cobra.Command{
			Use:   c.use,
			Short: c.short,
			Args:  c.args,
			RunE:  darwinOnlyRun,
		})
	}
}

func addDarwinOnlyPerformanceCommands() {
	commands := []struct {
		use   string
		short string
	}{
		{"show", "Click the Show Performance button"},
		{"status", "Check if performance data is available"},
		{"overview", "Select the Overview tab"},
		{"timeline", "Select the Timeline tab"},
		{"shaders", "Select the Shaders tab"},
		{"counters", "Select the Counters tab"},
		{"cost-graph", "Select the Cost Graph tab"},
		{"heat-map", "Select the Heat Map tab"},
		{"encoders", "Select the Encoders tab"},
		{"cost", "Select the Cost tab"},
	}
	for _, c := range commands {
		performanceCmd.AddCommand(&cobra.Command{
			Use:   c.use,
			Short: c.short,
			Args:  cobra.NoArgs,
			RunE:  darwinOnlyRun,
		})
	}
}
