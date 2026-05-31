package mlxprof

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/pprof/profile"
	"github.com/tmc/gputrace"
	"github.com/tmc/gputrace/internal/counter"
)

// GPUTraceProfiler provides comprehensive profiling from .gputrace files.
type GPUTraceProfiler struct {
	trace             *gputrace.Trace
	timings           []*gputrace.EncoderTiming
	timingSource      string
	timingApproximate bool
	basename          string
	sourceMapper      *gputrace.ShaderSourceMapper
	stats             *gputrace.PerfCounterStats
	streamStats       *counter.StreamDataStats
}

const (
	// TimingSourceProfiler indicates timings came from .gpuprofiler_raw streamData.
	TimingSourceProfiler = "profiler"
	// TimingSourceExtracted indicates timings came from capture timestamp extraction.
	TimingSourceExtracted = "extracted"
	// TimingSourceStore0 indicates timings came from store0 timing extraction.
	TimingSourceStore0 = "store0"
	// TimingSourceSynthetic indicates timings were estimated from kernel names.
	TimingSourceSynthetic = "synthetic"
)

type gpuTraceTimingSelection struct {
	timings     []*gputrace.EncoderTiming
	source      string
	approximate bool
}

// FromGPUTrace creates a comprehensive profiler from a .gputrace file.
// It generates multiple profile views:
//   - GPU profile: GPU timing data from Metal capture
//   - Combined profile: Unified timeline showing GPU work
//   - Memory profile: GPU memory usage (if available)
//
// Example:
//
//	prof, err := mlxprof.FromGPUTrace("trace.gputrace")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer prof.Close()
//
//	// Write individual profiles
//	prof.WriteGPUProfile("gpu.pprof")
//	prof.WriteCombinedProfile("combined.pprof")
//
//	// Or write all profiles with a common prefix
//	prof.WriteAll("output")
func FromGPUTrace(tracePath string, shaderSearchPaths ...string) (*GPUTraceProfiler, error) {
	// Open and parse the trace
	trace, err := gputrace.Open(tracePath)
	if err != nil {
		return nil, fmt.Errorf("open gputrace: %w", err)
	}

	streamStats, _ := counter.ExtractPipelineStatsFromTraceStreamData(trace)

	timingSelection, err := selectGPUTraceTimings(trace)
	if err != nil {
		return nil, err
	}

	// Initialize source mapper
	mapper := gputrace.NewShaderSourceMapper(shaderSearchPaths...)
	if err := mapper.IndexShaderSources(); err != nil {
		// Log error but continue - source mapping is optional
		fmt.Fprintf(os.Stderr, "Warning: failed to index shader sources: %v\n", err)
	}

	// Get basename for output files
	basename := filepath.Base(tracePath)
	if ext := filepath.Ext(basename); ext != "" {
		basename = basename[:len(basename)-len(ext)]
	}

	// Extract performance counters (optional)
	var stats *gputrace.PerfCounterStats
	if s, err := gputrace.ParsePerfCounters(trace); err == nil {
		stats = s
		fmt.Fprintf(os.Stderr, "Loaded performance counters with confidence %.2f\n", stats.ConfidenceLevel)
	} else {
		// Only log if verbose? Or just ignore silently as it's optional.
		// fmt.Printf("Note: No performance counters: %v\n", err)
	}

	return &GPUTraceProfiler{
		trace:             trace,
		timings:           timingSelection.timings,
		timingSource:      timingSelection.source,
		timingApproximate: timingSelection.approximate,
		basename:          basename,
		sourceMapper:      mapper,
		stats:             stats,
		streamStats:       streamStats,
	}, nil
}

