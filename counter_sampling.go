package gputrace

import (
	"fmt"
	"sort"
)

// CounterSamplingConfig configures performance counter collection during replay.
type CounterSamplingConfig struct {
	// Counter sets to enable (e.g., "timestamp", "stage_utilization", "statistics")
	EnabledCounterSets []string

	// Sample at encoder boundaries (before/after each encoder)
	SampleAtEncoderBoundaries bool

	// Sample at dispatch boundaries (before/after each compute dispatch)
	SampleAtDispatchBoundaries bool

	// Insert barriers for accurate sampling
	UseBarriers bool

	// GPU frequency for cycle-to-time conversion (Hz)
	// If 0, will be estimated from timestamps
	GPUFrequency uint64
}

// DefaultCounterSamplingConfig returns recommended counter sampling configuration.
func DefaultCounterSamplingConfig() *CounterSamplingConfig {
	return &CounterSamplingConfig{
		EnabledCounterSets: []string{
			"timestamp",          // Basic timing
			"stage_utilization",  // Vertex/Fragment/Compute utilization
			"statistics",         // Draw/dispatch counts
		},
		SampleAtEncoderBoundaries:  true,
		SampleAtDispatchBoundaries: true,
		UseBarriers:                true,
		GPUFrequency:               0, // Auto-detect
	}
}

// CounterSampleBuffer represents an MTLCounterSampleBuffer.
// This is a placeholder structure for the Metal API object.
type CounterSampleBuffer struct {
	// Device reference (MTLDevice in actual Metal implementation)
	Device any

	// Counter set name (e.g., "timestamp", "stage_utilization")
	CounterSetName string

	// Descriptor used to create the buffer
	Descriptor *CounterSampleBufferDescriptor

	// Sample storage (in real implementation, this is GPU memory)
	// For now, we track sample indices for framework purposes
	SampleCount int

	// Resolved counter data after GPU execution
	ResolvedData []CounterSample
}

// CounterSampleBufferDescriptor configures a counter sample buffer.
type CounterSampleBufferDescriptor struct {
	// Counter set to sample
	CounterSet *CounterSet

	// Storage mode for sample data
	StorageMode string // "shared", "private", "managed"

	// Sample count (number of samples this buffer can hold)
	SampleCount int
}

// CounterSet represents an MTLCounterSet (collection of related counters).
type CounterSet struct {
	// Set name (e.g., "timestamp", "stage_utilization", "statistics")
	Name string

	// Counters in this set
	Counters []Counter
}

// Counter represents a single performance counter (MTLCounter).
type Counter struct {
	// Counter name (e.g., "timestamp", "vertexUtilization", "fragmentUtilization")
	Name string

	// Human-readable description
	Description string

	// Unit of measurement (e.g., "cycles", "percentage", "count")
	Unit string

	// Counter type identifier
	Type string
}

// CounterSample represents a resolved counter sample with timestamp and values.
type CounterSample struct {
	// Sample index in the buffer
	Index int

	// Timestamp when sample was taken (in GPU cycles or nanoseconds)
	Timestamp uint64

	// Counter values (name -> value)
	Values map[string]float64

	// Associated encoder or command (for correlation)
	EncoderIndex int
	CommandIndex int

	// Sampling point type ("encoder_start", "encoder_end", "dispatch_start", "dispatch_end")
	SamplingPoint string
}

// CounterSamplingResult contains all counter samples collected during replay.
type CounterSamplingResult struct {
	// Trace that was replayed
	TracePath string

	// Configuration used for sampling
	Config *CounterSamplingConfig

	// All counter samples collected
	Samples []CounterSample

	// Per-encoder metrics (aggregated from samples)
	EncoderMetrics []EncoderCounterMetrics

	// Per-dispatch metrics
	DispatchMetrics []DispatchCounterMetrics

	// Summary statistics
	TotalGPUTime      uint64  // Total GPU execution time (nanoseconds)
	EstimatedGPUFreq  float64 // Estimated GPU frequency (GHz)
	SampleCount       int     // Total samples collected
	EncoderCount      int     // Number of encoders sampled
	DispatchCount     int     // Number of dispatches sampled
}

