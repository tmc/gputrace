package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/buildinfo"
)

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type versionOptions struct {
	json bool
}

var versionCmd = newVersionCommand(&versionOptions{})

func newVersionCommand(opts *versionOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print gputrace build version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := versionInfo{
				Version: buildinfo.EffectiveVersion(),
				Commit:  buildinfo.Commit,
				Date:    buildinfo.Date,
			}
			if opts.json {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "gputrace %s (commit %s, built %s)\n", info.Version, info.Commit, info.Date)
			return err
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", opts.json, "Output in JSON format")
	return cmd
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
