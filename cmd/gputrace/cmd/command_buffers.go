package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

type commandBuffersOptions struct {
	verbose  bool
	detailed bool
	json     bool
}

type commandBufferEncoderJSON struct {
	Index int    `json:"index"`
	Label string `json:"label,omitempty"`
}

type commandBufferJSON struct {
	Index           int                        `json:"index"`
	Label           string                     `json:"label,omitempty"`
	Offset          string                     `json:"offset"`
	Encoders        []commandBufferEncoderJSON `json:"encoders,omitempty"`
	Calls           int                        `json:"calls"`
	PipelineRecords int                        `json:"pipeline_records"`
	Dispatches      int                        `json:"dispatches"`
}

var commandBuffersCmd = newCommandBuffersCommand(&commandBuffersOptions{})

func newCommandBuffersCommand(opts *commandBuffersOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "command-buffers <trace.gputrace>",
		Short: "List and analyze command buffers in a GPU trace",
		Long: `List all Metal command buffers found in a GPU trace.

This command parses CUUU markers to identify command buffer submissions
and can provide detailed analysis of each command buffer including:
  - Number and types of encoders
  - API calls within each buffer
  - Dispatch calls and thread configurations

Examples:
  gputrace command-buffers trace.gputrace
  gputrace command-buffers trace.gputrace -v
  gputrace command-buffers trace.gputrace -d`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommandBuffers(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", false, "Show verbose output with encoder and API call counts")
	cmd.Flags().BoolVarP(&opts.detailed, "detailed", "d", false, "Show detailed analysis of each command buffer")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output in JSON format")
	return cmd
}

func init() {
	rootCmd.AddCommand(commandBuffersCmd)
}

func runCommandBuffers(cmd *cobra.Command, args []string, opts *commandBuffersOptions) error {
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

	// Parse command buffers
	commandBuffers, err := trace.ParseCommandBuffers()
	if err != nil {
		return fmt.Errorf("failed to parse command buffers: %w", err)
	}

	if opts.json {
		out, err := commandBuffersJSONOutput(trace, commandBuffers)
		if err != nil {
			return err
		}
		return writeCommandBuffersJSON(cmd.OutOrStdout(), out)
	}

	w := cmd.OutOrStdout()

	// Compact one-line-per-buffer output
	fmt.Fprintf(w, "%d command buffers:\n", len(commandBuffers))
	for _, cb := range commandBuffers {
		label := ""
		if cb.Label != "" {
			label = fmt.Sprintf(" label=%q", cb.Label)
		}
		if opts.verbose || opts.detailed {
			dcb, err := gputrace.ParseDetailedCommandBuffer(trace, cb.Index)
			if err != nil {
				fmt.Fprintf(w, "  %3d: offset=0x%08x%s (error: %v)\n", cb.Index, cb.Offset, label, err)
			} else {
				fmt.Fprintf(w, "  %3d: %d explicit encoders, %d pipeline records, %d dispatches%s\n",
					cb.Index, len(dcb.Encoders), len(dcb.Calls), len(dcb.Dispatches), label)
			}
		} else {
			fmt.Fprintf(w, "  %3d: offset=0x%08x%s\n", cb.Index, cb.Offset, label)
		}
	}

	// Show detailed analysis if requested
	if opts.detailed {
		fmt.Fprintf(w, "\n=== Detailed Analysis ===\n\n")
		for _, cb := range commandBuffers {
			if err := gputrace.DumpCommandBuffer(trace, w, cb.Index); err != nil {
				fmt.Fprintf(w, "Error dumping command buffer #%d: %v\n", cb.Index, err)
			}
		}
	}

	// Summary statistics (verbose only)
	if opts.verbose && !opts.detailed {
		totalEncoders := 0
		totalAPICalls := 0
		totalDispatches := 0
		for _, cb := range commandBuffers {
			dcb, err := gputrace.ParseDetailedCommandBuffer(trace, cb.Index)
			if err == nil {
				totalEncoders += len(dcb.Encoders)
				totalAPICalls += len(dcb.Calls)
				totalDispatches += len(dcb.Dispatches)
			}
		}
		if len(commandBuffers) > 0 {
			fmt.Fprintf(w, "\nTotal: %d explicit encoders, %d pipeline records, %d dispatches (%.1f enc/buf, %.1f records/buf, %.1f dispatches/buf)\n",
				totalEncoders, totalAPICalls, totalDispatches,
				float64(totalEncoders)/float64(len(commandBuffers)),
				float64(totalAPICalls)/float64(len(commandBuffers)),
				float64(totalDispatches)/float64(len(commandBuffers)))
		}
	}

	return nil
}

func commandBuffersJSONOutput(trace *gputrace.Trace, commandBuffers []*gputrace.CommandBuffer) ([]commandBufferJSON, error) {
	out := make([]commandBufferJSON, len(commandBuffers))
	for i, cb := range commandBuffers {
		entry := commandBufferJSON{
			Index:  cb.Index,
			Label:  cb.Label,
			Offset: fmt.Sprintf("0x%08x", cb.Offset),
		}
		dcb, err := gputrace.ParseDetailedCommandBuffer(trace, cb.Index)
		if err == nil {
			entry.Calls = len(dcb.Calls)
			entry.PipelineRecords = len(dcb.Calls)
			entry.Dispatches = len(dcb.Dispatches)
			for _, enc := range dcb.Encoders {
				entry.Encoders = append(entry.Encoders, commandBufferEncoderJSON{
					Index: enc.Index,
					Label: enc.Label,
				})
			}
		}
		out[i] = entry
	}
	return out, nil
}

func writeCommandBuffersJSON(w io.Writer, out []commandBufferJSON) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	return nil
}