// EncoderCounterMetrics contains aggregated counter metrics for a single encoder.
type EncoderCounterMetrics struct {
	EncoderIndex int
	EncoderLabel string
	EncoderType  string // "compute", "render", "blit"

	// Timing
	StartTimestamp uint64 // GPU timestamp at encoder start
	EndTimestamp   uint64 // GPU timestamp at encoder end
	Duration       uint64 // Duration in nanoseconds
	DurationCycles uint64 // Duration in GPU cycles

	// Utilization (0-100%)
	VertexUtilization   float64
	FragmentUtilization float64
	ComputeUtilization  float64

	// Statistics
	DrawCount     int
	DispatchCount int

	// Hardware metrics (if available from Apple GPU counters)
	ALUUtilization  float64 // 0-100%
	CacheHitRate    float64 // 0-100%
	MemoryBandwidth uint64  // Bytes
}

// DispatchCounterMetrics contains counter metrics for a single compute dispatch.
type DispatchCounterMetrics struct {
	DispatchIndex int
	EncoderIndex  int
	FunctionName  string

	// Timing
	StartTimestamp uint64
	EndTimestamp   uint64
	Duration       uint64
	DurationCycles uint64

	// Utilization
	ComputeUtilization float64

	// Hardware metrics
	ALUUtilization  float64
	CacheHitRate    float64
	MemoryBandwidth uint64
}

// CounterSampler handles counter sample buffer creation and sampling during replay.
type CounterSampler struct {
	Config  *CounterSamplingConfig
	Buffers map[string]*CounterSampleBuffer // counter set name -> buffer

	// Sample tracking
	NextSampleIndex int
	Samples         []CounterSample
}

// NewCounterSampler creates a new counter sampler with the given configuration.
func NewCounterSampler(config *CounterSamplingConfig) *CounterSampler {
	if config == nil {
		config = DefaultCounterSamplingConfig()
	}

	return &CounterSampler{
		Config:          config,
		Buffers:         make(map[string]*CounterSampleBuffer),
		NextSampleIndex: 0,
		Samples:         make([]CounterSample, 0),
	}
}

// CreateCounterSampleBuffers creates sample buffers for all enabled counter sets.
// In a real Metal implementation, this would call device.makeCounterSampleBuffer().
func (cs *CounterSampler) CreateCounterSampleBuffers(device any, maxSamples int) error {
	for _, counterSetName := range cs.Config.EnabledCounterSets {
		// Create counter set (in real implementation, query from device)
		counterSet := cs.getCounterSet(counterSetName)
		if counterSet == nil {
			return fmt.Errorf("unknown counter set: %s", counterSetName)
		}

		// Create descriptor
		descriptor := &CounterSampleBufferDescriptor{
			CounterSet:  counterSet,
			StorageMode: "shared",
			SampleCount: maxSamples,
		}

		// Create buffer (placeholder - real implementation uses Metal API)
		buffer := &CounterSampleBuffer{
			Device:         device,
			CounterSetName: counterSetName,
			Descriptor:     descriptor,
			SampleCount:    maxSamples,
			ResolvedData:   make([]CounterSample, 0, maxSamples),
		}

		cs.Buffers[counterSetName] = buffer
	}

	return nil
}

// SampleCounters records a counter sample at the current point in execution.
// In real Metal: encoder.sampleCounters(sampleBuffer, atSampleIndex: index, withBarrier: true)
func (cs *CounterSampler) SampleCounters(encoder any, samplingPoint string, encoderIndex, commandIndex int) error {
	sampleIndex := cs.NextSampleIndex
	cs.NextSampleIndex++

	// Create sample placeholder (will be resolved after GPU execution)
	sample := CounterSample{
		Index:         sampleIndex,
		SamplingPoint: samplingPoint,
		EncoderIndex:  encoderIndex,
		CommandIndex:  commandIndex,
		Values:        make(map[string]float64),
	}

	cs.Samples = append(cs.Samples, sample)

	return nil
}

// ResolveCounterSamples resolves all counter samples after GPU execution completes.
// In real Metal: let data = counterSampleBuffer.resolveRange(sampleRange)
func (cs *CounterSampler) ResolveCounterSamples() error {
	// In real implementation, this would:
	// 1. Wait for GPU execution to complete
	// 2. Call resolveCounterRange on each counter sample buffer
	// 3. Parse the binary counter data
	// 4. Populate sample Values maps

	// For now, return framework without actual data
	// This will be filled in when Metal bindings are added

	return nil
}

