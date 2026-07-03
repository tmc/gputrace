package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/fmtutil"
	"github.com/tmc/gputrace/internal/mtlb"
)

var mtlbInfoCmd = &cobra.Command{
	Use:   "info <trace>",
	Short: "Show MTLB header and metadata",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tracePath := args[0]

		files, err := mtlb.FindMTLBFiles(tracePath)
		if err != nil {
			return err
		}

		if len(files) == 0 {
			fmt.Println("No MTLB files found in trace.")
			return nil
		}

		for _, f := range files {
			fmt.Printf("\n=== Metal Library: %s ===\n", f.Name)

			data, err := os.ReadFile(f.Path)
			if err != nil {
				fmt.Printf("Error reading file: %v\n", err)
				continue
			}

			lib, err := mtlb.ParseMTLB(data)
			if err != nil {
				fmt.Printf("Error parsing MTLB: %v\n", err)
				continue
			}

			fmt.Printf("\nMagic:          %s\n", string(lib.Header.Magic[:]))
			fmt.Printf("Version:        %d\n", lib.Header.Version)
			fmt.Printf("Size:           %s\n", fmtutil.FormatBytes(int64(lib.Header.TotalSize), 1))
			// Assuming flags/reserved might have meaning later
			// fmt.Printf("Flags:          0x%x\n", lib.Header.Flags)

			funcs, _ := lib.ListFunctions()

			fmt.Println("\nSections:")
			fmt.Printf("  Functions:    %d\n", len(funcs))
			fmt.Printf("  Bytecode:     %s (offset 0x%x)\n", fmtutil.FormatBytes(int64(len(data))-int64(lib.Header.BytecodeOffset), 1), lib.Header.BytecodeOffset)

			// String table size estimation
			stringTableSize := int64(lib.Header.BytecodeOffset - lib.Header.StringTable)
			if stringTableSize > 0 {
				fmt.Printf("  Strings:      %s\n", fmtutil.FormatBytes(stringTableSize, 1))
			}
		}

		return nil
	},
}

func init() {
	mtlbCmd.AddCommand(mtlbInfoCmd)
}
