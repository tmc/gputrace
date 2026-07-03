package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var xcodeCountersCmd = newXcodeCountersCommand(&xcodeCountersOptions{
	format: "summary",
})

type xcodeCountersOptions struct {
	format string
	metric string
	top    int
}

func newXcodeCountersCommand(opts *xcodeCountersOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "xcode-counters <trace.gputrace>",
		Short:  "Display performance counters from Xcode Counters.csv",
		Hidden: true,
		Long: `Display hardware performance counters from Xcode Counters.csv file.

This command parses the Counters.csv file that Xcode Instruments generates
when capturing GPU traces with performance counters enabled. It provides
access to 240+ hardware metrics including:

- ALU Utilization
- Kernel Occupancy
- Memory Bandwidth
- Instruction Throughput
- Texture Cache Hit Rates
- and many more...

Examples:
  # List all encoders with summary metrics
  gputrace xcode-counters trace.gputrace

  # Show top 5 encoders by a specific metric
  gputrace xcode-counters trace.gputrace --metric "ALU Utilization" --top 5

  # List all available metrics
  gputrace xcode-counters trace.gputrace --format metrics
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runXcodeCounters(cmd, args, opts)
		},
	}

	cmd.Flags().StringVar(&opts.format, "format", opts.format, "Output format: summary, detailed, metrics, json")
	cmd.Flags().StringVar(&opts.metric, "metric", opts.metric, "Filter/sort by specific metric (e.g., 'ALU Utilization')")
	cmd.Flags().IntVar(&opts.top, "top", opts.top, "Show only top N encoders by metric value")
	return cmd
}

func init() {
	rootCmd.AddCommand(xcodeCountersCmd)
}

func runXcodeCounters(cmd *cobra.Command, args []string, opts *xcodeCountersOptions) error {
	if err := validateXcodeCountersOptions(opts.format, opts.top); err != nil {
		return err
	}

	tracePath := args[0]

	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Parse Xcode Counters.csv
	csvData, err := gputrace.ParseXcodeCountersCSV(trace, "")
	if err != nil {
		return fmt.Errorf("failed to parse Xcode Counters.csv: %w", err)
	}

	out := cmd.OutOrStdout()
	switch opts.format {
	case "summary":
		return printXcodeSummary(out, csvData, opts)
	case "detailed":
		return printXcodeDetailed(out, csvData, opts)
	case "metrics":
		return printXcodeMetrics(out, csvData)
	case "json":
		return printXcodeJSON(out, csvData)
	default:
		return fmt.Errorf("unknown format: %s (use summary, detailed, metrics, or json)", opts.format)
	}
}

func validateXcodeCountersOptions(format string, top int) error {
	switch format {
	case "summary", "detailed", "metrics", "json":
	default:
		return fmt.Errorf("unknown format: %s (valid: summary, detailed, metrics, json)", format)
	}
	if top < 0 {
		return errors.New("--top must be >= 0")
	}
	return nil
}

func printXcodeSummary(out io.Writer, csvData *gputrace.XcodeCounterData, opts *xcodeCountersOptions) error {
	fmt.Fprintf(out, "=== Xcode Performance Counters ===\n\n")
	fmt.Fprintf(out, "Total Encoders: %d\n", len(csvData.Encoders))
	fmt.Fprintf(out, "Total Metrics:  %d\n\n", len(csvData.Metrics))

	// Key metrics to display in summary
	keyMetrics := []string{
		"ALU Utilization",
		"Kernel Occupancy",
		"Kernel Invocations",
		"GPU Read Bandwidth",
		"GPU Write Bandwidth",
		"Instruction Throughput Utilization",
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Encoder\tCommand Buffer\t")
	for _, metric := range keyMetrics {
		fmt.Fprintf(w, "%s\t", metric)
	}
	fmt.Fprintf(w, "\n")

	// Print separator
	fmt.Fprintf(w, "-------\t--------------\t")
	for range keyMetrics {
		fmt.Fprintf(w, "--------\t")
	}
	fmt.Fprintf(w, "\n")

	// Filter encoders if --metric and --top specified
	encoders := csvData.Encoders
	if opts.metric != "" && opts.top > 0 {
		encoders = filterTopEncoders(csvData, opts.metric, opts.top)
	}

	// Print each encoder
	for i := range encoders {
		enc := &encoders[i]
		fmt.Fprintf(w, "%d\t%s\t", enc.Index, enc.CommandBufferLabel)

		for _, metric := range keyMetrics {
			if val, ok := enc.Counters[metric]; ok {
				// Format based on metric type
				if metric == "Kernel Invocations" {
					fmt.Fprintf(w, "%.0f\t", val)
				} else if metric == "GPU Read Bandwidth" || metric == "GPU Write Bandwidth" {
					fmt.Fprintf(w, "%.2f GB/s\t", val/1e9)
				} else {
					fmt.Fprintf(w, "%.2f%%\t", val)
				}
			} else {
				fmt.Fprintf(w, "-\t")
			}
		}
		fmt.Fprintf(w, "\n")
	}

	w.Flush()
	return nil
}

func printXcodeDetailed(out io.Writer, csvData *gputrace.XcodeCounterData, opts *xcodeCountersOptions) error {
	fmt.Fprintf(out, "=== Detailed Xcode Performance Counters ===\n\n")

	// Filter encoders if --metric and --top specified
	encoders := csvData.Encoders
	if opts.metric != "" && opts.top > 0 {
		encoders = filterTopEncoders(csvData, opts.metric, opts.top)
	}

	for i := range encoders {
		enc := &encoders[i]
		fmt.Fprintf(out, "Encoder %d:\n", enc.Index)
		fmt.Fprintf(out, "  Function Index:    %d\n", enc.FunctionIndex)
		fmt.Fprintf(out, "  Command Buffer:    %s\n", enc.CommandBufferLabel)
		fmt.Fprintf(out, "  Encoder Label:     %s\n", enc.EncoderLabel)
		fmt.Fprintf(out, "  Counter count:     %d\n\n", len(enc.Counters))

		// Sort counter names for consistent output
		names := make([]string, 0, len(enc.Counters))
		for name := range enc.Counters {
			names = append(names, name)
		}
		sort.Strings(names)

		// Print counters in columns
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, name := range names {
			val := enc.Counters[name]
			fmt.Fprintf(w, "  %s:\t%.2f\n", name, val)
		}
		w.Flush()
		fmt.Fprintln(out)
	}

	return nil
}

func printXcodeMetrics(out io.Writer, csvData *gputrace.XcodeCounterData) error {
	fmt.Fprintf(out, "=== Available Metrics (%d total) ===\n\n", len(csvData.Metrics))

	for i, metric := range csvData.Metrics {
		fmt.Fprintf(out, "%3d. %s\n", i+1, metric)
	}

	return nil
}

func printXcodeJSON(out io.Writer, csvData *gputrace.XcodeCounterData) error {
	output := xcodeCountersJSONOutput{
		Encoders: len(csvData.Encoders),
		Metrics:  len(csvData.Metrics),
		Data:     make([]xcodeCountersJSONEncoder, 0, len(csvData.Encoders)),
	}
	for i := range csvData.Encoders {
		enc := &csvData.Encoders[i]
		output.Data = append(output.Data, xcodeCountersJSONEncoder{
			Index:          enc.Index,
			FunctionIndex:  enc.FunctionIndex,
			CommandBuffer:  enc.CommandBufferLabel,
			EncoderLabel:   enc.EncoderLabel,
			CounterMetrics: enc.Counters,
		})
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

type xcodeCountersJSONOutput struct {
	Encoders int                        `json:"encoders"`
	Metrics  int                        `json:"metrics"`
	Data     []xcodeCountersJSONEncoder `json:"data"`
}

type xcodeCountersJSONEncoder struct {
	Index          int                `json:"index"`
	FunctionIndex  int                `json:"function_index"`
	CommandBuffer  string             `json:"command_buffer"`
	EncoderLabel   string             `json:"encoder_label"`
	CounterMetrics map[string]float64 `json:"counters"`
}

func filterTopEncoders(csvData *gputrace.XcodeCounterData, metricName string, top int) []gputrace.XcodeEncoderCounters {
	// Create a sortable slice
	type encoderValue struct {
		encoder gputrace.XcodeEncoderCounters
		value   float64
	}

	values := make([]encoderValue, 0, len(csvData.Encoders))
	for i := range csvData.Encoders {
		if val, ok := csvData.Encoders[i].Counters[metricName]; ok {
			values = append(values, encoderValue{
				encoder: csvData.Encoders[i],
				value:   val,
			})
		}
	}

	// Sort by value descending
	sort.Slice(values, func(i, j int) bool {
		return values[i].value > values[j].value
	})

	// Take top N. Non-positive values mean no limit for direct callers.
	if top <= 0 || top > len(values) {
		top = len(values)
	}

	result := make([]gputrace.XcodeEncoderCounters, top)
	for i := 0; i < top; i++ {
		result[i] = values[i].encoder
	}

	return result
}