func selectGPUTraceTimings(trace *gputrace.Trace) (gpuTraceTimingSelection, error) {
	// Strategy 1: Use profiler streamData when present. This is what the
	// Xcode Performance view uses for encoder spans.
	profilerTimings, totalTimeUs, profilerErr := counter.ExtractEncoderTimingsFromProfiler(trace)
	if profilerErr == nil && len(profilerTimings) > 0 {
		return gpuTraceTimingSelection{
			timings: encoderTimingsFromProfiler(profilerTimings, totalTimeUs),
			source:  TimingSourceProfiler,
		}, nil
	}

	// Strategy 2: Try standard timing extraction.
	timings, timingErr := gputrace.ExtractTimingData(trace)
	if len(timings) > 0 {
		return gpuTraceTimingSelection{
			timings: timings,
			source:  TimingSourceExtracted,
		}, nil
	}

	// Strategy 3: Try store0 timing extraction (for performance traces).
	store0Data, store0Err := gputrace.ExtractStore0Timing(trace)
	if store0Err == nil && len(store0Data.Encoders) > 0 {
		return gpuTraceTimingSelection{
			timings: gputrace.ConvertStore0ToEncoderTimings(trace, store0Data),
			source:  TimingSourceStore0,
		}, nil
	}

	// Strategy 4: Generate synthetic timing from kernel names. This provides
	// qualitative analysis even without real timing data.
	timings = gputrace.GenerateSyntheticTiming(trace)
	if len(timings) > 0 {
		return gpuTraceTimingSelection{
			timings:     timings,
			source:      TimingSourceSynthetic,
			approximate: true,
		}, nil
	}

	if timingErr == nil {
		timingErr = fmt.Errorf("standard timing extraction returned no timings")
	}
	return gpuTraceTimingSelection{}, fmt.Errorf("no timing data available (tried profiler, standard, store0, and synthetic): %w (profiler: %v, store0: %v)", timingErr, profilerErr, store0Err)
}

// TimingSource reports which timing strategy populated this profiler's encoder timings.
func (p *GPUTraceProfiler) TimingSource() string {
	if p == nil {
		return ""
	}
	return p.timingSource
}

// TimingsAreApproximate reports whether timings are estimated rather than measured.
func (p *GPUTraceProfiler) TimingsAreApproximate() bool {
	return p != nil && p.timingApproximate
}

func (p *GPUTraceProfiler) timingSourceDisplay() string {
	if p == nil || p.timingSource == "" {
		return ""
	}
	if p.timingApproximate {
		return p.timingSource + " (approximate)"
	}
	return p.timingSource
}

func encoderTimingsFromProfiler(in []counter.EncoderTimingInfo, totalTimeUs int) []*gputrace.EncoderTiming {
	out := make([]*gputrace.EncoderTiming, 0, len(in))
	var currentNs uint64
	totalNs := uint64(totalTimeUs) * 1000
	for _, pt := range in {
		label := pt.Label
		if label == "" {
			label = fmt.Sprintf("encoder_%d", pt.Index)
		}
		durationNs := uint64(pt.DurationMicros) * 1000
		percentage := float32(0)
		if totalNs > 0 {
			percentage = float32(float64(durationNs) / float64(totalNs) * 100)
		}
		out = append(out, &gputrace.EncoderTiming{
			Label:          label,
			StartTimestamp: currentNs,
			DurationNs:     durationNs,
			DurationMs:     float64(durationNs) / 1e6,
			Percentage:     percentage,
		})
		currentNs += durationNs
	}
	return out
}

// WriteGPUProfile writes a GPU-only pprof profile.
// This shows GPU kernel execution time organized hierarchically:
// GPU Trace > Command Queue > Encoder > Kernel
func (p *GPUTraceProfiler) WriteGPUProfile(path string) error {
	prof, err := gputrace.ToPprofWithMetrics(p.trace, p.sourceMapper, p.stats)
	if err != nil {
		return fmt.Errorf("generate pprof: %w", err)
	}

	return p.writeProfile(prof, path)
}

