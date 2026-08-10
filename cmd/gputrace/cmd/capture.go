package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace/internal/capture"
)

var captureCmd = newCaptureCommand(&captureOptions{})

type captureOptions struct {
	output string
	dir    string
	check  bool
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
			var stdout, stderr bytes.Buffer
			out, err := capture.Run(cmd.Context(), capture.Options{
				Output: opts.output,
				Dir:    opts.dir,
				Stdout: &stdout,
				Stderr: &stderr,
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
	f.StringVar(&opts.dir, "dir", "", "working directory for the target")
	f.BoolVar(&opts.check, "check", false, "report whether the target accepts the interposer, then exit")
	return cmd
}

func runCaptureCheck(cmd *cobra.Command, target string) error {
	if err := capture.Eligible(target); err != nil {
		if errors.Is(err, capture.ErrNotInterposable) {
			// An ineligible target is a verdict, not a malfunction: report it on
			// stdout and exit non-zero, without restating it on stderr.
			fmt.Fprintf(cmd.OutOrStdout(), "not capturable: %v\n", err)
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
