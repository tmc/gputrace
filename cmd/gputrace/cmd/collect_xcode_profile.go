//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
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

type xcodeProfileActionOutput struct {
	Success         bool   `json:"success"`
	Action          string `json:"action"`
	Target          string `json:"target,omitempty"`
	Method          string `json:"method,omitempty"`
	Input           string `json:"input,omitempty"`
	Output          string `json:"output,omitempty"`
	Source          string `json:"source,omitempty"`
	RequestedOutput string `json:"requested_output,omitempty"`
	Copied          bool   `json:"copied,omitempty"`
	Warning         string `json:"warning,omitempty"`
}

// Shared flags for collect-xcode-profile subcommands
var (
	collectProfileOutput     string
	collectProfileTimeout    time.Duration
	collectProfileDebug      bool
	collectProfileVerbose    bool
	collectProfileNoBundle   bool
	collectProfileBackground bool
	collectProfileNoPrompt   bool
	collectProfileJSON       bool
	collectProfileWait       time.Duration
	collectProfileForce      bool
	collectProfilePprof      bool // Enable pprof debug endpoints
)

func xcodeProfileStatusWriter() io.Writer {
	if collectProfileJSON {
		return os.Stderr
	}
	return os.Stdout
}

func encodeXcodeProfileJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeXcodeProfileActionOutput(output xcodeProfileActionOutput) error {
	if !collectProfileJSON {
		return nil
	}
	output.Success = true
	return encodeXcodeProfileJSON(os.Stdout, output)
}

func defaultXcodeProfileOutputPath(inputPath string) string {
	ext := filepath.Ext(inputPath)
	base := strings.TrimSuffix(inputPath, ext)
	return base + "-perfdata" + ext
}

func resolveXcodeProfileTraceOutputPath(outputPath string) (string, error) {
	if outputPath == "" {
		return "", nil
	}
	if commandOutputPathIsStdout(outputPath) {
		return "", fmt.Errorf("trace output must be a file path, not stdout")
	}
	abs, err := filepath.Abs(outputPath)
	if err != nil {
		return "", fmt.Errorf("invalid output path: %w", err)
	}
	return abs, nil
}

var collectXcodeProfileCmd = &cobra.Command{
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
  show-memory       Select Memory tab
  show-dependencies Click Show Dependencies button

Data Export:
  xcode-export-counters  Export GPU counters from Performance view to CSV
  xcode-export-memory    Export memory report from Performance view
  vertex-output          Extract vertex shader output from Xcode GPU debugger
  performance            Performance data commands

Example:
  gputrace xcode-profile my_capture.gputrace -o my_capture-perfdata.gputrace
  gputrace xp run my_capture.gputrace -o output.gputrace
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

		if collectProfileDebug || collectProfileVerbose {
			logProcessIdentity("pre-macgo")
		}

		// Setup macgo and verify Accessibility permission for all subcommands
		if err := setupMacgo(); err != nil {
			return err
		}

		if collectProfileDebug || collectProfileVerbose {
			logProcessIdentity("post-macgo")
		}

		// Check and request permissions with polling (Accessibility & Automation)
		if err := checkPermissions(); err != nil {
			return err
		}

		// Double-Check: Verify we actually have Accessibility permission by testing AX API
		if err := verifyAccessibilityPermission(); err != nil {
			logProcessIdentity("ax-failed")
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
		status := xcodeProfileStatusWriter()

		if collectProfileForce {
			fmt.Fprintf(status, "Warning: profiling appears to be running in %q, proceeding anyway (--force)\n", windowTitle)
			break
		}

		// Stale window from a previous session — close all Xcode GPU trace windows and retry.
		// This is common when a trace was already replayed but the window is still open.
		if firstAttempt {
			verboseLog("acquireProfileLock: detected stale GPU trace window %q, closing all Xcode windows and retrying", windowTitle)
			fmt.Fprintf(status, "  Closing stale Xcode GPU trace window %q...\n", windowTitle)
			closeAllXcodeWindows()
			time.Sleep(2 * time.Second)
			firstAttempt = false
			continue
		}

		// Check if we should wait
		if collectProfileWait == 0 || time.Now().After(deadline) {
			if collectProfileWait > 0 {
				return nil, fmt.Errorf("timed out waiting for profiling to complete in %q", windowTitle)
			}
			return nil, fmt.Errorf("profiling is running in %q. Use --wait to wait or --force to proceed anyway", windowTitle)
		}

		// Wait for profiling to complete
		fmt.Fprintf(status, "Waiting for profiling to complete in %q (timeout: %v)...\n", windowTitle, collectProfileWait)
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
				if title == "" {
					title = "(untitled Xcode window)"
				}
				return true, title
			}
		}
	}
	return false, ""
}

// verboseLog prints a diagnostic if verbose mode is enabled.
func verboseLog(format string, args ...interface{}) {
	if collectProfileVerbose || collectProfileDebug {
		fmt.Fprintf(os.Stderr, "[verbose] "+format+"\n", args...)
	}
}

