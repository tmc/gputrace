package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/mtlb"
)

var mtlbListCmd = &cobra.Command{
	Use:   "list [trace]",
	Short: "List all Metal Library files in a trace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tracePath := args[0]

		files, err := mtlb.FindMTLBFiles(tracePath)
		if err != nil {
			return err
		}

		fmt.Println("\n=== Metal Library Files ===")
		fmt.Println("")

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
		fmt.Fprintln(w, "File\tSize\tFunctions")
		fmt.Fprintln(w, "----\t----\t---------")

		totalFiles := len(files)
		totalFuncs := 0

		for _, f := range files {
			// Try to parse to get function count
			funcCount := 0
			data, err := os.ReadFile(f.Path)
			if err == nil {
				if lib, err := mtlb.ParseMTLB(data); err == nil {
					if funcs, err := lib.ListFunctions(); err == nil {
						funcCount = len(funcs)
						totalFuncs += funcCount
					}
				}
			}

			sizeStr := formatSize(f.Size)
			fmt.Fprintf(w, "%s\t%s\t%d\n", f.Name, sizeStr, funcCount)
		}

		w.Flush()
		fmt.Println("")
		fmt.Printf("Total: %d libraries, %d functions\n", totalFiles, totalFuncs)

		return nil
	},
}

func init() {
	mtlbCmd.AddCommand(mtlbListCmd)
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
