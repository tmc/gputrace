package cmd

import (
	"time"

	"github.com/spf13/cobra"
)

// collectXcodeProfileCmd is declared on every platform so that help output does
// not depend on the host running gputrace. Only the command execution hooks are
// platform-specific.
var collectXcodeProfileCmd = newXcodeProfileCommand()

var performanceCmd = newPerformanceCommand()

func newXcodeProfileCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "xcode-profile [trace_file]",
		Aliases: []string{"xp", "collect-xcode-profile"},
		Short:   "Interact with Xcode GPU trace viewer",
		Long: `Control and extract information from Xcode's GPU trace viewer.

This command uses Accessibility APIs to control Xcode's UI and extract data.

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
  show-memory       Click the Show Memory button
  show-dependencies Click Show Dependencies button

Data Export:
  xcode-export-counters  Export GPU counters from Performance view to CSV
  xcode-export-memory    Export memory report from Performance view
  vertex-output          Extract vertex shader output from Xcode GPU debugger
  performance            Performance data commands`,
		Args:              cobra.MaximumNArgs(1),
		PersistentPreRunE: platformXcodeProfilePreRun,
		RunE:              platformXcodeProfileRun("xcode-profile"),
	}

	cmd.PersistentFlags().Duration("timeout", 5*time.Minute, "Timeout for the operation")
	cmd.PersistentFlags().Bool("debug", false, "Print debug information")
	cmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose status information")
	cmd.PersistentFlags().Bool("no-bundle", false, "Skip macgo app bundle (use Terminal's Accessibility permission)")
	cmd.PersistentFlags().Bool("background", false, "Run without bringing Xcode to foreground")
	cmd.PersistentFlags().Bool("no-prompt", false, "Don't prompt for permissions, exit with error instead")
	cmd.PersistentFlags().Bool("json", false, "Output results in JSON format")
	cmd.PersistentFlags().Duration("wait", 0, "Wait for lock release (0=no wait, e.g. 5m)")
	cmd.PersistentFlags().Bool("force", false, "Override existing lock")
	cmd.PersistentFlags().Bool("pprof", false, "Enable pprof debug endpoints (:6060 or :6061 in macgo)")
	cmd.Flags().StringP("output", "o", "", "Output path for the exported trace (default: <input>-perfdata.gputrace)")

	for _, spec := range xcodeProfileCommandSpecs {
		cmd.AddCommand(newPlatformCommand(spec))
	}
	cmd.AddCommand(performanceCmd)
	cmd.AddCommand(newNavigatorCommand())
	return cmd
}

type platformCommandSpec struct {
	name         string
	use          string
	short        string
	long         string
	aliases      []string
	args         cobra.PositionalArgs
	hidden       bool
	silenceUsage bool
	flags        func(*cobra.Command)
}

func newPlatformCommand(spec platformCommandSpec) *cobra.Command {
	cmd := &cobra.Command{
		Use:          spec.use,
		Aliases:      spec.aliases,
		Short:        spec.short,
		Long:         spec.long,
		Args:         spec.args,
		Hidden:       spec.hidden,
		SilenceUsage: spec.silenceUsage,
		RunE:         platformXcodeProfileRun(spec.name),
	}
	if spec.flags != nil {
		spec.flags(cmd)
	}
	configurePlatformCommand(spec.name, cmd)
	return cmd
}

