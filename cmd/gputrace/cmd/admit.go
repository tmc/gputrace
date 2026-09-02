package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace/internal/admit"
)

type admitOptions struct {
	json bool
}

var admitCmd = newAdmitCommand(&admitOptions{})

func newAdmitCommand(opts *admitOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admit <raw.gputrace> <profiled.gputrace>",
		Short: "Check whether a profiled export supports a measured-timing claim",
		Long: `Check a profiled export against the raw capture it claims to measure.

Every criterion must pass for the export to be admitted:

  exported UUID matches raw               the export is of this capture
  streamData present and non-empty        profiler data was written
  payload self-contained                  the bundle carries its own evidence
  dispatch counts match raw               the replay ran the same work
  timing provenance is measured           not synthetic or capture-derived

A criterion that cannot be evaluated is reported as UNKNOWN and withholds
admission: an unanswerable question leaves the claim unsupported.

Exits non-zero when the export is not admitted, so it can gate a pipeline.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdmit(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output the verdict as JSON")
	return cmd
}

func init() {
	rootCmd.AddCommand(admitCmd)
}

func runAdmit(cmd *cobra.Command, args []string, opts *admitOptions) error {
	rawPath, profiledPath := args[0], args[1]
	if err := checkTraceFile(rawPath); err != nil {
		return err
	}
	if err := checkTraceFile(profiledPath); err != nil {
		return err
	}

	result := admit.Check(rawPath, profiledPath)
	out := cmd.OutOrStdout()

	if opts.json {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return fmt.Errorf("write verdict: %w", err)
		}
	} else if err := admit.WriteReport(out, result); err != nil {
		return err
	}

	if !result.Admitted() {
		// The report already says which criteria failed and why, so the
		// error only carries the exit status.
		return errNotAdmitted
	}
	return nil
}

// notAdmittedError carries the exit status for a rejected trace. The report
// has already named the failing criteria, so the entry point must not print
// the error again underneath them.
type notAdmittedError struct{}

func (notAdmittedError) Error() string    { return "trace not admitted" }
func (notAdmittedError) alreadyReported() {}

var errNotAdmitted = notAdmittedError{}
