package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/difftrace"
)

type diffOptions struct {
	JSON          bool
	CSV           bool
	By            string
	Limit         int
	MinDeltaUs    int
	OnlyEncoder   int
	OnlyFunction  string
	ShowMatches   bool
	ShowUnmatched bool
	ShowOccur     bool
	Explain       bool
	Quick         bool
	Divergence    bool
	DivergenceUs  int
	ByEncoder     bool
	MDOut         string
	PerfettoOut   string
	BenchDir      string
	Left          string
	Right         string
}

var diffCmd = newDiffCommand(&diffOptions{Limit: 20, OnlyEncoder: -1})

func newDiffCommand(opts *diffOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff [trace_a trace_b]",
		Short: "Compare two profiled traces at dispatch/kernel/encoder/timeline levels",
		Long: `Compare two traces using dispatch-level alignment from profiler streamData.

This command supports .gputrace bundles and -perfdata.gputrace bundles.
It reports total deltas, function-level contributors, encoder/pipeline deltas,
spike windows, unnamed dispatch impact, and matched/unmatched dispatches.

Examples:
  gputrace diff go-perfdata.gputrace py-perfdata.gputrace
  gputrace diff --bench-dir ~/bench-traces --quick --by-encoder
  gputrace diff --bench-dir ~/bench-traces --left go.gputrace --right py.gputrace
  gputrace diff a.gputrace b.gputrace --by function --limit 25 --explain
  gputrace diff a.gputrace b.gputrace --by encoder --only-encoder 2
  gputrace diff a.gputrace b.gputrace --json > diff.json
  gputrace diff a.gputrace b.gputrace --csv --by dispatch > outliers.csv
  gputrace diff a.gputrace b.gputrace --perfetto-out /tmp/diff_perfetto.json`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd, args, *opts)
		},
	}
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Output machine-readable JSON")
	cmd.Flags().BoolVar(&opts.CSV, "csv", false, "Output CSV for a single --by view")
	cmd.Flags().StringVar(&opts.By, "by", "", "View: function,encoder,pipeline,pipeline-pairs,timeline-windows,dispatch,unmatched,matches,occurrences")
	cmd.Flags().IntVar(&opts.Limit, "limit", 20, "Maximum rows per section")
	cmd.Flags().IntVar(&opts.MinDeltaUs, "min-delta-us", 0, "Filter top outliers by absolute delta in microseconds")
	cmd.Flags().IntVar(&opts.OnlyEncoder, "only-encoder", -1, "Only include dispatches for one encoder index")
	cmd.Flags().StringVar(&opts.OnlyFunction, "only-function", "", "Only include function names matching this regex")
	cmd.Flags().BoolVar(&opts.ShowMatches, "show-matches", false, "Show matched dispatch rows in text output")
	cmd.Flags().BoolVar(&opts.ShowUnmatched, "show-unmatched", false, "Show unmatched dispatch rows in text output")
	cmd.Flags().BoolVar(&opts.ShowOccur, "show-occurrences", false, "Show function+occurrence alignment rows in text output")
	cmd.Flags().BoolVar(&opts.Explain, "explain", false, "Print concise interpretation text")
	cmd.Flags().BoolVar(&opts.Quick, "quick", false, "Quick triage report (totals, top deltas, outliers, unnamed, spike windows)")
	cmd.Flags().BoolVar(&opts.Divergence, "divergence", false, "With --by encoder, compute first divergent encoder and tail slopes")
	cmd.Flags().IntVar(&opts.DivergenceUs, "divergence-threshold-us", 20, "Encoder divergence threshold in microseconds")
	cmd.Flags().BoolVar(&opts.ByEncoder, "by-encoder", false, "Encoder-focused report and dominance summary")
	cmd.Flags().StringVar(&opts.MDOut, "md-out", "", "Write markdown report to path")
	cmd.Flags().StringVar(&opts.PerfettoOut, "perfetto-out", "", "Write combined Perfetto/Chrome trace JSON with shared match IDs")
	cmd.Flags().StringVar(&opts.BenchDir, "bench-dir", "", "Auto-discover newest Go/Python perfdata pair from benchmark directory")
	cmd.Flags().StringVar(&opts.Left, "left", "", "Explicit left trace path (overrides auto-discovery)")
	cmd.Flags().StringVar(&opts.Right, "right", "", "Explicit right trace path (overrides auto-discovery)")
	return cmd
}

func init() {
	rootCmd.AddCommand(diffCmd)
}

