package shader

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/tmc/mlx-go/experiments/gputrace/internal/trace"
)

// Type alias
type EncoderTiming = trace.EncoderTiming

// ShaderCost represents the cost/timing data for a single shader invocation.
type ShaderCost struct {
	CostPercent   float64
	Name          string
	Type          string
	PipelineState string
	SIMDGroups    int
	AllocatedRegs int
	HighRegister  int
	SpilledBytes  int
}

// ParseShaderCosts parses shader cost data from Xcode Instruments export.
// Format:
// Cost    Name    Type    Pipeline State    # SIMD Groups    # Allocated Registers    High Register    Spilled Bytes
// 61.40%    steel_gemm_fused_nn_float32_float32_bm64_bn64_bk16_wm2_wn2    Compute    Compute Pipeline 0xa74c7a400    100    162    162    0 bytes
func ParseShaderCosts(r io.Reader) ([]ShaderCost, error) {
	scanner := bufio.NewScanner(r)
	var costs []ShaderCost

	// Skip header line
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty shader cost data")
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Parse tab-separated fields
		fields := strings.Split(line, "\t")
		if len(fields) < 8 {
			continue
		}

		// Parse cost percentage (e.g., "61.40%")
		costStr := strings.TrimSpace(fields[0])
		costStr = strings.TrimSuffix(costStr, "%")
		cost, err := strconv.ParseFloat(costStr, 64)
		if err != nil {
			continue
		}

		// Parse SIMD groups
		simdGroups, _ := strconv.Atoi(strings.TrimSpace(fields[4]))
		allocatedRegs, _ := strconv.Atoi(strings.TrimSpace(fields[5]))
		highReg, _ := strconv.Atoi(strings.TrimSpace(fields[6]))

		// Parse spilled bytes (e.g., "0 bytes")
		spilledStr := strings.TrimSpace(fields[7])
		spilledStr = strings.TrimSuffix(spilledStr, " bytes")
		spilledBytes, _ := strconv.Atoi(spilledStr)

		costs = append(costs, ShaderCost{
			CostPercent:   cost,
			Name:          strings.TrimSpace(fields[1]),
			Type:          strings.TrimSpace(fields[2]),
			PipelineState: strings.TrimSpace(fields[3]),
			SIMDGroups:    simdGroups,
			AllocatedRegs: allocatedRegs,
			HighRegister:  highReg,
			SpilledBytes:  spilledBytes,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return costs, nil
}

// ApplyShaderCostsToTimings applies shader cost percentages to encoder timings.
// This converts the percentage-based shader costs into absolute timing values.
func ApplyShaderCostsToTimings(encoderTimings []*EncoderTiming, shaderCosts []ShaderCost, totalGPUTimeMs float64) []*EncoderTiming {
	if len(shaderCosts) == 0 {
		return encoderTimings
	}

	// Build a map of shader names to costs
	shaderCostMap := make(map[string]float64)
	for _, cost := range shaderCosts {
		shaderCostMap[cost.Name] += cost.CostPercent
	}

	// Try to match encoder labels to shader names
	for _, timing := range encoderTimings {
		// Look for matching shader name
		bestMatch := ""
		bestScore := 0.0

		for shaderName := range shaderCostMap {
			score := matchShaderToEncoder(timing.Label, shaderName)
			if score > bestScore {
				bestScore = score
				bestMatch = shaderName
			}
		}

		// If we found a match, use the real cost
		if bestScore > 0.3 { // threshold for matching
			costPercent := shaderCostMap[bestMatch]
			timing.DurationMs = totalGPUTimeMs * (costPercent / 100.0)
			timing.DurationNs = uint64(timing.DurationMs * 1e6)
			timing.Percentage = float32(costPercent)
		}
	}

	return encoderTimings
}

// matchShaderToEncoder scores how well a shader name matches an encoder label.
// Returns a score from 0.0 (no match) to 1.0 (perfect match).
func matchShaderToEncoder(encoderLabel, shaderName string) float64 {
	labelLower := strings.ToLower(encoderLabel)
	shaderLower := strings.ToLower(shaderName)

	// Direct substring match
	if strings.Contains(shaderLower, labelLower) || strings.Contains(labelLower, shaderLower) {
		return 0.8
	}

	// Keyword matching
	keywords := []struct {
		encoder []string
		shader  []string
		score   float64
	}{
		// Matrix multiplication operations
		{[]string{"projection", "matmul", "gemm", "@"}, []string{"steel_gemm", "matmul"}, 0.9},
		{[]string{"q projection", "query"}, []string{"steel_gemm_fused_nn"}, 0.85},
		{[]string{"k projection", "key"}, []string{"steel_gemm_fused_nn"}, 0.85},
		{[]string{"v projection", "value"}, []string{"steel_gemm_fused_nn"}, 0.85},
		{[]string{"scores", "q @ k"}, []string{"steel_gemm_fused_nn"}, 0.9},
		{[]string{"output", "probs @ v"}, []string{"steel_gemm_fused_nt"}, 0.9},

		// Element-wise operations
		{[]string{"add", "residual"}, []string{"add", "vv_add", "svn_add", "vvn_add"}, 0.7},
		{[]string{"multiply", "scale"}, []string{"multiply", "vvn_multiply", "svn_multiply"}, 0.7},
		{[]string{"softmax"}, []string{"softmax", "block_softmax"}, 0.95},
		{[]string{"gelu", "activation"}, []string{"tanh", "vn_tanh"}, 0.6},
		{[]string{"norm", "normalize"}, []string{"row_reduce", "divide", "subtract", "sqrt"}, 0.5},
	}

	for _, kw := range keywords {
		encoderMatch := false
		for _, ek := range kw.encoder {
			if strings.Contains(labelLower, ek) {
				encoderMatch = true
				break
			}
		}

		shaderMatch := false
		for _, sk := range kw.shader {
			if strings.Contains(shaderLower, sk) {
				shaderMatch = true
				break
			}
		}

		if encoderMatch && shaderMatch {
			return kw.score
		}
	}

	return 0.0
}

// AggregateShaderCostsByEncoder groups shader costs by their likely encoder.
// This is useful when multiple shaders contribute to a single encoder's work.
func AggregateShaderCostsByEncoder(shaderCosts []ShaderCost, encoderLabels []string) map[string]float64 {
	aggregated := make(map[string]float64)

	// Initialize with encoder labels
	for _, label := range encoderLabels {
		aggregated[label] = 0.0
	}

	// Assign each shader cost to the best-matching encoder
	for _, cost := range shaderCosts {
		bestEncoder := ""
		bestScore := 0.0

		for _, label := range encoderLabels {
			score := matchShaderToEncoder(label, cost.Name)
			if score > bestScore {
				bestScore = score
				bestEncoder = label
			}
		}

		if bestScore > 0.3 && bestEncoder != "" {
			aggregated[bestEncoder] += cost.CostPercent
		} else {
			// Unmatched shaders go to "other"
			aggregated["other"] += cost.CostPercent
		}
	}

	return aggregated
}
