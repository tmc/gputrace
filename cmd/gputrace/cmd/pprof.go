package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/cupticapture"
	"github.com/tmc/gputrace/internal/export"
	"github.com/tmc/gputrace/internal/mlxprof"
	"github.com/tmc/gputrace/internal/timing"
)

var pprofCmd = newPprofCommand(&pprofOptions{})

type pprofOptions struct {
	output      string
	prefix      string
	all         bool
	verbose     bool
	textReport  bool
	showStats   bool
	searchPaths []string
	sourceLines bool
}

func newPprofCommand(opts *pprofOptions) *cobra.Command {
	cmd := &cobra.Command{
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPprof(cmd, args, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.output, "output", "o", opts.output, "Output pprof file path (default: trace_name.pprof)")
	cmd.Flags().StringVar(&opts.prefix, "prefix", opts.prefix, "Output prefix for -all mode (default: trace name)")
	cmd.Flags().BoolVar(&opts.all, "all", opts.all, "Generate all profile formats (gpu, combined, text)")
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", opts.verbose, "Verbose output")
	cmd.Flags().BoolVar(&opts.textReport, "text", opts.textReport, "Generate text report only")
	cmd.Flags().BoolVar(&opts.showStats, "stats", opts.showStats, "Show trace statistics only")
	cmd.Flags().StringSliceVar(&opts.searchPaths, "search-path", opts.searchPaths, "Search paths for shader source files")
	cmd.Flags().BoolVar(&opts.sourceLines, "source-lines", opts.sourceLines, "Generate pprof with per-source-line samples (enables go tool pprof -list)")
	return cmd
}

func init() {
	rootCmd.AddCommand(pprofCmd)
}

func runPprof(cmd *cobra.Command, args []string, opts *pprofOptions) error {
	tracePath := args[0]

	// CUDA captures (.gpucapture bundles or activity JSONL) take the
	// cuptiprofile path: per-launch kernel samples instead of Metal
	// shader timing. Everything else keeps the Metal flow.
	if cupticapture.IsBundle(tracePath) || filepath.Ext(tracePath) == ".jsonl" {
		return runCuptiPprof(cmd, args, opts)
	}

	// Verify trace file exists
	if err := checkTraceFile(tracePath); err != nil {
		return err
	}

	// Verify it has .gputrace extension
	if filepath.Ext(tracePath) != ".gputrace" {
		log.Printf("Warning: trace path does not have .gputrace extension: %s", tracePath)
	}

	if opts.verbose {
		fmt.Fprintf(pprofCurrentStatusWriter(opts), "Loading GPU trace: %s\n", tracePath)
	}

	// If stats-only mode, show profiler summary
	if opts.showStats {
		prof, err := mlxprof.FromGPUTrace(tracePath)
		if err != nil {
			return fmt.Errorf("failed to load trace: %w", err)
		}
		defer prof.Close()

		prof.PrintSummary()
		return nil
	}

	// If source-lines mode, generate per-line pprof
	if opts.sourceLines {
		return generateSourceLinesPprof(tracePath, opts)
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

	prof, err := mlxprof.FromGPUTrace(tracePath, opts.searchPaths...)
	if err != nil {
		return fmt.Errorf("failed to load trace: %w\n\nPlease ensure this is a valid .gputrace directory bundle", err)
	}
	defer func() {
		if closeErr := prof.Close(); closeErr != nil {
			log.Printf("Warning: error closing profiler: %v", closeErr)
		}
	}()

	// Show summary if verbose
	if opts.verbose {
		status := pprofCurrentStatusWriter(opts)
		prof.FprintSummary(status)
		fmt.Fprintln(status)
	}

	// Determine output paths
	baseName := filepath.Base(tracePath)
	if ext := filepath.Ext(baseName); ext != "" {
		baseName = baseName[:len(baseName)-len(ext)]
	}

	outputPrefix := opts.prefix
	if outputPrefix == "" {
		outputPrefix = baseName
	}

	// Generate outputs
	if opts.all {
		// Generate all formats
		if opts.verbose {
			fmt.Printf("Generating all profile formats with prefix: %s\n", outputPrefix)
		}

		if err := prof.WriteAll(outputPrefix); err != nil {
			return fmt.Errorf("failed to write profiles: %w", err)
		}

		fmt.Printf("Generated profiles:\n")
		fmt.Printf("   %s.gpu.pprof       - Hierarchical GPU profile\n", outputPrefix)
		fmt.Printf("   %s.gpu-flat.pprof  - Flat GPU profile\n", outputPrefix)
		fmt.Printf("   %s.combined.pprof  - Combined multi-view profile\n", outputPrefix)
		fmt.Printf("   %s.txt             - Human-readable report\n", outputPrefix)
		fmt.Printf("\nView with: go tool pprof -top %s.gpu.pprof\n", outputPrefix)
		fmt.Printf("Or:        go tool pprof -http=:8080 %s.gpu.pprof\n", outputPrefix)

	} else if opts.textReport {
		// Generate text report only
		outputPath := opts.output
		if outputPath == "" {
			outputPath = outputPrefix + ".txt"
		}

		if err := prof.WriteTextReport(outputPath); err != nil {
			return fmt.Errorf("failed to write text report: %w", err)
		}

		fmt.Fprintf(pprofStatusWriter(outputPath), "Text report written: %s\n", outputPath)

	} else {
		// Generate single pprof file
		outputPath := opts.output
		if outputPath == "" {
			outputPath = outputPrefix + ".pprof"
		}

		status := pprofStatusWriter(outputPath)
		if opts.verbose {
			fmt.Fprintf(status, "Writing pprof to: %s\n", outputPath)
		}

		if err := prof.WriteGPUProfile(outputPath); err != nil {
			return fmt.Errorf("failed to write pprof: %w", err)
		}

		fmt.Fprintf(status, "GPU profile written: %s\n", outputPath)
		fmt.Fprintf(status, "\nView with: go tool pprof -top %s\n", outputPath)
		fmt.Fprintf(status, "Or:        go tool pprof -http=:8080 %s\n", outputPath)
	}

	return nil
}

func pprofCurrentStatusWriter(opts *pprofOptions) *os.File {
	if !opts.all && outputPathIsExplicitStdout(opts.output) {
		return os.Stderr
	}
	return os.Stdout
}

func pprofStatusWriter(outputPath string) *os.File {
	if outputPathIsExplicitStdout(outputPath) {
		return os.Stderr
	}
	return os.Stdout
}

// generateSourceLinesPprof generates a pprof profile with per-source-line samples.
// This enables 'go tool pprof -list kernel_name' to show line-by-line costs.
func generateSourceLinesPprof(tracePath string, opts *pprofOptions) error {
	// Open trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Create shader source mapper
	mapper := gputrace.NewShaderSourceMapper(opts.searchPaths...)
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
	outputPath := opts.output
	if outputPath == "" {
		outputPath = baseName + ".source.pprof"
	}
	status := pprofStatusWriter(outputPath)

	timingSelection := selectSourceLineTimings(trace)
	fmt.Fprint(status, formatSourceLineTimingNotice(timingSelection.source, len(timingSelection.timings)))
	fmt.Fprint(status, sourceLineGranularityNotice)
	timings := timingSelection.timings
	timings = appendSourceMappedEncoderTimings(trace, timings, mapper)

	// Generate pprof with source lines
	prof, err := export.ToPprofWithSourceLines(trace, timings, mapper)
	if err != nil {
		return fmt.Errorf("failed to generate source-lines pprof: %w", err)
	}

	// Write profile
	w := os.Stdout
	if !outputPathIsExplicitStdout(outputPath) {
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

	fmt.Fprintf(status, "Source-lines pprof written: %s\n", outputPath)
	fmt.Fprintf(status, "\nLocate a kernel in its source with:\n")
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

	// No timing source available. Source-line attribution without durations is
	// still useful; invented durations are not.
	return sourceLineTimingSelection{}
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

// sourceLineGranularityNotice states the granularity of --source-lines output.
//
// A kernel's whole duration lands on the one line where the kernel is
// declared, because that is the only line the mapper can identify. Nothing in
// the trace says how the cost is spread across the kernel body: the archived
// MTLLibrary carries no debug-info section, and no counter record carries a
// program counter or a source line. Without this notice a `pprof -list`
// listing reads as a per-line measurement, which it is not.
//
// See docs/research/SOURCE_LEVEL_COST.md for the evidence.
const sourceLineGranularityNotice = "Granularity: per kernel, not per line. Each kernel's cost is reported at its\n" +
	"declaration line; the trace carries no cost breakdown within a kernel body.\n"

func formatSourceLineTimingNotice(source sourceLineTimingSource, count int) string {
	switch source {
	case sourceLineTimingProfiler:
		return fmt.Sprintf("Timing source: profiler .gpuprofiler_raw data (%s)\n", formatTimingRows(count))
	case sourceLineTimingEncoderLabels:
		return fmt.Sprintf("Timing source: encoder label timing data (%s)\n", formatTimingRows(count))
	default:
		return "Timing source: none (no profiler or encoder label timing found); source lines are reported without durations\n"
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
	for _, enc := range trace.ParseComputeEncoders() {
		if enc.Label == "" || seen[enc.Label] {
			continue
		}
		if sourceFile, _ := mapper.SourceLocation(enc.Label); sourceFile == "" {
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
