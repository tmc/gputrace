package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/gpuevent"
	"github.com/tmc/gputrace/internal/nvidia"
)

var devicesOpts = struct{ json bool }{}

var devicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "List GPUs and capture backend capabilities on this host",
	Long: `List GPUs and capture backend capabilities on this host.

Probes every known capture backend (CUDA/NVIDIA, Metal/Apple) and reports
availability, device count, and whether kernel tracing and device counters
are usable. This is the entry point for deciding how to trace a workload
on the current machine.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		backends := gpuevent.Registry()
		out := cmd.OutOrStdout()
		if devicesOpts.json {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(backends)
		}
		for _, b := range backends {
			status := "unavailable"
			switch {
			case !b.Available:
				status = "unavailable"
			case b.Tracing && b.Counters:
				status = "capture + counters"
			case b.Tracing:
				status = "capture"
			case b.Counters:
				status = "counters only"
			default:
				status = "enumerable only"
			}
			line := fmt.Sprintf("%-8s %-8s %-16s", b.Name, b.Vendor, status)
			if b.Devices > 0 {
				line += fmt.Sprintf(" %d device(s)", b.Devices)
			}
			fmt.Fprintln(out, line)
			if b.Detail != "" {
				fmt.Fprintf(out, "         %s\n", b.Detail)
			}
		}
		// Device detail for available NVIDIA hardware.
		if devices, err := nvidia.Devices(); err == nil && len(devices) > 0 {
			fmt.Fprintln(out, "\nNVIDIA devices:")
			for _, d := range devices {
				fmt.Fprintf(out, "  GPU %d: %s (%.1f GiB)\n",
					d.Index, d.Name, float64(d.MemoryTotal)/(1<<30))
			}
		}
		return nil
	},
}

func init() {
	devicesCmd.Flags().BoolVar(&devicesOpts.json, "json", false, "Output in JSON format")
	rootCmd.AddCommand(devicesCmd)
}