var xcodeProfileCommandSpecs = []platformCommandSpec{
	{name: "run", use: "run <trace_file>", short: "Run full automation (open, replay, export)", args: cobra.ExactArgs(1), silenceUsage: true, flags: outputFlag},
	{name: "open", use: "open <trace_file>", short: "Open a trace file in Xcode", long: `Opens a GPU trace file in Xcode and waits for the window to be ready.
By default, opens in background without stealing focus. Use --foreground to bring Xcode to front.`, args: cobra.ExactArgs(1), flags: foregroundFlag},
	{name: "close", use: "close [trace_file]", short: "Close the trace window in Xcode", long: "Closes the Xcode window for the specified trace file, or the first window if no file specified.", args: cobra.MaximumNArgs(1)},
	{name: "export", use: "export [output_path]", short: "Export the trace from Xcode", long: `Triggers File > Export in Xcode and saves to the specified path.
If no path is specified, it defaults to the trace file path with -perfdata suffix, inferred from the Xcode window.`, args: cobra.MaximumNArgs(1)},
	{name: "run-profile", use: "run-profile [trace_file]", aliases: []string{"run-replay"}, short: "Start profiling in Xcode", long: `Clicks the Profile button if available, otherwise falls back to Replay button.
The Profile button starts profiling directly without needing additional checkboxes.`, args: cobra.MaximumNArgs(1)},
	{name: "wait-profile", use: "wait-profile [trace_file]", aliases: []string{"wait-replay"}, short: "Wait for profiling to complete", long: "Polls Xcode until profiling completes (Show Performance button appears or Replay re-enabled).", args: cobra.MaximumNArgs(1)},
	{name: "check-status", use: "check-status [trace_file]", short: "Check profiling status", long: `Returns the current profiling status:
  - initializing: Trace loading, Replay button disabled
  - replay-ready: Ready to start replay
  - running: Profiling/replay in progress
  - complete: Performance data available
  - unknown: Unable to determine status`, args: cobra.MaximumNArgs(1), flags: debugFlag},
	{name: "check-permissions", use: "check-permissions", short: "Check required permissions", long: `Check if gputrace has the required permissions:
  - Accessibility: Required for UI automation via AX APIs
  - Screen Recording: Required for screenshots

Use --json for machine-readable output.
Use --no-prompt to check without triggering permission dialogs.`, args: cobra.NoArgs},
	{name: "select-tab", use: "select-tab <tab_name>", short: "Select a tab in the trace viewer", long: `Selects a tab in the Xcode GPU trace viewer.

Available tabs:
  summary      - Summary view with overview statistics
  counters     - GPU performance counters
  memory       - Memory allocation and usage
  encoders     - Encoder timeline
  dependencies - Resource dependencies
  performance  - Performance metrics (same as Show Performance button)`, args: cobra.ExactArgs(1)},
	{name: "show-performance", use: "show-performance", short: "Click the Show Performance button", args: cobra.NoArgs},
	{name: "show-summary", use: "show-summary", short: "Select the Summary tab", args: cobra.NoArgs},
	{name: "show-counters", use: "show-counters", short: "Select the Counters tab", args: cobra.NoArgs},
	{name: "show-memory", use: "show-memory", short: "Click the Show Memory button", args: cobra.NoArgs},
	{name: "show-dependencies", use: "show-dependencies", short: "Click the Show Dependencies button", args: cobra.NoArgs},
	{name: "xcode-export-counters", use: "xcode-export-counters [trace_file]", short: "Export GPU counters from Xcode's Performance view to CSV", long: xcodeExportCountersLong, args: cobra.MaximumNArgs(1), flags: forceFlag},
	{name: "xcode-export-memory", use: "xcode-export-memory [trace_file]", short: "Export memory report from Xcode's Performance view", long: xcodeExportMemoryLong, args: cobra.MaximumNArgs(1), flags: forceFlag},
	{name: "vertex-output", use: "vertex-output <trace.gputrace>", short: "Extract vertex shader output from Xcode GPU debugger", long: vertexOutputLong, args: cobra.ExactArgs(1), silenceUsage: true, flags: vertexOutputFlags},
	{name: "list-windows", use: "list-windows [trace_file]", short: "List Xcode windows", long: "Lists Xcode windows with their titles, checkboxes, and buttons. Optionally filter by trace filename.", args: cobra.MaximumNArgs(1), hidden: true},
	{name: "list-tabs", use: "list-tabs [trace_file]", short: "List available tabs in the trace viewer", args: cobra.MaximumNArgs(1), hidden: true},
	{name: "list-menus", use: "list-menus [menu-name]", short: "List menu bar items and their contents", long: "Lists the menu bar items in Xcode and optionally shows the items in a specific menu.", args: cobra.MaximumNArgs(1), hidden: true},
	{name: "click-menu", use: "click-menu <menu> <item>", short: "Click a menu item", long: "Clicks a menu item in Xcode's menu bar.", args: cobra.ExactArgs(2), hidden: true},
	{name: "list-buttons", use: "list-buttons", short: "List buttons using XCUIAutomation and AX", long: "Lists all buttons in Xcode using both XCUIAutomation framework and Accessibility APIs.", args: cobra.NoArgs, hidden: true},
	{name: "click-button", use: "click-button <name>", short: "Click a button by name in any Xcode window/dialog", args: cobra.ExactArgs(1), hidden: true},
	{name: "click-cancel", use: "click-cancel", short: "Click Cancel button in any Xcode dialog", args: cobra.NoArgs, hidden: true},
	{name: "click-replace", use: "click-replace", short: "Click Replace button in any Xcode dialog", args: cobra.NoArgs, hidden: true},
	{name: "open-export", use: "open-export [output_path]", short: "Open the export dialog and set the output path", long: `Opens the Export dialog in Xcode and sets the output path.

If output_path is specified, navigates to that directory and sets the filename.
If no path specified, uses the original trace name with -perfdata suffix.`, args: cobra.MaximumNArgs(1), hidden: true},
	{name: "click-save", use: "click-save", short: "Click the Save button in an open export dialog", args: cobra.NoArgs, hidden: true},
	{name: "send-key", use: "send-key <key>", short: "Send a keyboard shortcut (for debugging)", args: cobra.ExactArgs(1), hidden: true},
	{name: "check-goto-folder", use: "check-goto-folder", short: "Check if Go to Folder dialog is open", args: cobra.NoArgs, hidden: true},
	{name: "debug-file-browser", use: "debug-file-browser", short: "Debug: list file browser elements in export dialog", args: cobra.NoArgs, hidden: true},
	{name: "set-export-path", use: "set-export-path <absolute_path>", short: "Set the export path (note: directory navigation limited)", args: cobra.ExactArgs(1), hidden: true},
	{name: "set-export-filename", use: "set-export-filename <filename>", short: "Set the export filename (recommended)", args: cobra.ExactArgs(1), hidden: true},
	{name: "send-enter", use: "send-enter", short: "Send Enter key to Xcode", args: cobra.NoArgs, hidden: true},
	{name: "screenshot", use: "screenshot [trace_file]", short: "Capture a screenshot of the Xcode window", long: screenshotLong, args: cobra.MaximumNArgs(1), hidden: true, flags: screenshotFlags},
	{name: "debug-tree", use: "debug-tree [trace_file]", short: "Print UI tree to find key elements", long: "Prints the Accessibility tree structure showing paths to key buttons like Replay, Stop, Show Performance.", args: cobra.MaximumNArgs(1), hidden: true, flags: verboseFlag},
	{name: "ensure-checked", use: "ensure-checked <checkbox_title>", short: "Ensure a checkbox is checked", long: "Finds a checkbox by title in an Xcode window and ensures it is checked.", args: cobra.ExactArgs(1), hidden: true, flags: traceFlag},
	{name: "toggle-checkbox", use: "toggle-checkbox <checkbox_title>", short: "Toggle a checkbox", args: cobra.ExactArgs(1), hidden: true, flags: traceFlag},
}

