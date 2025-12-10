package cmd

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/mlxprof"
)

var (
	output     string
	prefix     string
	all        bool
	verbose    bool
	textReport bool
	showStats  bool
)

var pprofCmd = &cobra.Command{
	Use:   "pprof <trace.gputrace>",
	Short: "Convert .gputrace files to pprof format",
	Long: `Convert .gputrace files to pprof format with shader-level timing breakdowns.

This tool generates pprof profiles showing GPU shader timing breakdowns.
The resulting pprof files can be analyzed with standard Go profiling tools:

  go tool pprof output.pprof
  go tool pprof -http=:8080 output.pprof

Example workflow:

  # 1. Capture GPU trace from benchmark
  MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$ -benchtime=1x

  # 2. Convert to pprof
  gputrace pprof /tmp/forward_pass_*.gputrace -all -prefix gpu_analysis

  # 3. Analyze with pprof
  go tool pprof -top gpu_analysis.gpu.pprof
  go tool pprof -http=:8080 gpu_analysis.gpu.pprof

The pprof profile shows GPU time organized hierarchically:

  GPU Trace
    └─ CommandQueue
        └─ Encoder
            └─ Kernel (shader)

This makes it easy to identify which shaders are consuming the most GPU time.`,
	Args: cobra.ExactArgs(1),
	RunE: runPprof,
}

func init() {
	rootCmd.AddCommand(pprofCmd)

	pprofCmd.Flags().StringVarP(&output, "output", "o", "", "Output pprof file path (default: trace_name.pprof)")
	pprofCmd.Flags().StringVar(&prefix, "prefix", "", "Output prefix for -all mode (default: trace name)")
	pprofCmd.Flags().BoolVar(&all, "all", false, "Generate all profile formats (gpu, combined, text)")
	pprofCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	pprofCmd.Flags().BoolVar(&textReport, "text", false, "Generate text report only")
	pprofCmd.Flags().BoolVar(&showStats, "stats", false, "Show trace statistics only")
}

func runPprof(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Verify it has .gputrace extension
	if filepath.Ext(tracePath) != ".gputrace" {
		log.Printf("Warning: trace path does not have .gputrace extension: %s", tracePath)
	}

	if verbose {
		fmt.Printf("Loading GPU trace: %s\n", tracePath)
	}

	// If stats-only mode, show profiler summary
	if showStats {
		prof, err := mlxprof.FromGPUTrace(tracePath)
		if err != nil {
			return fmt.Errorf("failed to load trace: %w", err)
		}
		defer prof.Close()

		prof.PrintSummary()
		return nil
	}

	// Create profiler
	prof, err := mlxprof.FromGPUTrace(tracePath)
	if err != nil {
		return fmt.Errorf("failed to load trace: %w\n\nPlease ensure this is a valid .gputrace directory bundle", err)
	}
	defer func() {
		if closeErr := prof.Close(); closeErr != nil {
			log.Printf("Warning: error closing profiler: %v", closeErr)
		}
	}()

	// Show summary if verbose
	if verbose {
		prof.PrintSummary()
		fmt.Println()
	}

	// Determine output paths
	baseName := filepath.Base(tracePath)
	if ext := filepath.Ext(baseName); ext != "" {
		baseName = baseName[:len(baseName)-len(ext)]
	}

	outputPrefix := prefix
	if outputPrefix == "" {
		outputPrefix = baseName
	}

	// Generate outputs
	if all {
		// Generate all formats
		if verbose {
			fmt.Printf("Generating all profile formats with prefix: %s\n", outputPrefix)
		}

		if err := prof.WriteAll(outputPrefix); err != nil {
			return fmt.Errorf("failed to write profiles: %w", err)
		}

		fmt.Printf("✅ Generated profiles:\n")
		fmt.Printf("   %s.gpu.pprof       - Hierarchical GPU profile\n", outputPrefix)
		fmt.Printf("   %s.gpu-flat.pprof  - Flat GPU profile\n", outputPrefix)
		fmt.Printf("   %s.combined.pprof  - Combined multi-view profile\n", outputPrefix)
		fmt.Printf("   %s.txt             - Human-readable report\n", outputPrefix)
		fmt.Printf("\nView with: go tool pprof -top %s.gpu.pprof\n", outputPrefix)
		fmt.Printf("Or:        go tool pprof -http=:8080 %s.gpu.pprof\n", outputPrefix)

	} else if textReport {
		// Generate text report only
		outputPath := output
		if outputPath == "" {
			outputPath = outputPrefix + ".txt"
		}

		if err := prof.WriteTextReport(outputPath); err != nil {
			return fmt.Errorf("failed to write text report: %w", err)
		}

		fmt.Printf("✅ Text report written to: %s\n", outputPath)

	} else {
		// Generate single pprof file
		outputPath := output
		if outputPath == "" {
			outputPath = outputPrefix + ".pprof"
		}

		if verbose {
			fmt.Printf("Writing pprof to: %s\n", outputPath)
		}

		if err := prof.WriteGPUProfile(outputPath); err != nil {
			return fmt.Errorf("failed to write pprof: %w", err)
		}

		fmt.Printf("✅ GPU profile written to: %s\n", outputPath)
		fmt.Printf("\nView with: go tool pprof -top %s\n", outputPath)
		fmt.Printf("Or:        go tool pprof -http=:8080 %s\n", outputPath)
	}

	return nil
}