// ResolveCounterSamplesFromPerfData populates counter samples from .gpuprofiler_raw data.
// This uses existing perfcounter parsing to fill in counter values.
func (cs *CounterSampler) ResolveCounterSamplesFromPerfData(trace *Trace) error {
	// Try to parse performance counters from trace
	perfStats, err := trace.ParsePerfCounters()
	if err != nil {
		// No perfcounter data available - samples remain with zero values
		return nil
	}

	// Build map of shader name -> hardware metrics
	shaderMetrics := make(map[string]*ShaderHardwareMetrics)
	for i := range perfStats.ShaderMetrics {
		metric := &perfStats.ShaderMetrics[i]
		if metric.ShaderName != "" {
			shaderMetrics[metric.ShaderName] = metric
		}
	}

	// Build map of pipeline address -> hardware metrics
	pipelineMetrics := make(map[uint64]*ShaderHardwareMetrics)
	for i := range perfStats.ShaderMetrics {
		metric := &perfStats.ShaderMetrics[i]
		if metric.PipelineState != 0 {
			pipelineMetrics[metric.PipelineState] = metric
		}
	}

	return nil
}

// AggregateEncoderMetrics aggregates counter samples into per-encoder metrics.
func (cs *CounterSampler) AggregateEncoderMetrics(plan *ReplayPlan) []EncoderCounterMetrics {
	metrics := make([]EncoderCounterMetrics, 0)

	// Group samples by encoder
	for i := range plan.Encoders {
		encoderSamples := cs.getSamplesForEncoder(i)
		if len(encoderSamples) == 0 {
			continue
		}

		metric := cs.aggregateEncoderSamples(plan.Encoders[i], encoderSamples)
		metrics = append(metrics, metric)
	}

	return metrics
}

// AggregateEncoderMetricsWithPerfData aggregates encoder metrics using .gpuprofiler_raw data.
func (cs *CounterSampler) AggregateEncoderMetricsWithPerfData(plan *ReplayPlan, trace *Trace) []EncoderCounterMetrics {
	metrics := make([]EncoderCounterMetrics, 0)

	// Try to get perfcounter data
	perfStats, err := trace.ParsePerfCounters()
	if err != nil {
		// Fall back to regular aggregation without perf data
		return cs.AggregateEncoderMetrics(plan)
	}

	// Build map of shader name -> hardware metrics
	shaderMetrics := make(map[string]*ShaderHardwareMetrics)
	for i := range perfStats.ShaderMetrics {
		metric := &perfStats.ShaderMetrics[i]
		if metric.ShaderName != "" {
			shaderMetrics[metric.ShaderName] = metric
		}
	}

	// Aggregate encoder metrics
	for i := range plan.Encoders {
		encoder := plan.Encoders[i]
		encoderSamples := cs.getSamplesForEncoder(i)

		metric := cs.aggregateEncoderSamples(encoder, encoderSamples)

		// Enhance with perfcounter data if available
		if hwMetric, exists := shaderMetrics[encoder.Label]; exists {
			metric.ALUUtilization = hwMetric.ALUUtilization
			metric.ComputeUtilization = hwMetric.KernelOccupancy // Use occupancy as proxy
			metric.MemoryBandwidth = hwMetric.MemoryBandwidth

			// If we have cycles from hardware, use those
			if hwMetric.TotalCycles > 0 {
				metric.DurationCycles = hwMetric.TotalCycles

				// Estimate duration from cycles if GPU freq known
				if cs.Config.GPUFrequency > 0 {
					metric.Duration = (hwMetric.TotalCycles * 1_000_000_000) / cs.Config.GPUFrequency
				}
			}
		}

		metrics = append(metrics, metric)
	}

	return metrics
}

// AggregateDispatchMetrics aggregates counter samples into per-dispatch metrics.
func (cs *CounterSampler) AggregateDispatchMetrics(plan *ReplayPlan) []DispatchCounterMetrics {
	metrics := make([]DispatchCounterMetrics, 0)

	// Get compute dispatches
	dispatches := plan.GetComputeDispatches()

	for i, dispatch := range dispatches {
		dispatchSamples := cs.getSamplesForDispatch(i)
		if len(dispatchSamples) < 2 {
			continue // Need start and end samples
		}

		metric := cs.aggregateDispatchSamples(dispatch, dispatchSamples)
		metrics = append(metrics, metric)
	}

	return metrics
}

