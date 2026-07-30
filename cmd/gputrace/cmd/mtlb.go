package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/metallib"
	"github.com/tmc/gputrace/internal/trace"
)

type mtlbOptions struct {
	all bool
}

var mtlbCmd = newMTLBCommand(new(mtlbOptions))

func newMTLBCommand(opts *mtlbOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mtlb <trace-path/mtlb-file>",
		Short: "Inspect and analyze Metal Library Binary (MTLB) files",
		Long: `Inspect and analyze Metal Library Binary (MTLB) files.

Can inspect:
1. A single .gputrace bundle (scans for embedded MTLB files)
2. A direct path to an MTLB file (sidecar)

Displays header info, function table, and extraction stats.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMTLB(cmd, args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.all, "all", opts.all, "Show every function in direct inspection output")
	return cmd
}

func init() {
	rootCmd.AddCommand(mtlbCmd)
}

func runMTLB(cmd *cobra.Command, args []string, opts *mtlbOptions) error {
	path := args[0]
	w := cmd.OutOrStdout()

	// Check if it's a trace bundle
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		// Open trace and traverse
		t, err := trace.Open(path)
		if err != nil {
			return err
		}

		fmt.Fprintf(w, "Trace: %s\n", path)
		fmt.Fprintf(w, "Found %d parsed MTLB libraries:\n\n", len(t.MTLBLibraries))

		for i, lib := range t.MTLBLibraries {
			fmt.Fprintf(w, "=== Library %d ===\n", i+1)
			if err := printMTLBDetails(w, lib.Header, lib.ListFunctions, opts.all); err != nil {
				return err
			}
			fmt.Fprintln(w)
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

	fmt.Fprintf(w, "File: %s\n", path)
	return printMTLBDetails(w, mtlbFile.Header, mtlbFile.ListFunctions, opts.all)
}

func printMTLBDetails(w io.Writer, header metallib.Header, listFunctions func() ([]string, error), all bool) error {
	fmt.Fprintln(w, "Header:")
	fmt.Fprintf(w, "  Version:        %d\n", header.Version)
	fmt.Fprintf(w, "  Total Size:     %d bytes\n", header.TotalSize)
	fmt.Fprintf(w, "  Function Table: 0x%x\n", header.FunctionTable)
	fmt.Fprintf(w, "  String Table:   0x%x\n", header.StringTable)

	funcs, err := listFunctions()
	if err != nil {
		return fmt.Errorf("list MTLB functions: %w", err)
	}

	fmt.Fprintf(w, "\nFunctions (%d found):\n", len(funcs))
	shown := limitedCount(len(funcs), defaultHumanLimit)
	if all {
		shown = len(funcs)
	}
	for i, f := range funcs[:shown] {
		fmt.Fprintf(w, "  %d. %s\n", i+1, f)
	}
	if shown < len(funcs) {
		fmt.Fprintf(w, "  ... %d more functions omitted (use --all)\n", len(funcs)-shown)
	}
	return nil
}
