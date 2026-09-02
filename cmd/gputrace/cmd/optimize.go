package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/optimize"
)

var optimizeRunOpts = struct {
	warmups    int
	iterations int
	output     string
	dir        string
	json       bool
}{warmups: 1, iterations: 5}

var optimizeCmd = &cobra.Command{
	Use:   "optimize",
	Short: "Measure, compare, and iterate on GPU workload performance",
	Long: `Measure, compare, and iterate on GPU workload performance.

The optimize subcommands close the agent loop: run a workload
reproducibly, compare two runs with a noise-aware verdict, and cite the
measured deltas that justify each conclusion.`,
}

var optimizeRunCmd = &cobra.Command{
	Use:   "run -- <command> [args...]",
	Short: "Run a workload repeatedly and record wall-clock statistics",
	Long: `Run a workload repeatedly and record wall-clock statistics.

Executes the command after '--' warmups+iterations times, discards the
warmups, and reports median/quartile wall time of the measured runs. The
full result persists as JSON when --output is given, in the schema that
'gputrace optimize compare' consumes.

Child failures are recorded per iteration rather than aborting the
series; check failed_count before trusting a comparison.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("no command given; use: gputrace optimize run [flags] -- <command> [args...]")
		}
		res, err := optimize.Run(optimize.Config{
			Command:    args,
			Dir:        optimizeRunOpts.dir,
			Warmups:    optimizeRunOpts.warmups,
			Iterations: optimizeRunOpts.iterations,
			OutputPath: optimizeRunOpts.output,
		})
		if err != nil && res == nil {
			return err
		}
		out := cmd.OutOrStdout()
		if optimizeRunOpts.json {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(res)
		}
		fmt.Fprintf(out, "%d measured iterations (%d discarded warmups), %d failed\n",
			len(res.Iterations), optimizeRunOpts.warmups, res.FailedCount)
		fmt.Fprintf(out, "median %s  q1 %s  q3 %s\n",
			dur(res.MedianNS), dur(res.Q1NS), dur(res.Q3NS))
		if optimizeRunOpts.output != "" {
			fmt.Fprintf(out, "result written to %s\n", optimizeRunOpts.output)
		}
		return err
	},
}

func init() {
	optimizeRunCmd.Flags().IntVar(&optimizeRunOpts.warmups, "warmups", optimizeRunOpts.warmups, "Discarded runs before measurement")
	optimizeRunCmd.Flags().IntVar(&optimizeRunOpts.iterations, "iterations", optimizeRunOpts.iterations, "Measured runs")
	optimizeRunCmd.Flags().StringVarP(&optimizeRunOpts.output, "output", "o", optimizeRunOpts.output, "Persist full JSON result to this path")
	optimizeRunCmd.Flags().StringVar(&optimizeRunOpts.dir, "dir", "", "Working directory for the workload")
	optimizeRunCmd.Flags().BoolVar(&optimizeRunOpts.json, "json", false, "Print the full result as JSON")
	optimizeCmd.AddCommand(optimizeRunCmd)
	rootCmd.AddCommand(optimizeCmd)
}
