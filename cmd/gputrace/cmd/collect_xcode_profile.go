package cmd

import (
	"embed"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/osa"
	"github.com/tmc/macgo"
)

//go:embed applescripts/*.applescript
var embeddedScripts embed.FS

// scriptsDir is the directory to check for script overrides (for faster iteration)
var scriptsDir = filepath.Join(os.Getenv("HOME"), ".config", "gputrace", "applescripts")

// Shared flags for collect-xcode-profile subcommands
var (
	collectProfileOutput       string
	collectProfileTimeout      time.Duration
	collectProfileDebug        bool
	collectProfileVerbose      bool
	collectProfileNoBundle     bool
	collectProfileBackground   bool
	collectProfileNoPrompt     bool
	collectProfileJSON         bool
	collectProfileWait         time.Duration
	collectProfileForce        bool
	collectProfilePprof        bool // Enable pprof debug endpoints
)

var collectXcodeProfileCmd = &cobra.Command{
	Use:     "xcode-profile [trace_file]",
	Aliases: []string{"xp", "collect-xcode-profile"},
	Short:   "Interact with Xcode GPU trace viewer",
	Long: `Control and extract information from Xcode's GPU trace viewer.

This command uses Accessibility APIs to control Xcode's UI and extract data.

Core operations:
  run           Run full automation (open, replay, export)
  open          Open a trace file in Xcode
  close         Close the trace window
  export        Export the trace with performance data

Status and inspection:
  check-status  Check profiling status (ready, running, complete)
  list-windows  List all Xcode windows
  list-buttons  List available buttons
  list-tabs     List available tabs

Navigation:
  select-tab        Select a tab by name
  show-performance  Click Show Performance button
  show-summary      Select Summary tab
  show-counters     Select Counters tab
  show-memory       Select Memory tab

Example:
  gputrace xcode-profile my_capture.gputrace -o my_capture-perfdata.gputrace
  gputrace xp open my_capture.gputrace
  gputrace xp list-windows --json
  gputrace xp check-status --json
`,
	Args: cobra.MaximumNArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Start pprof server if requested
		if collectProfilePprof {
			port := "6060"
			// Use different port if running inside macgo app bundle
			if exe, err := os.Executable(); err == nil && strings.Contains(exe, ".app/") {
				port = "6061"
			}
			addr := ":" + port
			fmt.Fprintf(os.Stderr, "[pprof] starting debug server on http://localhost%s/debug/pprof/\n", addr)
			go func() {
				if err := http.ListenAndServe(addr, nil); err != nil {
					fmt.Fprintf(os.Stderr, "[pprof] server error: %v\n", err)
				}
			}()
		}

		// Setup macgo and verify Accessibility permission for all subcommands
		if err := setupMacgo(); err != nil {
			return err
		}

		// Check and request permissions with polling (Accessibility & Automation)
		if err := checkPermissions(); err != nil {
			return err
		}

		// Double-Check: Verify we actually have Accessibility permission by testing AX API
		if err := verifyAccessibilityPermission(); err != nil {
			return err
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// If no args and no subcommand, show help
		if len(args) == 0 {
			return cmd.Help()
		}
		// Run full automation for backwards compatibility
		return runCollectXcodeProfileFull(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(collectXcodeProfileCmd)

	// Persistent flags available to all subcommands
	collectXcodeProfileCmd.PersistentFlags().DurationVar(&collectProfileTimeout, "timeout", 5*time.Minute, "Timeout for the operation")
	collectXcodeProfileCmd.PersistentFlags().BoolVar(&collectProfileDebug, "debug", false, "Print debug information")
	collectXcodeProfileCmd.PersistentFlags().BoolVarP(&collectProfileVerbose, "verbose", "v", false, "Print verbose status information")
	collectXcodeProfileCmd.PersistentFlags().BoolVar(&collectProfileNoBundle, "no-bundle", false, "Skip macgo app bundle (use Terminal's Accessibility permission)")
	collectXcodeProfileCmd.PersistentFlags().BoolVar(&collectProfileBackground, "background", false, "Run without bringing Xcode to foreground")
	collectXcodeProfileCmd.PersistentFlags().BoolVar(&collectProfileNoPrompt, "no-prompt", false, "Don't prompt for permissions, exit with error instead")
	collectXcodeProfileCmd.PersistentFlags().BoolVar(&collectProfileJSON, "json", false, "Output results in JSON format")
	collectXcodeProfileCmd.PersistentFlags().DurationVar(&collectProfileWait, "wait", 0, "Wait for lock release (0=no wait, e.g. 5m)")
	collectXcodeProfileCmd.PersistentFlags().BoolVar(&collectProfileForce, "force", false, "Override existing lock")
	collectXcodeProfileCmd.PersistentFlags().BoolVar(&collectProfilePprof, "pprof", false, "Enable pprof debug endpoints (:6060 or :6061 in macgo)")

	// Local flags for the main command
	collectXcodeProfileCmd.Flags().StringVarP(&collectProfileOutput, "output", "o", "", "Output path for the exported trace (default: <input>-perfdata.gputrace)")
}

// acquireProfileLock checks if Xcode is currently running a profile by looking for
// the Stop button in any window. If a profile is running, it waits (if --wait is set)
// or returns an error.
// Returns a no-op cleanup function for API compatibility.
func acquireProfileLock() (func(), error) {
	deadline := time.Now()
	if collectProfileWait > 0 {
		deadline = deadline.Add(collectProfileWait)
	}

	pollInterval := 2 * time.Second
	firstAttempt := true

	for {
		running, windowTitle := isProfilingRunning()
		if !running {
			break
		}

		if collectProfileForce {
			fmt.Printf("Warning: profiling appears to be running in %q, proceeding anyway (--force)\n", windowTitle)
			break
		}

		// Check if we should wait
		if collectProfileWait == 0 || time.Now().After(deadline) {
			if collectProfileWait > 0 {
				return nil, fmt.Errorf("timed out waiting for profiling to complete in %q", windowTitle)
			}
			return nil, fmt.Errorf("profiling is running in %q. Use --wait to wait or --force to proceed anyway", windowTitle)
		}

		// Wait for profiling to complete
		if firstAttempt {
			fmt.Printf("Waiting for profiling to complete in %q (timeout: %v)...\n", windowTitle, collectProfileWait)
			firstAttempt = false
		}
		time.Sleep(pollInterval)
	}

	// No-op cleanup - we're not holding any external lock
	return func() {}, nil
}

// isProfilingRunning checks if any Xcode window has a Stop button visible AND enabled,
// AND profiling is not complete (no Show Performance button).
// The Stop button can be enabled for capturing new workloads even after profiling completes.
func isProfilingRunning() (bool, string) {
	appAX, err := FindXcodeApp()
	if err != nil {
		// Xcode not running, so no profile running
		return false, ""
	}
	defer cfRelease(appAX)

	windows := GetAllWindows(appAX)
	verboseLog("isProfilingRunning: checking %d windows", len(windows))
	for i, w := range windows {
		title := axString(w, "AXTitle")
		stopBtn := FindStopButton(w)
		if stopBtn != 0 {
			enabled := IsElementEnabled(stopBtn)
			btnTitle := axString(stopBtn, "AXTitle")
			btnDesc := axString(stopBtn, "AXDescription")
			verboseLog("isProfilingRunning: window %d (%q) has Stop button: title=%q desc=%q enabled=%v",
				i, title, btnTitle, btnDesc, enabled)
			// Only consider profiling running if Stop button exists AND is enabled
			// BUT also check if profiling is complete (Show Performance visible)
			if enabled {
				// Check if profiling is already complete in this window
				if hasShowPerformance(w) {
					verboseLog("isProfilingRunning: window %d has Show Performance - profiling complete, not running", i)
					continue
				}
				return true, title
			}
		}
	}
	return false, ""
}

// activateXcode brings Xcode to the foreground if not in background mode.
func activateXcode() error {
	if collectProfileBackground {
		return nil
	}
	return runOSAWithDebug(`tell application "Xcode" to activate`, collectProfileDebug)
}

// loadScript loads an AppleScript, checking for a disk override first.
func loadScript(name string) string {
	diskPath := filepath.Join(scriptsDir, name)
	if data, err := os.ReadFile(diskPath); err == nil {
		if collectProfileDebug {
			fmt.Printf("    [Using script from %s]\n", diskPath)
		}
		return string(data)
	}
	data, err := embeddedScripts.ReadFile("applescripts/" + name)
	if err != nil {
		return ""
	}
	return string(data)
}

// verboseLog prints a message if verbose mode is enabled.
func verboseLog(format string, args ...interface{}) {
	if collectProfileVerbose || collectProfileDebug {
		fmt.Printf("[verbose] "+format+"\n", args...)
	}
}

// setupMacgo initializes macgo for TCC permissions.
func setupMacgo() error {
	verboseLog("setupMacgo: PID=%d", os.Getpid())

	if collectProfileNoBundle || os.Getenv("GPUTRACE_SKIP_MACGO") != "" {
		verboseLog("setupMacgo: skipping macgo (--no-bundle or GPUTRACE_SKIP_MACGO)")
		fmt.Printf("Skipping macgo (using current process identity)\n")
		return nil
	}

	os.Setenv("MACGO_SERVICES_VERSION", "1")

	cfg := &macgo.Config{
		AppName:  "gputrace",
		BundleID: "com.tmc.gputrace",
		Permissions: []macgo.Permission{
			macgo.Accessibility,
		},
		Custom: []string{
			"com.apple.security.automation.apple-events",
		},
		AdHocSign: true,
		DevMode:   true, // Preserve TCC permissions across rebuilds
		UIMode:    macgo.UIModeBackground,
		Info: map[string]interface{}{
			"NSAppleEventsUsageDescription":   "gputrace needs to control Xcode to automate GPU trace operations.",
			"NSAccessibilityUsageDescription": "gputrace needs Accessibility access to control Xcode's UI for GPU trace automation.",
		},
	}

	verboseLog("setupMacgo: calling macgo.Start with BundleID=%s, UIMode=Background, DevMode=true", cfg.BundleID)

	if err := macgo.Start(cfg); err != nil {
		fmt.Printf(Colorize("macgo app bundle setup failed: %v\n", ColorRed), err)
		fmt.Printf("\nThe app bundle is required for Accessibility permissions.\n")
		fmt.Printf("Try these steps:\n")
		fmt.Printf("  1. Reset TCC: tccutil reset Accessibility com.tmc.gputrace\n")
		fmt.Printf("  2. Set debug: export MACGO_DEBUG=1\n")
		fmt.Printf("  3. Re-run the command\n")
		fmt.Printf("\nOr use --no-bundle if Terminal.app has Accessibility permission.\n")
		return fmt.Errorf("macgo setup failed: %w", err)
	}
	verboseLog("setupMacgo: macgo.Start completed successfully")
	return nil
}

// verifyAccessibilityPermission tests if we actually have Accessibility permission
// by making a real AX API call. Returns an error with helpful instructions if not.
func verifyAccessibilityPermission() error {
	verboseLog("verifyAccessibilityPermission: checking AX access")
	// Try to get Xcode's AX element - this will fail if we don't have permission
	appAX, err := FindXcodeApp()
	if err != nil {
		// Xcode not running is OK, we can't test permission without a target app
		// Just check the basic AXIsProcessTrusted
		if !osa.HasAccessibilityPermission() {
			return accessibilityPermissionError()
		}
		return nil // Xcode not running, but we have basic permission
	}
	defer cfRelease(appAX)

	// Try to get windows - this tests if AX API actually works
	_, axErr := axChildrenWithError(appAX)
	if axErr == -25211 {
		// API disabled - no Accessibility permission
		return accessibilityPermissionError()
	}

	return nil
}

// accessibilityPermissionError returns a helpful error for missing Accessibility permission.
func accessibilityPermissionError() error {
	fmt.Print(Colorize("Accessibility permission not granted.\n", ColorRed))
	fmt.Printf("\nPlease grant Accessibility permission to gputrace in:\n")
	fmt.Printf("  System Settings > Privacy & Security > Accessibility\n\n")
	fmt.Printf("Then re-run the command.\n")
	exec.Command("open", "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_Accessibility").Run()
	return fmt.Errorf("accessibility permission required")
}

// permissionsChecked tracks if we've already verified permissions in this process.
var permissionsChecked bool

// checkPermissions verifies Accessibility permission.
func checkPermissions() error {
	if permissionsChecked {
		return nil
	}

	if !osa.HasAccessibilityPermission() {
		if collectProfileNoPrompt {
			return fmt.Errorf("accessibility permission not granted (use axperms -enable gputrace)")
		}

		fmt.Print(Colorize("Note: Accessibility check returned false. Triggering prompt...\n", ColorYellow))
		osa.PromptAccessibilityPermission()

		fmt.Println("Waiting for Accessibility permission... (please click Allow in System Settings)")
		timeout := 60 * time.Second
		deadline := time.Now().Add(timeout)

		granted := false
		for time.Now().Before(deadline) {
			if osa.HasAccessibilityPermission() {
				granted = true
				fmt.Print(Colorize("\nAccessibility permission granted.\n", ColorGreen))
				break
			}
			fmt.Print(".")
			time.Sleep(1 * time.Second)
		}

		if !granted {
			fmt.Println()
			return fmt.Errorf("accessibility permission timed out")
		}
	}

	permissionsChecked = true
	return nil
}

// runOSA executes an AppleScript in-process via NSAppleScript.
func runOSA(script string) error {
	return runOSAWithDebug(script, false)
}

// runOSAWithTimeout executes an AppleScript with a timeout.
// Returns error if the script doesn't complete within the timeout.
func runOSAWithTimeout(script string, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- runOSA(script)
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("AppleScript execution timed out after %v", timeout)
	}
}

// runOSAWithDebug executes an AppleScript in-process with optional debug output.
func runOSAWithDebug(script string, debug bool) error {
	if debug {
		preview := script
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		fmt.Printf("    [DEBUG] Running script: %s\n", strings.ReplaceAll(preview, "\n", " "))
	}

	result, err := osa.Execute(script)

	if debug {
		fmt.Printf("    [DEBUG] Output: %s\n", strings.TrimSpace(result))
		if err != nil {
			fmt.Printf("    [DEBUG] Error: %v\n", err)
		}
	}

	if err != nil {
		return err
	}
	if result != "" {
		fmt.Printf("    AppleScript result: %s\n", result)
	}
	return nil
}

// Script loaders
func waitForXcodeScript() string     { return loadScript("wait_for_xcode.applescript") }
func replayScript() string           { return loadScript("replay.applescript") }
func debugUIHierarchyScript() string { return loadScript("debug_ui.applescript") }
func exportScript() string           { return loadScript("export.applescript") }

// osaExecuteRaw runs an AppleScript and returns the raw result.
func osaExecuteRaw(script string) (string, error) {
	return osa.Execute(script)
}
