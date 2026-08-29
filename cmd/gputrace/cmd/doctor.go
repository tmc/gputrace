package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/gpudoctor"
)

type doctorOptions struct {
	json   bool
	target string
}

var doctorCmd = newDoctorCommand(&doctorOptions{})

func newDoctorCommand(opts *doctorOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor [target]",
		Short: "Diagnose the GPU profiling environment",
		Long: `Diagnose the GPU profiling environment and print what to do about it.

Reports the NVIDIA driver, the CUDA toolkits and CUPTI libraries
installed, every nsys on the system with a verdict on whether its default
CUDA tracing works here, whether GPU performance counters are restricted
to admin users, and whether the capture shim builds.

Two of these failures are silent rather than loud: an nsys whose hardware
tracing drops every kernel record still writes a healthy-looking report,
and a CUPTI older than the running driver records nothing at all. Both
read as "the workload launched no kernels".

Pass a workload binary to also diagnose it for capturability: dynamic
CUDA linkage, and whether it is a Go binary needing an in-process flush.

Examples:
  gputrace doctor
  gputrace doctor ./my-workload
  gputrace doctor --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.target = args[0]
			}
			rep := gpudoctor.Run(gpudoctor.Options{Target: opts.target})
			if opts.json {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			writeDoctorReport(cmd.OutOrStdout(), rep)
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output in JSON format")
	return cmd
}

// writeDoctorReport renders the checks as a status column plus detail, so
// a failing row is findable without reading the whole page.
func writeDoctorReport(out io.Writer, rep *gpudoctor.Report) {
	for _, c := range rep.Checks {
		fmt.Fprintf(out, "%-6s %-16s %s\n", doctorMark(c.Status), c.Name, c.Detail)
		// Notes and remedies print one source line each: they carry paths
		// and shell commands that reflowing would corrupt.
		for _, n := range c.Notes {
			fmt.Fprintf(out, "         %s\n", n)
		}
		if c.Remedy != "" {
			for i, line := range strings.Split(c.Remedy, "\n") {
				label := "  fix: "
				if i > 0 {
					label = "       "
				}
				fmt.Fprintf(out, "%s%s\n", label, line)
			}
		}
	}
	switch rep.Worst() {
	case gpudoctor.StatusFail:
		fmt.Fprintln(out, "\nSomething here will not profile correctly; see the fix lines above.")
	case gpudoctor.StatusWarn:
		fmt.Fprintln(out, "\nUsable, with caveats noted above.")
	default:
		fmt.Fprintln(out, "\nEnvironment looks good for capture.")
	}
}

func doctorMark(s gpudoctor.Status) string {
	switch s {
	case gpudoctor.StatusOK:
		return "ok"
	case gpudoctor.StatusWarn:
		return "warn"
	case gpudoctor.StatusFail:
		return "FAIL"
	default:
		return "skip"
	}
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
