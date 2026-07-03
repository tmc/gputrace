//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

type openTraceOptions struct {
	foreground bool
}

func init() {
	opts := &openTraceOptions{}
	openCmd := &cobra.Command{
		Use:   "open <trace_file>",
		Short: "Open a trace file in Xcode",
		Long: `Opens a GPU trace file in Xcode and waits for the window to be ready.
By default, opens in background without stealing focus. Use --foreground to bring Xcode to front.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOpenTrace(cmd, args, opts)
		},
	}
	openCmd.Flags().BoolVar(&opts.foreground, "foreground", false, "Bring Xcode to foreground (default: open in background)")
	collectXcodeProfileCmd.AddCommand(openCmd)
}

func runOpenTrace(cmd *cobra.Command, args []string, opts *openTraceOptions) error {
	inputPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("invalid input path: %w", err)
	}

	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return fmt.Errorf("trace file does not exist: %s", inputPath)
	}

	status := xcodeProfileStatusWriter()
	fmt.Fprintf(status, "Opening trace in Xcode: %s\n", inputPath)

	// Use -g to open in background by default (doesn't steal focus)
	openArgs := xcodeOpenArgs()
	if !opts.foreground {
		openArgs = append(openArgs, "-g")
	}
	openArgs = append(openArgs, inputPath)

	openCmd := exec.Command("open", openArgs...)
	if output, err := openCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to open trace in Xcode: %w\n  output: %s", err, string(output))
	}

	fmt.Fprintln(status, "Waiting for Xcode window...")

	// Wait for window using AX polling (doesn't steal focus)
	deadline := time.Now().Add(30 * time.Second)
	var appAX uintptr
	var axErr error
	for time.Now().Before(deadline) {
		appAX, axErr = FindXcodeApp()
		if axErr == nil {
			windows := GetAllWindows(appAX)
			if len(windows) > 0 {
				break
			}
			cfRelease(appAX)
		}
		time.Sleep(500 * time.Millisecond)
	}

	if axErr != nil {
		return fmt.Errorf("Xcode not accessible: %w", axErr)
	}
	defer cfRelease(appAX)

	// Handle startup dialogs (Reopen, etc.)
	if err := dismissStartupDialogs(); err != nil {
		verboseLog("dismissStartupDialogs: %v", err)
	}

	// Ensure window is on-screen (may be restored to disconnected monitor position)
	windows := GetAllWindows(appAX)
	for _, w := range windows {
		x, y := axPosition(w)
		_, h := axSize(w)
		// If window is off-screen (negative Y or very far), move it
		if y < 0 || y > 2000 || x < -500 {
			verboseLog("Window at (%d,%d) appears off-screen, repositioning", x, y)
			setWindowPosition(w, 100, 100)
			time.Sleep(200 * time.Millisecond)
		}
		// Also ensure window has reasonable height (not minimized)
		if h < 100 {
			verboseLog("Window height %d too small, may be minimized", h)
		}
	}

	// Ensure the Debug navigator is shown using AX menu click
	if err := ClickMenuItem(appAX, []string{"View", "Navigators", "Debug"}); err != nil {
		if collectProfileOpts.debug {
			fmt.Fprintf(os.Stderr, "Warning: could not show Debug navigator via menu: %v\n", err)
		}
	}

	fmt.Fprint(status, Colorize("Trace opened successfully in Xcode\n", ColorGreen))
	return writeXcodeProfileActionOutput(xcodeProfileActionOutput{
		Action: "open",
		Input:  inputPath,
	})
}

func xcodeOpenArgs() []string {
	if app := os.Getenv("GPUTRACE_XCODE_APP"); app != "" {
		return []string{"-a", app}
	}
	return []string{"-a", "Xcode"}
}