// WriteGPUProfileSimple writes a flatter GPU profile.
// Each encoder is a top-level sample (no deep hierarchy).
func (p *GPUTraceProfiler) WriteGPUProfileSimple(path string) error {
	prof, err := gputrace.ToPprof(p.trace, p.timings)
	if err != nil {
		return fmt.Errorf("generate simple pprof: %w", err)
	}
	p.addProfileTimingComments(prof)

	return p.writeProfile(prof, path)
}

// WriteCombinedProfile writes a combined profile with multiple sample types.
// This creates a single pprof file with:
//   - gpu_time: GPU execution time
//   - gpu_utilization: GPU utilization percentage
func (p *GPUTraceProfiler) WriteCombinedProfile(path string) error {
	prof, err := p.buildCombinedProfile()
	if err != nil {
		return fmt.Errorf("build combined profile: %w", err)
	}

	return p.writeProfile(prof, path)
}

// WriteTextReport writes a human-readable text report.
func (p *GPUTraceProfiler) WriteTextReport(path string) error {
	w, closeOutput, err := createOutput(path)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}

	fmt.Fprintf(w, "GPU Trace Profile Report\n")
	fmt.Fprintf(w, "========================\n\n")
	fmt.Fprintf(w, "Trace: %s\n", p.trace.Path)
	fmt.Fprintf(w, "Command Queue: %s\n", p.trace.CommandQueueLabel)
	fmt.Fprintf(w, "Encoders: %d\n", len(p.timings))
	fmt.Fprintf(w, "Kernel Names: %d\n\n", len(p.trace.KernelNames))

	p.writeTimingSummary(w)
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Encoder Breakdown:\n")
	fmt.Fprintf(w, "%-30s %12s %12s %8s\n", "Label", "Duration (ms)", "Duration (ns)", "Percent")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 80))
	for _, t := range p.timings {
		fmt.Fprintf(w, "%-30s %12.2f %12d %7.1f%%\n",
			t.Label, t.DurationMs, t.DurationNs, t.Percentage)
	}

	if len(p.trace.KernelNames) > 0 {
		fmt.Fprintf(w, "\nKernel Names:\n")
		for i, name := range p.trace.KernelNames {
			fmt.Fprintf(w, "  %d. %s\n", i+1, name)
		}
	}

	return nil
}