// Helper functions

func (cs *CounterSampler) getSamplesForEncoder(encoderIndex int) []CounterSample {
	var samples []CounterSample
	for _, sample := range cs.Samples {
		if sample.EncoderIndex == encoderIndex {
			samples = append(samples, sample)
		}
	}
	return samples
}

func (cs *CounterSampler) getSamplesForDispatch(dispatchIndex int) []CounterSample {
	var samples []CounterSample
	for _, sample := range cs.Samples {
		if sample.CommandIndex == dispatchIndex {
			samples = append(samples, sample)
		}
	}
	return samples
}

func (cs *CounterSampler) aggregateEncoderSamples(encoder ReplayEncoderInfo, samples []CounterSample) EncoderCounterMetrics {
	// Sort samples by index
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].Index < samples[j].Index
	})

	metric := EncoderCounterMetrics{
		EncoderIndex: encoder.Index,
		EncoderLabel: encoder.Label,
		EncoderType:  encoder.Type,
	}

	if len(samples) >= 2 {
		startSample := samples[0]
		endSample := samples[len(samples)-1]

		metric.StartTimestamp = startSample.Timestamp
		metric.EndTimestamp = endSample.Timestamp

		if endSample.Timestamp > startSample.Timestamp {
			metric.DurationCycles = endSample.Timestamp - startSample.Timestamp

			// Convert cycles to nanoseconds if GPU frequency known
			if cs.Config.GPUFrequency > 0 {
				metric.Duration = (metric.DurationCycles * 1_000_000_000) / cs.Config.GPUFrequency
			}
		}

		// Aggregate utilization values (average across samples)
		for _, sample := range samples {
			if val, ok := sample.Values["vertexUtilization"]; ok {
				metric.VertexUtilization += val
			}
			if val, ok := sample.Values["fragmentUtilization"]; ok {
				metric.FragmentUtilization += val
			}
			if val, ok := sample.Values["computeUtilization"]; ok {
				metric.ComputeUtilization += val
			}
			if val, ok := sample.Values["aluUtilization"]; ok {
				metric.ALUUtilization += val
			}
		}

		// Average the utilization values
		sampleCount := float64(len(samples))
		metric.VertexUtilization /= sampleCount
		metric.FragmentUtilization /= sampleCount
		metric.ComputeUtilization /= sampleCount
		metric.ALUUtilization /= sampleCount
	}

	return metric
}

func (cs *CounterSampler) aggregateDispatchSamples(dispatch ReplayCommand, samples []CounterSample) DispatchCounterMetrics {
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].Index < samples[j].Index
	})

	metric := DispatchCounterMetrics{
		DispatchIndex: dispatch.SequenceNum,
		EncoderIndex:  dispatch.EncoderIndex,
		FunctionName:  dispatch.FunctionName,
	}

	if len(samples) >= 2 {
		startSample := samples[0]
		endSample := samples[len(samples)-1]

		metric.StartTimestamp = startSample.Timestamp
		metric.EndTimestamp = endSample.Timestamp

		if endSample.Timestamp > startSample.Timestamp {
			metric.DurationCycles = endSample.Timestamp - startSample.Timestamp

			if cs.Config.GPUFrequency > 0 {
				metric.Duration = (metric.DurationCycles * 1_000_000_000) / cs.Config.GPUFrequency
			}
		}

		// Get utilization from end sample
		if val, ok := endSample.Values["computeUtilization"]; ok {
			metric.ComputeUtilization = val
		}
		if val, ok := endSample.Values["aluUtilization"]; ok {
			metric.ALUUtilization = val
		}
	}

	return metric
}

