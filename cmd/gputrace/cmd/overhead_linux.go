//go:build linux

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/optimize"
)

type overheadOptions struct {
	iterations int
	warmups    int
	effectSize float64
	api        bool
	nvtx       bool
	json       bool
}

var overheadCmd = newOverheadCommand(&overheadOptions{iterations: 5, warmups: 1, effectSize: 5})

// OverheadReport is how much the capture shim perturbs the workload it
// measures, and whether that perturbation is large enough to invalidate
// the effect the user is studying.
type OverheadReport struct {
	Command       []string             `json:"command"`
	Mode          string               `json:"mode"`
	Baseline      *optimize.Result     `json:"baseline"`
	Instrumented  *optimize.Result     `json:"instrumented"`
	Comparison    *optimize.Comparison `json:"comparison"`
	OverheadPct   float64              `json:"overhead_pct"`
	EffectSizePct float64              `json:"effect_size_pct"`
	Usable        bool                 `json:"usable"`
	Verdict       string               `json:"verdict"`
}

func newOverheadCommand(opts *overheadOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "overhead [flags] -- <command> [args...]",
		Short: "Measure how much the capture shim perturbs a workload",
		Long: `Measure the capture shim's own cost by running a workload with and
without it.

A captured throughput number is only usable if the capture did not move
it. This runs the same command both ways, compares the wall-clock
distributions with the same noise-aware test 'optimize compare' uses, and
says whether the measured overhead is small enough for the effect you are
studying.

--effect-size names the difference you care about, as a percentage. When
the shim's overhead reaches it, captured timings describe the capture as
much as the workload, and the report says so.

Examples:
  gputrace overhead -- ./workload
  gputrace overhead --effect-size 3 -n 10 -- ./workload
  gputrace overhead --api -- ./workload`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOverhead(cmd, args, opts)
		},
	}
	cmd.Flags().IntVarP(&opts.iterations, "iterations", "n", opts.iterations, "Measured runs per side")
	cmd.Flags().IntVar(&opts.warmups, "warmups", opts.warmups, "Discarded runs before measurement, per side")
	cmd.Flags().Float64Var(&opts.effectSize, "effect-size", opts.effectSize, "Effect size under study, in percent; overhead at or above it invalidates captured timings")
	cmd.Flags().BoolVar(&opts.api, "api", opts.api, "Measure with host-side API records enabled")
	cmd.Flags().BoolVar(&opts.nvtx, "nvtx", opts.nvtx, "Measure with NVTX marker records enabled")
	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output machine-readable JSON")
	return cmd
}

func runOverhead(cmd *cobra.Command, args []string, opts *overheadOptions) error {
	if opts.iterations < 2 {
		return fmt.Errorf("overhead: --iterations must be at least 2 to separate signal from noise")
	}
	// The instrumented side writes into a throwaway bundle: the records
	// are not the point, the cost of producing them is.
	bundle, err := os.MkdirTemp("", "gputrace-overhead-*.gpucapture")
	if err != nil {
		return err
	}
	defer os.RemoveAll(bundle)

	env, err := cupticapture.PreloadEnv(cupticapture.Options{
		OutputPath: filepath.Join(bundle, cupticapture.EventsFileName),
		APIRecords: opts.api,
		NVTX:       opts.nvtx,
	})
	if err != nil {
		return err
	}

	base := optimize.Config{Command: args, Warmups: opts.warmups, Iterations: opts.iterations}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Measuring %d runs per side (%d warmup%s)...\n", opts.iterations, opts.warmups, plural(opts.warmups))

	baseline, err := optimize.Run(base)
	if err != nil {
		return fmt.Errorf("overhead: baseline: %w", err)
	}
	instrumented, err := optimize.Run(optimize.Config{
		Command: args, Dir: base.Dir, Warmups: opts.warmups, Iterations: opts.iterations, Env: env,
	})
	if err != nil {
		return fmt.Errorf("overhead: instrumented: %w", err)
	}

	rep := &OverheadReport{
		Command:       args,
		Mode:          overheadMode(opts),
		Baseline:      baseline,
		Instrumented:  instrumented,
		Comparison:    optimize.Compare(baseline, instrumented),
		EffectSizePct: opts.effectSize,
	}
	rep.OverheadPct = rep.Comparison.DeltaPct
	rep.Usable, rep.Verdict = overheadVerdict(rep)

	if opts.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	writeOverheadReport(out, rep)
	return nil
}

func overheadMode(opts *overheadOptions) string {
	mode := "activity"
	if opts.api {
		mode += "+api"
	}
	if opts.nvtx {
		mode += "+nvtx"
	}
	return mode
}

// overheadVerdict decides whether captured timings can carry a claim
// about an effect of the stated size. A change the capture itself could
// have produced is not evidence of anything.
func overheadVerdict(rep *OverheadReport) (bool, string) {
	switch rep.Comparison.Verdict {
	case optimize.Equivalent:
		return true, fmt.Sprintf("the shim's cost is inside run-to-run noise; captured timings carry effects down to %.1f%%", rep.EffectSizePct)
	case optimize.NoisyChange, optimize.Inconclusive:
		return false, fmt.Sprintf("the measurement itself is too noisy to bound the shim's cost (%s); rerun with more iterations", rep.Comparison.Verdict)
	}
	magnitude := rep.OverheadPct
	if magnitude < 0 {
		magnitude = -magnitude
	}
	if magnitude >= rep.EffectSizePct {
		return false, fmt.Sprintf("the shim moves wall time by %.1f%%, at or beyond the %.1f%% effect under study: captured timings describe the capture as much as the workload",
			magnitude, rep.EffectSizePct)
	}
	return true, fmt.Sprintf("the shim moves wall time by %.1f%%, below the %.1f%% effect under study", magnitude, rep.EffectSizePct)
}

func writeOverheadReport(out io.Writer, rep *OverheadReport) {
	fmt.Fprintf(out, "\ncapture mode: %s\n", rep.Mode)
	fmt.Fprintf(out, "baseline:     median %s  IQR [%s..%s]\n",
		dur(rep.Baseline.MedianNS), dur(rep.Baseline.Q1NS), dur(rep.Baseline.Q3NS))
	fmt.Fprintf(out, "instrumented: median %s  IQR [%s..%s]\n",
		dur(rep.Instrumented.MedianNS), dur(rep.Instrumented.Q1NS), dur(rep.Instrumented.Q3NS))
	fmt.Fprintf(out, "overhead:     %+.1f%% (%s)\n", rep.OverheadPct, signedDur(rep.Comparison.DeltaNS))
	fmt.Fprintf(out, "separation:   %s — %s\n", rep.Comparison.Verdict, rep.Comparison.Reason)
	if failed := rep.Baseline.FailedCount + rep.Instrumented.FailedCount; failed > 0 {
		fmt.Fprintf(out, "warning:      %d run%s exited nonzero; the workload may not be doing equal work on both sides\n", failed, plural(failed))
	}
	if rep.Usable {
		fmt.Fprintf(out, "\nusable: %s\n", rep.Verdict)
		return
	}
	fmt.Fprintf(out, "\nNOT usable for this effect size: %s\n", rep.Verdict)
}

func init() {
	rootCmd.AddCommand(overheadCmd)
}