// WriteAll writes all available profiles with a common prefix.
// For example, WriteAll("profile") creates:
//   - profile.gpu.pprof     (hierarchical GPU profile)
//   - profile.gpu-flat.pprof (flat GPU profile)
//   - profile.combined.pprof (combined multi-view profile)
//   - profile.txt               (human-readable report)
func (p *GPUTraceProfiler) WriteAll(prefix string) error {
	files := []struct {
		suffix string
		fn     func(string) error
	}{
		{".gpu.pprof", p.WriteGPUProfile},
		{".gpu-flat.pprof", p.WriteGPUProfileSimple},
		{".combined.pprof", p.WriteCombinedProfile},
		{".txt", p.WriteTextReport},
	}

	for _, f := range files {
		path := prefix + f.suffix
		if err := f.fn(path); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	return nil
}

// PrintSummary prints a summary of the GPU trace to stdout.
func (p *GPUTraceProfiler) PrintSummary() {
	p.FprintSummary(os.Stdout)
}

// FprintSummary writes a summary of the GPU trace to w.
func (p *GPUTraceProfiler) FprintSummary(w io.Writer) {
	fmt.Fprintf(w, "GPU Trace Profile Summary\n")
	fmt.Fprintf(w, "=========================\n\n")
	fmt.Fprintf(w, "Trace: %s\n", p.trace.Path)
	fmt.Fprintf(w, "Command Queue: %s\n", p.trace.CommandQueueLabel)
	fmt.Fprintf(w, "Encoders: %d\n", len(p.timings))
	fmt.Fprintf(w, "Kernels: %d\n\n", len(p.trace.KernelNames))

	p.writeTimingSummary(w)
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Top Encoders:\n")
	for i, t := range p.timings {
		if i >= 10 {
			break
		}
		fmt.Fprintf(w, "  %2d. %-30s %8.2f ms (%5.1f%%)\n",
			i+1, t.Label, t.DurationMs, t.Percentage)
	}
}

func (p *GPUTraceProfiler) writeTimingSummary(w io.Writer) {
	var totalMs float64
	for _, t := range p.timings {
		totalMs += t.DurationMs
	}

	fmt.Fprintf(w, "Total GPU Time: %.2f ms\n", totalMs)
	if source := p.timingSourceDisplay(); source != "" {
		fmt.Fprintf(w, "Timing Source: %s\n", source)
	}
	if p.streamStats != nil {
		if p.streamStats.EffectiveGPUTimeNs != nil {
			fmt.Fprintf(w, "Effective GPU Time: %.2f ms\n", float64(*p.streamStats.EffectiveGPUTimeNs)/1e6)
		} else {
			fmt.Fprintln(w, "Effective GPU Time: (not present in streamData)")
		}
		if p.streamStats.CommandBufferActiveNs > 0 {
			fmt.Fprintf(w, "CB Active Time: %.2f ms\n", float64(p.streamStats.CommandBufferActiveNs)/1e6)
		}
		if p.streamStats.CommandBufferWallNs > 0 {
			fmt.Fprintf(w, "CB Wall Time: %.2f ms\n", float64(p.streamStats.CommandBufferWallNs)/1e6)
		}
		if p.streamStats.TimingSource != "" && p.timingSource == "" {
			fmt.Fprintf(w, "Timing Source: %s\n", p.streamStats.TimingSource)
		} else if p.streamStats.TimingSource != "" {
			fmt.Fprintf(w, "StreamData Timing Source: %s\n", p.streamStats.TimingSource)
		}
	}
}

// Close closes any resources held by the profiler.
func (p *GPUTraceProfiler) Close() error {
	// Currently no resources to close
	return nil
}

// buildCombinedProfile creates a profile with multiple sample types.
func (p *GPUTraceProfiler) buildCombinedProfile() (*profile.Profile, error) {
	prof := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "gpu_time", Unit: "nanoseconds"},
			{Type: "gpu_utilization", Unit: "percentage"},
		},
		DefaultSampleType: "gpu_time",
	}

	if len(p.timings) > 0 {
		prof.TimeNanos = int64(p.timings[0].StartTimestamp)

		// Calculate total duration
		var totalNs uint64
		for _, t := range p.timings {
			totalNs += t.DurationNs
		}
		prof.DurationNanos = int64(totalNs)
	}

	// Create functions and locations
	functionMap := make(map[string]*profile.Function)
	locationMap := make(map[string]*profile.Location)

	getFunction := func(name string) *profile.Function {
		if fn, ok := functionMap[name]; ok {
			return fn
		}
		fn := &profile.Function{
			ID:         uint64(len(functionMap) + 1),
			Name:       name,
			SystemName: name,
		}
		functionMap[name] = fn
		prof.Function = append(prof.Function, fn)
		return fn
	}

	getLocation := func(name string) *profile.Location {
		if loc, ok := locationMap[name]; ok {
			return loc
		}
		loc := &profile.Location{
			ID: uint64(len(locationMap) + 1),
			Line: []profile.Line{
				{Function: getFunction(name)},
			},
		}
		locationMap[name] = loc
		prof.Location = append(prof.Location, loc)
		return loc
	}

	// Build hierarchy
	processLoc := getLocation("GPU Trace")
	queueLabel := p.trace.CommandQueueLabel
	if queueLabel == "" {
		queueLabel = "CommandQueue"
	}
	queueLoc := getLocation(queueLabel)

	// Map encoders to kernels
	kernelMap := p.buildKernelMap()

	// Create samples
	for _, timing := range p.timings {
		encoderLoc := getLocation(timing.Label)

		kernelName := "unknown_kernel"
		if kn, ok := kernelMap[timing.Label]; ok {
			kernelName = kn
		}
		kernelLoc := getLocation(kernelName)

		// Stack trace (leaf to root)
		stack := []*profile.Location{
			kernelLoc,
			encoderLoc,
			queueLoc,
			processLoc,
		}

		// Calculate utilization (percentage as integer)
		utilization := int64(timing.Percentage * 100)

		sample := &profile.Sample{
			Location: stack,
			Value:    []int64{int64(timing.DurationNs), utilization},
			Label: map[string][]string{
				"encoder": {timing.Label},
			},
		}

		prof.Sample = append(prof.Sample, sample)
	}
	p.addProfileTimingComments(prof)

	return prof, nil
}

