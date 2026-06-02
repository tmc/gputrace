//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/replay"
)

var counterCapsJSON bool

var counterCapsCmd = &cobra.Command{
	Use:   "counter-caps",
	Short: "Print public Metal counter sampling capabilities",
	Long: `Query the default Metal device for public MTLCounterSampleBuffer support.

This reports the counter sampling points and counter sets exposed by Metal. It
does not use Xcode, Accessibility, screen recording, or synthetic data.`,
	Args: cobra.NoArgs,
	RunE: runCounterCaps,
}

func init() {
	rootCmd.AddCommand(counterCapsCmd)
	counterCapsCmd.Flags().BoolVar(&counterCapsJSON, "json", false, "Output in JSON format")
}

func runCounterCaps(cmd *cobra.Command, args []string) error {
	caps, err := replay.QueryDeviceCapabilities()
	if err != nil {
		return err
	}
	if counterCapsJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(caps)
	}
	fmt.Println(caps.String())
	return nil
}