func runDiff(cmd *cobra.Command, args []string, opts diffOptions) error {
	if err := opts.validate(args); err != nil {
		return err
	}

	var onlyFn *regexp.Regexp
	if strings.TrimSpace(opts.OnlyFunction) != "" {
		re, err := regexp.Compile(opts.OnlyFunction)
		if err != nil {
			return fmt.Errorf("invalid --only-function regex: %w", err)
		}
		onlyFn = re
	}

	leftPath, rightPath, discoverNote, err := resolveDiffInputs(args, opts)
	if err != nil {
		return err
	}

	a, err := difftrace.LoadTraceData(leftPath, opts.OnlyEncoder, onlyFn)
	if err != nil {
		return fmt.Errorf("load trace A: %w", err)
	}
	b, err := difftrace.LoadTraceData(rightPath, opts.OnlyEncoder, onlyFn)
	if err != nil {
		return fmt.Errorf("load trace B: %w", err)
	}

	aligned := difftrace.AlignDispatches(a, b, difftrace.AlignOptions{
		OnlyEncoder:  opts.OnlyEncoder,
		OnlyFunction: opts.OnlyFunction,
		MinDeltaUs:   opts.MinDeltaUs,
	})
	report := difftrace.BuildReport(a, b, aligned, difftrace.ReportOptions{Limit: opts.Limit, MinDeltaUs: opts.MinDeltaUs})
	if diffByIncludes(opts.By, "pipeline-pairs") {
		report.PipelinePairs = difftrace.BuildPipelinePairs(a, b)
	}
	if opts.Divergence {
		divergence := difftrace.AnalyzeEncoderDivergence(a.Encoders, b.Encoders, opts.DivergenceUs)
		report.EncoderDivergence = &divergence
	}
	if discoverNote != "" {
		report.Warnings = append([]string{discoverNote}, report.Warnings...)
	}

	if strings.TrimSpace(opts.MDOut) != "" {
		if err := difftrace.WriteMarkdown(opts.MDOut, report, opts.Limit); err != nil {
			return fmt.Errorf("write markdown: %w", err)
		}
	}
	if strings.TrimSpace(opts.PerfettoOut) != "" {
		if err := difftrace.WritePerfetto(opts.PerfettoOut, a, b, aligned); err != nil {
			return fmt.Errorf("write perfetto: %w", err)
		}
	}

	if opts.JSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if opts.Quick {
			return enc.Encode(difftrace.NewQuickReport(report, 10))
		}
		return enc.Encode(report)
	}

	if opts.CSV {
		view := strings.TrimSpace(opts.By)
		if view == "" {
			view = "function"
		}
		csvText, err := difftrace.RenderCSV(report, view, opts.Limit)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), csvText)
		return err
	}

	var text string
	if opts.Quick {
		text = difftrace.RenderQuick(report, 10)
		if opts.ByEncoder {
			text += "\n" + difftrace.RenderEncoderFocus(report, opts.Limit)
		}
	} else if opts.ByEncoder {
		text = difftrace.RenderEncoderFocus(report, opts.Limit)
	} else if opts.Divergence {
		text = difftrace.RenderText(report, opts.By, opts.ShowMatches, opts.ShowUnmatched, opts.ShowOccur, opts.Explain, opts.Limit)
		text += "\n" + difftrace.RenderEncoderDivergence(*report.EncoderDivergence)
	} else {
		text = difftrace.RenderText(report, opts.By, opts.ShowMatches, opts.ShowUnmatched, opts.ShowOccur, opts.Explain, opts.Limit)
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), text)
	return err
}

