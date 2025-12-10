package cmd

import (
	"github.com/spf13/cobra"
)

var mtlbCmd = &cobra.Command{
	Use:   "mtlb",
	Short: "Inspect and analyze Metal Library Binary (MTLB) files",
	Long:  `Inspect, extract, and analyze Metal Library Binary (MTLB) files found in GPU traces.`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(mtlbCmd)
}
