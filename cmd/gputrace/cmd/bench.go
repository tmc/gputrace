package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/tracebench"
)

var benchCmd = newBenchCommand(new(benchOptions))

type benchOptions struct {
	format      string
	name        string
	work        uint64
	workUnit    string
	benchConfig benchfmtConfigFlags
}

func newBenchCommand(opts *benchOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bench <trace.gputrace>",
		Short: "Export trace evidence for Go benchmark tools",
		Long: `Export a stable, sectioned GPU trace report as JSON or Go benchmark text.

Without --bench-work, measurements are honest trace totals with units such as
dispatches/trace and dispatch_span_ns/trace. Per-work units require both a
positive --bench-work count and --bench-work-unit.

The JSON report keeps structural counts and measured profiler timing in
separate sections with source, status, and refusal details. Benchfmt output
records observer, payload, trace UUID, timing source, and declared work. It is
accepted directly by golang.org/x/perf/benchfmt and benchstat.

Go programs can use github.com/tmc/gputrace/tracebench instead of parsing this
command's output. Its ReportMetrics method writes the same values through
testing.B.ReportMetric.

Examples:
  gputrace bench run.gputrace --format json
  gputrace bench run-perfdata.gputrace --format benchfmt
  gputrace bench run-perfdata.gputrace --format benchfmt \
    --bench-name BenchmarkDecode --bench-work 32 --bench-work-unit token \
    --bench-config arm=candidate`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBench(cmd, args[0], opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.format, "format", "json", "Output format: json or benchfmt")
	f.StringVar(&opts.name, "bench-name", "BenchmarkGPUTrace", "Benchmark name for benchfmt output")
	f.Uint64Var(&opts.work, "bench-work", 0, "Logical work represented by the trace")
	f.StringVar(&opts.workUnit, "bench-work-unit", "", "Logical work unit: op, token, step, or byte")
	f.Var(&opts.benchConfig, "bench-config", "Set benchfmt configuration key=value (repeatable)")
	return cmd
}

func runBench(cmd *cobra.Command, path string, opts *benchOptions) error {
	var work *tracebench.Work
	switch {
	case opts.work == 0 && opts.workUnit != "":
		return fmt.Errorf("--bench-work-unit requires --bench-work")
	case opts.work > 0 && opts.workUnit == "":
		return fmt.Errorf("--bench-work requires --bench-work-unit")
	case opts.work > 0:
		work = &tracebench.Work{Count: opts.work, Unit: opts.workUnit}
	}
	report, err := tracebench.Analyze(path, tracebench.Options{Work: work})
	if err != nil {
		return err
	}
	switch opts.format {
	case "json":
		return tracebench.WriteJSON(cmd.OutOrStdout(), report)
	case "benchfmt":
		config := make([]tracebench.Config, len(opts.benchConfig))
		for i, item := range opts.benchConfig {
			config[i] = tracebench.Config{Key: item.Key, Value: item.Value}
		}
		return tracebench.WriteBenchfmt(cmd.OutOrStdout(), report, tracebench.BenchfmtOptions{
			Name:   opts.name,
			Config: config,
		})
	default:
		return fmt.Errorf("unsupported format %q", opts.format)
	}
}

func init() {
	rootCmd.AddCommand(benchCmd)
}
