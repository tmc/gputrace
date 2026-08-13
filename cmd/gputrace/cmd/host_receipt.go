package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tmc/gputrace/internal/hostevents"
)

type hostReceiptOptions struct {
	output string
}

var hostReceiptCmd = newHostReceiptCommand(&hostReceiptOptions{})

func newHostReceiptCommand(opts *hostReceiptOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host-receipt <host-events.jsonl> <live-timing.jsonl>",
		Short: "Bind measured host intervals to live GPU timing",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHostReceipt(cmd, args, opts)
		},
	}
	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "Write canonical receipt to path instead of stdout")
	return cmd
}

func init() {
	rootCmd.AddCommand(hostReceiptCmd)
}

func runHostReceipt(cmd *cobra.Command, args []string, opts *hostReceiptOptions) error {
	receipt, err := hostevents.Receipt(args[0], args[1])
	if err != nil {
		return fmt.Errorf("build host receipt: %w", err)
	}
	data, err := receipt.Canonical()
	if err != nil {
		return fmt.Errorf("encode host receipt: %w", err)
	}
	if opts.output == "" {
		if _, err := cmd.OutOrStdout().Write(data); err != nil {
			return fmt.Errorf("write host receipt: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(opts.output, data, 0o600); err != nil {
		return fmt.Errorf("write host receipt: %w", err)
	}
	return nil
}