// setupMacgo initializes macgo for TCC permissions.
func setupMacgo() error {
	verboseLog("setupMacgo: PID=%d", os.Getpid())

	if collectProfileNoBundle || os.Getenv("GPUTRACE_SKIP_MACGO") != "" {
		verboseLog("setupMacgo: skipping macgo (--no-bundle or GPUTRACE_SKIP_MACGO)")
		fmt.Fprintln(os.Stderr, "Skipping macgo (using current process identity)")
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
		DevMode:   true,
		UIMode:    macgo.UIModeAccessory,
		Info: map[string]interface{}{
			"NSAppleEventsUsageDescription":   "gputrace needs to control Xcode to automate GPU trace operations.",
			"NSAccessibilityUsageDescription": "gputrace needs Accessibility access to control Xcode's UI for GPU trace automation.",
		},
	}

	verboseLog("setupMacgo: calling macgo.Start with BundleID=%s, UIMode=Accessory, DevMode=true", cfg.BundleID)

	if err := macgo.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, Colorize("macgo app bundle setup failed: %v\n", ColorRed), err)
		fmt.Fprintln(os.Stderr, "\nThe app bundle is required for Accessibility permissions.")
		fmt.Fprintln(os.Stderr, "Try these steps:")
		fmt.Fprintln(os.Stderr, "  1. Reset TCC: tccutil reset Accessibility com.tmc.gputrace")
		fmt.Fprintln(os.Stderr, "  2. Set debug: export MACGO_DEBUG=1")
		fmt.Fprintln(os.Stderr, "  3. Re-run the command")
		fmt.Fprintln(os.Stderr, "\nOr use --no-bundle if Terminal.app has Accessibility permission.")
		return fmt.Errorf("macgo setup failed: %w", err)
	}
	verboseLog("setupMacgo: macgo.Start completed successfully")
	return nil
}

// logProcessIdentity prints diagnostic info about the current process's TCC identity.
// This helps debug cases where check-status passes but runtime fails (different process identities).
func logProcessIdentity(phase string) {
	exe, _ := os.Executable()
	inBundle := strings.Contains(exe, ".app/")
	bundlePath := os.Getenv("MACGO_BUNDLE_PATH")
	origExe := os.Getenv("MACGO_ORIGINAL_EXECUTABLE")
	trusted := osa.HasAccessibilityPermission()

	fmt.Fprintf(os.Stderr, "[debug:%s] PID=%d executable=%s\n", phase, os.Getpid(), exe)
	fmt.Fprintf(os.Stderr, "[debug:%s] in_bundle=%v AXIsProcessTrusted=%v\n", phase, inBundle, trusted)
	if bundlePath != "" {
		fmt.Fprintf(os.Stderr, "[debug:%s] MACGO_BUNDLE_PATH=%s\n", phase, bundlePath)
	}
	if origExe != "" {
		fmt.Fprintf(os.Stderr, "[debug:%s] MACGO_ORIGINAL_EXECUTABLE=%s\n", phase, origExe)
	}

	// Show codesign identity of the running executable
	if out, err := exec.Command("codesign", "-dvvv", exe).CombinedOutput(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Identifier=") ||
				strings.HasPrefix(line, "Authority=") ||
				strings.HasPrefix(line, "TeamIdentifier=") ||
				strings.HasPrefix(line, "Signature=") {
				fmt.Fprintf(os.Stderr, "[debug:%s] %s\n", phase, line)
			}
		}
	}
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
	fmt.Fprint(os.Stderr, Colorize("Accessibility permission not granted.\n", ColorRed))
	fmt.Fprintln(os.Stderr, "\nPlease grant Accessibility permission to gputrace in:")
	fmt.Fprintln(os.Stderr, "  System Settings > Privacy & Security > Accessibility")
	fmt.Fprintln(os.Stderr, "\nThen re-run the command.")
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

		fmt.Fprint(os.Stderr, Colorize("Note: Accessibility check returned false. Triggering prompt...\n", ColorYellow))
		osa.PromptAccessibilityPermission()

		fmt.Fprintln(os.Stderr, "Waiting for Accessibility permission... (please click Allow in System Settings)")
		timeout := 60 * time.Second
		deadline := time.Now().Add(timeout)

		granted := false
		for time.Now().Before(deadline) {
			if osa.HasAccessibilityPermission() {
				granted = true
				fmt.Fprint(os.Stderr, Colorize("\nAccessibility permission granted.\n", ColorGreen))
				break
			}
			fmt.Fprint(os.Stderr, ".")
			time.Sleep(1 * time.Second)
		}

		if !granted {
			fmt.Fprintln(os.Stderr)
			return fmt.Errorf("accessibility permission timed out")
		}
	}

	permissionsChecked = true
	return nil
}
