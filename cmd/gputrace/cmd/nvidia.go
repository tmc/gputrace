package cmd

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/nvidia"
)

type nvidiaOptions struct {
	json bool
}

var nvidiaOpts = &nvidiaOptions{}

var nvidiaCmd = newNVIDIACommand(nvidiaOpts)

func newNVIDIACommand(opts *nvidiaOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nvidia",
		Short: "Report NVIDIA GPU status via NVML",
		Long: `Report NVIDIA GPU status via NVML.

Lists each visible NVIDIA GPU with its name, UUID, driver version, memory,
utilization, temperature, and power draw. Requires the NVIDIA driver's
libnvidia-ml shared library on Linux.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			devices, err := nvidia.Devices()
			if err != nil {
				if errors.Is(err, nvidia.ErrNVMLUnavailable) {
					return fmt.Errorf("nvidia: %w (is the NVIDIA driver installed?)", err)
				}
				return err
			}
			out := cmd.OutOrStdout()
			if opts.json {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(devices)
			}
			driver, _ := nvidia.DriverVersion()
			fmt.Fprintf(out, "NVIDIA driver: %s (%d device%s)\n\n", driver, len(devices), plural(len(devices)))
			for _, d := range devices {
				fmt.Fprintf(out, "GPU %d: %s\n", d.Index, d.Name)
				if d.UUID != "" {
					fmt.Fprintf(out, "  UUID:     %s\n", d.UUID)
				}
				fmt.Fprintf(out, "  Memory:   %s used / %s total\n", humanBytes(d.MemoryUsed), humanBytes(d.MemoryTotal))
				fmt.Fprintf(out, "  Util:     gpu %d%%, memory %d%%\n", d.GPUUtilPct, d.MemUtilPct)
				if d.TempC > 0 {
					fmt.Fprintf(out, "  Temp:     %d C\n", d.TempC)
				}
				if d.PowerWatts > 0 {
					fmt.Fprintf(out, "  Power:    %d mW\n", d.PowerWatts)
				}
				fmt.Fprintln(out)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output in JSON format")
	return cmd
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func init() {
	rootCmd.AddCommand(nvidiaCmd)
	rootCmd.AddCommand(cuptiCmd)
}
