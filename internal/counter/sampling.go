package counter

import (
	"errors"
	"fmt"
	"sort"

	"github.com/tmc/gputrace/internal/fmtutil"
	"github.com/tmc/gputrace/internal/trace"
)

// ErrMetalCounterSamplingUnavailable reports that replay-time Metal counter
// sampling is not wired to real Metal bindings.
var ErrMetalCounterSamplingUnavailable = errors.New("metal counter sampling is unavailable without replay-time Metal bindings")

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
			"timestamp",         // Basic timing
			"stage_utilization", // Vertex/Fragment/Compute utilization
			"statistics",        // Draw/dispatch counts
		},
		SampleAtEncoderBoundaries:  true,
		SampleAtDispatchBoundaries: true,
		UseBarriers:                true,
		GPUFrequency:               0, // Auto-detect
	}
}

// CounterSampleBuffer describes a configured counter sample buffer. A backend
// may associate it with a platform object through CounterSampler.BackendBuffers.
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

// CounterSampleBackend supplies platform-specific operations for replay-time
// counter collection. Implementations preserve resolved bytes without
// interpreting an unverified hardware layout.
type CounterSampleBackend interface {
	CreateSampleBuffer(counterSet string, sampleCount int) (any, error)
	SampleCounters(encoder, sampleBuffer any, sampleIndex int) error
	ResolveCounterSamples(commandBuffer, sampleBuffer any, startIndex, count int) ([]byte, error)
}

type counterSampleBackendReleaser interface {
	ReleaseSampleBuffer(buffer any)
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
	TotalGPUTime     uint64            // Total GPU execution time (nanoseconds)
	EstimatedGPUFreq float64           // Estimated GPU frequency (GHz)
	SampleCount      int               // Total samples collected
	EncoderCount     int               // Number of encoders sampled
	DispatchCount    int               // Number of dispatches sampled
	RawData          map[string][]byte // Resolved bytes by counter set, undecoded
}

// CounterAttribution describes how counter metrics were joined to an encoder.
type CounterAttribution string

const (
	// CounterAttributionUnknown means no capture-backed encoder join exists.
	CounterAttributionUnknown CounterAttribution = "unknown"
	// CounterAttributionEncoder means EncoderIndex names the measured encoder.
	CounterAttributionEncoder CounterAttribution = "encoder"
)

