package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/gputrace/internal/shader"
	"github.com/tmc/gputrace/internal/trace"
)

var treeCmd = &cobra.Command{
	Use:   "tree <trace.gputrace>",
	Short: "Show hierarchical view of debug groups, encoders, and kernels",
	Long: `Display the trace in a hierarchical tree format similar to Xcode's GPU profiler:

  DebugGroup (% time)
  └─ Compute Encoder 0xa... kernel_function (% time)
     └─ N dispatches

Two view modes are available:
  - Default: Groups by MLX operation (Squeeze, QuantizedMatmul, etc.) from encoder labels
  - --by-kernel: Groups by actual kernel function names (copy, qmv, etc.)

Examples:
  gputrace tree trace.gputrace                    # Group by MLX operation
  gputrace tree trace.gputrace --by-kernel        # Group by kernel function
  gputrace tree trace.gputrace -f Squeeze         # Filter by debug group
  gputrace tree trace.gputrace -k copy            # Filter by kernel function`,
	Args: cobra.ExactArgs(1),
	RunE: runTree,
}

var (
	treeShowDispatches bool
	treeFilter         string
	treeKernelFilter   string
	treeByKernel       bool
)

func init() {
	rootCmd.AddCommand(treeCmd)
	treeCmd.Flags().BoolVar(&treeShowDispatches, "show-dispatches", false, "Show individual dispatch calls")
	treeCmd.Flags().StringVarP(&treeFilter, "filter", "f", "", "Filter by debug group or encoder label")
	treeCmd.Flags().StringVarP(&treeKernelFilter, "kernel", "k", "", "Filter by kernel function name")
	treeCmd.Flags().BoolVar(&treeByKernel, "by-kernel", false, "Group by kernel function instead of MLX operation")
}

// DebugGroupNode represents a debug group (MLX operation)
type DebugGroupNode struct {
	Name            string
	TimePercent     float64
	TotalDispatches int
	Encoders        []*EncoderNode
}

// EncoderNode represents an encoder with its kernel
type EncoderNode struct {
	Label         string  // Debug group label OR kernel function name
	KernelName    string  // Actual kernel function name
	Address       uint64
	TimePercent   float64
	DispatchCount int
}

