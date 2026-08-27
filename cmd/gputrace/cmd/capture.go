package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace/internal/capture"
)

var captureCmd = newCaptureCommand(&captureOptions{})

type captureOptions struct {
	output       string
	timingOutput string
	runID        string
	dir          string
	check        bool
}

func newCaptureCommand(opts *captureOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capture [flags] -- <command> [args...]",
		Short: "Run a Metal workload under the GPU capture interposer",
		Long: `Run a command with Apple's GPUToolsCapture interposer loaded and write a
.gputrace bundle. The target does not need to be recompiled or to call
MTLCaptureManager itself.

The interposer is loaded through DYLD_INSERT_LIBRARIES, which dyld ignores for
hardened-runtime binaries with library validation and for Apple platform
binaries. Those targets run normally and produce no trace. Use --check to test a
target without running it.

Interposable in practice: adhoc-signed and developer-signed binaries, unsigned
builds, and Homebrew interpreters such as python3. Not interposable: App Store
and notarized applications with the hardened runtime, and anything under
/System.

Examples:
  gputrace capture -o run.gputrace -- python3 bench.py
  gputrace capture --check python3`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.check {
				return runCaptureCheck(cmd, args[0])
			}
			if opts.output == "" {
				return errors.New("capture: -o is required")
			}
			var stderr bytes.Buffer
			out, err := capture.Run(cmd.Context(), capture.Options{
				Output:       opts.output,
				TimingOutput: opts.timingOutput,
				RunID:        opts.runID,
				Dir:          opts.dir,
				Stdin:        cmd.InOrStdin(),
				Stdout:       cmd.OutOrStdout(),
				Stderr:       &stderr,
			}, args...)
			if err != nil {
				if stderr.Len() > 0 {
					fmt.Fprint(os.Stderr, stderr.String())
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", out)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.output, "output", "o", "", "path of the .gputrace bundle to write")
	f.StringVar(&opts.timingOutput, "timing-sidecar", "", "write live command-buffer timing and clock samples as JSON lines")
	f.StringVar(&opts.runID, "run-id", "", "run identity shared by timing sidecar and host signposts")
	f.StringVar(&opts.dir, "dir", "", "working directory for the target")
	f.BoolVar(&opts.check, "check", false, "report whether the target accepts the interposer, then exit")
	// --api binds a Linux-only option and is registered beside the other
	// Linux-only capture flags; this file builds on every platform.
	return cmd
}

func runCaptureCheck(cmd *cobra.Command, target string) error {
	// Resolve through PATH exactly as capture.Run does. Handing the bare argv[0]
	// to codesign checks a file of that name in the working directory instead,
	// so the verdict would describe a different binary than the one a capture
	// would launch.
	target, err := exec.LookPath(target)
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	if err := capture.Eligible(target); err != nil {
		if errors.Is(err, capture.ErrNotInterposable) {
			// An ineligible target is a verdict, not a malfunction: report it on
			// stdout and exit non-zero, without restating it on stderr.
			fmt.Fprintf(cmd.OutOrStdout(), "not capturable: %s: %v\n", target, err)
			return reportedCaptureError{err}
		}
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "capturable: %s accepts the interposer\n", target)
	return nil
}

// reportedCaptureError carries a verdict already written to stdout, so the
// entry point exits non-zero without printing it a second time.
type reportedCaptureError struct{ error }

func (reportedCaptureError) alreadyReported() {}

func init() {
	rootCmd.AddCommand(captureCmd)
}