func (o diffOptions) validate(args []string) error {
	if o.JSON && o.CSV {
		return fmt.Errorf("--json and --csv are mutually exclusive")
	}
	if o.Limit <= 0 {
		return fmt.Errorf("--limit must be > 0")
	}
	if o.MinDeltaUs < 0 {
		return fmt.Errorf("--min-delta-us must be >= 0")
	}
	if o.DivergenceUs <= 0 {
		return fmt.Errorf("--divergence-threshold-us must be > 0")
	}
	if o.OnlyEncoder < -1 {
		return fmt.Errorf("--only-encoder must be >= -1")
	}
	if err := validateDiffBy(o.By); err != nil {
		return err
	}
	if len(args) == 1 {
		return fmt.Errorf("expected 0 or 2 positional traces, got 1")
	}

	left := strings.TrimSpace(o.Left)
	right := strings.TrimSpace(o.Right)
	benchDir := strings.TrimSpace(o.BenchDir)
	hasExplicitPair := left != "" || right != ""
	if (left == "") != (right == "") {
		return fmt.Errorf("--left and --right must be provided together")
	}
	if len(args) > 0 && hasExplicitPair {
		return fmt.Errorf("positional traces cannot be combined with --left/--right")
	}
	if len(args) > 0 && benchDir != "" {
		return fmt.Errorf("positional traces cannot be combined with --bench-dir")
	}
	if len(args) == 0 && !hasExplicitPair && benchDir == "" {
		return fmt.Errorf("missing traces: provide <trace_a> <trace_b>, --left/--right, or --bench-dir")
	}

	textOnlyFlags := o.ShowMatches || o.ShowUnmatched || o.ShowOccur || o.Explain || o.ByEncoder
	if o.JSON && textOnlyFlags {
		return fmt.Errorf("--json cannot be combined with text-only flags (--show-*, --explain, --by-encoder)")
	}
	if o.CSV {
		if o.Quick {
			return fmt.Errorf("--quick cannot be combined with --csv")
		}
		if textOnlyFlags {
			return fmt.Errorf("--csv cannot be combined with text-only flags (--show-*, --explain, --by-encoder)")
		}
		if strings.Contains(strings.TrimSpace(o.By), ",") {
			return fmt.Errorf("--csv requires a single --by view")
		}
	}
	if o.Quick {
		if strings.TrimSpace(o.By) != "" {
			return fmt.Errorf("--quick cannot be combined with --by")
		}
		if o.ShowMatches || o.ShowUnmatched || o.ShowOccur || o.Explain {
			return fmt.Errorf("--quick cannot be combined with --show-matches/--show-unmatched/--show-occurrences/--explain")
		}
	}
	if o.ByEncoder && strings.TrimSpace(o.By) != "" {
		return fmt.Errorf("--by-encoder cannot be combined with --by")
	}
	if o.Divergence && strings.TrimSpace(o.By) != "encoder" {
		return fmt.Errorf("--divergence requires --by encoder")
	}
	return nil
}

func resolveDiffInputs(args []string, opts diffOptions) (leftPath, rightPath, note string, err error) {
	left := strings.TrimSpace(opts.Left)
	right := strings.TrimSpace(opts.Right)
	if left != "" && right != "" {
		if strings.TrimSpace(opts.BenchDir) != "" {
			note = "--bench-dir ignored because --left/--right were provided"
		}
		return left, right, note, nil
	}

	if len(args) == 2 {
		return args[0], args[1], "", nil
	}

	benchDir := strings.TrimSpace(opts.BenchDir)
	if benchDir == "" {
		return "", "", "", fmt.Errorf("missing traces: provide <trace_a> <trace_b>, --left/--right, or --bench-dir")
	}
	pair, err := difftrace.DiscoverBenchPair(benchDir)
	if err != nil {
		return "", "", "", err
	}
	note = fmt.Sprintf("auto-pair: stem=%s left=%s right=%s", pair.Stem, filepath.Base(pair.Left), filepath.Base(pair.Right))
	if pair.LeftRaw != "" || pair.RightRaw != "" || pair.LeftCSV != "" || pair.RightCSV != "" {
		note += fmt.Sprintf(" siblings(left_raw=%s right_raw=%s left_csv=%s right_csv=%s)",
			emptyDash(filepath.Base(pair.LeftRaw)),
			emptyDash(filepath.Base(pair.RightRaw)),
			emptyDash(filepath.Base(pair.LeftCSV)),
			emptyDash(filepath.Base(pair.RightCSV)))
	}
	return pair.Left, pair.Right, note, nil
}

func emptyDash(s string) string {
	if s == "" || s == "." {
		return "-"
	}
	return s
}

func validateDiffBy(by string) error {
	by = strings.TrimSpace(by)
	if by == "" {
		return nil
	}
	allowed := map[string]bool{
		"function":         true,
		"encoder":          true,
		"pipeline-pairs":   true,
		"pipeline":         true,
		"timeline-windows": true,
		"dispatch":         true,
		"unmatched":        true,
		"matches":          true,
		"occurrences":      true,
	}
	for _, part := range strings.Split(by, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !allowed[part] {
			return fmt.Errorf("invalid --by value %q", part)
		}
	}
	return nil
}

func diffByIncludes(by, view string) bool {
	for _, part := range strings.Split(by, ",") {
		if strings.TrimSpace(part) == view {
			return true
		}
	}
	return false
}