const xcodeExportCountersLong = `Triggers Xcode's Export GPU Counters dialog and accepts the default save location.

This command:
1. Finds the Xcode window for the specified trace (or first window)
2. Clicks Editor > Export GPU Counters... menu via AX
3. Clicks Save to accept the default filename/location

The file is saved to wherever Xcode's save dialog defaults to.

Use --force to automatically replace existing files.`

const xcodeExportMemoryLong = `Triggers Xcode's Export Memory Report dialog and accepts the default save location.

This command:
1. Finds the Xcode window for the specified trace (or first window)
2. Clicks Editor > Export Memory Report... menu via AX
3. Clicks Save to accept the default filename/location

The file is saved to wherever Xcode's save dialog defaults to.

Use --force to automatically replace existing files.`

const vertexOutputLong = `Opens a .gputrace in Xcode, navigates to a specific draw call,
and extracts the vertex shader output table via Accessibility APIs.

This automates what you'd normally do manually:
  1. Open trace in Xcode
  2. Switch to Debug navigator
  3. Expand the draw call tree
  4. Click the target draw call
  5. Read the vertex output table from the editor area`

const screenshotLong = `Captures a screenshot of the current Xcode GPU trace window.

Uses CoreGraphics APIs to capture the specific window by ID, so the window
does not need to be in front or visible on screen.

If no output path is specified, saves to /tmp/xcode-screenshot-<timestamp>.png

Use --no-prompt to trigger a TCC database entry for Screen Recording permission
without prompting the user.`

func outputFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("output", "o", "", "Output path for the exported trace")
}

func foregroundFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("foreground", false, "Bring Xcode to foreground (default: open in background)")
}

func debugFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("debug", false, "Print debug info")
}

func forceFlag(cmd *cobra.Command) {
	cmd.Flags().BoolP("force", "f", false, "Replace existing file if it exists")
}

func vertexOutputFlags(cmd *cobra.Command) {
	cmd.Flags().Int("draw", 21, "Draw call number to inspect")
	cmd.Flags().StringP("output", "o", "", "Output file path (default: stdout)")
	cmd.Flags().String("format", "text", "Output format: text, json")
}

func screenshotFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("output", "o", "", "Output path for screenshot")
	cmd.Flags().Bool("no-prompt", false, "Trigger TCC entry without prompting")
}

func verboseFlag(cmd *cobra.Command) {
	cmd.Flags().BoolP("verbose", "v", false, "Print verbose progress info")
}

func traceFlag(cmd *cobra.Command) {
	cmd.Flags().String("trace", "", "Target window by trace filename")
}

func newPerformanceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "performance",
		Short: "Performance data commands",
		Long: `Commands for working with GPU performance data in Xcode.

Subcommands:
  show      Click the "Show Performance" button to reveal performance data
  status    Check if performance data is available
  summary   Extract visible summary statistics
  counters  Select the Counters tab
  memory    Extract memory usage info when visible`,
		RunE: platformXcodeProfileRun("performance"),
	}
	for _, name := range []string{"show", "status", "overview", "timeline", "shaders", "counters", "cost-graph", "heat-map", "encoders", "cost", "summary", "memory"} {
		hidden := name == "summary" || name == "memory"
		cmd.AddCommand(&cobra.Command{Use: name, Short: performanceCommandShort(name), Long: performanceCommandLong(name), Hidden: hidden, Args: cobra.NoArgs, RunE: platformXcodeProfileRun("performance/" + name)})
	}
	return cmd
}

func performanceCommandLong(name string) string {
	switch name {
	case "show":
		return `Clicks the "Show Performance" button in Xcode to reveal GPU performance data.`
	case "status":
		return `Checks whether the "Show Performance" button is available and enabled.`
	case "summary":
		return `Extracts visible label/value metrics from Xcode's Performance view.`
	case "memory":
		return `Extracts memory allocation and usage information from Xcode when visible.`
	default:
		return "Selects the " + name + " tab in Xcode's Performance view."
	}
}

func performanceCommandShort(name string) string {
	switch name {
	case "show":
		return "Click the Show Performance button"
	case "status":
		return "Check if performance data is available"
	case "summary":
		return "Extract summary statistics"
	case "memory":
		return "Extract memory usage info"
	default:
		return "Select the " + name + " tab"
	}
}

func newNavigatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "navigator",
		Short:  "Navigate to different sections in the Debug navigator",
		Long:   "Select items in the Debug navigator panel (left sidebar).",
		Hidden: true,
	}
	for _, name := range []string{"summary", "dependencies", "performance", "memory"} {
		cmd.AddCommand(&cobra.Command{Use: name, Short: "Select " + name + " in navigator", Args: cobra.NoArgs, RunE: platformXcodeProfileRun("navigator/" + name)})
	}
	return cmd
}

func init() {
	rootCmd.AddCommand(collectXcodeProfileCmd)
	rootCmd.AddCommand(newPlatformCommand(platformCommandSpec{name: "xcode-bindings", use: "xcode-bindings", short: "Inspect private Xcode GTShaderProfiler bindings", long: xcodeBindingsLong, flags: jsonFlag}))
	rootCmd.AddCommand(newPlatformCommand(platformCommandSpec{name: "xcode-parity", use: "xcode-parity <trace.gputrace>", short: "Audit Xcode metric parity for a trace", long: xcodeParityLong, args: cobra.ExactArgs(1), flags: jsonFlag}))
}

const xcodeBindingsLong = `Inspect the private GTShaderProfiler binding surface used for Xcode parity.

The command checks class and selector availability only. It does not construct
GTShaderProfiler objects or parse trace data, so it is safe to run as a
capability probe before enabling deeper profiler adapters.`

const xcodeParityLong = `Audit Xcode metric parity for a trace.

The report compares the trace's timeline metadata against the private
GTShaderProfiler binding surface and lists the remaining adapter work for any
missing Xcode-style fields.`

func jsonFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("json", false, "Output in JSON format")
}
