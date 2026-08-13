package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/evidence"
	"github.com/tmc/gputrace/internal/fmtutil"
)

var summaryCmd = newSummaryCommand(new(summaryOptions))

type summaryOptions struct {
	json  bool
	limit int
}

func newSummaryCommand(opts *summaryOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "summary <trace.gputrace>",
		Short: "Summarize structure, timing, and evidence gaps",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSummary(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output JSON")
	cmd.Flags().IntVar(&opts.limit, "limit", 5, "Maximum functions to show")
	return cmd
}

func init() {
	rootCmd.AddCommand(summaryCmd)
}

func runSummary(cmd *cobra.Command, args []string, opts *summaryOptions) error {
	if opts.limit < 0 {
		return fmt.Errorf("--limit must be >= 0")
	}
	path := args[0]
	if err := checkTraceFile(path); err != nil {
		return err
	}
	_, stats, err := loadProfilerStats(path)
	if err != nil {
		return err
	}
	tr, err := gputrace.Open(path)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}
	defer tr.Close()
	report, err := evidence.Build(tr, stats)
	if err != nil {
		return err
	}
	if opts.json {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	writeSummary(cmd.OutOrStdout(), report, opts.limit)
	return nil
}

func writeSummary(w io.Writer, report *evidence.Report, limit int) {
	fmt.Fprintf(w, "%d command buffers · %d profiler compute encoders · %d dispatches\n",
		report.CommandBuffers, report.ComputeEncoders, report.Dispatches)
	fmt.Fprintf(w, "Dispatch span %s · CB active %s · CB wall %s\n",
		formatSummaryDuration(report.DispatchSpan), formatSummaryDuration(report.CBActiveTime), formatSummaryDuration(report.CBWallSpan))
	fmt.Fprintf(w, "Timing: %s", report.TimingSource)
	if report.TimingApproximate {
		fmt.Fprint(w, " (approximate)")
	}
	fmt.Fprintln(w, "; dispatch spans may include boundary or gap time")
	fmt.Fprintf(w, "Labels: %d CS/debug label records, %d unique (not encoder instances)\n",
		report.CSLabels, report.UniqueCSLabels)

	fmt.Fprintln(w, "\nTop work")
	rows := report.Functions
	if len(rows) > limit {
		rows = rows[:limit]
	}
	for _, row := range rows {
		fmt.Fprintf(w, "%-44s %6d calls  %9s  %5.1f%%\n",
			fmtutil.TruncateString(row.Name, 44), row.Dispatches, formatSummaryDuration(row.Span), row.SpanShare)
	}

	fmt.Fprintln(w, "\nPacking")
	fmt.Fprintf(w, "median %.1f dispatches/encoder · %.1f dispatches/command buffer\n",
		report.Packing.MedianDispatchesPerEncoder, report.Packing.DispatchesPerCommandBuffer)
	if len(report.EvidenceGaps) > 0 {
		fmt.Fprintln(w, "\nEvidence gaps")
		for i, gap := range report.EvidenceGaps {
			if i > 0 {
				fmt.Fprint(w, " · ")
			}
			fmt.Fprint(w, gap)
		}
		fmt.Fprintln(w)
	}
}

func formatSummaryDuration(d time.Duration) string {
	if d <= 0 {
		return "unavailable"
	}
	return FormatDurationNs(uint64(d))
}
