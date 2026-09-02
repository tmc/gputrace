package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/optimize"
)

var compareOpts = struct{ json bool }{}

var optimizeCompareCmd = &cobra.Command{
	Use:   "compare <base.json> <variant.json>",
	Short: "Compare two optimize runs with a noise-aware verdict",
	Long: `Compare two optimize runs with a noise-aware verdict.

Reads two result files written by 'gputrace optimize run --output' and
decides whether the variant improved, regressed, is equivalent, or
whether the medians differ inside overlapping noise (noisy-change) —
in which case the only sound action is collecting more iterations.

The verdict cites its evidence: both IQRs and the median delta. Agents
must treat noisy-change as "not yet known", never as improvement.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		base, err := readRunResult(args[0])
		if err != nil {
			return err
		}
		variant, err := readRunResult(args[1])
		if err != nil {
			return err
		}
		cmp := optimize.Compare(base, variant)
		out := cmd.OutOrStdout()
		if compareOpts.json {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(cmp)
		}
		fmt.Fprintf(out, "verdict: %s\n", cmp.Verdict)
		fmt.Fprintf(out, "base:    median %s\n", dur(cmp.BaseMedianNS))
		fmt.Fprintf(out, "variant: median %s (%+.1f%%)\n", dur(cmp.VariantMedianNS), cmp.DeltaPct)
		fmt.Fprintf(out, "%s\n", cmp.Reason)
		return nil
	},
}

func readRunResult(path string) (*optimize.Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var res optimize.Result
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &res, nil
}

func init() {
	optimizeCompareCmd.Flags().BoolVar(&compareOpts.json, "json", false, "Output machine-readable comparison")
	optimizeCmd.AddCommand(optimizeCompareCmd)
}
