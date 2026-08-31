package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/fmtutil"
	"github.com/tmc/gputrace/internal/trace"
)

type residencyOptions struct {
	json bool
}

var residencyCmd = newResidencyCommand(&residencyOptions{})

func newResidencyCommand(opts *residencyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "residency <trace.gputrace>",
		Short: "Report allocated footprint by storage mode and whether residency is explicit",
		Long: `Report how a capture allocates memory and whether it manages residency.

Two things are shown together because they are one finding seen from two
directions. An all-shared allocation profile and an uncommitted residency set
both mean the process is leaving placement and residency to the driver.

  - Allocated footprint per MTLStorageMode, from buffer-creation records.
  - Counts of newResidencySet, requestResidency, and addResidencySet.

What is not shown, because the capture format does not record it in any way
this decodes: which buffers belong to which residency set. There is therefore
no wired-bytes figure distinct from the allocated figure. When no residency set
is committed, every allocation is under the driver's automatic residency, and
the allocated total is the working upper bound on what can be made resident.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResidency(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output in JSON format")
	return cmd
}

func init() {
	rootCmd.AddCommand(residencyCmd)
}

func runResidency(cmd *cobra.Command, args []string, opts *residencyOptions) error {
	t, err := trace.Open(args[0])
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}
	r, err := t.ResidencyReport()
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if opts.json {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	writeResidencyReport(w, r)
	return nil
}

func writeResidencyReport(w io.Writer, r *trace.ResidencyReport) {
	fmt.Fprintln(w, "Allocated footprint by storage mode")
	if len(r.Storage) == 0 {
		fmt.Fprintln(w, "  (no buffer-creation records in this capture)")
	} else {
		fmt.Fprintf(w, "  %-12s %8s %12s\n", "mode", "buffers", "bytes")
		for _, f := range r.Storage {
			fmt.Fprintf(w, "  %-12s %8d %12s\n", f.Mode, f.Buffers, fmtutil.FormatBytes(int64(f.Bytes), 1))
		}
		fmt.Fprintf(w, "  %-12s %8d %12s\n", "total", r.Buffers, fmtutil.FormatBytes(int64(r.Bytes), 1))
	}

	fmt.Fprintln(w, "\nResidency")
	fmt.Fprintf(w, "  %-20s %6d\n", "newResidencySet", r.Residency.NewResidencySet)
	fmt.Fprintf(w, "  %-20s %6d\n", "requestResidency", r.Residency.RequestResidency)
	fmt.Fprintf(w, "  %-20s %6d\n", "addResidencySet", r.Residency.AddResidencySet)
	switch {
	case r.Residency.Explicit():
		fmt.Fprintln(w, "  Residency is managed explicitly.")
	case r.Residency.Any():
		fmt.Fprintln(w, "  Residency is not managed explicitly; the driver's automatic")
		fmt.Fprintln(w, "  residency covers every allocation.")
	default:
		fmt.Fprintln(w, "  No residency records were decoded, which is not the same as")
		fmt.Fprintln(w, "  observing that the program manages no residency.")
	}

	if d := r.Disagreements(); len(d) > 0 {
		modes := make([]string, 0, len(d))
		for mode := range d {
			modes = append(modes, mode)
		}
		sort.Strings(modes)
		fmt.Fprintln(w, "\nScanner disagreement")
		fmt.Fprintf(w, "  %-12s %10s %10s\n", "mode", "decoded", "scanned")
		for _, mode := range modes {
			fmt.Fprintf(w, "  %-12s %10d %10d\n", mode, d[mode][0], d[mode][1])
		}
		fmt.Fprintln(w, "  Two independent scans of the same capture disagree, so neither")
		fmt.Fprintln(w, "  count above is trustworthy. Both are shown rather than one picked.")
	}

	if f := r.Finding(); f != "" {
		fmt.Fprintf(w, "\n%s\n", f)
	}

	fmt.Fprintln(w, "\nWhat this does and does not measure")
	fmt.Fprintln(w, "  Buffer and residency records are found by scanning the whole capture for")
	fmt.Fprintln(w, "  record markers. That scan is independent of the dispatch decoding whose")
	fmt.Fprintln(w, "  coverage \"gputrace api-calls\" reports, so a low decoded-dispatch fraction")
	fmt.Fprintln(w, "  does not bound these counts. The limit is that the scan finds the record")
	fmt.Fprintln(w, "  shapes it knows; a shape it does not know is absent, not counted.")
	fmt.Fprintln(w, "  Residency-set membership is not decoded, so there is no wired-bytes figure")
	fmt.Fprintln(w, "  separate from the allocated one.")
	if r.Unsized > 0 {
		fmt.Fprintf(w, "  %d buffer record(s) carried a zero length, which Metal does not create;\n", r.Unsized)
		fmt.Fprintln(w, "  the byte totals understate by whatever those held.")
	}
}