func runTree(cmd *cobra.Command, args []string) error {
	tracePath := args[0]

	t, err := trace.Open(tracePath)
	if err != nil {
		return fmt.Errorf("failed to open trace: %w", err)
	}

	// Get shader metrics for timing percentages
	shaderMetrics, _ := shader.ExtractShaderMetrics(t)
	kernelTiming := make(map[string]float64)
	if shaderMetrics != nil {
		for _, shader := range shaderMetrics.Shaders {
			kernelTiming[shader.Name] = shader.PercentOfTotal
		}
	}

	// Get kernel stats for dispatch counts
	kernelStats, _ := t.AnalyzeKernels()

	// Get compute encoders - these have the actual kernel function names
	computeEncoders, err := t.ParseComputeEncoders()
	if err != nil {
		return fmt.Errorf("parse compute encoders: %w", err)
	}

	// Parse API calls to get debug group labels
	apiCallList, _ := t.ParseAPICallList()

	// Build encoder address to debug group label mapping
	addrToDebugLabel := make(map[uint64]string)
	if apiCallList != nil {
		for _, cb := range apiCallList.CommandBuffers {
			for _, call := range cb.Calls {
				if call.Type == "encoder" && call.Details == "computeCommandEncoder" && call.Label != "" {
					addrToDebugLabel[call.Address] = call.Label
				}
			}
		}
	}

	// Build the tree based on mode
	debugGroups := make(map[string]*DebugGroupNode)

	for _, enc := range computeEncoders {
		kernelName := enc.Label
		if kernelName == "" {
			kernelName = "unknown"
		}

		// Get timing for this kernel
		timePct := kernelTiming[kernelName]

		// Get dispatch count
		dispatchCount := 1
		if stat, ok := kernelStats[kernelName]; ok && stat.DispatchCount > 0 {
			dispatchCount = stat.DispatchCount
		}

		// Get debug group label from API calls
		debugLabel := addrToDebugLabel[enc.Address]
		if debugLabel == "" {
			debugLabel = kernelName
		}

		// Determine grouping
		var groupName string
		if treeByKernel {
			// Group by kernel function type
			groupName = inferDebugGroup(kernelName)
		} else {
			// Group by first MLX operation from debug label
			groupName = extractFirstOperation(debugLabel)
			if groupName == "" {
				groupName = inferDebugGroup(kernelName)
			}
		}

		// Apply kernel filter
		if treeKernelFilter != "" {
			if !strings.Contains(strings.ToLower(kernelName), strings.ToLower(treeKernelFilter)) {
				continue
			}
		}

		// Create or update debug group
		if _, exists := debugGroups[groupName]; !exists {
			debugGroups[groupName] = &DebugGroupNode{
				Name:     groupName,
				Encoders: make([]*EncoderNode, 0),
			}
		}

		// Check if we already have this kernel in this group (aggregate by kernel name)
		found := false
		for _, e := range debugGroups[groupName].Encoders {
			if e.KernelName == kernelName {
				e.DispatchCount += dispatchCount
				found = true
				break
			}
		}

		if !found {
			debugGroups[groupName].Encoders = append(debugGroups[groupName].Encoders, &EncoderNode{
				Label:         debugLabel,
				KernelName:    kernelName,
				Address:       enc.Address,
				TimePercent:   timePct,
				DispatchCount: dispatchCount,
			})
		}

		debugGroups[groupName].TotalDispatches += dispatchCount
		debugGroups[groupName].TimePercent += timePct
	}

	// Filter and convert to sorted slice
	var dgList []*DebugGroupNode
	for _, dg := range debugGroups {
		if treeFilter != "" {
			filterLower := strings.ToLower(treeFilter)
			dgMatches := strings.Contains(strings.ToLower(dg.Name), filterLower)

			var matchingEncoders []*EncoderNode
			for _, enc := range dg.Encoders {
				if strings.Contains(strings.ToLower(enc.Label), filterLower) ||
					strings.Contains(strings.ToLower(enc.KernelName), filterLower) {
					matchingEncoders = append(matchingEncoders, enc)
				}
			}

			if !dgMatches && len(matchingEncoders) == 0 {
				continue
			}

			if len(matchingEncoders) > 0 && !dgMatches {
				dg.Encoders = matchingEncoders
				// Recalculate totals
				dg.TotalDispatches = 0
				dg.TimePercent = 0
				for _, enc := range dg.Encoders {
					dg.TotalDispatches += enc.DispatchCount
					dg.TimePercent += enc.TimePercent
				}
			}
		}

		dgList = append(dgList, dg)
	}

	// Sort by time percentage descending, then by dispatch count
	sort.Slice(dgList, func(i, j int) bool {
		if dgList[i].TimePercent != dgList[j].TimePercent {
			return dgList[i].TimePercent > dgList[j].TimePercent
		}
		return dgList[i].TotalDispatches > dgList[j].TotalDispatches
	})

	// Print the tree
	fmt.Println("=== GPU Trace Hierarchy ===")
	if treeByKernel {
		fmt.Println("(grouped by kernel function)")
	} else {
		fmt.Println("(grouped by MLX operation)")
	}
	fmt.Println()

	totalDispatches := 0
	totalEncoders := 0
	var totalTime float64
	for _, dg := range dgList {
		totalDispatches += dg.TotalDispatches
		totalEncoders += len(dg.Encoders)
		totalTime += dg.TimePercent
	}

	for i, dg := range dgList {
		isLast := i == len(dgList)-1
		connector := "├─"
		if isLast {
			connector = "└─"
		}

		fmt.Printf("%s %s", connector, dg.Name)
		if dg.TimePercent > 0 {
			fmt.Printf(" (%.1f%%)", dg.TimePercent)
		}
		fmt.Printf(" [%d kernels, %d dispatches]\n", len(dg.Encoders), dg.TotalDispatches)

		// Sort encoders by time percentage, then dispatch count
		sort.Slice(dg.Encoders, func(a, b int) bool {
			if dg.Encoders[a].TimePercent != dg.Encoders[b].TimePercent {
				return dg.Encoders[a].TimePercent > dg.Encoders[b].TimePercent
			}
			return dg.Encoders[a].DispatchCount > dg.Encoders[b].DispatchCount
		})

		childPrefix := "   "
		if !isLast {
			childPrefix = "│  "
		}

		for j, enc := range dg.Encoders {
			encIsLast := j == len(dg.Encoders)-1
			encConnector := "├─"
			if encIsLast {
				encConnector = "└─"
			}

			fmt.Printf("%s%s %s", childPrefix, encConnector, enc.KernelName)
			if enc.TimePercent > 0 {
				fmt.Printf(" (%.1f%%)", enc.TimePercent)
			}
			if enc.DispatchCount > 1 {
				fmt.Printf(" [%d dispatches]", enc.DispatchCount)
			}
			fmt.Println()
		}
	}

	fmt.Println()
	fmt.Printf("Total: %d groups, %d unique kernels, %d dispatches", len(dgList), totalEncoders, totalDispatches)
	if totalTime > 0 {
		fmt.Printf(" (%.1f%% GPU time)", totalTime)
	}
	fmt.Println()

	return nil
}

