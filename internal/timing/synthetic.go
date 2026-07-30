package timing

import (
	"strings"

	"github.com/tmc/gputrace/internal/trace"
)

// GenerateSyntheticTiming creates timing data from kernel names when no real timing is available.
// This is useful for qualitative analysis even when performance counters weren't captured.
func GenerateSyntheticTiming(t *trace.Trace) []*EncoderTiming {
	names := observedKernelLabels(t)
	if len(names) == 0 {
		return nil
	}

	timings := make([]*EncoderTiming, 0, len(names))
	baseTime := uint64(1000000000000000) // Arbitrary start time
	currentTime := baseTime

	for _, kernelName := range names {
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

func observedKernelLabels(t *trace.Trace) []string {
	encoders, err := t.ParseComputeEncoders()
	if err == nil {
		seen := make(map[string]bool)
		var names []string
		for _, encoder := range encoders {
			if encoder.Label == "" || seen[encoder.Label] {
				continue
			}
			seen[encoder.Label] = true
			names = append(names, encoder.Label)
		}
		if len(names) > 0 {
			return names
		}
	}
	return t.KernelNames
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
	if strings.Contains(name, "affine_qmm") {
		return matmulNs
	}
	if strings.Contains(name, "affine_qmv") {
		return qmvNs
	}
	if strings.Contains(name, "matmul") || strings.Contains(name, "gemm") {
		return matmulNs
	}

	// Quantization operations
	if strings.Contains(name, "dequantize") || strings.Contains(name, "quantize") {
		return dequantNs
	}

	// Attention operations
	if strings.Contains(name, "attention") || strings.Contains(name, "sdpa") || strings.Contains(name, "steel") {
		return attentionNs
	}

	// RoPE and positional encodings
	if strings.Contains(name, "rope") || strings.Contains(name, "rotary") {
		return ropeNs
	}

	// Normalization
	if strings.Contains(name, "norm") || strings.Contains(name, "softmax") {
		return normalizationNs
	}

	// Sampling operations
	if strings.Contains(name, "argmax") || strings.Contains(name, "sample") {
		return samplingNs
	}

	// Element-wise operations (typically fast)
	if strings.Contains(name, "add") || strings.Contains(name, "multiply") ||
		strings.Contains(name, "sigmoid") || strings.Contains(name, "divide") ||
		strings.Contains(name, "subtract") || strings.Contains(name, "minimum") ||
		strings.Contains(name, "log") || strings.Contains(name, "negative") ||
		strings.Contains(name, "copy") {
		return elementWiseNs
	}

	// Gather/scatter operations
	if strings.Contains(name, "gather") || strings.Contains(name, "scatter") {
		return baseNs
	}

	// Default
	return baseNs
}
