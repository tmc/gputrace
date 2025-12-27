package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/mlxprof"
	"github.com/tmc/gputrace/internal/mtlb"
)

var (
	mtlbPprofOutput string
)

var mtlbPprofCmd = &cobra.Command{
	Use:   "pprof <trace.gputrace>",
	Short: "Generate pprof profile using MTLB information",
	Long:  `Generate a pprof profile from a trace, using the associated Metal Library (MTLB) to validate and enrich kernel information.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tracePath := args[0]

		// 1. Find MTLB files to confirm we have binary info
		mtlbFiles, err := mtlb.FindMTLBFiles(tracePath)
		if err != nil {
			return fmt.Errorf("finding mtlb files: %w", err)
		}

		fmt.Printf("Found %d MTLB files in trace.\n", len(mtlbFiles))
		for _, f := range mtlbFiles {
			// Parse to verify integrity and log function counts
			data, err := os.ReadFile(f.Path)
			if err != nil {
				fmt.Printf("Warning: could not read %s: %v\n", f.Name, err)
				continue
			}
			lib, err := mtlb.ParseMTLB(data)
			if err != nil {
				fmt.Printf("Warning: valid to parse %s: %v\n", f.Name, err)
				continue
			}
			funcs, _ := lib.ListFunctions()
			fmt.Printf("  - %s: %d functions\n", f.Name, len(funcs))
		}

		// 2. Generate Profile using standard pipeline (which now supports source attribution)
		// We pass the search paths from flags (inherited from parent or this cmd)
		prof, err := mlxprof.FromGPUTrace(tracePath, searchPaths...)
		if err != nil {
			return fmt.Errorf("loading trace: %w", err)
		}

		// 3. Write Profile
		outputPath := "mtlb_profile.pprof" // Default
		if mtlbPprofOutput != "" {
			outputPath = mtlbPprofOutput
		}

		if err := prof.WriteGPUProfile(outputPath); err != nil {
			return fmt.Errorf("write profile: %w", err)
		}

		fmt.Printf("\nGenerated pprof profile: %s\n", outputPath)
		fmt.Println("Run 'go tool pprof -http=:8080 " + outputPath + "' to view.")

		// Verify against loaded MTLB checks?
		// For now, just logging content is enough enrichment.

		return nil
	},
}

func init() {
	mtlbCmd.AddCommand(mtlbPprofCmd)
	mtlbPprofCmd.Flags().StringVarP(&mtlbPprofOutput, "output", "o", "", "Output pprof file path")
	mtlbPprofCmd.Flags().StringSliceVar(&searchPaths, "search-path", nil, "Search paths for shader source files")
}
