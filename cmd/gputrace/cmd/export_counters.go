package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var exportCountersCmd = newExportCountersCommand(&exportCountersOptions{})

type exportCountersOptions struct {
	output string
}

func newExportCountersCommand(opts *exportCountersOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "export-counters <trace.gputrace>",
		Short:  "Export performance counters in Xcode Counters.csv format",
		Hidden: true,
		Long: `Export performance counter data in Xcode Instruments Counters.csv format.

Generates a 246-column CSV with the same column schema used by an Xcode
Instruments counter export. Schema compatibility does not mean that every row
contains source-backed Xcode measurements. This includes:

Metadata Columns (1-5):
  - Index: Sequential row number
  - Encoder FunctionIndex: Encoder function index
  - CommandBuffer Label: Command buffer identifier
  - Encoder Label: Encoder identifier
  - (Empty column)

Performance Metrics (6-246):
  241 performance counter metrics including:
  - ALU Utilization
  - Memory bandwidth (Buffer/Texture Device Memory Bytes)
  - Cache miss rates (L1, Texture Cache)
  - Shader-specific metrics (VS/FS/Compute)
  - Pipeline utilization and limiters
  - Invocation counts and statistics

Data Source:
  This exporter writes encoder identity and leaves metric columns blank. Parsed
  .gpuprofiler_raw counter rows are pipeline-scoped, not encoder-scoped, and
  are withheld until a stable join exists. The command reports that source
  state on stderr, so stdout remains valid CSV when exporting there.

  A capture-backed encoder join or replay-collected measurements can populate
  the metric columns in a future export.

Output Format:
  Standard CSV with quoted strings and an Xcode-compatible column schema.
  Can be imported into spreadsheet tools or compared with Xcode's output.

Examples:
  # Export counters to CSV file
  gputrace export-counters trace.gputrace -o counters.csv

  # Export to stdout
  gputrace export-counters trace.gputrace

  # Compare with Xcode's export
  diff <(gputrace export-counters trace.gputrace) xcode_counters.csv

Use Cases:
  - Validate CSV format matches Xcode structure
  - Import into analysis tools (Excel, pandas, etc.)
  - Automate performance reporting
  - Compare across different trace captures

Related Commands:
  - gputrace timeline: Visual timeline with counter tracks
  - gputrace profiler: Extract profiler timing data
  - gputrace xcode-profile xcode-export-counters: Export counters through Xcode`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExportCounters(cmd, args, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output,
		"Output CSV file (default: stdout)")
	return cmd
}

func init() {
	rootCmd.AddCommand(exportCountersCmd)
}

func runExportCounters(cmd *cobra.Command, args []string, opts *exportCountersOptions) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Create CSV exporter
	exporter := gputrace.NewCountersCSVExporter(trace)
	sourceSummary, sourceSummaryErr := summarizeExportCounterSources(trace)

	writer, closeOutput, err := createCommandOutput(opts.output)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	if closeOutput != nil {
		defer closeOutput()
	}

	// Export CSV
	if err := exporter.ExportCountersCSV(writer); err != nil {
		return fmt.Errorf("failed to export counters CSV: %w", err)
	}

	if sourceSummaryErr == nil {
		fmt.Fprint(cmd.ErrOrStderr(), formatExportCounterSourceNotice(sourceSummary))
	}

	// Print success message to stderr (not stdout which has CSV data)
	if opts.output != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "Counter CSV written: %s\n", opts.output)
	}

	return nil
}

type exportCounterSourceSummary struct {
	totalRows           int
	metadataOnlyRows    int
	perfCountersPresent bool
}

func summarizeExportCounterSources(trace *gputrace.Trace) (exportCounterSourceSummary, error) {
	encoders := trace.ParseComputeEncoders()

	summary := exportCounterSourceSummary{
		totalRows:           len(encoders),
		metadataOnlyRows:    len(encoders),
		perfCountersPresent: trace.HasPerfCounters(),
	}
	return summary, nil
}

func formatExportCounterSourceNotice(summary exportCounterSourceSummary) string {
	switch {
	case summary.totalRows == 0:
		return "counter export data source: no encoder rows exported\n"
	case summary.perfCountersPresent:
		return fmt.Sprintf("counter export data source: metadata only (%s); performance-counter rows are pipeline-scoped and lack an encoder join\n", formatRows(summary.metadataOnlyRows))
	default:
		return fmt.Sprintf("counter export data source: metadata only (%s); no parsed .gpuprofiler_raw counter data found\n", formatRows(summary.metadataOnlyRows))
	}
}

func formatRows(n int) string {
	if n == 1 {
		return "1 row"
	}
	return fmt.Sprintf("%d rows", n)
}
