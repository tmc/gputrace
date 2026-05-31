package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/export"
	"github.com/tmc/gputrace/internal/mlxprof"
	"github.com/tmc/gputrace/internal/timing"
)

var (
	output      string
	prefix      string
	all         bool
	verbose     bool
	textReport  bool
	showStats   bool
	searchPaths []string
	sourceLines bool
)

var pprofCmd = &cobra.Command{
	Use:   "pprof <trace.gputrace>",
	Short: "Convert .gputrace files to pprof format",
	Long: `Convert .gputrace files to pprof format with shader-level timing breakdowns.

This tool generates pprof profiles showing GPU shader timing breakdowns.
The resulting pprof files can be analyzed with standard Go profiling tools:

  go tool pprof output.pprof
  go tool pprof -http=:8080 output.pprof

This tool automatically recovers kernel names from Metal Library (MTLB) sidecar files
if explicit debug labels are missing from the command stream (common in MLX traces).

Example workflow:

  # 1. Capture GPU trace from benchmark
  MTL_CAPTURE_ENABLED=1 go test -bench=BenchmarkForwardPass$ -benchtime=1x

  # 2. Convert to pprof (automatically handles anonymous traces)
  gputrace pprof /tmp/forward_pass_*.gputrace --all --prefix gpu_analysis

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
	pprofCmd.Flags().StringSliceVar(&searchPaths, "search-path", nil, "Search paths for shader source files")
	pprofCmd.Flags().BoolVar(&sourceLines, "source-lines", false, "Generate pprof with per-source-line samples (enables go tool pprof -list)")
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
		fmt.Fprintf(pprofCurrentStatusWriter(), "Loading GPU trace: %s\n", tracePath)
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

	// If source-lines mode, generate per-line pprof
	if sourceLines {
		return generateSourceLinesPprof(tracePath, searchPaths)
	}

	// Create profiler
	// Note: We're using the mlxprof wrapper here which uses internal/mlxprof
	// We should update mlxprof.FromGPUTrace to extract counters too.
	// OR we can bypass mlxprof and use internal/export directly if we want explicit control,
	// but mlxprof wrapper provides nice conveniences.

	// Let's look at mlxprof.FromGPUTrace implementation again.
	// It calls gputrace.Open and then returns a GPUTraceProfiler.
	// We need to inject stats into it.

	// Actually, based on previous steps, I updated GPUTraceProfiler struct to have a `stats` field,
	// but I didn't update FromGPUTrace to populate it.
	// So I should update mlxprof.FromGPUTrace first to extract counters.

	prof, err := mlxprof.FromGPUTrace(tracePath, searchPaths...)
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
		status := pprofCurrentStatusWriter()
		prof.FprintSummary(status)
		fmt.Fprintln(status)
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

		fmt.Fprintf(pprofStatusWriter(outputPath), "✅ Text report written to: %s\n", outputPath)

	} else {
		// Generate single pprof file
		outputPath := output
		if outputPath == "" {
			outputPath = outputPrefix + ".pprof"
		}

		status := pprofStatusWriter(outputPath)
		if verbose {
			fmt.Fprintf(status, "Writing pprof to: %s\n", outputPath)
		}

		if err := prof.WriteGPUProfile(outputPath); err != nil {
			return fmt.Errorf("failed to write pprof: %w", err)
		}

		fmt.Fprintf(status, "✅ GPU profile written to: %s\n", outputPath)
		fmt.Fprintf(status, "\nView with: go tool pprof -top %s\n", outputPath)
		fmt.Fprintf(status, "Or:        go tool pprof -http=:8080 %s\n", outputPath)
	}

	return nil
}

func pprofCurrentStatusWriter() *os.File {
	if !all && pprofOutputPathIsStdout(output) {
		return os.Stderr
	}
	return os.Stdout
}

func pprofStatusWriter(outputPath string) *os.File {
	if pprofOutputPathIsStdout(outputPath) {
		return os.Stderr
	}
	return os.Stdout
}

func pprofOutputPathIsStdout(path string) bool {
	return path == "-" || path == "/dev/stdout"
}

// generateSourceLinesPprof generates a pprof profile with per-source-line samples.
// This enables 'go tool pprof -list kernel_name' to show line-by-line costs.
func generateSourceLinesPprof(tracePath string, searchPaths []string) error {
	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Create shader source mapper
	mapper := gputrace.NewShaderSourceMapper(searchPaths...)
	if err := mapper.IndexShaderSources(); err != nil {
		log.Printf("Warning: failed to index shader sources: %v", err)
	}
	if err := mapper.IndexTraceBundleSources(tracePath); err != nil {
		log.Printf("Warning: failed to index trace shader sources: %v", err)
	}

	// Determine output path
	baseName := filepath.Base(tracePath)
	if ext := filepath.Ext(baseName); ext != "" {
		baseName = baseName[:len(baseName)-len(ext)]
	}
	outputPath := output
	if outputPath == "" {
		outputPath = baseName + ".source.pprof"
	}
	status := pprofStatusWriter(outputPath)

	timingSelection := selectSourceLineTimings(trace)
	fmt.Fprint(status, formatSourceLineTimingNotice(timingSelection.source, len(timingSelection.timings)))
	timings := timingSelection.timings
	timings = appendSourceMappedEncoderTimings(trace, timings, mapper)

	// Generate pprof with source lines
	prof, err := export.ToPprofWithSourceLines(trace, timings, mapper)
	if err != nil {
		return fmt.Errorf("failed to generate source-lines pprof: %w", err)
	}

	// Write profile
	w := os.Stdout
	if !pprofOutputPathIsStdout(outputPath) {
		f, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer f.Close()
		w = f
	}

	if err := prof.Write(w); err != nil {
		return fmt.Errorf("failed to write pprof: %w", err)
	}

	fmt.Fprintf(status, "✅ Source-lines pprof written to: %s\n", outputPath)
	fmt.Fprintf(status, "\nView per-line costs with:\n")
	fmt.Fprintf(status, "  go tool pprof -list <kernel_name> %s\n", outputPath)
	fmt.Fprintf(status, "\nOr interactive mode:\n")
	fmt.Fprintf(status, "  go tool pprof %s\n", outputPath)
	fmt.Fprintf(status, "  (pprof) list <kernel_name>\n")

	return nil
}

type sourceLineTimingSource string

const (
	sourceLineTimingProfiler      sourceLineTimingSource = "profiler"
	sourceLineTimingEncoderLabels sourceLineTimingSource = "encoder_labels"
	sourceLineTimingSynthetic     sourceLineTimingSource = "synthetic"
)

type sourceLineTimingSelection struct {
	timings []*export.EncoderTiming
	source  sourceLineTimingSource
}

func selectSourceLineTimings(trace *gputrace.Trace) sourceLineTimingSelection {
	profilerTimings, _, profilerErr := gputrace.ExtractEncoderTimingsFromProfiler(trace)
	if profilerErr == nil && len(profilerTimings) > 0 {
		return sourceLineTimingSelection{
			timings: sourceLineProfilerTimings(profilerTimings),
			source:  sourceLineTimingProfiler,
		}
	}

	extracted, err := timing.ExtractTimingData(trace)
	if err == nil && len(extracted) > 0 {
		return sourceLineTimingSelection{
			timings: extracted,
			source:  sourceLineTimingEncoderLabels,
		}
	}

	return sourceLineTimingSelection{
		timings: timing.GenerateSyntheticTiming(trace),
		source:  sourceLineTimingSynthetic,
	}
}

func sourceLineProfilerTimings(profilerTimings []gputrace.EncoderTimingInfo) []*export.EncoderTiming {
	timings := make([]*export.EncoderTiming, 0, len(profilerTimings))
	var currentTimeNs uint64
	for _, pt := range profilerTimings {
		durationNs := uint64(pt.DurationMicros) * 1000 // Convert us to ns.
		label := pt.Label
		if label == "" {
			label = fmt.Sprintf("encoder_%d", pt.Index)
		}
		timings = append(timings, &export.EncoderTiming{
			Label:          label,
			DurationNs:     durationNs,
			StartTimestamp: currentTimeNs,
		})
		currentTimeNs += durationNs
	}
	return timings
}

func formatSourceLineTimingNotice(source sourceLineTimingSource, count int) string {
	switch source {
	case sourceLineTimingProfiler:
		return fmt.Sprintf("Timing source: profiler .gpuprofiler_raw data (%s)\n", formatTimingRows(count))
	case sourceLineTimingEncoderLabels:
		return fmt.Sprintf("Timing source: encoder label timing data (%s)\n", formatTimingRows(count))
	case sourceLineTimingSynthetic:
		return fmt.Sprintf("Timing source: synthetic fallback (%s; no real profiler or encoder label timing found)\n", formatTimingRows(count))
	default:
		return fmt.Sprintf("Timing source: unknown (%s)\n", formatTimingRows(count))
	}
}

func formatTimingRows(count int) string {
	if count == 1 {
		return "1 encoder"
	}
	return fmt.Sprintf("%d encoders", count)
}

func appendSourceMappedEncoderTimings(trace *gputrace.Trace, timings []*export.EncoderTiming, mapper *gputrace.ShaderSourceMapper) []*export.EncoderTiming {
	if trace == nil || mapper == nil {
		return timings
	}
	seen := make(map[string]bool)
	var maxEnd uint64
	for _, timing := range timings {
		seen[timing.Label] = true
		if timing.EndTimestamp > maxEnd {
			maxEnd = timing.EndTimestamp
		}
	}
	if maxEnd == 0 {
		maxEnd = 1000000000000000
	}
	encoders, err := trace.ParseComputeEncoders()
	if err != nil {
		return timings
	}
	for _, enc := range encoders {
		if enc.Label == "" || seen[enc.Label] {
			continue
		}
		if sourceFile, _ := mapper.GetSourceLocation(enc.Label); sourceFile == "" {
			continue
		}
		durationNs := uint64(1000000)
		timings = append(timings, &export.EncoderTiming{
			Label:          enc.Label,
			StartTimestamp: maxEnd,
			EndTimestamp:   maxEnd + durationNs,
			DurationNs:     durationNs,
			DurationMs:     float64(durationNs) / 1e6,
		})
		seen[enc.Label] = true
		maxEnd += durationNs + 10000
	}
	return timings
}