// extractFirstOperation extracts the first operation from a cumulative path
// e.g., "SqueezeRMSNormQuantizedMatmul" -> "Squeeze"
// This matches Xcode's behavior where the first operation is the top-level debug group
func extractFirstOperation(path string) string {
	if path == "" {
		return ""
	}

	// Known MLX operation names to look for
	ops := []string{
		"Argmax", "Softmax", "LogSoftmax",
		"QuantizedMatmul", "Matmul", "GEMM",
		"RMSNorm", "LayerNorm", "BatchNorm",
		"RoPE", "Attention", "SDPA", "ScaledDotProductAttention",
		"SliceUpdate", "Slice", "Concat", "Split",
		"Reshape", "Transpose", "Squeeze", "ExpandDims",
		"Add", "Subtract", "Multiply", "Divide",
		"Sigmoid", "Tanh", "ReLU", "GELU", "SiLU",
		"Broadcast", "Arange", "Full", "Zeros", "Ones",
		"Copy", "Negative", "Abs", "Synchronize",
		"RandomBits", "AsType",
	}

	// Find the first occurrence of any operation
	firstOp := ""
	firstPos := len(path)

	for _, op := range ops {
		pos := strings.Index(path, op)
		if pos >= 0 && pos < firstPos {
			firstPos = pos
			firstOp = op
		}
	}

	if firstOp != "" {
		return firstOp
	}

	// Fallback: return the path as-is if short, or truncate
	if len(path) > 30 {
		return path[:30]
	}
	return path
}

// inferDebugGroup infers the debug group (MLX operation) from a kernel name
func inferDebugGroup(kernelName string) string {
	lower := strings.ToLower(kernelName)

	// Quantized operations
	if strings.Contains(lower, "qmv") || strings.Contains(lower, "quantized") || strings.Contains(lower, "affine_") || strings.Contains(lower, "dequantize") {
		return "QuantizedMatmul"
	}

	// Copy/dtype conversions
	if strings.Contains(lower, "copy") {
		return "Copy"
	}

	// Attention
	if strings.Contains(lower, "sdpa") || strings.Contains(lower, "attention") {
		return "Attention"
	}

	// Normalization
	if strings.Contains(lower, "rms") || strings.Contains(lower, "norm") {
		return "RMSNorm"
	}

	// RoPE
	if strings.Contains(lower, "rope") {
		return "RoPE"
	}

	// Sampling
	if strings.Contains(lower, "argmax") || strings.Contains(lower, "softmax") {
		return "Argmax"
	}

	// GEMM
	if strings.Contains(lower, "gemm") || strings.Contains(lower, "gemv") || strings.Contains(lower, "matmul") {
		return "Matmul"
	}

	// Elementwise
	if strings.Contains(lower, "add") || strings.Contains(lower, "multiply") ||
		strings.Contains(lower, "subtract") || strings.Contains(lower, "divide") ||
		strings.Contains(lower, "sigmoid") || strings.Contains(lower, "log") ||
		strings.Contains(lower, "negative") || strings.Contains(lower, "select") ||
		strings.Contains(lower, "minimum") || strings.Contains(lower, "maximum") {
		return "Elementwise"
	}

	// Comparison
	if strings.Contains(lower, "equal") || strings.Contains(lower, "greater") ||
		strings.Contains(lower, "less") {
		return "Comparison"
	}

	// Index operations
	if strings.Contains(lower, "arange") || strings.Contains(lower, "gather") ||
		strings.Contains(lower, "scatter") {
		return "Indexing"
	}

	// Random
	if strings.Contains(lower, "rbit") || strings.Contains(lower, "random") {
		return "Random"
	}

	// Other
	return "Other"
}