// EncoderCounterMetrics contains counter metrics with their attribution.
// EncoderIndex is -1 unless Attribution is CounterAttributionEncoder.
type EncoderCounterMetrics struct {
	EncoderIndex int
	EncoderLabel string
	EncoderType  string // "compute", "render", "blit"
	Attribution  CounterAttribution

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
	MemoryBandwidth uint64  // Bytes (total)

	// Detailed memory bandwidth metrics (from gputrace-65)
	BytesReadFromDeviceMemory      uint64  // Device memory read bytes
	BytesWrittenToDeviceMemory     uint64  // Device memory write bytes
	BufferDeviceMemoryBytesRead    uint64  // Buffer-specific read bytes
	BufferDeviceMemoryBytesWritten uint64  // Buffer-specific write bytes
	DeviceMemoryBandwidthGBps      float64 // Device memory bandwidth (GB/s)
	GPUReadBandwidthGBps           float64 // GPU read bandwidth (GB/s)
	GPUWriteBandwidthGBps          float64 // GPU write bandwidth (GB/s)

	// Shader Launch Limiters (from gputrace-67)
	ComputeShaderLaunchLimiter  float64 // Compute shader launch limiter percentage
	FragmentShaderLaunchLimiter float64 // Fragment shader launch limiter percentage
	VertexShaderLaunchLimiter   float64 // Vertex shader launch limiter percentage

	// Pipeline Limiters (from gputrace-67)
	ControlFlowLimiter           float64 // Control flow limiter percentage
	InstructionThroughputLimiter float64 // Instruction throughput limiter percentage
	IntegerAndComplexLimiter     float64 // Integer and complex instruction limiter percentage
	IntegerAndConditionalLimiter float64 // Integer and conditional instruction limiter percentage
	F16Limiter                   float64 // FP16 instruction limiter percentage
	F32Limiter                   float64 // FP32 instruction limiter percentage

	// Memory Limiters (from gputrace-67)
	L1CacheLimiter        float64 // L1 cache limiter percentage
	LastLevelCacheLimiter float64 // Last level cache limiter percentage
	MMULimiter            float64 // MMU limiter percentage

	// Texture Limiters (from gputrace-67)
	TextureFilteringLimiter float64 // Texture filtering limiter percentage
	TextureWriteLimiter     float64 // Texture write limiter percentage
	TextureReadLimiter      float64 // Texture read limiter percentage

	// Buffer L1 Cache Metrics (gputrace-66)
	BufferL1MissRate       float64 // Buffer L1 cache miss rate percentage (0-100)
	BufferL1ReadAccesses   float64 // Buffer L1 read accesses, percent of total L1 reads
	BufferL1ReadBandwidth  float64 // Buffer L1 read bandwidth (GB/s)
	BufferL1WriteAccesses  float64 // Buffer L1 write accesses, percent of total L1 writes
	BufferL1WriteBandwidth float64 // Buffer L1 write bandwidth (GB/s)

	// Shader Utilization Metrics (gputrace-67)
	ComputeShaderUtilization  float64 // Compute shader utilization percentage (0-100)
	FragmentShaderUtilization float64 // Fragment shader utilization percentage (0-100)
	VertexShaderUtilization   float64 // Vertex shader utilization percentage (0-100)
	ControlFlowUtilization    float64 // Control flow utilization percentage (0-100)
	InstructionThroughputUtil float64 // Instruction throughput utilization percentage (0-100)
	IntegerAndComplexUtil     float64 // Integer and complex instruction utilization percentage (0-100)
	IntegerAndConditionalUtil float64 // Integer and conditional instruction utilization percentage (0-100)
	F16Utilization            float64 // FP16 instruction utilization percentage (0-100)
	F32Utilization            float64 // FP32 instruction utilization percentage (0-100)
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

// CounterSampler handles counter sample buffer creation and sampling during
// replay. Without a backend it remains useful for analysis-only builds and
// fails closed before claiming hardware data.
type CounterSampler struct {
	Config         *CounterSamplingConfig
	Buffers        map[string]*CounterSampleBuffer // counter set name -> buffer
	Backend        CounterSampleBackend
	BackendBuffers map[string]any
	RawData        map[string][]byte // Resolved bytes by counter set, undecoded

	// Sample tracking
	NextSampleIndex int
	Samples         []CounterSample
}

// NewCounterSampler creates a new counter sampler with the given configuration.
func NewCounterSampler(config *CounterSamplingConfig) *CounterSampler {
	return NewCounterSamplerWithBackend(config, nil)
}

// NewCounterSamplerWithBackend creates a sampler backed by platform-specific
// counter operations. A nil backend retains fail-closed analysis behavior.
func NewCounterSamplerWithBackend(config *CounterSamplingConfig, backend CounterSampleBackend) *CounterSampler {
	if config == nil {
		config = DefaultCounterSamplingConfig()
	}

	return &CounterSampler{
		Config:          config,
		Buffers:         make(map[string]*CounterSampleBuffer),
		Backend:         backend,
		BackendBuffers:  make(map[string]any),
		RawData:         make(map[string][]byte),
		NextSampleIndex: 0,
		Samples:         make([]CounterSample, 0),
	}
}

// CreateCounterSampleBuffers validates and allocates the enabled counter sets.
// With no backend it retains the analysis-only fail-closed behavior.
func (cs *CounterSampler) CreateCounterSampleBuffers(device any, maxSamples int) error {
	if cs.Backend != nil {
		if maxSamples <= 0 {
			return errors.New("counter sample count must be positive")
		}
		for _, counterSetName := range cs.Config.EnabledCounterSets {
			buffer, err := cs.Backend.CreateSampleBuffer(counterSetName, maxSamples)
			if err != nil {
				cs.Close()
				return fmt.Errorf("create counter sample buffer %q: %w", counterSetName, err)
			}
			if buffer == nil {
				cs.Close()
				return fmt.Errorf("create counter sample buffer %q: backend returned nil", counterSetName)
			}
			cs.Buffers[counterSetName] = &CounterSampleBuffer{
				Device:         device,
				CounterSetName: counterSetName,
				SampleCount:    maxSamples,
			}
			cs.BackendBuffers[counterSetName] = buffer
		}
		return nil
	}
	for _, counterSetName := range cs.Config.EnabledCounterSets {
		counterSet := cs.getCounterSet(counterSetName)
		if counterSet == nil {
			return fmt.Errorf("unknown counter set: %s", counterSetName)
		}
	}

	return ErrMetalCounterSamplingUnavailable
}

// SampleCounters records a counter sample at the current point in execution.
// With a backend it also inserts the platform-specific sample operation.
func (cs *CounterSampler) SampleCounters(encoder any, samplingPoint string, encoderIndex, commandIndex int) error {
	if cs.Backend != nil {
		for name, buffer := range cs.BackendBuffers {
			if err := cs.Backend.SampleCounters(encoder, buffer, cs.NextSampleIndex); err != nil {
				return fmt.Errorf("sample counter set %q: %w", name, err)
			}
		}
		cs.Samples = append(cs.Samples, CounterSample{
			Index:         cs.NextSampleIndex,
			Values:        make(map[string]float64),
			EncoderIndex:  encoderIndex,
			CommandIndex:  commandIndex,
			SamplingPoint: samplingPoint,
		})
		cs.NextSampleIndex++
		return nil
	}
	return ErrMetalCounterSamplingUnavailable
}

// ResolveCounterSamples resolves all counter samples after GPU execution completes.
//
// IMPORTANT: Two Separate Approaches for Performance Counters
//
// 1. THIS APPROACH (Replay with MTLCounterSampleBuffer):
//   - Re-execute the GPU workload from the trace
//   - Insert counter sampling during replay: encoder.sampleCounters(buffer, index, barrier: true)
//   - Collect FRESH counter data: buffer.resolveCounterRange(sampleRange)
//   - Uses public Metal API (stable, documented)
//   - Requires Metal bindings (CGo/Swift) to actually execute
//   - Command: gputrace replay-counters
//
// 2. ALTERNATIVE APPROACH (Parse .gpuprofiler_raw):
//   - Read HISTORICAL counter data from Xcode Instruments captures
//   - Parse binary .gpuprofiler_raw files that already exist
//   - No GPU execution required
//   - Problem: Binary format undocumented, reverse engineering needed
//   - Command: gputrace perfcounters
//
// These are ALTERNATIVES, not meant to be combined. Choose based on your needs:
// - Need fresh data from re-running workload? Use THIS approach (replay-counters)
// - Have existing Instruments data? Use perfcounters approach
//
// In real Metal implementation, this function would:
// 1. Wait for GPU command buffer to complete
// 2. Call counterSampleBuffer.resolveCounterRange(range) for each buffer
// 3. Parse the binary counter data from resolved buffers
// 4. Populate sample.Values maps with actual counter readings
//
// Example Metal pseudocode:
//
//	commandBuffer.waitUntilCompleted()
//	let data = timestampBuffer.resolveCounterRange(0..<sampleCount)
//	// Parse data bytes to extract timestamp values
//	for i in 0..<sampleCount {
//	    samples[i].Values["timestamp"] = parseUInt64(data, offset: i*8)
//	}
func (cs *CounterSampler) ResolveCounterSamples() error {
	if cs.Backend != nil {
		return errors.New("counter: resolve requires a command buffer")
	}
	return ErrMetalCounterSamplingUnavailable
}

// ResolveCounterSamplesWithCommandBuffer resolves all backend buffers after
// commandBuffer has completed. The bytes are retained exactly as returned by
// Metal; decoding is a separate hardware-specific concern.
func (cs *CounterSampler) ResolveCounterSamplesWithCommandBuffer(commandBuffer any) error {
	return cs.ResolveCounterSamplesWithCommandBufferRange(commandBuffer, 0, len(cs.Samples))
}

// ResolveCounterSamplesWithCommandBufferRange resolves a portion of the
// sample buffer and appends its exact bytes to RawData.
func (cs *CounterSampler) ResolveCounterSamplesWithCommandBufferRange(commandBuffer any, startIndex, count int) error {
	if cs.Backend == nil {
		return ErrMetalCounterSamplingUnavailable
	}
	if startIndex < 0 || count < 0 || startIndex+count > len(cs.Samples) {
		return fmt.Errorf("counter: sample range %d:%d is outside %d samples", startIndex, startIndex+count, len(cs.Samples))
	}
	if count == 0 {
		return nil
	}
	for name, buffer := range cs.BackendBuffers {
		data, err := cs.Backend.ResolveCounterSamples(commandBuffer, buffer, startIndex, count)
		if err != nil {
			return fmt.Errorf("resolve counter set %q: %w", name, err)
		}
		cs.RawData[name] = append(cs.RawData[name], data...)
	}
	return nil
}

// Close releases backend-owned sample buffers when the backend supports it.
func (cs *CounterSampler) Close() {
	if cs == nil || cs.Backend == nil {
		return
	}
	if releaser, ok := cs.Backend.(counterSampleBackendReleaser); ok {
		for _, buffer := range cs.BackendBuffers {
			releaser.ReleaseSampleBuffer(buffer)
		}
	}
	cs.BackendBuffers = nil
	cs.Buffers = nil
}

// BackendBuffer returns the backend-owned buffer for counterSet. It is used by
// platform replay code that needs to attach a sample buffer to an encoder.
func (cs *CounterSampler) BackendBuffer(counterSet string) any {
	if cs == nil {
		return nil
	}
	return cs.BackendBuffers[counterSet]
}

// AggregateEncoderMetrics aggregates counter samples into per-encoder metrics.
//
// NOTE: This aggregates data from MTLCounterSampleBuffer samples collected during
// replay. It does NOT use .gpuprofiler_raw data - that's a separate approach
// (see perfcounters.go). The replay approach collects FRESH counter data by
// re-executing the GPU workload with MTLCounterSampleBuffer.
// func (cs *CounterSampler) AggregateEncoderMetrics(plan *ReplayPlan) []EncoderCounterMetrics {
// 	metrics := make([]EncoderCounterMetrics, 0)
//
// 	// Group samples by encoder
// 	for i := range plan.Encoders {
// 		encoderSamples := cs.getSamplesForEncoder(i)
// 		if len(encoderSamples) == 0 {
// 			continue
// 		}
//
// 		metric := cs.aggregateEncoderSamples(plan.Encoders[i], encoderSamples)
// 		metrics = append(metrics, metric)
// 	}
//
// 	return metrics
// }

// AggregateDispatchMetrics aggregates counter samples into per-dispatch metrics.
// func (cs *CounterSampler) AggregateDispatchMetrics(plan *ReplayPlan) []DispatchCounterMetrics {
// 	metrics := make([]DispatchCounterMetrics, 0)
//
// 	// Get compute dispatches
// 	dispatches := plan.ComputeDispatchCommands()
//
// 	for i, dispatch := range dispatches {
// 		dispatchSamples := cs.getSamplesForDispatch(i)
// 		if len(dispatchSamples) < 2 {
// 			continue // Need start and end samples
// 		}
//
// 		metric := cs.aggregateDispatchSamples(dispatch, dispatchSamples)
// 		metrics = append(metrics, metric)
// 	}
//
// 	return metrics
// }

// Helper functions

// func (cs *CounterSampler) aggregateEncoderSamples(encoder ReplayEncoderInfo, samples []CounterSample) EncoderCounterMetrics {
// 	// Sort samples by index
// 	sort.Slice(samples, func(i, j int) bool {
// 		return samples[i].Index < samples[j].Index
// 	})
//
// 	metric := EncoderCounterMetrics{
// 		EncoderIndex: encoder.Index,
// 		EncoderLabel: encoder.Label,
// 		EncoderType:  encoder.Type,
// 	}
//
// 	if len(samples) >= 2 {
// 		startSample := samples[0]
// 		endSample := samples[len(samples)-1]
//
// 		metric.StartTimestamp = startSample.Timestamp
// 		metric.EndTimestamp = endSample.Timestamp
//
// 		if endSample.Timestamp > startSample.Timestamp {
// 			metric.DurationCycles = endSample.Timestamp - startSample.Timestamp
//
// 			// Convert cycles to nanoseconds if GPU frequency known
// 			if cs.Config.GPUFrequency > 0 {
// 				metric.Duration = (metric.DurationCycles * 1_000_000_000) / cs.Config.GPUFrequency
// 			}
// 		}
//
// 		// Aggregate utilization values (average across samples)
// 		for _, sample := range samples {
// 			if val, ok := sample.Values["vertexUtilization"]; ok {
// 				metric.VertexUtilization += val
// 			}
// 			if val, ok := sample.Values["fragmentUtilization"]; ok {
// 				metric.FragmentUtilization += val
// 			}
// 			if val, ok := sample.Values["computeUtilization"]; ok {
// 				metric.ComputeUtilization += val
// 			}
// 			if val, ok := sample.Values["aluUtilization"]; ok {
// 				metric.ALUUtilization += val
// 			}
// 		}
//
// 		// Average the utilization values
// 		sampleCount := float64(len(samples))
// 		metric.VertexUtilization /= sampleCount
// 		metric.FragmentUtilization /= sampleCount
// 		metric.ComputeUtilization /= sampleCount
// 		metric.ALUUtilization /= sampleCount
// 	}
//
// 	return metric
// }

// func (cs *CounterSampler) aggregateDispatchSamples(dispatch ReplayCommand, samples []CounterSample) DispatchCounterMetrics {
// 	sort.Slice(samples, func(i, j int) bool {
// 		return samples[i].Index < samples[j].Index
// 	})
//
// 	metric := DispatchCounterMetrics{
// 		DispatchIndex: dispatch.SequenceNum,
// 		EncoderIndex:  dispatch.EncoderIndex,
// 		FunctionName:  dispatch.FunctionName,
// 	}
//
// 	if len(samples) >= 2 {
// 		startSample := samples[0]
// 		endSample := samples[len(samples)-1]
//
// 		metric.StartTimestamp = startSample.Timestamp
// 		metric.EndTimestamp = endSample.Timestamp
//
// 		if endSample.Timestamp > startSample.Timestamp {
// 			metric.DurationCycles = endSample.Timestamp - startSample.Timestamp
//
// 			if cs.Config.GPUFrequency > 0 {
// 				metric.Duration = (metric.DurationCycles * 1_000_000_000) / cs.Config.GPUFrequency
// 			}
// 		}
//
// 		// Get utilization from end sample
// 		if val, ok := endSample.Values["computeUtilization"]; ok {
// 			metric.ComputeUtilization = val
// 		}
// 		if val, ok := endSample.Values["aluUtilization"]; ok {
// 			metric.ALUUtilization = val
// 		}
// 	}
//
// 	return metric
// }

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

// PopulateEncoderMetricsFromBinaryParsing parses Counters_f_*.raw and returns
// one EncoderCounterMetrics per pipeline, not per encoder, despite the name and
// the type. The two counts coincide on some captures; on
// staticmask-warm-tokens2-4 it returns 18 rows for 23 encoders.
//
// The returned slice therefore must not be indexed by encoder position. No
// encoder join for these rows is established: see
// docs/research/XCODE_PARITY.md, which records both the timebase disagreement
// and the refuted kick_software_id candidate.
func PopulateEncoderMetricsFromBinaryParsing(t *trace.Trace) ([]EncoderCounterMetrics, error) {
	// Parse performance counters from Counters_f_*.raw files
	stats, err := ParsePerfCounters(t)
	if err != nil {
		return nil, err
	}

	return PopulateEncoderMetricsFromPerfCounterStats(stats)
}

// PopulateEncoderMetricsFromPerfCounterStats converts parsed pipeline counter
// rows without claiming an encoder attribution the input does not establish.
func PopulateEncoderMetricsFromPerfCounterStats(stats *PerfCounterStats) ([]EncoderCounterMetrics, error) {
	if stats == nil {
		return nil, fmt.Errorf("nil performance counter stats")
	}

	metrics := make([]EncoderCounterMetrics, 0, len(stats.ShaderMetrics))

	// ShaderMetrics rows are pipeline-scoped. No field in PerfCounterStats
	// establishes which encoder, if any, owns a row.
	for _, shaderMetric := range stats.ShaderMetrics {
		// Start with base metrics from Counters files
		metric := EncoderCounterMetrics{
			EncoderIndex: -1,
			EncoderLabel: shaderMetric.ShaderName,
			EncoderType:  "compute", // Most traces are compute-heavy
			Attribution:  CounterAttributionUnknown,

			// From binary parsing (gputrace-44 validated approach)
			ALUUtilization:  shaderMetric.ALUUtilization,
			MemoryBandwidth: shaderMetric.MemoryBandwidth, // Bytes (total)

			// Detailed memory bandwidth from gputrace-65
			BytesReadFromDeviceMemory:      shaderMetric.BytesReadFromDeviceMemory,
			BytesWrittenToDeviceMemory:     shaderMetric.BytesWrittenToDeviceMemory,
			BufferDeviceMemoryBytesRead:    shaderMetric.BufferDeviceMemoryBytesRead,
			BufferDeviceMemoryBytesWritten: shaderMetric.BufferDeviceMemoryBytesWritten,
			DeviceMemoryBandwidthGBps:      shaderMetric.DeviceMemoryBandwidthGBps,
			GPUReadBandwidthGBps:           shaderMetric.GPUReadBandwidthGBps,
			GPUWriteBandwidthGBps:          shaderMetric.GPUWriteBandwidthGBps,

			// Shader Launch Limiters from gputrace-67
			ComputeShaderLaunchLimiter:  shaderMetric.ComputeShaderLaunchLimiter,
			FragmentShaderLaunchLimiter: shaderMetric.FragmentShaderLaunchLimiter,
			VertexShaderLaunchLimiter:   shaderMetric.VertexShaderLaunchLimiter,

			// Pipeline Limiters from gputrace-67
			ControlFlowLimiter:           shaderMetric.ControlFlowLimiter,
			InstructionThroughputLimiter: shaderMetric.InstructionThroughputLimiter,
			IntegerAndComplexLimiter:     shaderMetric.IntegerAndComplexLimiter,
			IntegerAndConditionalLimiter: shaderMetric.IntegerAndConditionalLimiter,
			F16Limiter:                   shaderMetric.F16Limiter,
			F32Limiter:                   shaderMetric.F32Limiter,

			// Memory Limiters from gputrace-67
			L1CacheLimiter:        shaderMetric.L1CacheLimiter,
			LastLevelCacheLimiter: shaderMetric.LastLevelCacheLimiter,
			MMULimiter:            shaderMetric.MMULimiter,

			// Texture Limiters from gputrace-67
			TextureFilteringLimiter: shaderMetric.TextureFilteringLimiter,
			TextureWriteLimiter:     shaderMetric.TextureWriteLimiter,
			TextureReadLimiter:      shaderMetric.TextureReadLimiter,

			// Buffer L1 Cache Metrics from gputrace-66
			BufferL1MissRate:       shaderMetric.BufferL1MissRate,
			BufferL1ReadAccesses:   shaderMetric.BufferL1ReadAccesses,
			BufferL1ReadBandwidth:  shaderMetric.BufferL1ReadBandwidth,
			BufferL1WriteAccesses:  shaderMetric.BufferL1WriteAccesses,
			BufferL1WriteBandwidth: shaderMetric.BufferL1WriteBandwidth,

			// Shader Utilization Metrics from gputrace-67
			ComputeShaderUtilization:  shaderMetric.ComputeShaderUtilization,
			FragmentShaderUtilization: shaderMetric.FragmentShaderUtilization,
			VertexShaderUtilization:   shaderMetric.VertexShaderUtilization,
			ControlFlowUtilization:    shaderMetric.ControlFlowUtilization,
			InstructionThroughputUtil: shaderMetric.InstructionThroughputUtil,
			IntegerAndComplexUtil:     shaderMetric.IntegerAndComplexUtil,
			IntegerAndConditionalUtil: shaderMetric.IntegerAndConditionalUtil,
			F16Utilization:            shaderMetric.F16Utilization,
			F32Utilization:            shaderMetric.F32Utilization,

			// Execution counts (validated with 100% accuracy on Encoder 5)
			DispatchCount: shaderMetric.ExecutionCount, // This is kernel invocations

			DurationCycles: shaderMetric.TotalCycles,
		}

		metrics = append(metrics, metric)
	}

	return metrics, nil
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
		output += fmtutil.RepeatChar('-', 75) + "\n"

		for _, metric := range result.EncoderMetrics {
			durationMs := float64(metric.Duration) / 1e6
			label := metric.EncoderLabel
			if label == "" {
				label = "(unlabeled)"
			}

			output += fmt.Sprintf("%-5d %-30s %12.3f %12d %9.1f%%\n",
				metric.EncoderIndex,
				fmtutil.TruncateString(label, 30),
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
		output += fmtutil.RepeatChar('-', 75) + "\n"

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
				fmtutil.TruncateString(funcName, 40),
				durationMs,
				metric.ComputeUtilization)
		}
		output += "\n"
	}

	return output
}
