package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
)

var (
	exportCountersOutput string
)

var exportCountersCmd = &cobra.Command{
	Use:    "export-counters <trace.gputrace>",
	Short:  "Export performance counters in Xcode Counters.csv format",
	Hidden: true,
	Long: `Export performance counter data in Xcode Instruments Counters.csv format.

Generates a 246-column CSV file matching the exact format used by Xcode
Instruments when exporting GPU performance counter data. This includes:

Metadata Columns (1-5):
  - Index: Sequential row number
  - Encoder FunctionIndex: Encoder function index
  - CommandBuffer Label: Command buffer identifier
  - Encoder Label: Encoder identifier
  - (Empty column)

Performance Metrics (6-246):
  241 performance counter metrics including:
  - ALU Utilization, Kernel Occupancy
  - Memory bandwidth (Buffer/Texture Device Memory Bytes)
  - Cache miss rates (L1, Texture Cache)
  - Shader-specific metrics (VS/FS/Compute)
  - Pipeline utilization and limiters
  - Invocation counts and statistics

Data Source:
  Exports parsed counter rows from .gpuprofiler_raw data when available.
  Any encoder row without parsed metrics is emitted with SYNTHETIC FALLBACK
  values. The command reports the row source counts on stderr, so stdout
  remains valid CSV when exporting there.

  As Metal replay support with MTLCounterSampleBuffer matures, replay-collected
  rows can replace remaining fallback rows with hardware measurements.

Output Format:
  Standard CSV with quoted strings, matching Xcode's export format exactly.
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
  - gputrace perfcounters: Parse .gpuprofiler_raw files
  - gputrace replay-counters: Collect fresh counters via replay`,
	Args: cobra.ExactArgs(1),
	RunE: runExportCounters,
}

func init() {
	rootCmd.AddCommand(exportCountersCmd)

	exportCountersCmd.Flags().StringVarP(&exportCountersOutput, "output", "o", "",
		"Output CSV file (default: stdout)")
}

func runExportCounters(cmd *cobra.Command, args []string) error {
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

	writer, closeOutput, err := createCommandOutput(exportCountersOutput)
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
		fmt.Fprint(os.Stderr, formatExportCounterSourceNotice(sourceSummary))
	}

	// Print success message to stderr (not stdout which has CSV data)
	if exportCountersOutput != "" {
		fmt.Fprintf(os.Stderr, "✓ Exported counters to: %s\n", exportCountersOutput)
	}

	return nil
}

type exportCounterSourceSummary struct {
	totalRows             int
	parsedCounterRows     int
	syntheticFallbackRows int
	perfCountersPresent   bool
}

func summarizeExportCounterSources(trace *gputrace.Trace) (exportCounterSourceSummary, error) {
	encoders, err := trace.ParseComputeEncoders()
	if err != nil {
		return exportCounterSourceSummary{}, err
	}

	summary := exportCounterSourceSummary{
		totalRows:             len(encoders),
		syntheticFallbackRows: len(encoders),
		perfCountersPresent:   trace.HasPerfCounters(),
	}

	if !summary.perfCountersPresent {
		return summary, nil
	}

	metrics, err := counter.PopulateEncoderMetricsFromBinaryParsing(trace)
	if err != nil || len(metrics) == 0 {
		return summary, nil
	}

	summary.parsedCounterRows = len(metrics)
	if summary.parsedCounterRows > summary.totalRows {
		summary.parsedCounterRows = summary.totalRows
	}
	summary.syntheticFallbackRows = summary.totalRows - summary.parsedCounterRows

	return summary, nil
}

func formatExportCounterSourceNotice(summary exportCounterSourceSummary) string {
	switch {
	case summary.totalRows == 0:
		return "counter export data source: no encoder rows exported\n"
	case summary.syntheticFallbackRows == 0:
		return fmt.Sprintf("counter export data source: parsed counter data (%s)\n", formatRows(summary.parsedCounterRows))
	case summary.parsedCounterRows == 0 && summary.perfCountersPresent:
		return fmt.Sprintf("counter export data source: synthetic fallback (%s); performance counter files were present but no parsed row metrics were available\n", formatRows(summary.syntheticFallbackRows))
	case summary.parsedCounterRows == 0:
		return fmt.Sprintf("counter export data source: synthetic fallback (%s); no parsed .gpuprofiler_raw counter data found\n", formatRows(summary.syntheticFallbackRows))
	default:
		return fmt.Sprintf("counter export data source: parsed counter data (%s), synthetic fallback (%s)\n",
			formatRows(summary.parsedCounterRows),
			formatRows(summary.syntheticFallbackRows))
	}
}

func formatRows(n int) string {
	if n == 1 {
		return "1 row"
	}
	return fmt.Sprintf("%d rows", n)
}
