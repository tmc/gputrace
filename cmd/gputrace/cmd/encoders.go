package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace"
)

var (
	encodersVerbose bool
	encodersJSON    bool
)

var encodersCmd = &cobra.Command{
	Use:   "encoders <trace.gputrace>",
	Short: "List compute command encoders in a GPU trace",
	Long: `List all Metal compute command encoders found in a GPU trace.

This command parses Cul records to identify compute command encoder
creation and usage. Compute encoders are used to encode compute
commands (kernel dispatches) into command buffers.

Examples:
  gputrace encoders trace.gputrace
  gputrace encoders trace.gputrace -v`,
	Args: cobra.ExactArgs(1),
	RunE: runEncoders,
}

type encodersCommandBufferSummary struct {
	index        int
	encoderCount int
}

func init() {
	rootCmd.AddCommand(encodersCmd)

	encodersCmd.Flags().BoolVarP(&encodersVerbose, "verbose", "v", false, "Show verbose output with encoder details")
	encodersCmd.Flags().BoolVar(&encodersJSON, "json", false, "Output in JSON format")
}

func runEncoders(cmd *cobra.Command, args []string) error {
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

	// Parse compute encoders
	encoders, err := trace.ParseComputeEncoders()
	if err != nil {
		return fmt.Errorf("failed to parse compute encoders: %w", err)
	}

	if encodersJSON {
		return writeEncodersJSON(cmd.OutOrStdout(), encoders)
	}

	commandBufferCount := 0
	var commandBuffers []encodersCommandBufferSummary
	if encodersVerbose {
		cbs, err := trace.ParseCommandBuffers()
		if err == nil && len(cbs) > 0 {
			commandBufferCount = len(cbs)
			for _, cb := range cbs {
				dcb, err := gputrace.ParseDetailedCommandBuffer(trace, cb.Index)
				if err != nil {
					continue
				}
				commandBuffers = append(commandBuffers, encodersCommandBufferSummary{
					index:        cb.Index,
					encoderCount: len(dcb.Encoders),
				})
			}
		}
	}

	return writeEncodersText(cmd.OutOrStdout(), encoders, commandBufferCount, commandBuffers)
}

func writeEncodersJSON(w io.Writer, encoders []*gputrace.ComputeEncoder) error {
	type encoderJSON struct {
		Index   int    `json:"index"`
		Label   string `json:"label"`
		Address string `json:"address"`
	}
	out := make([]encoderJSON, len(encoders))
	for i, enc := range encoders {
		out[i] = encoderJSON{
			Index:   enc.Index,
			Label:   enc.Label,
			Address: fmt.Sprintf("0x%x", enc.Address),
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal json: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(data)); err != nil {
		return fmt.Errorf("write encoders json: %w", err)
	}
	return nil
}

func writeEncodersText(w io.Writer, encoders []*gputrace.ComputeEncoder, commandBufferCount int, commandBuffers []encodersCommandBufferSummary) error {
	if _, err := fmt.Fprintf(w, "%d encoders:\n", len(encoders)); err != nil {
		return fmt.Errorf("write encoders: %w", err)
	}
	for _, encoder := range encoders {
		var err error
		if encoder.Label != "" {
			_, err = fmt.Fprintf(w, "  %3d: %s\n", encoder.Index, encoder.Label)
		} else {
			_, err = fmt.Fprintf(w, "  %3d: (unlabeled) 0x%x\n", encoder.Index, encoder.Address)
		}
		if err != nil {
			return fmt.Errorf("write encoders: %w", err)
		}
	}

	if commandBufferCount > 0 {
		if _, err := fmt.Fprintf(w, "\n%d command buffers (%.1f encoders/buffer avg)\n",
			commandBufferCount, float64(len(encoders))/float64(commandBufferCount)); err != nil {
			return fmt.Errorf("write encoders: %w", err)
		}
		for _, cb := range commandBuffers {
			if _, err := fmt.Fprintf(w, "  CB %d: %d encoders\n", cb.index, cb.encoderCount); err != nil {
				return fmt.Errorf("write encoders: %w", err)
			}
		}
	}

	return nil
}
