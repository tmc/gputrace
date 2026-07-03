package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/metallib"
	"github.com/tmc/gputrace/internal/trace"
)

var mtlbCmd = &cobra.Command{
	Use:   "mtlb <trace-path/mtlb-file>",
	Short: "Inspect and analyze Metal Library Binary (MTLB) files",
	Long: `Inspect and analyze Metal Library Binary (MTLB) files.

Can inspect:
1. A single .gputrace bundle (scans for embedded MTLB files)
2. A direct path to an MTLB file (sidecar)

Displays header info, function table, and extraction stats.`,
	Args: cobra.ExactArgs(1),
	RunE: runMtlb,
}

func init() {
	rootCmd.AddCommand(mtlbCmd)
}

func runMtlb(cmd *cobra.Command, args []string) error {
	path := args[0]

	// Check if it's a trace bundle
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		// Open trace and traverse
		t, err := trace.Open(path)
		if err != nil {
			return err
		}

		fmt.Printf("Trace: %s\n", path)
		fmt.Printf("Found %d MTLB Libraries associated with parsing:\n\n", len(t.MTLBLibraries))

		for i, lib := range t.MTLBLibraries {
			fmt.Printf("=== Library %d ===\n", i+1)
			printMTLBDetails(lib)
			fmt.Println()
		}
		return nil
	}

	// Direct file
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Verify magic "MTLB"
	if len(data) < 4 || string(data[0:4]) != "MTLB" {
		return fmt.Errorf("invalid MTLB file (magic bytes mismatch)")
	}

	mtlbFile, err := metallib.Parse(data)
	if err != nil {
		return err
	}

	fmt.Printf("File: %s\n", path)
	printMTLBDetails(mtlbFile)
	return nil
}

func printMTLBDetails(lib *metallib.File) {
	fmt.Printf("Header:\n")
	fmt.Printf("  Version:        %d\n", lib.Header.Version)
	fmt.Printf("  Total Size:     %d bytes\n", lib.Header.TotalSize)
	fmt.Printf("  Function Table: 0x%x\n", lib.Header.FunctionTable)
	fmt.Printf("  String Table:   0x%x\n", lib.Header.StringTable)

	funcs, err := lib.ListFunctions()
	if err != nil {
		fmt.Printf("Error listing functions: %v\n", err)
		return
	}

	fmt.Printf("\nFunctions (%d found):\n", len(funcs))
	for i, f := range funcs {
		fmt.Printf("  %d. %s\n", i, f)
	}
}
