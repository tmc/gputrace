package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/cuptitrace"
	"github.com/tmc/gputrace/internal/gpuevent"
)

var optimizeCaptureCompareCmd = &cobra.Command{
	Use:   "compare-captures <base.gpucapture|events.jsonl> <variant...>",
	Short: "Compare kernel activity between two captures (noise-free)",
	Long: `Compare kernel activity between two captures.

Unlike 'optimize compare' (wall-clock, noise-sensitive), this diffs the
kernel-level analyses of two capture bundles or JSONL files: per-kernel
launch counts, mean durations, and total time, with a verdict based on
which kernels moved beyond a 5% threshold.

This is the right comparison when process startup or unified-memory
overhead dominates wall clock — e.g. integrated-GPU hosts where a real
28% kernel win can read as "equivalent" end to end.`,
	Args:               cobra.ExactArgs(2),
	DisableFlagParsing: false,
	RunE: func(cmd *cobra.Command, args []string) error {
		base, err := loadCaptureReport(args[0])
		if err != nil {
			return err
		}
		variant, err := loadCaptureReport(args[1])
		if err != nil {
			return err
		}
		cmp := gpuevent.CompareCaptures(base, variant)
		out := cmd.OutOrStdout()
		if compareCapturesOpts.json {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(cmp)
		}
		fmt.Fprintf(out, "verdict: %s\n", cmp.Verdict)
		fmt.Fprintf(out, "%s\n", cmp.Summary)
		fmt.Fprintf(out, "total kernel time: %.2f ms -> %.2f ms (%+.1f%%)\n\n",
			float64(cmp.BaseTotalNS)/1e6, float64(cmp.VariantTotalNS)/1e6, cmp.TotalDeltaPct)
		fmt.Fprintf(out, "Per-kernel deltas (by impact):\n")
		for _, d := range cmp.KernelDeltas {
			fmt.Fprintf(out, "  %6.1f%%  %4dx->%-4dx  mean %9s -> %-9s  %s\n",
				d.DeltaPct, d.BaseCount, d.VariantCount,
				dur(d.BaseMeanNS), dur(d.VariantMeanNS), shortKernel(d.Name))
		}
		return nil
	},
}

var compareCapturesOpts struct{ json bool }

// loadCaptureReport reads a bundle or JSONL and analyzes it.
func loadCaptureReport(path string) (*gpuevent.Report, error) {
	r, closers, err := cupticapture.OpenEvents(path)
	if err != nil {
		return nil, err
	}
	defer closers()
	cap, err := gpuevent.DecodeJSONL(r)
	if err != nil {
		return nil, err
	}
	samplesPath := cupticapture.ResolveSamples(path, "")
	if samplesPath != "" {
		sf, sErr := os.Open(samplesPath)
		if sErr == nil {
			sc, dErr := gpuevent.DecodeJSONL(sf)
			if dErr == nil {
				cap.Samples = sc.Samples
			}
			sf.Close()
		}
	}
	for i := range cap.Events {
		if cap.Events[i].Kind == gpuevent.KindKernel && cap.Events[i].Name == "" {
			cap.Events[i].Name = cuptitrace.Demangle(cap.Events[i].RawSymbol)
		}
	}
	rep := gpuevent.Analyze(cap.Events, cap.Samples)
	health := gpuevent.MeasureCompleteness(cap)
	rep.Completeness = &health
	return rep, nil
}

func init() {
	optimizeCaptureCompareCmd.Flags().BoolVar(&compareCapturesOpts.json, "json", false, "Output machine-readable comparison")
	optimizeCmd.AddCommand(optimizeCaptureCompareCmd)
}
