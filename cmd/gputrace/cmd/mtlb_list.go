package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/fmtutil"
	"github.com/tmc/gputrace/internal/metallib"
)

var mtlbListCmd = &cobra.Command{
	Use:   "list <trace>",
	Short: "List all Metal Library files in a trace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tracePath := args[0]

		files, err := metallib.FindFiles(tracePath)
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
				if lib, err := metallib.Parse(data); err == nil {
					if funcs, err := lib.ListFunctions(); err == nil {
						funcCount = len(funcs)
						totalFuncs += funcCount
					}
				}
			}

			sizeStr := fmtutil.FormatBytes(f.Size, 1)
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