func (p *GPUTraceProfiler) addProfileTimingComments(prof *profile.Profile) {
	if prof == nil || p == nil {
		return
	}
	if p.timingSource != "" {
		prof.Comments = append(prof.Comments, "gputrace timing_source: "+p.timingSource)
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace timing_approximate: %t", p.timingApproximate))
	}
	if p.streamStats == nil {
		return
	}
	stats := p.streamStats
	if stats.TimingSource != "" {
		if p.timingSource == "" {
			prof.Comments = append(prof.Comments, "gputrace timing_source: "+stats.TimingSource)
		} else {
			prof.Comments = append(prof.Comments, "gputrace stream_timing_source: "+stats.TimingSource)
		}
	}
	if stats.EffectiveGPUTimeNs != nil {
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace effective_gpu_time_ns: %d", *stats.EffectiveGPUTimeNs))
	} else {
		prof.Comments = append(prof.Comments, "gputrace effective_gpu_time_ns: not present in streamData")
	}
	if stats.CommandBufferActiveNs > 0 {
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace command_buffer_active_time_ns: %d", stats.CommandBufferActiveNs))
	}
	if stats.CommandBufferWallNs > 0 {
		prof.Comments = append(prof.Comments, fmt.Sprintf("gputrace command_buffer_wall_time_ns: %d", stats.CommandBufferWallNs))
	}
}

// writeProfile writes a profile to disk with gzip compression.
func (p *GPUTraceProfiler) writeProfile(prof *profile.Profile, path string) error {
	w, closeOutput, err := createOutput(path)
	if err != nil {
		return err
	}
	if closeOutput != nil {
		defer closeOutput()
	}

	// Use gzip if path ends with .gz
	if filepath.Ext(path) == ".gz" {
		gzw := gzip.NewWriter(w)
		defer gzw.Close()
		w = gzw
	}

	return prof.Write(w)
}

func createOutput(path string) (io.Writer, func() error, error) {
	if path == "/dev/stdout" {
		return os.Stdout, nil, nil
	}

	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}

// buildKernelMap creates a mapping from encoder labels to kernel names.
func (p *GPUTraceProfiler) buildKernelMap() map[string]string {
	m := make(map[string]string)

	// Use the trace's kernel map logic
	for _, label := range p.trace.EncoderLabels {
		for _, kernel := range p.trace.KernelNames {
			if matchesEncoderLabel(label, kernel) {
				m[label] = kernel
				break
			}
		}
	}

	return m
}

// matchesEncoderLabel checks if a kernel name matches an encoder label.
func matchesEncoderLabel(label, kernel string) bool {
	labelLower := strings.ToLower(label)
	kernelLower := strings.ToLower(kernel)

	// Check for number match
	for i := '1'; i <= '9'; i++ {
		if strings.ContainsRune(labelLower, i) && strings.ContainsRune(kernelLower, i) {
			return true
		}
	}

	// Check for name component match
	keywords := []string{"normalize", "relu", "scale", "conv", "matmul", "softmax"}
	for _, keyword := range keywords {
		if strings.Contains(labelLower, keyword) && strings.Contains(kernelLower, keyword) {
			return true
		}
	}

	return false
}
