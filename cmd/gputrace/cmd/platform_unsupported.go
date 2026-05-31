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
  summary   Extract visible summary statistics
  counters  Select the Counters tab
  memory    Extract memory usage info when visible`,
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
		hide  bool
		flags func(*cobra.Command)
	}{
		{"run <trace_file>", "Run full automation (open, replay, export)", cobra.ExactArgs(1), false, func(cmd *cobra.Command) {
			cmd.Flags().StringP("output", "o", "", "Output path for the exported trace")
		}},
		{"open <trace_file>", "Open a trace file in Xcode", cobra.ExactArgs(1), false, func(cmd *cobra.Command) {
			cmd.Flags().Bool("foreground", false, "Bring Xcode to foreground (default: open in background)")
		}},
		{"close [trace_file]", "Close the trace window", cobra.MaximumNArgs(1), false, nil},
		{"export [output_path]", "Export the trace from Xcode", cobra.MaximumNArgs(1), false, nil},
		{"run-profile [trace_file]", "Start profiling in Xcode", cobra.MaximumNArgs(1), false, nil},
		{"wait-profile [trace_file]", "Wait for profiling to complete", cobra.MaximumNArgs(1), false, nil},
		{"check-status [trace_file]", "Check profiling status", cobra.MaximumNArgs(1), false, func(cmd *cobra.Command) {
			cmd.Flags().Bool("debug", false, "Print debug info")
		}},
		{"check-permissions", "Check required permissions", cobra.NoArgs, false, nil},
		{"select-tab <tab_name>", "Select a tab in the trace viewer", cobra.ExactArgs(1), false, nil},
		{"show-performance", "Click the Show Performance button", cobra.NoArgs, false, nil},
		{"show-summary", "Select the Summary tab", cobra.NoArgs, false, nil},
		{"show-counters", "Select the Counters tab", cobra.NoArgs, false, nil},
		{"show-memory", "Click the Show Memory button", cobra.NoArgs, false, nil},
		{"show-dependencies", "Click the Show Dependencies button", cobra.NoArgs, false, nil},
		{"xcode-export-counters [trace_file]", "Export GPU counters from Xcode's Performance view to CSV", cobra.MaximumNArgs(1), false, func(cmd *cobra.Command) {
			cmd.Flags().BoolP("force", "f", false, "Replace existing file if it exists")
		}},
		{"xcode-export-memory [trace_file]", "Export memory report from Xcode's Performance view", cobra.MaximumNArgs(1), false, func(cmd *cobra.Command) {
			cmd.Flags().BoolP("force", "f", false, "Replace existing file if it exists")
		}},
		{"vertex-output <trace.gputrace>", "Extract vertex shader output from Xcode GPU debugger", cobra.ExactArgs(1), false, nil},
		{"list-windows [trace_file]", "List Xcode windows", cobra.MaximumNArgs(1), true, nil},
		{"list-tabs [trace_file]", "List available tabs in the trace viewer", cobra.MaximumNArgs(1), true, nil},
		{"list-menus [menu-name]", "List menu bar items and their contents", cobra.MaximumNArgs(1), true, nil},
		{"click-menu <menu> <item>", "Click a menu item", cobra.ExactArgs(2), true, nil},
		{"list-buttons", "List buttons using XCUIAutomation and AX", cobra.NoArgs, true, nil},
		{"click-button <name>", "Click a button by name in any Xcode window/dialog", cobra.ExactArgs(1), true, nil},
		{"click-cancel", "Click Cancel button in any Xcode dialog", cobra.NoArgs, true, nil},
		{"click-replace", "Click Replace button in any Xcode dialog", cobra.NoArgs, true, nil},
		{"open-export [output_path]", "Open the export dialog and set the output path", cobra.MaximumNArgs(1), true, nil},
		{"click-save", "Click the Save button in an open export dialog", cobra.NoArgs, true, nil},
		{"send-key <key>", "Send a keyboard shortcut (for debugging)", cobra.ExactArgs(1), true, nil},
		{"check-goto-folder", "Check if Go to Folder dialog is open", cobra.NoArgs, true, nil},
		{"debug-file-browser", "Debug: list file browser elements in export dialog", cobra.NoArgs, true, nil},
		{"set-export-path <absolute_path>", "Set the export path (note: directory navigation limited)", cobra.ExactArgs(1), true, nil},
		{"set-export-filename <filename>", "Set the export filename (recommended)", cobra.ExactArgs(1), true, nil},
		{"send-enter", "Send Enter key to Xcode", cobra.NoArgs, true, nil},
		{"screenshot [trace_file]", "Capture a screenshot of the Xcode window", cobra.MaximumNArgs(1), true, func(cmd *cobra.Command) {
			cmd.Flags().StringP("output", "o", "", "Output path for screenshot")
			cmd.Flags().Bool("no-prompt", false, "Trigger TCC entry without prompting")
		}},
		{"debug-tree [trace_file]", "Print UI tree to find key elements", cobra.MaximumNArgs(1), true, func(cmd *cobra.Command) {
			cmd.Flags().BoolP("verbose", "v", false, "Print verbose progress info")
		}},
		{"ensure-checked <checkbox_title>", "Ensure a checkbox is checked", cobra.ExactArgs(1), true, func(cmd *cobra.Command) {
			cmd.Flags().String("trace", "", "Target window by trace filename")
		}},
		{"toggle-checkbox <checkbox_title>", "Toggle a checkbox", cobra.ExactArgs(1), true, func(cmd *cobra.Command) {
			cmd.Flags().String("trace", "", "Target window by trace filename")
		}},
	}
	for _, c := range commands {
		cmd := &cobra.Command{
			Use:    c.use,
			Short:  c.short,
			Hidden: c.hide,
			Args:   c.args,
			RunE:   darwinOnlyRun,
		}
		if c.flags != nil {
			c.flags(cmd)
		}
		collectXcodeProfileCmd.AddCommand(cmd)
	}

	navigatorCmd := &cobra.Command{
		Use:    "navigator",
		Short:  "Navigate to different sections in the Debug navigator",
		Hidden: true,
		RunE:   darwinOnlyRun,
	}
	collectXcodeProfileCmd.AddCommand(navigatorCmd)
	for _, c := range []struct {
		use   string
		short string
	}{
		{"summary", "Select Summary in navigator"},
		{"dependencies", "Select Dependencies in navigator"},
		{"performance", "Select Performance in navigator"},
		{"memory", "Select Memory in navigator"},
	} {
		navigatorCmd.AddCommand(&cobra.Command{
			Use:   c.use,
			Short: c.short,
			Args:  cobra.NoArgs,
			RunE:  darwinOnlyRun,
		})
	}
}

func addDarwinOnlyPerformanceCommands() {
	commands := []struct {
		use   string
		short string
		hide  bool
	}{
		{"show", "Click the Show Performance button", false},
		{"status", "Check if performance data is available", false},
		{"overview", "Select the Overview tab", false},
		{"timeline", "Select the Timeline tab", false},
		{"shaders", "Select the Shaders tab", false},
		{"counters", "Select the Counters tab", false},
		{"cost-graph", "Select the Cost Graph tab", false},
		{"heat-map", "Select the Heat Map tab", false},
		{"encoders", "Select the Encoders tab", false},
		{"cost", "Select the Cost tab", false},
		{"summary", "Extract summary statistics", true},
		{"memory", "Extract memory usage info", true},
	}
	for _, c := range commands {
		performanceCmd.AddCommand(&cobra.Command{
			Use:    c.use,
			Short:  c.short,
			Hidden: c.hide,
			Args:   cobra.NoArgs,
			RunE:   darwinOnlyRun,
		})
	}
}