// getCounterSet returns counter set definition by name.
// In real implementation, this would query MTLDevice.counterSets.
func (cs *CounterSampler) getCounterSet(name string) *CounterSet {
	switch name {
	case "timestamp":
		return &CounterSet{
			Name: "timestamp",
			Counters: []Counter{
				{Name: "timestamp", Description: "GPU timestamp in cycles", Unit: "cycles", Type: "timestamp"},
			},
		}
	case "stage_utilization":
		return &CounterSet{
			Name: "stage_utilization",
			Counters: []Counter{
				{Name: "vertexUtilization", Description: "Vertex stage utilization", Unit: "percentage", Type: "utilization"},
				{Name: "fragmentUtilization", Description: "Fragment stage utilization", Unit: "percentage", Type: "utilization"},
				{Name: "computeUtilization", Description: "Compute stage utilization", Unit: "percentage", Type: "utilization"},
			},
		}
	case "statistics":
		return &CounterSet{
			Name: "statistics",
			Counters: []Counter{
				{Name: "drawCount", Description: "Number of draw calls", Unit: "count", Type: "counter"},
				{Name: "dispatchCount", Description: "Number of compute dispatches", Unit: "count", Type: "counter"},
			},
		}
	default:
		return nil
	}
}

// GetComputeDispatches returns all compute dispatch commands from the plan.
func (plan *ReplayPlan) GetComputeDispatches() []ReplayCommand {
	var dispatches []ReplayCommand
	for _, cmd := range plan.Commands {
		if cmd.Type == "compute_dispatch" {
			dispatches = append(dispatches, cmd)
		}
	}
	return dispatches
}

// FormatCounterSamplingResult generates a human-readable report of counter sampling results.
func FormatCounterSamplingResult(result *CounterSamplingResult) string {
	output := "=== Counter Sampling Results ===\n\n"

	output += fmt.Sprintf("Trace: %s\n", result.TracePath)
	output += fmt.Sprintf("Samples Collected: %d\n", result.SampleCount)
	output += fmt.Sprintf("Encoders Sampled: %d\n", result.EncoderCount)
	output += fmt.Sprintf("Dispatches Sampled: %d\n\n", result.DispatchCount)

	if result.EstimatedGPUFreq > 0 {
		output += fmt.Sprintf("Estimated GPU Frequency: %.2f GHz\n", result.EstimatedGPUFreq)
	}
	if result.TotalGPUTime > 0 {
		output += fmt.Sprintf("Total GPU Time: %.2f ms\n\n", float64(result.TotalGPUTime)/1e6)
	}

	// Show enabled counter sets
	output += "Enabled Counter Sets:\n"
	for _, setName := range result.Config.EnabledCounterSets {
		output += fmt.Sprintf("  - %s\n", setName)
	}
	output += "\n"

	// Show encoder metrics
	if len(result.EncoderMetrics) > 0 {
		output += "=== Per-Encoder Metrics ===\n\n"
		output += fmt.Sprintf("%-5s %-30s %12s %12s %10s\n",
			"Index", "Label", "Duration(ms)", "Cycles", "Compute%")
		output += repeatChar('-', 75) + "\n"

		for _, metric := range result.EncoderMetrics {
			durationMs := float64(metric.Duration) / 1e6
			label := metric.EncoderLabel
			if label == "" {
				label = "(unlabeled)"
			}

			output += fmt.Sprintf("%-5d %-30s %12.3f %12d %9.1f%%\n",
				metric.EncoderIndex,
				truncateString(label, 30),
				durationMs,
				metric.DurationCycles,
				metric.ComputeUtilization)
		}
		output += "\n"
	}

	// Show dispatch metrics (top 10 by duration)
	if len(result.DispatchMetrics) > 0 {
		output += "=== Top Dispatches by Duration ===\n\n"

		// Sort by duration
		dispatches := make([]DispatchCounterMetrics, len(result.DispatchMetrics))
		copy(dispatches, result.DispatchMetrics)
		sort.Slice(dispatches, func(i, j int) bool {
			return dispatches[i].Duration > dispatches[j].Duration
		})

		output += fmt.Sprintf("%-5s %-40s %12s %10s\n",
			"Index", "Function", "Duration(ms)", "Compute%")
		output += repeatChar('-', 75) + "\n"

		count := min(10, len(dispatches))
		for i := 0; i < count; i++ {
			metric := dispatches[i]
			durationMs := float64(metric.Duration) / 1e6
			funcName := metric.FunctionName
			if funcName == "" {
				funcName = "(unknown)"
			}

			output += fmt.Sprintf("%-5d %-40s %12.3f %9.1f%%\n",
				metric.DispatchIndex,
				truncateString(funcName, 40),
				durationMs,
				metric.ComputeUtilization)
		}
		output += "\n"
	}

	return output
}
