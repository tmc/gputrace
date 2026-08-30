package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/gate"
)

type gateOptions struct {
	tokens                int
	exactTokens           bool
	invariant             string
	slack                 int
	stationarityThreshold float64
	blockSize             int
	json                  bool
	ranges                []string
	compare               bool
}

var gateCmd = newGateCommand(&gateOptions{
	slack:                 2,
	stationarityThreshold: 0.15,
	blockSize:             16,
})

func newGateCommand(opts *gateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gate [flags] <bundle>...",
		Short: "Gate a GPU capture against workload invariants and stationarity",
		Long: `Gate a GPU capture before trusting anything in it.

Evaluates three independent checks:

  1. completeness  - scores against a workload invariant (an op the model runs
                     once per token), not just the tracer's self-reported drop
                     counter which reads zero when records are stranded.
  2. stationarity  - the per-token trajectory must be flat across blocks; a mid-run
                     excursion leaves per-kernel medians intact while inflating
                     summed time.
  3. staging       - reports observed data movement (CUDA HtoD transfers or Metal
                     streamData blit calls) with explicit distinction between
                     recorded zero and absent data.

Exit status:
  0: all evaluated gates passed
  1: capture failed a gate (e.g. completeness loss or stationarity excursion)
  2: capture was not evaluable (e.g. 0 invariant matches or missing required flags)`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGate(cmd, args, opts)
		},
	}

	cmd.Flags().IntVarP(&opts.tokens, "tokens", "t", 0, "tokens the run was asked to generate")
	cmd.Flags().BoolVar(&opts.exactTokens, "exact-tokens", false, "score want = tokens without prefill +1")
	cmd.Flags().StringVarP(&opts.invariant, "invariant", "k", "", "symbol substring for an op that fires once per token (default: arg_reduce on CUDA; required on Metal)")
	cmd.Flags().IntVar(&opts.slack, "slack", 2, "tokens allowed missing: flush-window residual, not a loss budget")
	cmd.Flags().Float64Var(&opts.stationarityThreshold, "stationarity-threshold", 0.15, "max allowed relative excursion for stationarity (0.15 = 15%)")
	cmd.Flags().IntVar(&opts.blockSize, "block-size", 16, "token block size for trajectory stationarity analysis")
	cmd.Flags().BoolVar(&opts.json, "json", false, "output machine-readable JSON verdict")
	cmd.Flags().StringSliceVar(&opts.ranges, "ranges", nil, "reserved seam for nested half-open range monotonicity checks")
	cmd.Flags().BoolVar(&opts.compare, "compare", false, "reserved seam for two-bundle residency/staging comparison")

	return cmd
}

func init() {
	rootCmd.AddCommand(gateCmd)
}

func runGate(cmd *cobra.Command, args []string, opts *gateOptions) error {
	gateOpts := gate.Options{
		Tokens:                opts.tokens,
		ExactTokens:           opts.exactTokens,
		InvariantSymbol:       opts.invariant,
		Slack:                 opts.slack,
		StationarityThreshold: opts.stationarityThreshold,
		BlockSize:             opts.blockSize,
		Ranges:                opts.ranges,
	}

	var results []*gate.Result
	hasFail := false
	hasNotEvaluable := false

	out := cmd.OutOrStdout()

	for _, bundle := range args {
		res, err := gate.Evaluate(bundle, gateOpts)
		if err != nil {
			return fmt.Errorf("gate %s: %w", bundle, err)
		}
		results = append(results, res)

		switch res.Verdict {
		case gate.VerdictFail:
			hasFail = true
		case gate.VerdictNotEvaluable:
			hasNotEvaluable = true
		}

		if !opts.json {
			printGateResult(out, res)
		}
	}

	if opts.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if len(results) == 1 {
			if err := enc.Encode(results[0]); err != nil {
				return fmt.Errorf("write json: %w", err)
			}
		} else {
			if err := enc.Encode(results); err != nil {
				return fmt.Errorf("write json: %w", err)
			}
		}
	}

	if hasFail {
		return errGateFailed
	}
	if hasNotEvaluable {
		return errGateNotEvaluable
	}
	return nil
}

func printGateResult(w io.Writer, r *gate.Result) {
	fmt.Fprintln(w, r.Summary)
}

type gateFailedError struct{}

func (gateFailedError) Error() string     { return "capture failed gate" }
func (gateFailedError) exitCode() int     { return 1 }
func (gateFailedError) alreadyReported()  {}

type gateNotEvaluableError struct{}

func (gateNotEvaluableError) Error() string     { return "capture not evaluable" }
func (gateNotEvaluableError) exitCode() int     { return 2 }
func (gateNotEvaluableError) alreadyReported()  {}

var (
	errGateFailed       = gateFailedError{}
	errGateNotEvaluable = gateNotEvaluableError{}
)
