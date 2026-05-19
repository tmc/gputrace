package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var (
	cmdBuffersVerbose  bool
	cmdBuffersDetailed bool
	cmdBuffersJSON     bool
)

var commandBuffersCmd = &cobra.Command{
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
	RunE: runCommandBuffers,
}

func init() {
	rootCmd.AddCommand(commandBuffersCmd)

	commandBuffersCmd.Flags().BoolVarP(&cmdBuffersVerbose, "verbose", "v", false, "Show verbose output with encoder and API call counts")
	commandBuffersCmd.Flags().BoolVarP(&cmdBuffersDetailed, "detailed", "d", false, "Show detailed analysis of each command buffer")
	commandBuffersCmd.Flags().BoolVar(&cmdBuffersJSON, "json", false, "Output in JSON format")
}

func runCommandBuffers(cmd *cobra.Command, args []string) error {
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

	if cmdBuffersJSON {
		type cbEncoderJSON struct {
			Index int    `json:"index"`
			Label string `json:"label,omitempty"`
		}
		type cbJSON struct {
			Index           int             `json:"index"`
			Label           string          `json:"label,omitempty"`
			Offset          string          `json:"offset"`
			Encoders        []cbEncoderJSON `json:"encoders,omitempty"`
			Calls           int             `json:"calls"`
			PipelineRecords int             `json:"pipeline_records"`
			Dispatches      int             `json:"dispatches"`
		}
		out := make([]cbJSON, len(commandBuffers))
		for i, cb := range commandBuffers {
			entry := cbJSON{
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
					entry.Encoders = append(entry.Encoders, cbEncoderJSON{
						Index: enc.Index,
						Label: enc.Label,
					})
				}
			}
			out[i] = entry
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal json: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	// Compact one-line-per-buffer output
	fmt.Printf("%d command buffers:\n", len(commandBuffers))
	for _, cb := range commandBuffers {
		label := ""
		if cb.Label != "" {
			label = fmt.Sprintf(" label=%q", cb.Label)
		}
		if cmdBuffersVerbose || cmdBuffersDetailed {
			dcb, err := gputrace.ParseDetailedCommandBuffer(trace, cb.Index)
			if err != nil {
				fmt.Printf("  %3d: offset=0x%08x%s (error: %v)\n", cb.Index, cb.Offset, label, err)
			} else {
				fmt.Printf("  %3d: %d explicit encoders, %d pipeline records, %d dispatches%s\n",
					cb.Index, len(dcb.Encoders), len(dcb.Calls), len(dcb.Dispatches), label)
			}
		} else {
			fmt.Printf("  %3d: offset=0x%08x%s\n", cb.Index, cb.Offset, label)
		}
	}

	// Show detailed analysis if requested
	if cmdBuffersDetailed {
		fmt.Printf("\n=== Detailed Analysis ===\n\n")
		for _, cb := range commandBuffers {
			if err := gputrace.DumpCommandBuffer(trace, cmd.OutOrStdout(), cb.Index); err != nil {
				fmt.Printf("Error dumping command buffer #%d: %v\n", cb.Index, err)
			}
		}
	}

	// Summary statistics (verbose only)
	if cmdBuffersVerbose && !cmdBuffersDetailed {
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
			fmt.Printf("\nTotal: %d explicit encoders, %d pipeline records, %d dispatches (%.1f enc/buf, %.1f records/buf, %.1f dispatches/buf)\n",
				totalEncoders, totalAPICalls, totalDispatches,
				float64(totalEncoders)/float64(len(commandBuffers)),
				float64(totalAPICalls)/float64(len(commandBuffers)),
				float64(totalDispatches)/float64(len(commandBuffers)))
		}
	}

	return nil
}
