//go:build darwin

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func platformXcodeProfilePreRun(cmd *cobra.Command, args []string) error {
	if err := syncCollectProfileOptions(cmd); err != nil {
		return err
	}
	if cmd.Name() == "check-permissions" {
		return setupMacgo()
	}
	return collectXcodeProfilePreRun(cmd, args)
}

func configurePlatformCommand(name string, cmd *cobra.Command) {
	switch name {
	case "list-menus", "click-menu", "ensure-checked", "toggle-checkbox", "send-key", "check-goto-folder", "debug-file-browser":
		cmd.Args = unsupportedXcodeProfileJSONArgs(name, cmd.Args)
		documentUnsupportedXcodeProfileJSON(cmd)
	}
}

func platformXcodeProfileRun(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := syncCollectProfileOptions(cmd); err != nil {
			return err
		}
		switch name {
		case "xcode-profile":
			if len(args) == 0 {
				return cmd.Help()
			}
			collectProfileOpts.output, _ = cmd.Flags().GetString("output")
			return runCollectXcodeProfileFull(cmd, args)
		case "run":
			collectProfileOpts.output, _ = cmd.Flags().GetString("output")
			return runCollectXcodeProfileFull(cmd, args)
		case "open":
			foreground, _ := cmd.Flags().GetBool("foreground")
			return runOpenTrace(cmd, args, &openTraceOptions{foreground: foreground})
		case "close":
			return runCloseTrace(cmd, args)
		case "export":
			return runExport(cmd, args)
		case "run-profile":
			return runReplay(cmd, args)
		case "wait-profile":
			return runWaitReplay(cmd, args)
		case "check-status":
			debug, _ := cmd.Flags().GetBool("debug")
			return runCheckStatus(cmd, args, &checkStatusOptions{debug: debug})
		case "check-permissions":
			return runCheckPermissions(cmd, args)
		case "select-tab":
			return runSelectTab(cmd, args)
		case "show-performance":
			return runShowPerformance(cmd, args)
		case "show-summary":
			return runSelectTab(cmd, []string{"Summary"})
		case "show-counters":
			return runSelectTab(cmd, []string{"Counters"})
		case "show-memory":
			return runClickButton(cmd, []string{"Show Memory"})
		case "show-dependencies":
			return runClickButton(cmd, []string{"Show Dependencies"})
		case "xcode-export-counters":
			force, _ := cmd.Flags().GetBool("force")
			return runXcodeExportCounters(cmd, args, &xcodeExportCountersOptions{force: force})
		case "xcode-export-memory":
			force, _ := cmd.Flags().GetBool("force")
			return runXcodeExportMemory(cmd, args, &xcodeExportMemoryOptions{force: force})
		case "vertex-output":
			drawCall, _ := cmd.Flags().GetInt("draw")
			output, _ := cmd.Flags().GetString("output")
			format, _ := cmd.Flags().GetString("format")
			return runVertexOutput(cmd, args, &vertexOutputOptions{drawCall: drawCall, output: output, format: format})
		case "list-windows":
			return runListWindows(cmd, args)
		case "list-tabs":
			return runListTabs(cmd, args)
		case "list-menus":
			return runListMenus(cmd, args)
		case "click-menu":
			return runClickMenu(cmd, args)
		case "list-buttons":
			return runListButtons(cmd, args)
		case "click-button":
			return runClickButton(cmd, args)
		case "click-cancel":
			return runClickButton(cmd, []string{"Cancel"})
		case "click-replace":
			return runClickButton(cmd, []string{"Replace"})
		case "open-export":
			return runOpenExport(cmd, args)
		case "click-save":
			return runClickSave(cmd, args)
		case "send-key":
			return runSendKey(cmd, args)
		case "check-goto-folder":
			return runCheckGoToFolder(cmd, args)
		case "debug-file-browser":
			return runDebugFileBrowser(cmd, args)
		case "set-export-path":
			return runSetExportPath(cmd, args)
		case "set-export-filename":
			return runSetExportFilename(cmd, args)
		case "send-enter":
			return runSendEnter(cmd, args)
		case "screenshot":
			output, _ := cmd.Flags().GetString("output")
			noPrompt, _ := cmd.Flags().GetBool("no-prompt")
			return runScreenshot(cmd, args, &screenshotOptions{output: output, noPrompt: noPrompt})
		case "debug-tree":
			verbose, _ := cmd.Flags().GetBool("verbose")
			return runDebugTree(cmd, args, &debugTreeOptions{verbose: verbose})
		case "ensure-checked", "toggle-checkbox":
			trace, _ := cmd.Flags().GetString("trace")
			opts := &checkboxOptions{trace: trace}
			if name == "ensure-checked" {
				return runEnsureChecked(cmd, args, opts)
			}
			return runToggleCheckbox(cmd, args, opts)
		case "performance":
			return cmd.Help()
		case "performance/show":
			return runPerformanceShow(cmd, args)
		case "performance/status":
			return runPerformanceStatus(cmd, args)
		case "performance/summary":
			return runPerformanceSummary(cmd, args)
		case "performance/memory":
			return runPerformanceMemory(cmd, args)
		case "navigator/summary", "navigator/dependencies", "navigator/performance", "navigator/memory":
			return runSelectNavigatorItem(name[len("navigator/"):])
		case "xcode-bindings":
			jsonOutput, _ := cmd.Flags().GetBool("json")
			return runXcodeBindings(cmd, args, &xcodeBindingsOptions{json: jsonOutput})
		case "xcode-parity":
			jsonOutput, _ := cmd.Flags().GetBool("json")
			return runXcodeParity(cmd, args, &xcodeParityOptions{json: jsonOutput})
		}
		const prefix = "performance/"
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			return runPerformanceView(name[len(prefix):])
		}
		return fmt.Errorf("unknown Xcode automation command %q", name)
	}
}

func syncCollectProfileOptions(cmd *cobra.Command) error {
	if cmd.Flags().Lookup("timeout") == nil {
		return nil
	}
	var err error
	if collectProfileOpts.timeout, err = cmd.Flags().GetDuration("timeout"); err != nil {
		return err
	}
	collectProfileOpts.debug, _ = cmd.Flags().GetBool("debug")
	collectProfileOpts.verbose, _ = cmd.Flags().GetBool("verbose")
	collectProfileOpts.noBundle, _ = cmd.Flags().GetBool("no-bundle")
	collectProfileOpts.background, _ = cmd.Flags().GetBool("background")
	collectProfileOpts.noPrompt, _ = cmd.Flags().GetBool("no-prompt")
	collectProfileOpts.json, _ = cmd.Flags().GetBool("json")
	collectProfileOpts.wait, _ = cmd.Flags().GetDuration("wait")
	collectProfileOpts.force, _ = cmd.Flags().GetBool("force")
	collectProfileOpts.pprof, _ = cmd.Flags().GetBool("pprof")
	return nil
}
