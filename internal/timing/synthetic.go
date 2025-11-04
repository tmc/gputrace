package timing

import (
	"fmt"

	"github.com/google/pprof/profile"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// GenerateSyntheticTiming creates timing data from kernel names when no real timing is available.
// This is useful for qualitative analysis even when performance counters weren't captured.
func GenerateSyntheticTiming(t *trace.Trace) []*EncoderTiming {
	if len(t.KernelNames) == 0 {
		return nil
	}

	timings := make([]*EncoderTiming, 0, len(t.KernelNames))
	baseTime := uint64(1000000000000000) // Arbitrary start time
	currentTime := baseTime

	for _, kernelName := range t.KernelNames {
		// Estimate duration based on kernel type (for visualization only)
		durationNs := estimateKernelDuration(kernelName)

		timing := &EncoderTiming{
			Label:          kernelName,
			StartTimestamp: currentTime,
			EndTimestamp:   currentTime + durationNs,
			DurationNs:     durationNs,
			DurationMs:     float64(durationNs) / 1e6,
		}

		timings = append(timings, timing)
		currentTime += durationNs

		// Add small gap between operations
		currentTime += 10000 // 10µs gap
	}

	// Calculate percentages
	calculatePercentages(timings)

	return timings
}

// estimateKernelDuration provides rough duration estimates based on kernel name patterns.
// These are NOT real timings - just reasonable estimates for visualization purposes.
func estimateKernelDuration(kernelName string) uint64 {
	const (
		baseNs          = 1000000 // 1ms
		matmulNs        = 5000000 // 5ms
		dequantNs       = 2000000 // 2ms
		qmvNs           = 3000000 // 3ms
		elementWiseNs   = 500000  // 0.5ms
		normalizationNs = 1500000 // 1.5ms
		ropeNs          = 2000000 // 2ms
		attentionNs     = 4000000 // 4ms
		samplingNs      = 500000  // 0.5ms
	)

	name := toLowerSimple(kernelName)

	// Matrix operations (usually slowest)
	if containsSubstring(name, "affine_qmm") {
		return matmulNs
	}
	if containsSubstring(name, "affine_qmv") {
		return qmvNs
	}
	if containsSubstring(name, "matmul") || containsSubstring(name, "gemm") {
		return matmulNs
	}

	// Quantization operations
	if containsSubstring(name, "dequantize") || containsSubstring(name, "quantize") {
		return dequantNs
	}

	// Attention operations
	if containsSubstring(name, "attention") || containsSubstring(name, "sdpa") || containsSubstring(name, "steel") {
		return attentionNs
	}

	// RoPE and positional encodings
	if containsSubstring(name, "rope") || containsSubstring(name, "rotary") {
		return ropeNs
	}

	// Normalization
	if containsSubstring(name, "norm") || containsSubstring(name, "softmax") {
		return normalizationNs
	}

	// Sampling operations
	if containsSubstring(name, "argmax") || containsSubstring(name, "sample") {
		return samplingNs
	}

	// Element-wise operations (typically fast)
	if containsSubstring(name, "add") || containsSubstring(name, "multiply") ||
		containsSubstring(name, "sigmoid") || containsSubstring(name, "divide") ||
		containsSubstring(name, "subtract") || containsSubstring(name, "minimum") ||
		containsSubstring(name, "log") || containsSubstring(name, "negative") ||
		containsSubstring(name, "copy") {
		return elementWiseNs
	}

	// Gather/scatter operations
	if containsSubstring(name, "gather") || containsSubstring(name, "scatter") {
		return baseNs
	}

	// Default
	return baseNs
}

// ToPprofWithSynthetic creates a pprof profile, using synthetic timing if needed.
func ToPprofWithSynthetic(t *trace.Trace, timings []*EncoderTiming) (*profile.Profile, error) {
	// If no timing data, generate synthetic timing from kernel names
	if len(timings) == 0 && len(t.KernelNames) > 0 {
		timings = GenerateSyntheticTiming(t)
		if len(timings) == 0 {
			return nil, fmt.Errorf("no timing data and unable to generate synthetic timing")
		}
	}

	// TODO: Re-enable when ToPprof is properly defined
	// Use the regular ToPprof function
	// return t.ToPprof(timings)
	return nil, fmt.Errorf("ToPprof not yet implemented")
}
